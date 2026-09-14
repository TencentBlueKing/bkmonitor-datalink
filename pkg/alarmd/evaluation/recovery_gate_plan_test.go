// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package evaluation

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

// A held RECOVERY envelope has to reach the Plan's counts, because those
// counts are the only way the metric can say the gate was ever exercised. A
// count that stayed at zero because the evaluator never added to it would
// read exactly like a gate that never had to hold, which is the reading that
// tells nobody to look. So the two arms run through the evaluator: a sibling
// Level still warming holds the envelope and counts once, and the same
// record with both Levels' histories complete sends it and counts nothing.
func TestEvaluatorCountsAHeldRecoveryEnvelopeOnThePlan(t *testing.T) {
	for _, arm := range []struct {
		name          string
		siblingWarm   bool
		wantHeld      uint64
		wantEnvelopes int
	}{
		{name: "sibling Level warming holds the envelope and counts it", siblingWarm: true, wantHeld: 1, wantEnvelopes: 0},
		{name: "both Levels complete send the envelope and count nothing", siblingWarm: false, wantHeld: 0, wantEnvelopes: 1},
	} {
		t.Run(arm.name, func(t *testing.T) {
			plan := compiledTwoLevels(t)
			history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
				{LevelID: 5, DetectFingerprint: plan.Levels()[0].Fingerprints().Detect, Result: execution.LevelFactAnomalous},
				{LevelID: 6, DetectFingerprint: plan.Levels()[1].Fingerprints().Detect, Result: execution.LevelFactAnomalous},
			}}}
			req := requestFixtureTwoLevels(t, plan, json.RawMessage(`10`), history)
			if arm.siblingWarm {
				req.State.Items[0].Levels[1].HistoryCompleteness = execution.HistoryWarming
				req.State.Items[0].Levels[1].GapReasonCode = execution.ReasonCode(contract.ReasonHistoryWarming)
			}
			result, err := newEvaluator(t).Evaluate(context.Background(), req)
			if err != nil {
				t.Fatalf("Evaluate()=%v", err)
			}
			plan0 := result.Plans[0]
			if len(plan0.LevelOutcomes) != 2 || plan0.LevelOutcomes[0].Outcome != execution.LevelOutcomeRecovery {
				t.Fatalf("outcomes=%+v want the first Level RECOVERY", plan0.LevelOutcomes)
			}
			wantSibling := execution.LevelOutcomeRecovery
			if arm.siblingWarm {
				wantSibling = execution.LevelOutcomeUnknown
			}
			if plan0.LevelOutcomes[1].Outcome != wantSibling {
				t.Fatalf("sibling outcome=%s want %s", plan0.LevelOutcomes[1].Outcome, wantSibling)
			}
			want := execution.RecoveryGateCounts{HeldLevelUnavailable: arm.wantHeld}
			if plan0.RecoveryGate != want {
				t.Fatalf("gate counts=%+v want %+v", plan0.RecoveryGate, want)
			}
			if len(plan0.StateResults) != 1 {
				t.Fatalf("state results=%d want the record's state written either way", len(plan0.StateResults))
			}
			if got := len(plan0.StateResults[0].Events); got != arm.wantEnvelopes {
				t.Fatalf("envelopes=%d want %d", got, arm.wantEnvelopes)
			}
		})
	}
}

