// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// heldSource is a Query Group this process has held since before the Slot,
// so no Slot of it is a takeover's.
func heldSource(t *testing.T, fallback time.Duration) (*ProductionSlotSource, *takeoverObserver) {
	t.Helper()
	source, observer := divertSource(t, divertLimits(fallback))
	source.takeovers.Anchor(source.queryGroup, testFence(7), time.Unix(int64(divertSlot)-120, 0))
	return source, observer
}

func classifyAt(t *testing.T, source *ProductionSlotSource, deadline int64, at time.Time) execution.Operation {
	t.Helper()
	operation, facts, err := source.classifyRecovery(context.Background(), divertSlot, deadline, 0, at, testFence(7))
	if err != nil {
		t.Fatalf("classifyRecovery() error = %v", err)
	}
	if facts.validate(operation) != nil {
		t.Fatalf("%s with facts %+v", operation, facts)
	}
	return operation
}

// A read that would start with exactly its query's time left before the
// deadline still runs; a millisecond later it could not finish, and the
// round goes to replay before it starts, said once as readiness_late with the
// time it would have had and the bound it fell short of.
func TestAReadStartingShortOfItsQueryTimeGoesToReplay(t *testing.T) {
	source, observer := heldSource(t, 5*time.Second)
	if operation := classifyAt(t, source, divertDeadline, time.UnixMilli(divertDeadline-5_000)); operation != execution.OperationNormal || len(observer.diversions) != 0 {
		t.Fatalf("at deadline - bound: %s, diversions %+v, want a normal round", operation, observer.diversions)
	}
	late := time.UnixMilli(divertDeadline - 5_000 + 1)
	if operation := classifyAt(t, source, divertDeadline, late); operation != execution.OperationReplay {
		t.Fatalf("a millisecond past deadline - bound: %s, want replay", operation)
	}
	if len(observer.diversions) != 1 || len(observer.facts) != 0 {
		t.Fatalf("diversions %+v, takeover facts %+v: want one readiness_late and no takeover fact", observer.diversions, observer.facts)
	}
	got := observer.diversions[0]
	if got.Reason != observability.SlotDiversionReadinessLate || got.RemainingSeconds != 4.999 || got.BoundSeconds != 5 ||
		got.BoundSource != observability.TakeoverFloorFallback {
		t.Fatalf("diversion %+v", got)
	}
	// Classified again on the next tick it is the same diversion, said once.
	if operation := classifyAt(t, source, divertDeadline, late.Add(300*time.Millisecond)); operation != execution.OperationReplay || len(observer.diversions) != 1 {
		t.Fatalf("again: %s, diversions %+v", operation, observer.diversions)
	}
}

// A round reached with most of its budget left runs as normal and says
// nothing.
func TestARoundWithBudgetToSpareIsNotDiverted(t *testing.T) {
	source, observer := heldSource(t, 5*time.Second)
	if operation := classifyAt(t, source, divertDeadline, time.UnixMilli(divertDeadline-30_000)); operation != execution.OperationNormal ||
		len(observer.diversions) != 0 || len(observer.facts) != 0 {
		t.Fatalf("%s, diversions %+v, facts %+v: want a normal round", operation, observer.diversions, observer.facts)
	}
}

// The group's own reading of its query is the bound when there is one, in
// both directions: a query slower than the time left is diverted though the
// fallback would let it run, and a quicker one runs though the fallback would
// divert it.
func TestTheGroupsOwnReadingIsTheReadinessBound(t *testing.T) {
	at := time.UnixMilli(divertDeadline - 8_000)
	for name, tc := range map[string]struct {
		fallback, observed time.Duration
		divert             bool
	}{
		"a slow query, a short fallback": {5 * time.Second, 12 * time.Second, true},
		"a quick query, a long fallback": {20 * time.Second, 6 * time.Second, false},
	} {
		source, observer := heldSource(t, tc.fallback)
		source.queryLatency = func(group execution.QueryGroupIdentity) (time.Duration, bool) {
			return tc.observed, group == source.queryGroup
		}
		if got := classifyAt(t, source, divertDeadline, at) == execution.OperationReplay; got != tc.divert {
			t.Fatalf("%s: diverted %v, want %v", name, got, tc.divert)
		}
		if tc.divert && (len(observer.diversions) != 1 || observer.diversions[0].BoundSource != observability.TakeoverFloorObserved ||
			observer.diversions[0].BoundSeconds != 12) {
			t.Fatalf("%s: diversions %+v, want the observed bound named", name, observer.diversions)
		}
	}
}

