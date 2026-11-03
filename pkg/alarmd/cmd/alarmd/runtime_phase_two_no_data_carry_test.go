// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// editThreshold changes only the item's threshold: a detection edit, which
// moves the Plan's state generation and changes nothing about no-data.
func editThreshold(item map[string]any) {
	for _, algorithm := range item["algorithms"].([]any) {
		for _, group := range algorithm.(map[string]any)["config"].([]any) {
			for _, condition := range group.([]any) {
				condition.(map[string]any)["threshold"] = 85
			}
		}
	}
}

// editCondition adds a filter to the item's query: the same metric, grouped
// the same way, over other data.
func editCondition(item map[string]any) {
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["agg_condition"] = []any{map[string]any{"key": "device", "method": "eq", "value": []any{"cpu0"}}}
}

// editGrouping adds a grouping dimension to the item's query.
func editGrouping(item map[string]any) {
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["agg_dimension"] = []any{"bk_target_ip", "bk_target_cloud_id", "device"}
}

// both applies edits in order.
func both(edits ...func(map[string]any)) func(map[string]any) {
	return func(item map[string]any) {
		for _, edit := range edits {
			edit(item)
		}
	}
}

// memoryAbsenceIn is a group's stored absence under one memory key, zero when
// it is held as present or not held.
func (fixture *lifecycleFixture) memoryAbsenceIn(key, ip string) (firstAbsent int64, held bool) {
	fixture.t.Helper()
	value, err := fixture.redis.HGet(context.Background(), key, "g:"+followsGroupKey(ip)).Result()
	if err != nil {
		return 0, false
	}
	if value == "p" {
		return 0, true
	}
	var absence struct {
		FirstAbsent int64 `json:"first_absent"`
	}
	if err := json.Unmarshal([]byte(value), &absence); err != nil {
		fixture.t.Fatalf("memory for %s = %q, not an absence: %v", ip, value, err)
	}
	return absence.FirstAbsent, true
}

// noDataCarryOutcomes is every carry decision the Leader reported for the
// Plan's no-data state.
func (fixture *lifecycleFixture) noDataCarryOutcomes() map[string]int {
	fixture.observationsMu.Lock()
	defer fixture.observationsMu.Unlock()
	outcomes := map[string]int{}
	for _, observation := range fixture.observations {
		if observation.StateCarry != nil && observation.StateCarry.Scope == "no_data_plan" {
			outcomes[observation.StateCarry.Result] += observation.StateCarry.Count
		}
	}
	return outcomes
}

// abnormalsAfter is the ABNORMAL events in events evaluated after round.
func (fixture *lifecycleFixture) abnormalsAfter(events []contract.TriggerEventV1, round int64) []contract.TriggerEventV1 {
	var found []contract.TriggerEventV1
	for _, event := range events {
		if event.EventKind == contract.TriggerEventAbnormal && event.EvaluationTime > fixture.evaluationAt(round) {
			found = append(found, event)
		}
	}
	return found
}

