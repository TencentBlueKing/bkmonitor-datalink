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
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func heldQuery(f *fixture) Query {
	slot := f.clock.now().Unix()
	q := query("qg", slot, minute, sourceLog)
	q.Contract.ReadHoldMillis = (2 * time.Minute).Milliseconds()
	q.ReadyAt = f.clock.now().Add(3 * time.Minute) // source delay + h
	q.Spec.PlanFacts.QueryDelaySeconds = 60
	return q
}

func TestEarlierReadIsReservedOnceAndComparedOnlyWithTheFormalFirstRead(t *testing.T) {
	for _, equal := range []bool{true, false} {
		t.Run(map[bool]string{true: "equal", false: "different"}[equal], func(t *testing.T) {
			f := newFixture(t)
			observed := make(chan EarlierReadEvidence, 1)
			f.engine.options.OnEarlierRead = func(evidence EarlierReadEvidence) {
				_ = f.engine.Stats() // callbacks cannot hold Engine.mu
				observed <- evidence
			}
			q := heldQuery(f)
			for index := 0; index < 4; index++ {
				f.engine.Prepare(q)
			}
			trial := f.group("qg").prepared
			if trial == nil || trial.candidateHold != time.Minute || !trial.readyAt.Equal(q.ReadyAt.Add(-time.Minute)) || !trial.at.Equal(trial.readyAt.Add(-RecheckTimeout)) {
				t.Fatalf("prepared %+v", trial)
			}
			if stats := f.engine.Stats(); stats.Pending != 1 || stats.Sources[sourceLog].FirstReads != 0 {
				t.Fatalf("Prepare is not one reservation: %+v", stats)
			}
			f.clock.set(trial.at)
			f.answers <- full(point(int64(q.Contract.Slot.EvaluationTime), "1"))
			f.engine.StepEarly(context.Background())
			f.waitFor(func(stats Stats) bool { return stats.PendingBytes == summaryEntryBytes })
			if len(observed) != 0 {
				t.Fatal("an earlier read was evidence before its formal first read")
			}
			f.clock.set(q.ReadyAt)
			read := f.engine.Begin(q)
			if read.summary == nil || read.earlier != trial {
				t.Fatal("Begin did not continue the reserved sample")
			}
			if f.engine.Stats().PendingBytes != summaryEntryBytes {
				t.Fatal("the admitted earlier summary was dropped before the formal read ended")
			}
			value := "1"
			if !equal {
				value = "2"
			}
			read.Series(point(int64(q.Contract.Slot.EvaluationTime), value), 100)
			read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
			evidence := <-observed
			if !evidence.Observed || evidence.Equal != equal || evidence.Contract != q.Contract || evidence.CandidateHold != time.Minute {
				t.Fatalf("comparison %+v", evidence)
			}
			f.mu.Lock()
			calls := len(f.specs)
			f.mu.Unlock()
			if calls != 1 || f.group("qg").prepared != nil {
				t.Fatalf("%d earlier reads, retained trial=%+v", calls, f.group("qg").prepared)
			}
		})
	}
}

