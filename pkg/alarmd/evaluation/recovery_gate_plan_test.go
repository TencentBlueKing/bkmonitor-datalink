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

// A RECOVERY decided beside a Level that cannot answer sends its envelope
// and reaches the Plan's counts under that Level's state, run through the
// evaluator and the result contract the Worker applies to the same result.
// The count is how the change is read in production: a count that stayed at
// zero because the evaluator never added to it would read like a deployment
// where no RECOVERY ever went beside such a Level. The same record with both
// Levels' histories complete sends it too and counts nothing.
func TestEvaluatorSendsARecoveryBesideAnUnavailableLevelAndCountsIt(t *testing.T) {
	for _, arm := range []struct {
		name          string
		siblingWarm   bool
		wantBeside    uint64
		wantEnvelopes int
	}{
		{name: "beside a sibling Level warming, sent and counted", siblingWarm: true, wantBeside: 1, wantEnvelopes: 1},
		{name: "both Levels complete, sent and not counted", siblingWarm: false, wantBeside: 0, wantEnvelopes: 1},
	} {
		t.Run(arm.name, func(t *testing.T) {
			// The beside arm needs a sibling that genuinely cannot answer, not
			// merely one under a guard. Since decision-022 a guarded Level
			// whose recovery window is fully observed recovers, so a sibling
			// asking for one window would close the envelope rather than hold
			// it. Asking for two consecutive windows is what the loaded
			// history cannot give: the older of the two was anomalous, so the
			// run of misses ends there and the Level stays unavailable, which
			// is the state this gate is about.
			plan := compiledTwoLevelsShaped(t, "50", "50", func(p *contract.EvaluationPlanV2) {
				if !arm.siblingWarm {
					return
				}
				p.StrategyIR.Levels[1].RecoveryPlan.Config = json.RawMessage(`{"enabled":true,"consecutive_windows":2}`)
			})
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
			want := execution.RecoveryGateCounts{BesideLevelUnavailable: arm.wantBeside}
			if plan0.RecoveryGate != want {
				t.Fatalf("gate counts=%+v want %+v", plan0.RecoveryGate, want)
			}
			if len(plan0.StateResults) != 1 {
				t.Fatalf("state results=%d want the record's state written either way", len(plan0.StateResults))
			}
			if got := len(plan0.StateResults[0].Events); got != arm.wantEnvelopes {
				t.Fatalf("envelopes=%d want %d", got, arm.wantEnvelopes)
			}
			// Nothing is held, so no outcome says it was: the result contract,
			// which the Worker runs on this very result, then expects the
			// envelope, and the record has it.
			if plan0.LevelOutcomes[0].EnvelopeHeld || plan0.LevelOutcomes[1].EnvelopeHeld {
				t.Fatalf("EnvelopeHeld = (%t, %t), want neither", plan0.LevelOutcomes[0].EnvelopeHeld, plan0.LevelOutcomes[1].EnvelopeHeld)
			}
			if err := result.Validate(req); err != nil {
				t.Fatalf("the result contract refused the evaluator's own result: %v", err)
			}
		})
	}
}