// A detection-only edit leaves a group that went quiet before it exactly
// where it was. Host B reports in round 1 and is absent from round 2, so its
// no-data alert opens. The threshold is then edited, which moves the Plan's
// state generation. Nothing about no-data changed: the same metric, grouped
// the same way, under the same no-data section. B's absence goes on under the
// new generation from the round it began, and its alert keeps being said.
//
// Under a history roster this is the whole of B's detection: the roster is
// the memory, so a memory left behind with the old generation is a group no
// round ever mentions again, its alert open with nothing to end it. Under a
// target roster the target still names B, so the new generation does meet it
// - as a new absence, its clock started over at the edit.
func TestADetectionEditCarriesTheNoDataStateOfAGroupThatWentQuietBeforeIt(t *testing.T) {
	for name, targets := range map[string][]string{
		"history roster": nil,
		"target roster":  {lifecycleHostA, lifecycleHostB},
	} {
		t.Run(name, func(t *testing.T) {
			strategy := lifecycleStrategy{revision: 7, continuous: 1, targets: targets, dimensions: lifecycleHostPair}
			fixture := startLifecycleFixture(t, strategy, nil)
			fixture.serve(false, 5, lifecycleHostA, lifecycleHostB)
			fixture.mustAttempt(1)
			fixture.serve(false, 5, lifecycleHostA)
			fixture.mustAttempt(2)
			raised := fixture.noDataEventsFor(lifecycleHostB)
			if len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal {
				t.Fatalf("round 2 sent host B's no-data events %+v, want the ABNORMAL that opens its alert", raised)
			}
			fixture.publishOpenAlert(raised[0].DedupeMD5)
			fixture.waitOpenAlertSetRead(2)
			before := fixture.memoryKeys()

			strategy.revision, strategy.edit = 8, editThreshold
			first := fixture.change(strategy, 2)
			for round := int64(3); round <= first+3; round++ {
				fixture.mustAttempt(round)
			}
			added := keysNotIn(before, fixture.memoryKeys())
			if len(added) != 1 {
				t.Fatalf("memory keys after the edit %v (before %v), want one under the new generation", fixture.memoryKeys(), before)
			}
			if firstAbsent, held := fixture.memoryAbsenceIn(added[0], lifecycleHostB); !held || firstAbsent != fixture.evaluationAt(2) {
				t.Fatalf("under the new generation B's absence starts at %d (held %t), want it carried from round 2 (%d)",
					firstAbsent, held, fixture.evaluationAt(2))
			}
			continued := fixture.abnormalsAfter(fixture.noDataEventsFor(lifecycleHostB), first)
			if len(continued) == 0 {
				t.Fatalf("after the edit (from round %d) host B's no-data alert was not said again", first)
			}
			for _, event := range continued {
				if event.DedupeMD5 != raised[0].DedupeMD5 {
					t.Fatalf("after the edit B's no-data ABNORMAL is under %s, its alert is %s", event.DedupeMD5, raised[0].DedupeMD5)
				}
			}
			if got := fixture.noDataCarryOutcomes(); got["carried"] != 1 {
				t.Fatalf("the Leader's no-data carry decisions = %v, want one carried", got)
			}
		})
	}
}

// A threshold alert written to end on no data, whose series went quiet before
// a detection edit, still ends. Host B is over the threshold in round 1 and
// absent from round 2; the threshold is then edited, which moves the
// generation. Its end entry - the alert's fingerprint and its last real point
// - follows it to the new generation, and it ends (R + T + 1) periods after
// its last real point, under its own alert.
func TestADetectionEditCarriesTheEndOfAnAlertWhoseSeriesWentQuietBeforeIt(t *testing.T) {
	strategy := lifecycleStrategy{revision: 7, noDataOff: true, statusSetter: "recovery-nodata"}
	fixture := startLifecycleFixture(t, strategy, nil)
	fixture.serve(false, 90, lifecycleHostA, lifecycleHostB)
	fixture.mustAttempt(1)
	openedA, openedB := fixture.thresholdEventsFor(lifecycleHostA), fixture.thresholdEventsFor(lifecycleHostB)
	if len(openedA) != 1 || len(openedB) != 1 || openedB[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 1 sent A %+v and B %+v, want one ABNORMAL each", openedA, openedB)
	}
	fixture.publishOpenAlert(openedA[0].DedupeMD5)
	fixture.publishOpenAlert(openedB[0].DedupeMD5)
	fixture.serve(false, 90, lifecycleHostA)
	fixture.mustAttempt(2)
	fixture.waitOpenAlertSetRead(2)

	strategy.revision, strategy.edit = 8, editThreshold
	first := fixture.change(strategy, 2)
	ended := 0
	for round := int64(3); round <= first+6 && ended == 0; round++ {
		fixture.mustAttempt(round)
		ended = len(recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB)))
	}
	ends := recoveriesOf(fixture.thresholdEventsFor(lifecycleHostB))
	if len(ends) != 1 {
		t.Fatalf("B's ends within six rounds of the edit (round %d) = %+v, want one", first, ends)
	}
	if end := ends[0]; !end.RecordRef.Absent || end.DedupeMD5 != openedB[0].DedupeMD5 ||
		end.LevelResults[0].DecisionWindow.Recovery.EndedBy != contract.RecoveryEndedByNoData {
		t.Fatalf("B's end = %+v, want an absent RECOVERY ended by no data under B's alert", end)
	}
	if got := recoveriesOf(fixture.thresholdEventsFor(lifecycleHostA)); len(got) != 0 {
		t.Fatalf("A, reporting over the threshold, was ended: %+v", got)
	}
}

