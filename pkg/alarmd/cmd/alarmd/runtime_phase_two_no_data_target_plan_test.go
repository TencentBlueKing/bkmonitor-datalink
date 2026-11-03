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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// targetPlanGroupPrefix is the dynamic group writer's key prefix these cases
// render, separator included, as the writer spells it.
const targetPlanGroupPrefix = "alarmd-rounds:"

// targetPlanHostKey is the no-data group key of a host read by host id.
func targetPlanHostKey(hostID string) string {
	return "bk_host_id=" + hostID + "," + contract.NoDataDimensionTag + "=true"
}

// targetPlanItem makes the item's target a target plan read by host identity
// (decision-017 section 2): host 1 named statically and the dynamic groups
// given, with the query and no-data both on bk_host_id, which is exactly the
// dimension the host_id rule keys a member by, so the plan's members are the
// no-data roster (decision-017 section 3.4, second item).
func targetPlanItem(groups ...string) func(map[string]any) {
	return func(item map[string]any) {
		item["query_configs"].([]any)[0].(map[string]any)["agg_dimension"] = []any{"bk_host_id"}
		references := make([]any, 0, len(groups))
		for _, id := range groups {
			references = append(references, map[string]any{"dynamic_group_id": id})
		}
		item["target"] = []any{}
		item["target_plan"] = map[string]any{
			"schema_version": 1, "model_id": "cw-Host", "target_rule": "host_id", "failure_policy": "no_match",
			"static_targets":     []any{map[string]any{"bk_host_id": 1}},
			"dynamic_groups":     references,
			"dynamic_topologies": []any{},
		}
		item["no_data_config"] = map[string]any{
			"is_enabled": true, "continuous": 1, "level": noDataConfiguredLevelID, "agg_dimension": []any{"bk_host_id"},
		}
	}
}

// The group documents, in the writer's shape (decision-017 section 3.2): a
// root model, the model_inst_ids summary and the member list. A member of a
// host group read under host_id has to carry its bk_host_id; one that does not
// is dropped in validation.
const (
	// targetPlanGroupHostTwo resolves completely to host 2.
	targetPlanGroupHostTwo = `{"model_id":"cw-Host","model_inst_ids":["2"],"member_list":[` +
		`{"model_id":"cw-Host","model_inst_id":"2","bk_host_id":2}]}`
	// targetPlanGroupHostThree resolves completely to host 3.
	targetPlanGroupHostThree = `{"model_id":"cw-Host","model_inst_ids":["3"],"member_list":[` +
		`{"model_id":"cw-Host","model_inst_id":"3","bk_host_id":3}]}`
	// targetPlanGroupOneDropped keeps host 3 and drops instance 4, which
	// carries no host id: every selector answered, and a member was lost.
	targetPlanGroupOneDropped = `{"model_id":"cw-Host","model_inst_ids":["3","4"],"member_list":[` +
		`{"model_id":"cw-Host","model_inst_id":"3","bk_host_id":3},{"model_id":"cw-Host","model_inst_id":"4"}]}`
)