// The hold is a property of the record, stated on each of its RECOVERY
// outcomes. The contract pins that in both directions on a real two-Level
// result: a record held on every RECOVERY outcome is accepted without its
// envelope, a record held on one RECOVERY outcome and not the other is
// refused, and a hold beside an ABNORMAL outcome of the same record is
// refused. A partial hold let through would be a record with some of its
// envelope sent and some not, which is worse than either whole answer.
func TestResultContractPinsTheHoldAcrossLevels(t *testing.T) {
	recovered := func(t *testing.T) (execution.EvaluationResult, execution.EvaluationRequest) {
		t.Helper()
		plan := compiledTwoLevels(t)
		history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
			{LevelID: 5, DetectFingerprint: plan.Levels()[0].Fingerprints().Detect, Result: execution.LevelFactAnomalous},
			{LevelID: 6, DetectFingerprint: plan.Levels()[1].Fingerprints().Detect, Result: execution.LevelFactAnomalous},
		}}}
		req := requestFixtureTwoLevels(t, plan, json.RawMessage(`10`), history)
		result, err := newEvaluator(t).Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("Evaluate()=%v", err)
		}
		outcomes := result.Plans[0].LevelOutcomes
		if len(outcomes) != 2 || outcomes[0].Outcome != execution.LevelOutcomeRecovery || outcomes[1].Outcome != execution.LevelOutcomeRecovery ||
			len(result.Plans[0].StateResults) != 1 || len(result.Plans[0].StateResults[0].Events) != 1 {
			t.Fatalf("fixture did not recover both Levels with one envelope: %+v", result.Plans[0])
		}
		return result, req
	}

	t.Run("held on every RECOVERY outcome and without its envelope is accepted", func(t *testing.T) {
		result, req := recovered(t)
		result.Plans[0].LevelOutcomes[0].EnvelopeHeld = true
		result.Plans[0].LevelOutcomes[1].EnvelopeHeld = true
		result.Plans[0].StateResults[0].Events = nil
		if err := result.Validate(req); err != nil {
			t.Fatalf("a record held on both RECOVERY outcomes was refused: %v", err)
		}
	})

	t.Run("held on one RECOVERY outcome and not the other is refused", func(t *testing.T) {
		result, req := recovered(t)
		result.Plans[0].LevelOutcomes[1].EnvelopeHeld = true
		result.Plans[0].StateResults[0].Events = nil
		if err := result.Validate(req); err == nil || !strings.Contains(err.Error(), "disagree on whether its envelope was held") {
			t.Fatalf("a partial hold must be refused, got %v", err)
		}
	})

	t.Run("a hold beside an ABNORMAL outcome of the same record is refused", func(t *testing.T) {
		// Level 6 triggers at 5 while Level 5 recovers at 50: the record is
		// ABNORMAL with one RECOVERY sibling, and the gate is never consulted.
		plan := compiledTwoLevelsWithThresholds(t, "50", "5")
		history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{
			{LevelID: 5, DetectFingerprint: plan.Levels()[0].Fingerprints().Detect, Result: execution.LevelFactAnomalous},
			{LevelID: 6, DetectFingerprint: plan.Levels()[1].Fingerprints().Detect, Result: execution.LevelFactAnomalous},
		}}}
		req := requestFixtureTwoLevels(t, plan, json.RawMessage(`10`), history)
		result, err := newEvaluator(t).Evaluate(context.Background(), req)
		if err != nil {
			t.Fatalf("Evaluate()=%v", err)
		}
		outcomes := result.Plans[0].LevelOutcomes
		if outcomes[0].Outcome != execution.LevelOutcomeRecovery || outcomes[1].Outcome != execution.LevelOutcomeAbnormal || outcomes[0].EnvelopeHeld {
			t.Fatalf("fixture did not produce RECOVERY beside ABNORMAL with no hold: %+v", outcomes)
		}
		if err := result.Validate(req); err != nil {
			t.Fatalf("the evaluator's own ABNORMAL result was refused: %v", err)
		}
		result.Plans[0].LevelOutcomes[0].EnvelopeHeld = true
		if err := result.Validate(req); err == nil || !strings.Contains(err.Error(), "cannot stand beside an ABNORMAL outcome") {
			t.Fatalf("a hold beside an ABNORMAL outcome must be refused, got %v", err)
		}
	})
}

func compiledTwoLevels(t *testing.T) *strategy.CompiledPlan {
	return compiledTwoLevelsWithThresholds(t, "50", "50")
}

// compiledTwoLevelsWithThresholds compiles Levels 5 and 6 with their own
// Threshold values, so a record can put the two Levels in different states.
func compiledTwoLevelsWithThresholds(t *testing.T, threshold5, threshold6 string) *strategy.CompiledPlan {
	return compiledTwoLevelsShaped(t, threshold5, threshold6, nil)
}

// compiledTwoLevelsShaped lets a test reshape the Plan document before it is
// compiled, for the shapes the default fixture does not have: a frozen
// revision, a wire format, an output identity.
func compiledTwoLevelsShaped(t *testing.T, threshold5, threshold6 string, shape func(*contract.EvaluationPlanV2)) *strategy.CompiledPlan {
	t.Helper()
	c, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{MaxPlanBytes: 1 << 20, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16, MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 16, MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32, MaxTriggerComputeCost: 1 << 20, MaxCompiledPlanBytes: 1 << 20, MaxCacheEntries: 16, MaxCacheBytes: 1 << 20, NegativeCacheTTL: time.Minute, BudgetRevision: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "r1"}
	projection := contract.InputProjectionV2{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired}
	level := func(id, priority uint32, threshold string) contract.LevelIRV2 {
		return contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: id, Priority: priority}, Connector: contract.LevelConnectorAND, DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: "Threshold", Version: 1, Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"` + threshold + `"}]}]}`)}}}, TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)}, RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)}}
	}
	p := contract.EvaluationPlanV2{PlanID: "7", StrategyRef: ref, InputProjection: projection, StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref, InputProjection: projection, ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120}, Levels: []contract.LevelIRV2{level(5, 1, threshold5), level(6, 2, threshold6)}}}
	if shape != nil {
		shape(&p)
	}
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