// editContinuous changes the no-data section's continuous count.
func editContinuous(item map[string]any) {
	item["no_data_config"].(map[string]any)["continuous"] = 2
}

// publish installs a changed strategy and has the Leader read and publish
// it, without waiting for a Segment of the fixture's Query Group: an edit to
// the query names another Query Group.
func (fixture *lifecycleFixture) publish(strategy lifecycleStrategy) {
	fixture.t.Helper()
	fixture.install(strategy)
	for confirm := 0; confirm < 2; confirm++ {
		if err := fixture.bundle.refreshAndReconcile(context.Background(), true); err != nil {
			fixture.t.Fatalf("refresh after the change: %v", err)
		}
	}
}

// An edit that changes what no-data means carries no no-data state, even
// with the detection edit beside it: a filter over other data, another
// grouping, or another no-data section. The quiet group's memory is not the
// edited strategy's to read, so the new generation starts with none and the
// Leader says why; the edit is what decides what its open alerts become.
//
// An edit to the query names another Query Group, and the Leader finds the
// previous version under the Query Group the Plan is in now: it reports that
// version unreadable, which carries nothing either. The comparison of the
// two queries is decided apart (TestTheNoDataCarryIsDecidedFromTheTwoVersionsAlone).
func TestAnEditThatChangesNoDataSemanticsCarriesNoNoDataState(t *testing.T) {
	for name, test := range map[string]struct {
		edit func(map[string]any)
		// rounds is whether the edited Plan stays in the fixture's Query
		// Group, so the case can run it and read its new memory.
		rounds bool
	}{
		"another condition":       {edit: both(editThreshold, editCondition)},
		"another grouping":        {edit: both(editThreshold, editGrouping)},
		"another no-data section": {edit: both(editThreshold, editContinuous), rounds: true},
	} {
		t.Run(name, func(t *testing.T) {
			strategy := lifecycleStrategy{revision: 7, continuous: 1, dimensions: lifecycleHostPair}
			fixture := startLifecycleFixture(t, strategy, nil)
			fixture.serve(false, 5, lifecycleHostA, lifecycleHostB)
			fixture.mustAttempt(1)
			fixture.serve(false, 5, lifecycleHostA)
			fixture.mustAttempt(2)
			raised := fixture.noDataEventsFor(lifecycleHostB)
			if len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal {
				t.Fatalf("round 2 sent host B's no-data events %+v, want the ABNORMAL that opens its alert", raised)
			}
			before := fixture.memoryKeys()

			strategy.revision, strategy.edit = 8, test.edit
			if !test.rounds {
				fixture.publish(strategy)
			} else {
				first := fixture.change(strategy, 2)
				for round := int64(3); round <= first+3; round++ {
					fixture.mustAttempt(round)
				}
				added := keysNotIn(before, fixture.memoryKeys())
				if len(added) != 1 {
					t.Fatalf("memory keys after the edit %v (before %v), want one under the new generation", fixture.memoryKeys(), before)
				}
				if firstAbsent, held := fixture.memoryAbsenceIn(added[0], lifecycleHostB); held {
					t.Fatalf("the new generation holds B (absent from %d); an edit to no-data carries nothing", firstAbsent)
				}
			}
			got := fixture.noDataCarryOutcomes()
			if got["carried"] != 0 || got["carried_ends_only"] != 0 ||
				got["not_carried_semantics_changed"]+got["not_carried_previous_unreadable"] != 1 {
				t.Fatalf("the Leader's no-data carry decisions = %v, want one not carried", got)
			}
			if test.rounds && got["not_carried_semantics_changed"] != 1 {
				t.Fatalf("the Leader's no-data carry decisions = %v, want not_carried_semantics_changed", got)
			}
		})
	}
}
