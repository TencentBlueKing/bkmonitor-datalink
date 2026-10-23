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

// readings gives the source a reading of its own and an inherited one, each
// zero for none, and counts how often the inherited one is asked.
func readings(source *ProductionSlotSource, own, inherited time.Duration) *int {
	asked := 0
	source.queryLatency = func(group execution.QueryGroupIdentity) (time.Duration, bool) {
		return own, own > 0 && group == source.queryGroup
	}
	source.inheritedLatency = func(group execution.QueryGroupIdentity) (time.Duration, bool) {
		asked++
		return inherited, inherited > 0 && group == source.queryGroup
	}
	return &asked
}

// A round taken over with eight seconds left is judged by the group's own
// reading where there is one, by the reading its previous owner left in its
// read-hold record where there is not, and by the fallback only without
// either - in both directions, and named by where the floor came from.
func TestATakenOverRoundIsJudgedByItsOwnReadingThenTheInheritedOneThenTheFallback(t *testing.T) {
	reached := time.UnixMilli(divertDeadline - 8_000)
	for name, tc := range map[string]struct {
		fallback, own, inherited time.Duration
		divert                   bool
		floor                    float64
		source                   string
		inheritedAsked           int
	}{
		"own quick, inherited slow: own runs it":     {5 * time.Second, 6 * time.Second, 12 * time.Second, false, 0, "", 0},
		"own slow, inherited quick: own diverts it":  {5 * time.Second, 12 * time.Second, 6 * time.Second, true, 12, observability.TakeoverFloorObserved, 0},
		"no own, inherited slow: inherited diverts":  {5 * time.Second, 0, 12 * time.Second, true, 12, observability.TakeoverFloorInherited, 1},
		"no own, inherited quick: inherited runs it": {20 * time.Second, 0, 6 * time.Second, false, 0, "", 1},
		"neither, a short fallback runs it":          {5 * time.Second, 0, 0, false, 0, "", 1},
		"neither, a long fallback diverts it":        {10 * time.Second, 0, 0, true, 10, observability.TakeoverFloorFallback, 1},
	} {
		source, observer := divertSource(t, divertLimits(tc.fallback))
		asked := readings(source, tc.own, tc.inherited)
		operation, _, err := source.classifyRecovery(context.Background(), divertSlot, divertDeadline, 0, reached, testFence(7))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := operation == execution.OperationReplay; got != tc.divert {
			t.Fatalf("%s: %s, want diverted %v", name, operation, tc.divert)
		}
		if tc.divert && (len(observer.facts) != 1 || observer.facts[0].FloorSource != tc.source || observer.facts[0].FloorSeconds != tc.floor) {
			t.Fatalf("%s: facts %+v, want the %s floor of %vs named", name, observer.facts, tc.source, tc.floor)
		}
		// The inherited reading is asked only when there is no own one; a
		// classification asks the bound once, and once more for a round it
		// lets run (its decision).
		if tc.inheritedAsked == 0 && *asked != 0 {
			t.Fatalf("%s: the inherited reading was asked %d times beside an own one", name, *asked)
		}
		if tc.inheritedAsked != 0 && *asked == 0 {
			t.Fatalf("%s: the inherited reading was never asked", name)
		}
	}
}

// A round of a group held here, reached at the edge of its deadline, is
// judged the same way: with no reading of its own, by the inherited one,
// named so on its readiness_late diversion.
func TestAHeldGroupsReadinessIsJudgedByTheInheritedReading(t *testing.T) {
	source, observer := heldSource(t, 5*time.Second)
	readings(source, 0, 12*time.Second)
	if operation := classifyAt(t, source, divertDeadline, time.UnixMilli(divertDeadline-8_000)); operation != execution.OperationReplay {
		t.Fatalf("%s, want replay", operation)
	}
	if len(observer.diversions) != 1 || observer.diversions[0].BoundSource != observability.TakeoverFloorInherited ||
		observer.diversions[0].BoundSeconds != 12 || len(observer.facts) != 0 {
		t.Fatalf("diversions %+v, facts %+v: want one readiness_late on the inherited 12 s", observer.diversions, observer.facts)
	}
}

