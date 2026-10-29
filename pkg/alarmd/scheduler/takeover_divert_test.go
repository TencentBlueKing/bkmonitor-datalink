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

const divertSlot = execution.EvaluationTime(1_700_124_000)

// divertDeadline is a minute Slot's query deadline: its minute less a
// five-second reserve.
var divertDeadline = int64(divertSlot)*1000 + 55_000

func divertLimits(floor time.Duration) RecoveryLimits {
	limits := testRecoveryLimits()
	limits.TakeoverDivertFloor = floor
	return limits
}

// divertSource is one Query Group's source under limits, with its own
// takeover clock and an observer of its takeover facts.
func divertSource(t *testing.T, limits RecoveryLimits) (*ProductionSlotSource, *takeoverObserver) {
	t.Helper()
	source, _ := replayClassificationSource(t, 60, divertSlot, 30*time.Second, limits)
	observer := &takeoverObserver{}
	source.observer, source.takeovers = observer, NewTakeoverClock()
	return source, observer
}

// A rebalance hands a worker more Query Groups than it has query permits a
// second and a half before their current round's deadline. Started as
// normal rounds they would queue for the permits and, at the deadline, the
// waiting ones would give up and the running ones time out with no data.
// Every one goes to replay before it starts instead, and each says so once.
func TestRoundsTakenOverJustBeforeTheirDeadlineGoToReplay(t *testing.T) {
	limits := divertLimits(5 * time.Second)
	groups := 3 * limits.ProcessQueryPermits
	reached := time.UnixMilli(divertDeadline - 1_500)
	diverted := 0
	for index := 0; index < groups; index++ {
		source, observer := divertSource(t, limits)
		operation, facts, err := source.classifyRecovery(context.Background(), divertSlot, divertDeadline, 0, reached, testFence(7))
		if err != nil {
			t.Fatalf("group %d: classifyRecovery() error = %v", index, err)
		}
		if operation != execution.OperationReplay || facts.Disposition != ReplayEligible || facts.Age != 0 ||
			facts.RecheckAtUnixMilli != divertDeadline || facts.validate(operation) != nil {
			t.Fatalf("group %d: %s %+v, want replay now, not behind, rechecked at its deadline", index, operation, facts)
		}
		if len(observer.facts) != 1 || observer.facts[0].Outcome != observability.ReplayTakeoverDiverted ||
			observer.facts[0].RemainingSeconds != 1.5 || observer.facts[0].FloorSeconds != 5 ||
			observer.facts[0].FloorSource != observability.TakeoverFloorFallback {
			t.Fatalf("group %d: takeover facts %+v, want one diverted by the fallback floor", index, observer.facts)
		}
		// Classified again on the next tick it is the same diversion, said once.
		if again, _, _ := source.classifyRecovery(context.Background(), divertSlot, divertDeadline, 0, reached.Add(time.Second/2), testFence(7)); again != execution.OperationReplay || len(observer.facts) != 1 {
			t.Fatalf("group %d: again %s with facts %+v", index, again, observer.facts)
		}
		diverted++
	}
	if diverted != groups {
		t.Fatalf("diverted %d of %d", diverted, groups)
	}
}

// A takeover in the middle of a round leaves it most of its budget: it runs
// as the normal round it is.
func TestARoundTakenOverMidMinuteRunsAsNormal(t *testing.T) {
	source, observer := divertSource(t, divertLimits(5*time.Second))
	operation, facts, err := source.classifyRecovery(context.Background(), divertSlot, divertDeadline, 0, time.UnixMilli(divertDeadline-30_000), testFence(7))
	if err != nil || operation != execution.OperationNormal || facts.Disposition != ReplayLive || len(observer.facts) != 0 {
		t.Fatalf("%s %+v %v, facts %+v: want a normal live round", operation, facts, err, observer.facts)
	}
}

