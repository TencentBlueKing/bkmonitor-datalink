// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// noDataPreflightContract is this package's own frozen contract. The external
// test package has one; this file is internal, because the preflight it tests
// is unexported.
func noDataPreflightContract(t *testing.T, plans []execution.DuePlan) execution.FrozenExecutionContractRef {
	t.Helper()
	digest, err := execution.DeriveDuePlanSetDigest(plans, nil)
	if err != nil {
		t.Fatal(err)
	}
	return execution.FrozenExecutionContractRef{
		Slot:                 execution.SlotIdentity{QueryGroup: "query-group", EvaluationTime: 1_788_000_000},
		SnapshotRevision:     "snapshot-v1",
		QueryRevision:        "query-v1",
		ScheduleRevision:     "schedule-v1",
		ScheduleSegmentStart: 1_787_999_940,
		DuePlanSetDigest:     digest,
	}
}

func noDataPreflightPlan(t *testing.T, strategyID string, noData *contract.NoDataConfigV1) *strategy.CompiledPlan {
	t.Helper()
	compiler, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{
		MaxPlanBytes: 64 << 10, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16,
		MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 4096,
		MaxRecoveryConsecutiveWindows: 4096, MaxRequiredHistoryPoints: 4096, MaxTriggerComputeCost: 1 << 20,
		MaxCompiledPlanBytes: 64 << 10, MaxCacheEntries: 64, MaxCacheBytes: 4 << 20,
		NegativeCacheTTL: time.Minute, BudgetRevision: "worker-test-v1",
	})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: strategyID, Revision: "strategy-v1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	plan := contract.EvaluationPlanV2{
		PlanID: strategyID, StrategyRef: ref, InputProjection: projection, NoData: noData,
		StrategyIR: contract.StrategyIRV2{
			Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref,
			InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{
				EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300,
				AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120,
			},
			Levels: []contract.LevelIRV2{{
				Definition: contract.LevelDefinitionV2{LevelID: 5, Priority: 1}, Connector: contract.LevelConnectorAND,
				DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{
					Type: "Threshold", Version: 1,
					Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`),
				}}},
				TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
				RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
			}},
		},
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan,
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64),
			IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time",
		},
		StateSemantics: strategy.StateSemantics{
			StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1",
			IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1",
			HistoryCellSemanticsVersion: "detect-history-cell-v1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("the test plan did not compile: %+v", result.PlanTerminal())
	}
	return compiled
}

// Only the Plans that detect no-data are asked about.
//
// A Plan without the section has no memory and never will, so including it
// would be one store read per Slot for an answer that is always "nothing
// there" - and it would put every Plan in the deployment into a result a
// reader counts no-data Plans from.
func TestNoDataPreflightAsksOnlyAboutPlansThatDetectNoData(t *testing.T) {
	detecting := execution.DuePlan{
		Identity:        execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"},
		CompiledPlan:    noDataPreflightPlan(t, "7", &contract.NoDataConfigV1{Continuous: 3, Level: 2}),
		StateGeneration: "state-v1", StateApplyEpoch: 1, ScheduleRevision: "plan-schedule-v1",
	}
	plain := execution.DuePlan{
		Identity:        execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "8"},
		CompiledPlan:    noDataPreflightPlan(t, "8", nil),
		StateGeneration: "state-v1", StateApplyEpoch: 1, ScheduleRevision: "plan-schedule-v1",
	}
	duePlans := []execution.DuePlan{plain, detecting}
	header := execution.InternalExecutionHeader{
		Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans,
	}

	items, err := noDataPreflightForHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("preflight = %d items, want only the Plan that detects no-data", len(items))
	}
	if items[0].Identity.Plan != detecting.Identity {
		t.Fatalf("preflight asked about %+v, want %+v", items[0].Identity.Plan, detecting.Identity)
	}
	if items[0].Identity.StateGeneration != detecting.StateGeneration {
		t.Fatalf("generation = %q, want the Plan's", items[0].Identity.StateGeneration)
	}
	if items[0].ScheduleRevision != detecting.ScheduleRevision {
		t.Fatalf("schedule revision = %q, want the Plan's", items[0].ScheduleRevision)
	}

	// The retention travels with the item, because that is what the key's
	// lifetime is derived from. Without it every no-data key would take the
	// floor, which is wrong for a Plan whose window reaches back further.
	want, err := execution.DeriveStateRetentionRequirement(detecting.CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(items[0].Retention) != len(want) || len(want) == 0 {
		t.Fatalf("retention = %+v, want the Plan's own %+v", items[0].Retention, want)
	}
	for index := range want {
		if items[0].Retention[index] != want[index] {
			t.Fatalf("retention[%d] = %+v, want %+v", index, items[0].Retention[index], want[index])
		}
	}

	// And a Slot in which nothing detects no-data asks about nothing at all.
	empty, err := noDataPreflightForHeader(execution.InternalExecutionHeader{
		Contract: noDataPreflightContract(t, []execution.DuePlan{plain}), DuePlans: []execution.DuePlan{plain},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("preflight = %+v, want nothing asked", empty)
	}
}

// A Slot with no no-data Plan does not reach the store. An empty request is
// refused rather than answered with nothing, so sending one would turn every
// ordinary Slot into a failure.
func TestNoDataLoadSkipsTheStoreWhenNoPlanDetectsNoData(t *testing.T) {
	store := &emptyNoDataStore{}
	duePlans := []execution.DuePlan{{
		Identity:        execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "8"},
		CompiledPlan:    noDataPreflightPlan(t, "8", nil),
		StateGeneration: "state-v1", StateApplyEpoch: 1, ScheduleRevision: "plan-schedule-v1",
	}}
	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{ports: Ports{NoData: store}},
		header: execution.InternalExecutionHeader{
			Contract: noDataPreflightContract(t, duePlans), DuePlans: duePlans,
		},
	}
	if err := stream.loadNoDataMemory(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.loads != 0 {
		t.Fatalf("the store was read %d times for a Slot in which no Plan detects no-data", store.loads)
	}
	if len(stream.noData.Items) != 0 {
		t.Fatalf("noData = %+v, want nothing", stream.noData.Items)
	}
}