func TestEarlierRefusalOrIncompleteReadsSupplyNoDecreaseEvidence(t *testing.T) {
	for _, outcome := range []string{EarlierPermitRefused, EarlierMemoryRefused, EarlierReadFailed, EarlierFirstIncomplete, EarlierOvertaken, EarlierOwnerLost, EarlierMultiQuery} {
		t.Run(outcome, func(t *testing.T) {
			f := newFixture(t)
			observed := make(chan EarlierReadEvidence, 1)
			f.engine.options.OnEarlierRead = func(evidence EarlierReadEvidence) { observed <- evidence }
			q := heldQuery(f)
			f.engine.Prepare(q)
			trial := f.group("qg").prepared
			f.clock.set(trial.at)
			switch outcome {
			case EarlierPermitRefused:
				f.set(func() { f.refuse = "waiters" })
			case EarlierMemoryRefused:
				f.engine.options.Memory = func(uint64) bool { return false }
			case EarlierMultiQuery:
				other := q
				other.Spec.Digest += "-other"
				f.engine.Prepare(other)
			}
			if outcome != EarlierOvertaken && outcome != EarlierMultiQuery {
				if outcome != EarlierPermitRefused && outcome != EarlierMemoryRefused {
					if outcome == EarlierReadFailed {
						f.answers <- func(execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
							return execution.ProviderCompletion{Completeness: execution.CompletenessPartial}, nil
						}
					} else {
						f.answers <- full(point(int64(q.Contract.Slot.EvaluationTime), "1"))
					}
				}
				f.engine.StepEarly(context.Background())
				f.waitFor(func(Stats) bool {
					f.engine.mu.Lock()
					defer f.engine.mu.Unlock()
					return trial.done
				})
			}
			f.clock.set(q.ReadyAt)
			read := f.engine.Begin(q)
			read.Series(point(int64(q.Contract.Slot.EvaluationTime), "1"), 100)
			completion := execution.CompletenessFull
			if outcome == EarlierFirstIncomplete {
				completion = execution.CompletenessPartial
			}
			if outcome == EarlierOwnerLost {
				f.set(func() { f.owned["qg"] = false })
			}
			read.Complete(execution.ProviderCompletion{Completeness: completion}, nil)
			evidence := <-observed
			if evidence.Observed || evidence.Equal || evidence.Outcome != outcome {
				t.Fatalf("a %s read supplied comparison evidence: %+v", outcome, evidence)
			}
			if f.engine.Stats().Sources[sourceLog].EarlierReads[outcome] != 1 {
				t.Fatal("the unobserved outcome was not counted once")
			}
		})
	}
}

func TestEarlierReadUsesTheBaseHoldDuringADecreaseTransition(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	f.engine.options.CurrentReadHold = func(execution.QueryGroupIdentity) time.Duration { return 130 * time.Second }
	q := heldQuery(f)
	q.Contract.ReadHoldMillis = 300_000
	q.ReadyAt = f.clock.now().Add(360 * time.Second)
	f.engine.Prepare(q)
	trial := f.group("qg").prepared
	if trial.candidateHold != 65*time.Second || !trial.readyAt.Equal(f.clock.now().Add(125*time.Second)) {
		t.Fatalf("transition trial %+v: want baseline60 + base130/2, not half the frozen 300", trial)
	}
	other := q
	other.Operation, other.AttemptNo = execution.OperationRetry, 2
	f.engine.Prepare(other)
	if f.group("qg").prepared != trial {
		t.Fatal("a retry took another sample")
	}
}

func TestAnEarlierReadThatFinishesAfterCandidateReadinessIsUnobserved(t *testing.T) {
	f := newFixture(t)
	observed := make(chan EarlierReadEvidence, 1)
	f.engine.options.OnEarlierRead = func(item EarlierReadEvidence) { observed <- item }
	q := heldQuery(f)
	f.engine.Prepare(q)
	trial := f.group("qg").prepared
	f.clock.set(trial.at)
	f.answers <- func(sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		f.clock.set(trial.readyAt.Add(time.Millisecond))
		return full(point(int64(q.Contract.Slot.EvaluationTime), "1"))(sink)
	}
	f.engine.StepEarly(context.Background())
	f.waitFor(func(Stats) bool {
		f.engine.mu.Lock()
		defer f.engine.mu.Unlock()
		return trial.done
	})
	f.clock.set(q.ReadyAt)
	read := f.engine.Begin(q)
	read.Series(point(int64(q.Contract.Slot.EvaluationTime), "1"), 100)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	if item := <-observed; item.Observed || item.Outcome != EarlierOvertaken {
		t.Fatalf("post-candidate success supplied decrease evidence: %+v", item)
	}
}

func TestAStaleCandidateCannotSupplyEvidenceForANewBaseHold(t *testing.T) {
	f := newFixture(t)
	observed := make(chan EarlierReadEvidence, 1)
	f.engine.options.OnEarlierRead = func(item EarlierReadEvidence) { observed <- item }
	base := 2 * time.Minute
	f.engine.options.CurrentReadHold = func(execution.QueryGroupIdentity) time.Duration { return base }
	q := heldQuery(f)
	f.engine.Prepare(q)
	trial := f.group("qg").prepared
	f.clock.set(trial.at)
	f.answers <- full(point(int64(q.Contract.Slot.EvaluationTime), "1"))
	f.engine.StepEarly(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.PendingBytes == summaryEntryBytes })
	base = time.Minute // h/2 was 60 seconds; the new candidate would be 30.
	f.clock.set(q.ReadyAt)
	read := f.engine.Begin(q)
	read.Series(point(int64(q.Contract.Slot.EvaluationTime), "1"), 100)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	if item := <-observed; item.Observed || item.Outcome != EarlierOvertaken {
		t.Fatalf("stale candidate supplied evidence for the new hold: %+v", item)
	}
}