// A group whose whole budget is below the fallback is judged only by its own
// reading: without one its round runs however little is left, with one it
// is diverted when the reading does not fit.
func TestAShortBudgetGroupIsJudgedOnlyByItsOwnReadingForReadiness(t *testing.T) {
	deadline := int64(divertSlot)*1000 + 4_000
	at := time.UnixMilli(deadline - 1_000)
	source, observer := heldSource(t, 5*time.Second)
	if operation := classifyAt(t, source, deadline, at); operation != execution.OperationNormal || len(observer.diversions) != 0 {
		t.Fatalf("no reading: %s, diversions %+v, want normal", operation, observer.diversions)
	}
	source, observer = heldSource(t, 5*time.Second)
	source.queryLatency = func(execution.QueryGroupIdentity) (time.Duration, bool) { return 2 * time.Second, true }
	if operation := classifyAt(t, source, deadline, at); operation != execution.OperationReplay || len(observer.diversions) != 1 {
		t.Fatalf("a 2s reading with 1s left: %s, diversions %+v, want diverted", operation, observer.diversions)
	}
}

// A round due before a takeover and short of time meets both rules; it is
// diverted once and reported as the takeover's, not as readiness_late too.
func TestATakenOverRoundShortOfTimeIsReportedAsTheTakeovers(t *testing.T) {
	source, observer := divertSource(t, divertLimits(5*time.Second))
	if operation := classifyAt(t, source, divertDeadline, time.UnixMilli(divertDeadline-1_500)); operation != execution.OperationReplay {
		t.Fatalf("%s, want replay", operation)
	}
	if len(observer.facts) != 1 || observer.facts[0].Outcome != observability.ReplayTakeoverDiverted || len(observer.diversions) != 0 {
		t.Fatalf("takeover facts %+v, diversions %+v: want the takeover's diverted only", observer.facts, observer.diversions)
	}
}

// A Query Group the degraded pool holds keeps its rounds where they are: its
// replay could not run.
func TestACooldownHeldGroupIsNotDivertedForReadiness(t *testing.T) {
	source, observer := heldSource(t, 5*time.Second)
	operation, _, err := source.classifyRecovery(withQueryCooldownHeld(context.Background(), true), divertSlot, divertDeadline, 0,
		time.UnixMilli(divertDeadline-1_000), testFence(7))
	if err != nil || operation != execution.OperationNormal || len(observer.diversions) != 0 {
		t.Fatalf("%s %v, diversions %+v: want normal", operation, err, observer.diversions)
	}
}

// The time a held round is classified at is when its read would start: a
// round deferred for readiness is not run again where it stood, the Runner
// asks the source for it again at the readiness access named, and the source
// classifies it then. So the bound is measured against the readiness access
// computed, not a time the scheduler guessed.
func TestARoundDeferredForReadinessIsClassifiedAgainAtItsReadiness(t *testing.T) {
	current := time.UnixMilli(1_700_000_000_000)
	readyAt := current.Add(50 * time.Second)
	slot := frozenSlot("query-group-1")
	executor := &readinessDeferredExecutor{readyAt: readyAt}
	inner := &fakeSlotSource{slot: slot, facts: SlotDueFacts{IntervalSeconds: 60}}
	source := &clockedSlotSource{inner: inner, now: func() time.Time { return current }}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: slot.Dispatch.OwnerFence}, source, executor,
		NewFlightCoordinator(), func() time.Time { return current })
	if err != nil {
		t.Fatal(err)
	}
	if _, attempted, err := runner.RunOne(context.Background()); err != nil || !attempted {
		t.Fatalf("RunOne(deferred) = attempted %t, err %v", attempted, err)
	}
	current = readyAt
	executor.deferred = false
	if _, _, err := runner.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne(at readiness) = %v", err)
	}
	if len(source.asked) != 2 || !source.asked[1].Equal(readyAt) {
		t.Fatalf("source asked at %v, want once at the start and again at the readiness %s", source.asked, readyAt)
	}
}

// clockedSlotSource records when the Runner asks it for a Slot.
type clockedSlotSource struct {
	inner *fakeSlotSource
	now   func() time.Time
	asked []time.Time
}

func (source *clockedSlotSource) Next(ctx context.Context, group execution.QueryGroupIdentity) (FrozenSlot, bool, SlotDueFacts, error) {
	source.asked = append(source.asked, source.now())
	return source.inner.Next(ctx, group)
}
