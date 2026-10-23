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

// scheduledQuery is the held first read of the Slot at slot, as access frees it: its windows and digest those of
// that Slot, its readiness a minute's source delay and the 2 minute hold after it, and its schedule's next Slot a
// minute on.
func scheduledQuery(t *testing.T, slot int64) Query {
	t.Helper()
	q := query("qg", slot, minute)
	q.Spec.PlanFacts = facts(t, minute, "", execution.QueryClause{})
	q.Spec.ProviderRange, q.Spec.AcceptedRange = q.Spec.LogicalWindow, q.Spec.LogicalWindow
	digest, err := execution.DerivePhysicalQueryDigest(q.Spec)
	if err != nil {
		t.Fatal(err)
	}
	q.Spec.Digest = digest
	q.Contract.ReadHoldMillis = (2 * time.Minute).Milliseconds()
	q.ReadyAt = time.Unix(slot, 0).Add(3 * time.Minute)
	q.FollowingSlot = execution.EvaluationTime(slot + 60)
	return q
}

// aheadOvertaken is the overtaken trials of the Query Group's source by path, only the non-zero ones.
func aheadOvertaken(f *fixture) map[string]uint64 {
	got := map[string]uint64{}
	for path, count := range f.engine.Stats().Sources[sourceTimeSeries].EarlierOvertaken {
		if count > 0 {
			got[path] = count
		}
	}
	return got
}

func preparedTrial(f *fixture) *earlierSample {
	f.engine.mu.Lock()
	defer f.engine.mu.Unlock()
	return f.engine.groups["qg"].prepared
}

// A held Slot whose first attempt comes past where its own trial had to start - behind the previous Slot's held
// read, as it is under a hold past the period less the reread bound - reserves the trial of the Slot its schedule
// has next, before that Slot's point. The reservation stands through the reserving Slot's retry and formal read,
// the next Slot's first attempt adopts it with its own frozen query, and the trial is compared with that Slot's
// formal read as any trial is.
func TestASlotPastItsTrialStartReservesTheNextSlotsTrial(t *testing.T) {
	f := newFixture(t)
	observed := make(chan EarlierReadEvidence, 1)
	f.engine.options.OnEarlierRead = func(evidence EarlierReadEvidence) { observed <- evidence }
	slot := f.clock.now().Unix()
	q := scheduledQuery(t, slot)
	// The first attempt at the Slot's own point: a minute past where its trial had to start.
	f.clock.set(q.ReadyAt.Add(-time.Minute))
	f.engine.Prepare(q)
	trial := preparedTrial(f)
	next := scheduledQuery(t, slot+60)
	// Frozen, the next Slot's contract carries what moving this one cannot know: the due Plans' digest.
	next.Contract.DuePlanSetDigest = "next-due-plans"
	if trial == nil || !trial.ahead || trial.query.Contract.Slot != next.Contract.Slot || trial.query.Spec.Digest != next.Spec.Digest ||
		!trial.query.ReadyAt.Equal(next.ReadyAt) || trial.candidateHold != time.Minute ||
		!trial.readyAt.Equal(next.ReadyAt.Add(-time.Minute)) || !trial.at.Equal(trial.readyAt.Add(-RecheckTimeout)) {
		t.Fatalf("reserved %+v, want the next Slot's trial of a 1 minute hold before its point", trial)
	}
	if got := aheadOvertaken(f); len(got) != 0 {
		t.Fatalf("a trial reserved ahead was counted overtaken: %v", got)
	}
	// Its readiness retried, then read: the next Slot's reservation stands, and its formal read takes no sample.
	f.clock.set(q.ReadyAt)
	f.engine.Prepare(q)
	formalRead(f, q)
	if preparedTrial(f) != trial || trial.formal || trial.done {
		t.Fatal("the reserving Slot's retry or read ended the next Slot's trial")
	}
	// The reserving Slot reserved a trial: its retry, which finds that reservation standing, is not a Slot
	// without one.
	if sources, group := skippedBy(f); len(sources) != 0 || len(group) != 0 {
		t.Fatalf("the reserving Slot's retry was counted skipped: %v, on its group %v", sources, group)
	}
	f.clock.set(trial.at)
	f.answers <- full(point(int64(next.Contract.Slot.EvaluationTime), "1"))
	f.engine.StepEarly(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.PendingBytes == summaryEntryBytes })
	// The next Slot's first attempt, frozen as the trial was moved: adopted.
	f.clock.set(next.ReadyAt.Add(-time.Minute))
	f.engine.Prepare(next)
	if preparedTrial(f) != trial || trial.ahead || trial.query.Contract != next.Contract {
		t.Fatalf("the next Slot did not adopt its trial: %+v", trial)
	}
	formalRead(f, next)
	evidence := <-observed
	if !evidence.Observed || !evidence.Equal || evidence.Contract != next.Contract || evidence.CandidateHold != time.Minute {
		t.Fatalf("evidence %+v, want the next Slot's equal trial", evidence)
	}
}

