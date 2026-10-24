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

// replayRead is q read as a replay - a round the scheduler sent there at its
// readiness, short of time - which the lookback does not take as a formal
// first read.
func replayRead(f *fixture, q Query) *Read {
	f.clock.set(q.ReadyAt)
	replay := q
	replay.Operation = execution.OperationReplay
	return f.engine.Begin(replay)
}

// A Slot whose trial was reserved - its own, or ahead from an earlier Slot -
// and which is then read as a replay supplies no evidence: the replay is not
// the formal first read the trial is compared with, at another time under
// another deadline. The trial is not compared, gives no evidence, and the next
// Slot's first attempt drops it, once, as next_slot_first; nothing stays
// reserved for a Slot that is not read normally.
func TestATrialWhoseSlotIsReplayedGivesNoEvidenceAndIsDroppedOnce(t *testing.T) {
	for _, c := range []struct {
		name  string
		ahead bool
	}{
		{"its own trial", false},
		{"reserved ahead", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.engine.options.OnEarlierRead = func(evidence EarlierReadEvidence) {
				t.Errorf("a replayed Slot's trial gave evidence %+v", evidence)
			}
			var ends []EarlierTrialEnd
			f.engine.options.OnEarlierTrialEnd = func(end EarlierTrialEnd) { ends = append(ends, end) }
			slot := f.clock.now().Unix()
			q := scheduledQuery(t, slot)
			read := q
			if c.ahead {
				// Past its own trial's start: the trial is the next Slot's.
				f.clock.set(q.ReadyAt.Add(-time.Minute))
				f.engine.Prepare(q)
				formalRead(f, q)
				read = scheduledQuery(t, slot+60)
				f.clock.set(read.ReadyAt.Add(-time.Minute))
				f.engine.Prepare(read)
			} else {
				f.engine.Prepare(q)
			}
			trial := preparedTrial(f)
			if trial == nil || trial.query.Contract.Slot != read.Contract.Slot || trial.ahead {
				t.Fatalf("fixture: reserved %+v for %d", trial, read.Contract.Slot.EvaluationTime)
			}
			f.clock.set(trial.at)
			f.answers <- full(point(int64(read.Contract.Slot.EvaluationTime), "1"))
			f.engine.StepEarly(context.Background())
			f.waitFor(func(stats Stats) bool { return stats.PendingBytes == summaryEntryBytes })
			if got := replayRead(f, read); got != nil {
				t.Fatalf("a replay was read as a formal first read: %+v", got)
			}
			if trial.formal {
				t.Fatal("the replay paired with the trial")
			}
			following := scheduledQuery(t, int64(read.Contract.Slot.EvaluationTime)+60)
			f.clock.set(following.ReadyAt.Add(-3 * time.Minute))
			f.engine.Prepare(following)
			f.engine.Step(context.Background())
			if got := aheadOvertaken(f); len(got) != 1 || got[OvertakenNextSlot] != 1 {
				t.Fatalf("overtaken by %v, want next_slot_first once", got)
			}
			if len(ends) != 1 || ends[0].Contract.Slot != read.Contract.Slot || ends[0].OvertakenBy != OvertakenNextSlot {
				t.Fatalf("trial ends %+v, want the replayed Slot's trial once", ends)
			}
			if preparedTrial(f) == trial {
				t.Fatal("the replayed Slot's trial is still reserved")
			}
		})
	}
}

// A trial reserved several Slots ahead outlives a Slot between it and its own
// that is read as a replay: that Slot's first attempt keeps the reservation
// standing, the replay does not touch it, and the Slot it is for adopts it and
// is compared with it as any Slot.
func TestAReplayedSlotBetweenLeavesATrialReservedPastItStanding(t *testing.T) {
	f := newFixture(t)
	observed := make(chan EarlierReadEvidence, 1)
	f.engine.options.OnEarlierRead = func(evidence EarlierReadEvidence) { observed <- evidence }
	slot := f.clock.now().Unix()
	q := scheduledQuery(t, slot)
	// Past the next Slot's trial start: the trial two Slots ahead.
	f.clock.set(time.Unix(slot+200, 0))
	f.engine.Prepare(q)
	trial := preparedTrial(f)
	if trial == nil || trial.aheadSlots != 2 {
		t.Fatalf("fixture: reserved %+v", trial)
	}
	formalRead(f, q)
	between := scheduledQuery(t, slot+60)
	f.engine.Prepare(between)
	if got := replayRead(f, between); got != nil {
		t.Fatalf("a replay was read as a formal first read: %+v", got)
	}
	target := scheduledQuery(t, slot+120)
	f.clock.set(trial.at)
	f.answers <- full(point(int64(target.Contract.Slot.EvaluationTime), "1"))
	f.engine.StepEarly(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.PendingBytes == summaryEntryBytes })
	f.clock.set(target.ReadyAt.Add(-time.Minute))
	f.engine.Prepare(target)
	if preparedTrial(f) != trial || trial.ahead {
		t.Fatalf("the Slot past the replay did not adopt its trial: %+v", trial)
	}
	formalRead(f, target)
	if evidence := <-observed; !evidence.Equal || evidence.Contract.Slot != target.Contract.Slot {
		t.Fatalf("evidence %+v, want the adopting Slot's equal trial", evidence)
	}
	if got := aheadOvertaken(f); len(got) != 0 {
		t.Fatalf("overtaken by %v", got)
	}
}

// A Slot sent to replay at its first classification is never prepared: a
// trial reserved ahead for it is not adopted, gives no evidence, and the next
// Slot prepared normally drops it once.
func TestATrialReservedForASlotNeverPreparedIsDroppedByTheNextSlot(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(evidence EarlierReadEvidence) {
		t.Errorf("a trial of a Slot never prepared gave evidence %+v", evidence)
	}
	slot := f.clock.now().Unix()
	q := scheduledQuery(t, slot)
	f.clock.set(q.ReadyAt.Add(-time.Minute))
	f.engine.Prepare(q)
	trial := preparedTrial(f)
	if trial == nil || !trial.ahead || trial.query.Contract.Slot.EvaluationTime != execution.EvaluationTime(slot+60) {
		t.Fatalf("fixture: reserved %+v", trial)
	}
	formalRead(f, q)
	diverted := scheduledQuery(t, slot+60)
	if got := replayRead(f, diverted); got != nil {
		t.Fatalf("a replay was read as a formal first read: %+v", got)
	}
	following := scheduledQuery(t, slot+120)
	f.clock.set(following.ReadyAt.Add(-3 * time.Minute))
	f.engine.Prepare(following)
	if got := aheadOvertaken(f); len(got) != 1 || got[OvertakenNextSlot] != 1 {
		t.Fatalf("overtaken by %v, want next_slot_first once", got)
	}
	if preparedTrial(f) == trial {
		t.Fatal("the trial of the Slot never prepared is still reserved")
	}
}