func TestAnEarlierReadDoesNotStartBeforeReadinessWithoutTheHold(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	q := heldQuery(f)
	q.Contract.ReadHoldMillis = 8_000
	q.ReadyAt = f.clock.now().Add(68 * time.Second)
	f.engine.Prepare(q)
	trial := f.group("qg").prepared
	// Half of 8 s is less than a step: the trial tries no hold at all, at
	// the readiness without h and no earlier.
	baseline := q.ReadyAt.Add(-8 * time.Second)
	if trial.candidateHold != 0 || !trial.at.Equal(baseline) || !trial.readyAt.Equal(baseline) {
		t.Fatalf("short hold trial %+v does not try zero at the no-h readiness %v", trial, baseline)
	}
	f.clock.set(baseline.Add(-time.Millisecond))
	f.engine.StepEarly(context.Background())
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.specs) != 0 {
		t.Fatal("trial read started before readiness without h")
	}
}

func TestASelectedSampleMissedBeforePrepareRemainsUnobserved(t *testing.T) {
	f := newFixture(t)
	observed := make(chan EarlierReadEvidence, 1)
	f.engine.options.OnEarlierRead = func(item EarlierReadEvidence) { observed <- item }
	q := heldQuery(f)
	f.clock.set(q.ReadyAt.Add(-30 * time.Second))
	f.engine.Prepare(q)
	f.engine.StepEarly(context.Background())
	f.mu.Lock()
	count := len(f.specs)
	f.mu.Unlock()
	if count != 0 {
		t.Fatal("a missed candidate was read late")
	}
	f.clock.set(q.ReadyAt)
	read := f.engine.Begin(q)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	if item := <-observed; item.Observed || item.Outcome != EarlierOvertaken {
		t.Fatalf("a missed candidate supplied evidence: %+v", item)
	}
}

func TestWholeWindowArrivalPublishesTheFirstConfirmedAgeBeforeTheDeepProbe(t *testing.T) {
	f := newFixture(t)
	evidence := make(chan ReadHoldEvidence, 2)
	f.engine.options.OnWholeWindowReadEarly = func(item ReadHoldEvidence) {
		_ = f.engine.Stats()
		evidence <- item
	}
	readAt := f.clock.now()
	slot := readAt.Unix() - 120
	q := query("qg", slot, minute, sourceLog)
	q.Contract.ReadHoldMillis = 60_000
	q.Spec.PlanFacts.QueryDelaySeconds = 60
	q.ReadyAt = readAt.Add(-30 * time.Second) // 30 seconds waiting for a permit is not the readiness baseline
	f.capture(q, point(slot, "1"))
	f.recheck(sourceLog, readAt, 0, minute, full(point(slot, "3")), RecheckCompared)
	if len(evidence) != 0 {
		t.Fatal("one revised value was treated as confirmed arrival")
	}
	f.recheck(sourceLog, readAt, 1, minute, full(point(slot, "3")), RecheckCompared)
	select {
	case item := <-evidence:
		if item.Contract != q.Contract || item.FirstReadAge != 90*time.Second || item.ArrivalAge != 210*time.Second || item.Rung != RungNames[0] || !reflect.DeepEqual(item.Buckets, []int64{slot - 60}) {
			t.Fatalf("whole-window evidence %+v", item)
		}
	case <-time.After(time.Second):
		t.Fatal("confirmed arrival waited for the deep probe")
	}
	if f.group("qg").probe == nil {
		t.Fatal("the existing deep probe was lost")
	}
	f.recheck(sourceLog, readAt, len(RungNames)-1, minute, full(point(slot, "3")), RecheckCompared)
	if len(evidence) != 0 {
		t.Fatal("the deep probe published the same arrival twice")
	}
}

