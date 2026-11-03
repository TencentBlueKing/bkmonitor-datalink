// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// overtakenPaths is the source's overtaken trials by path, only the non-zero ones.
func overtakenPaths(f *fixture) map[string]uint64 {
	got := map[string]uint64{}
	for path, count := range f.engine.Stats().Sources[sourceLog].EarlierOvertaken {
		if count > 0 {
			got[path] = count
		}
	}
	return got
}

func formalRead(f *fixture, q Query) {
	f.clock.set(q.ReadyAt)
	read := f.engine.Begin(q)
	read.Series(point(int64(q.Contract.Slot.EvaluationTime), "1"), 100)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
}

func waitTrialDone(f *fixture, trial *earlierSample) {
	f.waitFor(func(Stats) bool {
		f.engine.mu.Lock()
		defer f.engine.mu.Unlock()
		return trial.done
	})
}

// Every way a trial ends overtaken is counted under what overtook it, once, and only that.
func TestAnOvertakenTrialIsCountedByWhatOvertookIt(t *testing.T) {
	cases := []struct {
		path string
		run  func(f *fixture, q Query)
	}{
		{OvertakenPreparedLate, func(f *fixture, q Query) {
			// prepared 30 s past the candidate readiness point (baseline 60 s + h/2 60 s)
			f.clock.set(q.ReadyAt.Add(-30 * time.Second))
			f.engine.Prepare(q)
			formalRead(f, q)
		}},
		{OvertakenFormalFirst, func(f *fixture, q Query) {
			f.engine.Prepare(q)
			formalRead(f, q)
		}},
		{OvertakenNextSlot, func(f *fixture, q Query) {
			f.engine.Prepare(q)
			next := q
			next.Contract.Slot.EvaluationTime += 60
			next.ReadyAt = q.ReadyAt.Add(time.Minute)
			f.engine.Prepare(next)
		}},
		{OvertakenStartedLate, func(f *fixture, q Query) {
			f.engine.Prepare(q)
			trial := f.group("qg").prepared
			f.clock.set(trial.readyAt.Add(time.Millisecond))
			f.engine.StepEarly(context.Background())
			waitTrialDone(f, trial)
			formalRead(f, q)
		}},
		{OvertakenFinishedLate, func(f *fixture, q Query) {
			f.engine.Prepare(q)
			trial := f.group("qg").prepared
			f.clock.set(trial.at)
			f.answers <- func(sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
				f.clock.set(trial.readyAt.Add(time.Millisecond))
				return full(point(int64(q.Contract.Slot.EvaluationTime), "1"))(sink)
			}
			f.engine.StepEarly(context.Background())
			waitTrialDone(f, trial)
			formalRead(f, q)
		}},
		{OvertakenHoldMoved, func(f *fixture, q Query) {
			base := 2 * time.Minute
			f.engine.options.CurrentReadHold = func(execution.QueryGroupIdentity) time.Duration { return base }
			f.engine.Prepare(q)
			trial := f.group("qg").prepared
			f.clock.set(trial.at)
			f.answers <- full(point(int64(q.Contract.Slot.EvaluationTime), "1"))
			f.engine.StepEarly(context.Background())
			f.waitFor(func(stats Stats) bool { return stats.PendingBytes == summaryEntryBytes })
			base = time.Minute
			formalRead(f, q)
		}},
	}
	for _, c := range cases {
		t.Run(c.path, func(t *testing.T) {
			f := newFixture(t)
			f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
			c.run(f, heldQuery(f))
			got := overtakenPaths(f)
			if len(got) != 1 || got[c.path] != 1 {
				t.Fatalf("overtaken by path %v, want only %s once", got, c.path)
			}
			if f.engine.Stats().Sources[sourceLog].EarlierReads[EarlierOvertaken] != 1 {
				t.Fatal("the trial was not counted overtaken once")
			}
			// The group's own reading says the same, so groups can be told apart.
			reading, found := f.engine.GroupReading("qg")
			if !found || reading.EarlierOvertaken[c.path] != 1 {
				t.Fatalf("the group's reading overtaken by %v, want %s once", reading.EarlierOvertaken, c.path)
			}
			for path, count := range reading.EarlierOvertaken {
				if path != c.path && count != 0 {
					t.Fatalf("the group's reading also counts %s: %v", path, reading.EarlierOvertaken)
				}
			}
		})
	}
}

// How late a Slot reached its trial point is kept, so a reading says by how much the Slots miss it.
func TestAPreparedLateTrialKeepsHowLateItWas(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	q := heldQuery(f)
	f.clock.set(q.ReadyAt.Add(-30 * time.Second))
	f.engine.Prepare(q)
	if got := f.engine.Stats().Sources[sourceLog].EarlierPreparedLateMaxSeconds; got != 30 {
		t.Fatalf("prepared late max %v s, want 30", got)
	}
	f.engine.Prepare(heldQuery(f))
	if got := f.engine.Stats().Sources[sourceLog].EarlierPreparedLateMaxSeconds; got != 30 {
		t.Fatalf("a trial prepared in time lowered the max to %v", got)
	}
}

// The group's rest is judged at the Slot's formal read. A held Slot's first attempt, before the rest ends, reserves
// the trial of the Slot whose formal read comes after it; a Slot whose formal read comes before the rest ends
// reserves none, at either attempt.
func TestATrialIsReservedAtTheFirstAttemptOfTheSlotWhoseReadEndsTheRest(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	early := heldQuery(f)
	f.engine.Prepare(early)
	f.engine.mu.Lock()
	state := f.engine.groups["qg"]
	state.prepared = nil
	state.nextAt = early.ReadyAt.Add(30 * time.Second)
	f.engine.mu.Unlock()

	f.engine.Prepare(early)
	f.clock.set(early.ReadyAt)
	f.engine.Prepare(early)
	f.engine.mu.Lock()
	reserved := state.prepared
	f.engine.mu.Unlock()
	if reserved != nil {
		t.Fatal("a Slot read before the rest ends reserved a trial")
	}

	f.clock.set(early.ReadyAt.Add(-2 * time.Minute))
	later := heldQuery(f)
	later.Contract.Slot.EvaluationTime += 60
	f.engine.Prepare(later)
	f.engine.mu.Lock()
	trial := state.prepared
	f.engine.mu.Unlock()
	if trial == nil || trial.query.Contract.Slot != later.Contract.Slot || trial.done {
		t.Fatalf("the first attempt of the Slot read after the rest reserved %+v, want its live trial", trial)
	}
}
