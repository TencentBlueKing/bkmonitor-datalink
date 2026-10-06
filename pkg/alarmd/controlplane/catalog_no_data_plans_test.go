// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

func noDataPlan(id string, scope *contract.TargetScopeV2, dimensions []string) FrozenPlan {
	return FrozenPlan{Plan: contract.EvaluationPlanV2{
		PlanID: id, TargetScope: scope,
		NoData: &contract.NoDataConfigV1{Continuous: 1, Level: 2, AggDimension: dimensions},
	}}
}

func noDataCatalog(plans []FrozenPlan, dispositions []ObjectDisposition) Catalog {
	return Catalog{
		QueryGroups:  []QueryGroup{{QueryPlan: execution.QueryPlanFacts{}, Plans: plans}},
		Dispositions: dispositions,
	}
}

var staticScope = &contract.TargetScopeV2{Groups: []contract.TargetScopeGroupV2{{
	Conditions: []contract.TargetScopeConditionV2{{
		Field: contract.TargetScopeHost, Method: contract.TargetScopeInclude, Keys: []string{"10.0.0.1|0"},
	}},
}}}

// Each accepted no-data Plan is counted once, under the source its expected set
// actually comes from - which is the classification the compiler already made,
// asked again rather than restated.
func TestNoDataPlansCountEachAcceptedPlanUnderItsSource(t *testing.T) {
	hostPair := []string{"bk_target_ip", "bk_target_cloud_id"}
	composition := ComposeCatalog(noDataCatalog([]FrozenPlan{
		noDataPlan("1", staticScope, hostPair),
		noDataPlan("2", nil, hostPair),
		noDataPlan("3", nil, nil),
		noDataPlan("4", staticScope, []string{"device"}),
		// A Plan that does not detect no-data is in none of these.
		{Plan: contract.EvaluationPlanV2{PlanID: "5"}},
	}, nil))

	for source, want := range map[nodata.RosterSource]int{
		nodata.RosterTargetStatic: 1,
		nodata.RosterHistory:      2,
		nodata.RosterWhole:        1,
	} {
		if got := composition.NoDataPlans[source]; got != want {
			t.Fatalf("NoDataPlans[%s] = %d, want %d", source, got, want)
		}
	}
	if composition.NoDataPlansUnclassified != 0 {
		t.Fatalf("unclassified = %d; the compiler refuses those, so one here is a Plan that got past it",
			composition.NoDataPlansUnclassified)
	}
}

// The sources and the no-data reasons are one partition over the items that
// asked for no-data detection. Three sources adding to fewer than are
// configured is the reading that matters, and it is only visible against what
// the withheld side holds.
func TestNoDataPartitionCoversEveryItemThatAskedForDetection(t *testing.T) {
	hostPair := []string{"bk_target_ip", "bk_target_cloud_id"}
	composition := ComposeCatalog(noDataCatalog(
		[]FrozenPlan{noDataPlan("1", staticScope, hostPair), noDataPlan("2", nil, hostPair)},
		[]ObjectDisposition{
			{SourceID: "3", Disposition: DispositionConfigRejected, Reason: "NO_DATA_CONFIG_INVALID"},
			{SourceID: "4", Disposition: DispositionUnsupported, Reason: "NO_DATA_ROSTER_UNSUPPORTED"},
			// Not a no-data reason, so not in this partition.
			{SourceID: "5", Disposition: DispositionConfigRejected, Reason: "PLAN_INVALID"},
		},
	))
	if got, want := composition.NoDataPlansPartition(), 4; got != want {
		t.Fatalf("partition = %d, want %d: two accepted and two withheld for a no-data reason", got, want)
	}
}

// A strategy refused for the first time is CONFIG_REJECTED; the same one is
// STALE_CONFIG once its last good Plan is retained, with the reason unchanged.
// Reading the reason at one disposition would drop the count by one on the
// round it changes state, which reads as a gauge that lost something rather
// than as a strategy that moved.
func TestNoDataPartitionHoldsWhenARejectionBecomesAStaleConfig(t *testing.T) {
	hostPair := []string{"bk_target_ip", "bk_target_cloud_id"}
	accepted := []FrozenPlan{noDataPlan("1", staticScope, hostPair)}

	firstRound := ComposeCatalog(noDataCatalog(accepted, []ObjectDisposition{
		{SourceID: "2", Disposition: DispositionConfigRejected, Reason: "NO_DATA_CONFIG_INVALID"},
	}))
	secondRound := ComposeCatalog(noDataCatalog(accepted, []ObjectDisposition{
		{SourceID: "2", Disposition: DispositionStaleConfig, Reason: "NO_DATA_CONFIG_INVALID"},
	}))

	if first, second := firstRound.NoDataPlansPartition(), secondRound.NoDataPlansPartition(); first != second {
		t.Fatalf("partition = %d then %d; the same strategy changed disposition, not existence", first, second)
	}
	if got, want := secondRound.NoDataPlansPartition(), 2; got != want {
		t.Fatalf("partition = %d, want %d", got, want)
	}
}

// Every source the classification can return has to be pre-created, or a source
// with no Plans reads as nothing rather than as zero - and "no Plan uses the
// history roster" and "nobody computed it" are not the same answer.
func TestNoDataPlansPreCreatesEverySource(t *testing.T) {
	composition := ComposeCatalog(Catalog{})
	if len(composition.NoDataPlans) != len(NoDataRosterSources) {
		t.Fatalf("NoDataPlans = %v, want a zero for each of %v", composition.NoDataPlans, NoDataRosterSources)
	}
	for _, source := range NoDataRosterSources {
		if _, present := composition.NoDataPlans[source]; !present {
			t.Fatalf("source %s has no series; zero and absent read the same to a reader", source)
		}
	}
}

// The compiler refuses a Plan whose expected set cannot be classified, so this
// state should not exist - which is exactly why it is counted rather than
// skipped. A skip would take the Plan out of the partition, and the three
// sources would read one low with nothing saying why; the count reads zero
// while the invariant holds and says where to look when it does not.
//
// The Plan here is built directly, past the compiler, because that is the only
// way to reach a state the compiler exists to prevent. Without this the skip
// and the count are indistinguishable: a counter whose expected value is zero
// and that no test ever makes non-zero is not being tested at all.
func TestNoDataPlansCountAnUnclassifiablePlanRatherThanDropIt(t *testing.T) {
	excluded := &contract.TargetScopeV2{Groups: []contract.TargetScopeGroupV2{{
		Conditions: []contract.TargetScopeConditionV2{{
			Field: contract.TargetScopeHost, Method: contract.TargetScopeExclude, Keys: []string{"10.0.0.1|0"},
		}},
	}}}
	composition := ComposeCatalog(noDataCatalog([]FrozenPlan{
		noDataPlan("1", excluded, []string{"bk_target_ip", "bk_target_cloud_id"}),
	}, nil))

	if composition.NoDataPlansUnclassified != 1 {
		t.Fatalf("unclassified = %d, want the Plan counted rather than dropped",
			composition.NoDataPlansUnclassified)
	}
	for _, source := range NoDataRosterSources {
		if count := composition.NoDataPlans[source]; count != 0 {
			t.Fatalf("NoDataPlans[%s] = %d; an unclassifiable Plan was filed under a source", source, count)
		}
	}
	if got, want := composition.NoDataPlansPartition(), 1; got != want {
		t.Fatalf("partition = %d, want %d: the Plan asked for detection and has to be in the partition",
			got, want)
	}
}