// A trial reserved ahead was read for the Slot as it was moved there. That Slot frozen otherwise - its hold moved,
// its query revised, its readiness elsewhere - drops it as hold_moved, unread or read, and no evidence is given.
func TestATrialReservedAheadIsDroppedWhenItsSlotIsFrozenOtherwise(t *testing.T) {
	for _, c := range []struct {
		name   string
		frozen func(*Query)
		read   bool
	}{
		{"hold moved", func(q *Query) {
			q.Contract.ReadHoldMillis = (3 * time.Minute).Milliseconds()
			q.ReadyAt = q.ReadyAt.Add(time.Minute)
		}, true},
		{"query revised", func(q *Query) { q.Spec.Digest = "revised" }, true},
		{"readiness elsewhere", func(q *Query) { q.ReadyAt = q.ReadyAt.Add(7 * time.Second) }, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.engine.options.OnEarlierRead = func(evidence EarlierReadEvidence) {
				if evidence.Observed {
					t.Errorf("a trial gave evidence %+v", evidence)
				}
			}
			slot := f.clock.now().Unix()
			q := scheduledQuery(t, slot)
			f.clock.set(q.ReadyAt.Add(-time.Minute))
			f.engine.Prepare(q)
			trial := preparedTrial(f)
			if trial == nil || !trial.ahead {
				t.Fatalf("fixture: reserved %+v", trial)
			}
			next := scheduledQuery(t, slot+60)
			if c.read {
				f.clock.set(trial.at)
				f.answers <- full(point(int64(next.Contract.Slot.EvaluationTime), "1"))
				f.engine.StepEarly(context.Background())
				waitTrialDone(f, trial)
			}
			c.frozen(&next)
			f.clock.set(trial.readyAt)
			f.engine.Prepare(next)
			if got := aheadOvertaken(f); len(got) != 1 || got[OvertakenHoldMoved] != 1 {
				t.Fatalf("overtaken by %v, want hold_moved once", got)
			}
			if preparedTrial(f) == trial {
				t.Fatal("the dropped trial is still reserved")
			}
			formalRead(f, next)
		})
	}
}

// A trial reserved ahead rests as any trial: its rest is judged at the formal
// read of the Slot it is for, not of the Slot that reserves it, and the Slot
// reserved is the first whose formal read ends the rest.
func TestATrialReservedAheadIsJudgedRestedAtItsOwnSlotsRead(t *testing.T) {
	for _, c := range []struct {
		name     string
		restEnds time.Duration // after the reserving Slot's readiness
		slots    int
	}{
		{"rest ends before the next Slot's read", 30 * time.Second, 1},
		{"rest ends after it", 90 * time.Second, 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
			slot := f.clock.now().Unix()
			q := scheduledQuery(t, slot)
			f.clock.set(q.ReadyAt.Add(-time.Minute))
			f.engine.mu.Lock()
			state := f.engine.groupLocked(q, f.clock.now())
			state.nextAt = q.ReadyAt.Add(c.restEnds)
			f.engine.mu.Unlock()
			f.engine.Prepare(q)
			trial := preparedTrial(f)
			if trial == nil || !trial.ahead || trial.aheadSlots != c.slots || trial.query.ReadyAt.Before(q.ReadyAt.Add(c.restEnds)) {
				t.Fatalf("reserved %+v, want the trial %d Slots ahead, read after the rest", trial, c.slots)
			}
		})
	}
}

// A Slot whose schedule's next Slot is unknown reserves its own trial, past its point or not, as before.
func TestASlotWithoutItsNextSlotReservesOnlyItsOwnTrial(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	q := scheduledQuery(t, f.clock.now().Unix())
	q.FollowingSlot = 0
	f.clock.set(q.ReadyAt.Add(-30 * time.Second))
	f.engine.Prepare(q)
	if trial := preparedTrial(f); trial == nil || trial.ahead || trial.query.Contract.Slot != q.Contract.Slot {
		t.Fatalf("reserved %+v, want the Slot's own trial", trial)
	}
	formalRead(f, q)
	if got := aheadOvertaken(f); got[OvertakenPreparedLate] != 1 {
		t.Fatalf("overtaken by %v, want prepared_after_readiness", got)
	}
}