func TestPartialRevisionAndVanishingValuesDoNotRaiseTheReadHold(t *testing.T) {
	for _, partial := range []bool{true, false} {
		t.Run(map[bool]string{true: ClassPartialRevised, false: IgnoredNoWholeWindowArrival}[partial], func(t *testing.T) {
			f := newFixture(t)
			arrived := make(chan ReadHoldEvidence, 1)
			ignored := make(chan string, 1)
			f.engine.options.OnWholeWindowReadEarly = func(evidence ReadHoldEvidence) { arrived <- evidence }
			f.engine.options.OnReadHoldIgnored = func(_ ReadHoldEvidence, reason string) { ignored <- reason }
			slot := f.clock.now().Unix()
			first := []*execution.Dataset{point(slot, "1")}
			if partial {
				first = append(first, dataset("h2", map[int64]string{slot - 60: "1"}))
			}
			f.classSample(0, first, func(slot int64) []*execution.Dataset {
				if partial {
					return []*execution.Dataset{point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "3"})}
				}
				return nil
			})
			if len(arrived) != 0 {
				t.Fatal("a partial revision or disappearing value raised the whole-window hold")
			}
			reason := map[bool]string{true: ClassPartialRevised, false: IgnoredNoWholeWindowArrival}[partial]
			select {
			case got := <-ignored:
				if got != reason || f.engine.Stats().Sources[sourceLog].ReadHoldIgnored[reason] != 1 {
					t.Fatalf("ignored=%s want %s", got, reason)
				}
			case <-time.After(time.Second):
				t.Fatal("ignored evidence was not counted separately")
			}
		})
	}
}

func TestDirectedNextReadSubtractsTheFrozenHoldAndUsesTheNextSlotsEffectiveHold(t *testing.T) {
	f, _ := earlyFixture(t, SupplementOutcome{})
	readAt := f.clock.now()
	slot := readAt.Unix() - 175
	f.engine.options.CurrentReadHold = func(execution.QueryGroupIdentity) time.Duration { return 30 * time.Second }
	nextHold := 55 * time.Second
	f.engine.options.ReadHoldAt = func(_ execution.QueryGroupIdentity, evaluation execution.EvaluationTime) time.Duration {
		if evaluation != execution.EvaluationTime(slot+60) {
			t.Errorf("hold was asked for Slot %d", evaluation)
		}
		return nextHold
	}
	q := earlyQuery(slot, readAt, 60)
	q.Contract.ReadHoldMillis = 115_000
	read := f.engine.Begin(q)
	read.Series(point(slot, "1"), 100)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	f.engine.mu.Lock()
	directed := f.engine.groups["qg"].directed[execution.EvaluationTime(slot)]
	next := directed.early.next
	f.engine.mu.Unlock()
	if !next.Equal(readAt) {
		t.Fatalf("R1=%v want %v: readyAt+60-115+55", next, readAt)
	}
	// A decrease whose next ready point has already passed is named overtaken.
	nextHold = 30 * time.Second
	f.engine.StepEarly(context.Background())
	if f.early()[EarlyOvertaken] != 1 {
		t.Fatalf("decreased hold did not overtake the early read: %+v", f.early())
	}
}

func TestADecreaseThatOvertakesTheFirstReadIsNamedOvertaken(t *testing.T) {
	f, _ := earlyFixture(t, SupplementOutcome{})
	readAt := f.clock.now()
	slot := readAt.Unix() - 175
	f.engine.options.ReadHoldAt = func(execution.QueryGroupIdentity, execution.EvaluationTime) time.Duration {
		return 55 * time.Second
	}
	q := earlyQuery(slot, readAt, 60)
	q.Contract.ReadHoldMillis = 115_000
	read := f.engine.Begin(q)
	read.Series(point(slot, "1"), 100)
	f.clock.set(readAt.Add(time.Second))
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	if f.early()[EarlyOvertaken] != 1 || f.early()[EarlyAnchorPassed] != 0 {
		t.Fatalf("hold decrease while first read ran: %+v", f.early())
	}
}