// The group's own reading of its query decides when there is one: a query
// that takes longer than the time left is diverted though the fallback would
// let it run, and one that takes less runs though the fallback would divert.
func TestTheGroupsOwnQueryDurationDecidesOverTheFallback(t *testing.T) {
	reached := time.UnixMilli(divertDeadline - 8_000)
	for name, tc := range map[string]struct {
		fallback time.Duration
		observed time.Duration
		divert   bool
	}{
		"a slow query, a short fallback": {5 * time.Second, 12 * time.Second, true},
		"a quick query, a long fallback": {20 * time.Second, 6 * time.Second, false},
	} {
		source, observer := divertSource(t, divertLimits(tc.fallback))
		source.queryLatency = func(group execution.QueryGroupIdentity) (time.Duration, bool) {
			return tc.observed, group == source.queryGroup
		}
		operation, _, err := source.classifyRecovery(context.Background(), divertSlot, divertDeadline, 0, reached, testFence(7))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := operation == execution.OperationReplay; got != tc.divert {
			t.Fatalf("%s: %s, want diverted %v", name, operation, tc.divert)
		}
		if tc.divert && (len(observer.facts) != 1 || observer.facts[0].FloorSource != observability.TakeoverFloorObserved || observer.facts[0].FloorSeconds != 12) {
			t.Fatalf("%s: facts %+v, want the observed floor named", name, observer.facts)
		}
	}
}

// A round due after this process took its Query Group over is its own: short
// of time, it is not a takeover's and says nothing as one. It goes to replay
// for the same reason any round of a group held here does (readiness_late).
func TestARoundDueAfterTheTakeoverIsNotATakeoverDiversion(t *testing.T) {
	source, observer := divertSource(t, divertLimits(5*time.Second))
	source.takeovers.Anchor(source.queryGroup, testFence(7), time.Unix(int64(divertSlot)-30, 0))
	operation, facts, err := source.classifyRecovery(context.Background(), divertSlot, divertDeadline, 0, time.UnixMilli(divertDeadline-1_500), testFence(7))
	if err != nil || operation != execution.OperationReplay || facts.Disposition != ReplayEligible || len(observer.facts) != 0 ||
		len(observer.diversions) != 1 || observer.diversions[0].Reason != observability.SlotDiversionReadinessLate {
		t.Fatalf("%s %+v %v, takeover facts %+v, diversions %+v: want replay as readiness_late, no takeover fact",
			operation, facts, err, observer.facts, observer.diversions)
	}
}

// Without a floor the diversion is off, as for every scheduler built before
// it; a floor below zero or at the replay age is refused.
func TestTheTakeoverDivertFloorIsBounded(t *testing.T) {
	source, observer := divertSource(t, divertLimits(0))
	if operation, _, _ := source.classifyRecovery(context.Background(), divertSlot, divertDeadline, 0, time.UnixMilli(divertDeadline-1_500), testFence(7)); operation != execution.OperationNormal || len(observer.facts) != 0 {
		t.Fatalf("no floor: %s, facts %+v", operation, observer.facts)
	}
	for _, floor := range []time.Duration{-time.Second, testRecoveryLimits().MaxReplayAge} {
		if err := divertLimits(floor).Validate(); err == nil {
			t.Fatalf("floor %v accepted", floor)
		}
	}
	if err := divertLimits(5 * time.Second).Validate(); err != nil {
		t.Fatalf("floor 5s refused: %v", err)
	}
}

// A short-period group's every round has less budget than the fallback, so
// the fallback says nothing about it: taken over a second before its
// deadline it runs as normal. Its own reading still decides when there is
// one.
func TestAShortPeriodGroupIsJudgedOnlyByItsOwnReading(t *testing.T) {
	deadline := int64(divertSlot)*1000 + 4_000
	reached := time.UnixMilli(deadline - 1_000)
	source, observer := divertSource(t, divertLimits(5*time.Second))
	if operation, _, err := source.classifyRecovery(context.Background(), divertSlot, deadline, 0, reached, testFence(7)); err != nil || operation != execution.OperationNormal || len(observer.facts) != 0 {
		t.Fatalf("no reading: %s %v, facts %+v, want normal", operation, err, observer.facts)
	}
	source, observer = divertSource(t, divertLimits(5*time.Second))
	source.queryLatency = func(execution.QueryGroupIdentity) (time.Duration, bool) { return 2 * time.Second, true }
	if operation, _, err := source.classifyRecovery(context.Background(), divertSlot, deadline, 0, reached, testFence(7)); err != nil || operation != execution.OperationReplay || len(observer.facts) != 1 {
		t.Fatalf("a 2s reading with 1s left: %s %v, facts %+v, want diverted", operation, err, observer.facts)
	}
}