func compiledTwoLevels(t *testing.T) *strategy.CompiledPlan {
	t.Helper()
	c, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{MaxPlanBytes: 1 << 20, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16, MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 16, MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32, MaxTriggerComputeCost: 1 << 20, MaxCompiledPlanBytes: 1 << 20, MaxCacheEntries: 16, MaxCacheBytes: 1 << 20, NegativeCacheTTL: time.Minute, BudgetRevision: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "r1"}
	projection := contract.InputProjectionV2{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired}
	level := func(id, priority uint32) contract.LevelIRV2 {
		return contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: id, Priority: priority}, Connector: contract.LevelConnectorAND, DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: "Threshold", Version: 1, Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`)}}}, TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)}, RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)}}
	}
	p := contract.EvaluationPlanV2{PlanID: "7", StrategyRef: ref, InputProjection: projection, StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref, InputProjection: projection, ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120}, Levels: []contract.LevelIRV2{level(5, 1), level(6, 2)}}}
	r, err := c.Compile(context.Background(), strategy.CompileRequest{Plan: p, DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64), IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time"}, StateSemantics: strategy.StateSemantics{StateSchemaVersion: "s", CodecSemanticsVersion: "c", IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "t", HistoryCellSemanticsVersion: "h"}})
	if err != nil {
		t.Fatal(err)
	}
	plan, ok := r.Plan()
	if !ok {
		t.Fatalf("compile terminal=%+v", r.PlanTerminal())
	}
	return plan
}

// requestFixtureTwoLevels is requestFixtureForPlan for a two-Level Plan: one
// consumer, one named input and one runtime Level state per Level, both
// histories complete unless the test says otherwise.
func requestFixtureTwoLevels(t *testing.T, plan *strategy.CompiledPlan, value json.RawMessage, history []execution.StateHistoryPoint) execution.EvaluationRequest {
	t.Helper()
	records := []contract.CanonicalRecordV2{{RecordID: strings.Repeat("b", 64), SourceTime: 100, BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)}, Values: map[string]json.RawMessage{"value": value}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 100}}
	id := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	due := execution.DuePlan{Identity: id, CompiledPlan: plan, StateGeneration: "state-v1", StateApplyEpoch: 1, ScheduleRevision: "plan-schedule", CompletionDeadlineUnixMilli: 200000}
	levels := plan.Levels()
	consumers := make([]execution.ConsumerRef, len(levels))
	requirementConsumers := make([]execution.DataRequirementConsumer, len(levels))
	for i, level := range levels {
		consumers[i] = execution.ConsumerRef{Plan: id, LevelID: level.Definition().LevelID, HasLevel: true}
		requirementConsumers[i] = execution.DataRequirementConsumer{Consumer: consumers[i], ConsumerDeadlineUnixMilli: 200000, DownstreamExecutionReserveMilliSec: 1000}
	}
	requirement := execution.DataRequirement{RequirementID: "main", DatasetName: "main", Role: execution.InputRolePrimary, LogicalQueryRef: "q", RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: -60, EndOffsetSeconds: 0, HalfOpen: true}, StepMillis: 60000, AlignmentMillis: 60000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessEager, RequiredColumns: []string{"value"}, Consumers: requirementConsumers}
	digest, err := execution.DeriveDuePlanSetDigest([]execution.DuePlan{due}, []execution.DataRequirement{requirement})
	if err != nil {
		t.Fatal(err)
	}
	contractRef := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "qg", EvaluationTime: 100}, SnapshotRevision: "snapshot", QueryRevision: "query", ScheduleRevision: "schedule", ScheduleSegmentStart: 100, DuePlanSetDigest: digest}
	series := execution.SeriesIdentityDigest(records[0].DimensionIdentity.Digest)
	dataset := execution.NewDataset(records)
	view, _ := execution.NewDatasetView(dataset, []uint32{0})
	provider := strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(func(context.Context, string, string, string) (*time.Location, error) {
		return time.UTC, nil
	}))
	requests := make([]strategy.EffectiveTimeRequest, len(levels))
	for i, level := range levels {
		requests[i] = strategy.EffectiveTimeRequest{TenantID: "tenant", BusinessID: "2", EvaluationTime: 100, Requirement: level.EffectiveTimeRequirement()}
	}
	facts, err := provider.Resolve(context.Background(), requests)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := execution.DeriveRuntimeLevelContractRefs(plan)
	if err != nil {
		t.Fatal(err)
	}
	effective := make([]execution.BoundEffectiveTimeFact, len(levels))
	inputs := make([]execution.SeriesEvaluationInputRequest, len(levels))
	levelStates := make([]execution.RuntimeLevelStateView, len(levels))
	for i, level := range levels {
		effective[i] = execution.BoundEffectiveTimeFact{Consumer: consumers[i], SeriesIdentity: series, Fact: facts[i]}
		binding := execution.NamedInputBinding{Consumer: consumers[i], RequirementID: "main", DatasetName: "main", Role: execution.InputRolePrimary, ProviderResult: "provider", QueryWindow: execution.QueryWindow{Start: 40, End: 100}, Dataset: dataset, View: view, Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactPlan, Provenance: execution.InputProvenance{PhysicalQuery: "physical", AttemptNo: 1}}
		inputs[i] = execution.SeriesEvaluationInputRequest{Contract: contractRef, Consumer: consumers[i], SeriesIdentity: series, RequirementIDs: []execution.RequirementID{"main"}, Inputs: []execution.NamedInputBinding{binding}}
		levelStates[i] = execution.RuntimeLevelStateView{LevelID: level.Definition().LevelID, LevelStateCompatibility: refs[i].LevelStateCompatibility, WarmupRequirementRef: refs[i].WarmupRequirementRef, HistoryCompleteness: execution.HistoryFull}
	}
	stateView := execution.RuntimeStateView{Identity: execution.StateKeyIdentity{Plan: id, StateGeneration: "state-v1", SeriesIdentityDigest: series}, Status: execution.StateFoundReady, BlobRevision: 1, VersionComparison: execution.ApplyVersionPersistedOlder, History: append([]execution.StateHistoryPoint(nil), history...), Levels: levelStates}
	return execution.EvaluationRequest{Header: execution.InternalExecutionHeader{ExecutionID: "execution", Contract: contractRef, DuePlans: []execution.DuePlan{due}, Requirements: []execution.DataRequirement{requirement}, EffectiveTimeFacts: effective, RequiredPhysicalQueries: []execution.PlannedPhysicalQueryRef{{Digest: "physical", QueryRevision: "query"}}, DeadlineUnixMilli: 200000}, Inputs: inputs, State: execution.StatePreflightResult{Items: []execution.RuntimeStateView{stateView}}, Gaps: execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{Identity: execution.PlanGapIdentity{Plan: id, StateGeneration: "state-v1"}, Status: execution.GapMissing}}}}
}