// A target plan's absence is judged only against a complete resolution.
//
// decision-017 section 3.4, third item, and the ruling quoted in section 4:
// when a selector of the plan cannot be resolved, the Plan's no-data pauses
// for the round - no NORMAL and no ANOMALY is synthesised, the memory is not
// written and first_absent does not move - under
// SKIPPED_TARGET_SELECTOR_UNAVAILABLE; when every selector answered and
// members were dropped in validation, the same pause under its own name,
// SKIPPED_TARGET_MEMBERS_DROPPED. The members that did resolve still admit
// their records, so the threshold detection goes on. With a complete
// resolution the same edit is judged, which is the other side: the host the
// plan no longer names is closed exactly once (A8) and the one it now names
// is expected.
//
// Each case opens host 2's absence under a plan whose group resolves to host
// 2, then moves the group reference to the case's group. Read as an empty
// set, a group that did not resolve would close host 2's absence; read as the
// roster, a group that lost a member would close it too. Neither may.
func TestATargetPlanIsJudgedOnlyAgainstACompleteResolution(t *testing.T) {
	for _, test := range []struct {
		name string
		// document is the edited reference's group, or empty for a group
		// whose key the writer has not written.
		document string
		outcome  string
	}{
		{name: "a group that cannot be read", outcome: "SKIPPED_TARGET_SELECTOR_UNAVAILABLE"},
		{name: "a group that lost a member", document: targetPlanGroupOneDropped, outcome: "SKIPPED_TARGET_MEMBERS_DROPPED"},
		{name: "a group that resolves completely", document: targetPlanGroupHostThree, outcome: "EVALUATED"},
	} {
		t.Run(test.name, func(t *testing.T) {
			address, client := startPhaseTwoRedis(t)
			ctx := context.Background()
			if err := client.Set(ctx, targetPlanGroupPrefix+"dynamic_group:501", targetPlanGroupHostTwo, 0).Err(); err != nil {
				t.Fatal(err)
			}
			if test.document != "" {
				if err := client.Set(ctx, targetPlanGroupPrefix+"dynamic_group:502", test.document, 0).Err(); err != nil {
					t.Fatal(err)
				}
			}
			installRoundsStrategy(t, ctx, client, 7, targetPlanItem("501"))
			fixture := startRoundsFixture(t, roundsOptions{hosts: true, groupPrefix: targetPlanGroupPrefix,
				address: address, client: client})
			fixture.report(hostIDSeries("1"))
			hostTwo := targetPlanHostKey("2")

			mark := fixture.mark()
			fixture.run(1)
			outcomesAre(t, 1, fixture.outcomesSince(mark), map[string]int{"EVALUATED": 1})
			opened, _ := eventsAbout(fixture.written(), "bk_host_id", "2")
			if len(opened) != 1 || opened[0].EventKind != contract.TriggerEventAbnormal {
				t.Fatalf("fixture: round 1 sent host 2's no-data events %+v, want the anomaly that opens its absence", opened)
			}
			if got, want := fixture.firstAbsentOf(hostTwo), fixture.evaluationAt(1); got != want {
				t.Fatalf("fixture: host 2's absence starts at %d, want %d", got, want)
			}

			edited := fixture.firstRoundUnder(8, targetPlanItem("502"))
			for round := int64(2); round < edited; round++ {
				fixture.run(round)
			}

			fixture.value.Store(hostThresholdValue)
			mark, sent := fixture.mark(), len(fixture.written())
			fixture.run(edited)
			outcomesAre(t, edited, fixture.outcomesSince(mark), map[string]int{test.outcome: 1})
			round := fixture.written()[sent:]
			thresholdAbnormalAbout(t, edited, round, "bk_host_id", "1")
			two, _ := eventsAbout(round, "bk_host_id", "2")
			three, _ := eventsAbout(round, "bk_host_id", "3")
			if test.outcome != "EVALUATED" {
				noNoDataEventsIn(t, edited, round)
				if got, want := fixture.firstAbsentOf(hostTwo), fixture.evaluationAt(1); got != want {
					t.Fatalf("after the paused round host 2's absence starts at %d, want it still open from %d", got, want)
				}
				return
			}
			if len(two) != 1 || two[0].EventKind == contract.TriggerEventAbnormal {
				t.Fatalf("the judged round sent host 2's no-data events %+v; want the one closing event for the "+
					"host the plan stopped naming", two)
			}
			if len(three) != 1 || three[0].EventKind != contract.TriggerEventAbnormal {
				t.Fatalf("the judged round sent host 3's no-data events %+v; want the anomaly of the host the plan "+
					"now names and that never reported", three)
			}
			if value, held := fixture.memoryOf(hostTwo); held {
				t.Fatalf("host 2 is still remembered as %q after the round that closed it", value)
			}
		})
	}
}