// A group whose whole budget is below the fallback is judged only by a
// reading, and an inherited reading is one: taken over a second before its
// deadline, a 2 s inherited reading diverts it, and a 0.5 s one lets it run
// - the fallback, which says nothing about such a group, is not applied
// beside it either way.
func TestAShortBudgetGroupIsJudgedByAnInheritedReading(t *testing.T) {
	deadline := int64(divertSlot)*1000 + 4_000
	reached := time.UnixMilli(deadline - 1_000)
	source, observer := divertSource(t, divertLimits(5*time.Second))
	readings(source, 0, 2*time.Second)
	if operation, _, err := source.classifyRecovery(context.Background(), divertSlot, deadline, 0, reached, testFence(7)); err != nil ||
		operation != execution.OperationReplay || len(observer.facts) != 1 ||
		observer.facts[0].FloorSource != observability.TakeoverFloorInherited || observer.facts[0].FloorSeconds != 2 {
		t.Fatalf("a 2 s inherited reading with 1 s left: %s %v, facts %+v, want diverted on it", operation, err, observer.facts)
	}
	source, observer = divertSource(t, divertLimits(5*time.Second))
	readings(source, 0, 500*time.Millisecond)
	if operation, _, err := source.classifyRecovery(context.Background(), divertSlot, deadline, 0, reached, testFence(7)); err != nil ||
		operation != execution.OperationNormal || len(observer.facts) != 0 {
		t.Fatalf("a 0.5 s inherited reading with 1 s left: %s %v, facts %+v, want a normal round", operation, err, observer.facts)
	}
}

// A round let run keeps the bound it was judged on and where it came from:
// the inherited reading is named inherited on its decision, apart from the
// group's own query duration and the fallback.
func TestARoundLetRunNamesAnInheritedBound(t *testing.T) {
	at := time.UnixMilli(divertDeadline - 20_000)
	for name, tc := range map[string]struct {
		own, inherited time.Duration
		millis         int64
		source         string
	}{
		"own":       {6 * time.Second, 9 * time.Second, 6_000, observability.DivertBoundQueryDuration},
		"inherited": {0, 9 * time.Second, 9_000, observability.DivertBoundInherited},
		"fallback":  {0, 0, 5_000, observability.DivertBoundFallback},
	} {
		source, _ := divertSource(t, divertLimits(5*time.Second))
		readings(source, tc.own, tc.inherited)
		operation, facts, err := source.classifyRecovery(context.Background(), divertSlot, divertDeadline, 0, at, testFence(7))
		if err != nil || operation != execution.OperationNormal || facts.Decision == nil {
			t.Fatalf("%s: %s %+v %v, want a normal round with its decision", name, operation, facts, err)
		}
		if facts.Decision.BoundSource != tc.source || facts.Decision.BoundMillis != tc.millis {
			t.Fatalf("%s: decision %+v, want %s of %d ms", name, facts.Decision, tc.source, tc.millis)
		}
	}
}

// Without the option the source has no inherited reading, as every source
// built before it.
func TestWithoutTheOptionNoReadingIsInherited(t *testing.T) {
	source, _ := divertSource(t, divertLimits(5*time.Second))
	if _, _, ok := source.queryReading(); ok {
		t.Fatal("a source without readings has one")
	}
	if err := WithInheritedQueryLatency(func(execution.QueryGroupIdentity) (time.Duration, bool) { return time.Second, true })(source); err != nil {
		t.Fatal(err)
	}
	if got, from, ok := source.queryReading(); !ok || got != time.Second || from != observability.TakeoverFloorInherited {
		t.Fatalf("with the option: %v %q %v", got, from, ok)
	}
}
