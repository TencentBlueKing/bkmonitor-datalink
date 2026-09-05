package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/detect"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/trigger"
)

func TestEvaluatorProducesValidatedProvisionalAbnormalWithoutMutatingViews(t *testing.T) {
	req := requestFixture(t, json.RawMessage(`80`), nil)
	before := req.State.Items[0].BlobRevision
	result, err := newEvaluator(t).Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate() error=%v", err)
	}
	if len(result.Plans) != 1 || len(result.Plans[0].StateResults) != 1 || len(result.Plans[0].StateResults[0].Events) != 1 || result.Plans[0].LevelOutcomes[0].Outcome != execution.LevelOutcomeAbnormal {
		t.Fatalf("result=%+v", result)
	}
	if req.State.Items[0].BlobRevision != before || len(req.State.Items[0].History) != 0 {
		t.Fatal("Evaluator mutated State view")
	}
	if err := result.Validate(req); err != nil {
		t.Fatalf("Validate()=%v", err)
	}
}

func TestEvaluatorUsesHistoryForNormalThenRecovery(t *testing.T) {
	history := []execution.StateHistoryPoint{{RecordID: strings.Repeat("a", 64), SourceTime: 40, Levels: []execution.StateLevelFact{{LevelID: 5, DetectFingerprint: "", Result: execution.LevelFactAnomalous}}}}
	req := requestFixture(t, json.RawMessage(`10`), history)
	history[0].Levels[0].DetectFingerprint = req.Header.DuePlans[0].CompiledPlan.Levels()[0].Fingerprints().Detect
	req.State.Items[0].History = history
	result, err := newEvaluator(t).Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate()=%v", err)
	}
	if got := result.Plans[0].LevelOutcomes[0].Outcome; got != execution.LevelOutcomeRecovery {
		t.Fatalf("outcome=%s want RECOVERY", got)
	}
}

func TestEvaluatorRejectsBatchBudgetWithoutSideEffectPorts(t *testing.T) {
	e := newEvaluator(t)
	e.limits.MaxRecords = 0
	if _, err := e.Evaluate(context.Background(), requestFixture(t, json.RawMessage(`80`), nil)); err == nil {
		t.Fatal("Evaluate accepted zero budget")
	}
}

func TestEvaluatorRejectsRecordLimitBeforeCopyingPrimaryView(t *testing.T) {
	req := requestFixture(t, json.RawMessage(`80`), nil)
	count := 20_000
	records := make([]contract.CanonicalRecordV2, count)
	ordinals := make([]uint32, count)
	for index := range records {
		records[index] = contract.CanonicalRecordV2{RecordID: strings.Repeat("b", 64), SourceTime: 60,
			DimensionIdentity: contract.DimensionIdentityV2{Digest: string(req.Inputs[0].SeriesIdentity)}}
		ordinals[index] = uint32(index)
	}
	dataset := execution.NewDataset(records)
	view, err := execution.NewDatasetView(dataset, ordinals)
	if err != nil {
		t.Fatal(err)
	}
	for input := range req.Inputs {
		for binding := range req.Inputs[input].Inputs {
			if req.Inputs[input].Inputs[binding].Role == execution.InputRolePrimary {
				req.Inputs[input].Inputs[binding].Dataset, req.Inputs[input].Inputs[binding].View = dataset, view
			}
		}
	}
	evaluator := newEvaluator(t)
	allocation := testing.Benchmark(func(b *testing.B) {
		for index := 0; index < b.N; index++ {
			_, err := evaluator.Evaluate(context.Background(), req)
			if err == nil || !strings.Contains(err.Error(), "record budget exceeded") {
				b.Fatalf("error=%v", err)
			}
		}
	})
	if allocation.AllocedBytesPerOp() > 64<<10 {
		t.Fatalf("oversized PRIMARY was copied before rejection: %d bytes/op", allocation.AllocedBytesPerOp())
	}
}

// This measures one supported 500-record series, not the process heap or the
// maximum combination of levels and history. The input is built outside timing.
func TestEvaluatorRetainsSupportedRecordLimit(t *testing.T) {
	records := make([]contract.CanonicalRecordV2, 500)
	for i := range records {
		records[i] = contract.CanonicalRecordV2{RecordID: fmt.Sprintf("%064d", i), SourceTime: int64(100 + i*60), BusinessID: "2",
			DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)},
			Values:            map[string]json.RawMessage{"value": json.RawMessage(`80`)},
			Dimensions:        map[string]json.RawMessage{"host": json.RawMessage(`"` + strings.Repeat("h", 4096) + `"`)}, ReceivedTime: int64(100 + i*60)}
	}
	req := requestFixtureForPlan(t, compiled(t), records, nil)
	evaluator := newEvaluator(t)
	evaluator.limits.MaxRecords = 500
	evaluator.limits.Trigger.MaxEvidenceBytesPerEvent = 64 << 10
	allocation := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			result, err := evaluator.Evaluate(context.Background(), req)
			if err != nil {
				b.Fatal(err)
			}
			if len(result.Plans) != 1 || len(result.Plans[0].LevelOutcomes) != 500 || len(result.Plans[0].StateResults) != 1 || len(result.Plans[0].StateResults[0].Events) != 500 {
				b.Fatal("supported series lost records or exceeded one folded state mutation")
			}
		}
	})
	t.Logf("500 records, one level, 4 KiB dimensions: %d bytes/op (total allocations, not live heap)", allocation.AllocedBytesPerOp())
}

func TestEvaluatorFoldsSameSeriesRecordsInSourceOrder(t *testing.T) {
	series := strings.Repeat("c", 64)
	records := []contract.CanonicalRecordV2{
		{RecordID: strings.Repeat("d", 64), SourceTime: 240, BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: series}, Values: map[string]json.RawMessage{"value": json.RawMessage(`80`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 240},
		{RecordID: strings.Repeat("b", 64), SourceTime: 180, BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: series}, Values: map[string]json.RawMessage{"value": json.RawMessage(`80`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 180},
	}
	req := requestFixtureForPlan(t, compiledWindow(t, 2, 2), records, nil)
	req.State.Items[0].Status = execution.StateMissingWarming
	req.State.Items[0].BlobRevision = 0
	req.State.Items[0].Levels = nil
	evaluated, err := newEvaluator(t).Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate()=%v", err)
	}
	result := evaluated.Plans[0]
	if len(result.LevelOutcomes) != 2 || result.LevelOutcomes[0].Record.SourceTime != 180 || result.LevelOutcomes[1].Record.SourceTime != 240 {
		t.Fatalf("outcomes are not source ordered: %+v", result.LevelOutcomes)
	}
	if result.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown || result.LevelOutcomes[1].Outcome != execution.LevelOutcomeAbnormal {
		t.Fatalf("N-of-M did not observe provisional history: %+v", result.LevelOutcomes)
	}
	if len(result.StateResults) != 1 || len(result.StateResults[0].Mutation.Points) != 2 || len(result.StateResults[0].Mutation.AffectedRecords) != 2 || len(result.StateResults[0].Events) != 1 {
		t.Fatalf("same series was not folded to one bounded mutation: %+v", result.StateResults)
	}
	if result.StateResults[0].Mutation.Levels[0].HistoryCompleteness != execution.HistoryWarming {
		t.Fatalf("ABNORMAL prematurely cleared recovery guard: %+v", result.StateResults[0].Mutation.Levels)
	}
	if result.StateResults[0].Mutation.Points[0].SourceTime != 180 || result.StateResults[0].Mutation.Points[1].SourceTime != 240 {
		t.Fatalf("mutation points are not source ordered: %+v", result.StateResults[0].Mutation.Points)
	}
}

func TestEvaluatorInactiveLevelStillAdvancesDetectHistory(t *testing.T) {
	req := requestFixtureForPlan(t, compiledWindowWithUptime(t, 1, 1, true), []contract.CanonicalRecordV2{{RecordID: strings.Repeat("b", 64), SourceTime: 100, BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)}, Values: map[string]json.RawMessage{"value": json.RawMessage(`80`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 100}}, nil)
	if got := req.Header.EffectiveTimeFacts[0].Fact.Status(); got != strategy.EffectiveTimeInactive {
		t.Fatalf("fixture status=%s, want INACTIVE", got)
	}
	evaluated, err := newEvaluator(t).Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate()=%v", err)
	}
	result := evaluated.Plans[0]
	if len(result.StateResults) != 1 || len(result.StateResults[0].Mutation.Points) != 1 || result.StateResults[0].Mutation.Points[0].Levels[0].Result != execution.LevelFactAnomalous {
		t.Fatalf("INACTIVE detect history did not advance: %+v", result.StateResults)
	}
	if len(result.StateResults[0].Events) != 0 {
		t.Fatalf("INACTIVE level emitted event: %+v", result.StateResults[0].Events)
	}
}

func TestEvaluatorBoundsPersistedHistoryAcrossOneSeriesBatch(t *testing.T) {
	series := strings.Repeat("c", 64)
	records := make([]contract.CanonicalRecordV2, 0, 3)
	for i, source := range []int64{300, 180, 240} {
		records = append(records, contract.CanonicalRecordV2{RecordID: strings.Repeat(string(rune('d'+i)), 64), SourceTime: source, BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: series}, Values: map[string]json.RawMessage{"value": json.RawMessage(`80`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: source})
	}
	req := requestFixtureForPlan(t, compiledWindow(t, 2, 2), records, nil)
	req.State.Items[0].Status = execution.StateMissingWarming
	req.State.Items[0].BlobRevision = 0
	req.State.Items[0].Levels = nil
	evaluated, err := newEvaluator(t).Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate()=%v", err)
	}
	result := evaluated.Plans[0]
	if len(result.StateResults) != 1 || len(result.StateResults[0].Mutation.AffectedRecords) != 3 || len(result.StateResults[0].Mutation.Points) != 2 {
		t.Fatalf("history was not bounded independently from affected records: %+v", result.StateResults)
	}
}

func TestEvaluatorClearsSeriesWarmingOnlyOnFollowingFullSlot(t *testing.T) {
	plan := compiledWindow(t, 2, 2)
	series := strings.Repeat("c", 64)
	cold := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{
		{RecordID: strings.Repeat("b", 64), SourceTime: 180, BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: series}, Values: map[string]json.RawMessage{"value": json.RawMessage(`80`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 180},
		{RecordID: strings.Repeat("d", 64), SourceTime: 240, BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: series}, Values: map[string]json.RawMessage{"value": json.RawMessage(`80`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 240},
	}, nil)
	cold.State.Items[0].Status = execution.StateMissingWarming
	cold.State.Items[0].BlobRevision = 0
	cold.State.Items[0].Levels = nil
	first, err := newEvaluator(t).Evaluate(context.Background(), cold)
	if err != nil {
		t.Fatalf("cold Evaluate()=%v", err)
	}
	firstPlan := first.Plans[0]
	if firstPlan.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown || firstPlan.LevelOutcomes[0].ReasonCode != execution.ReasonCode(contract.ReasonHistoryWarming) ||
		firstPlan.LevelOutcomes[1].Outcome != execution.LevelOutcomeAbnormal || len(firstPlan.StateResults[0].Events) != 1 ||
		firstPlan.StateResults[0].Mutation.Levels[0].HistoryCompleteness != execution.HistoryWarming ||
		firstPlan.StateResults[0].Mutation.Levels[0].GapReasonCode != execution.ReasonCode(contract.ReasonHistoryWarming) {
		t.Fatalf("cold slot did not retain its required guard: %+v", firstPlan)
	}

	next := requestFixtureForPlan(t, plan, []contract.CanonicalRecordV2{{RecordID: strings.Repeat("e", 64), SourceTime: 300, BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: series}, Values: map[string]json.RawMessage{"value": json.RawMessage(`10`)}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 300}}, nil)
	next.State.Items[0] = stateViewFromMutation(firstPlan.StateResults[0].Mutation, execution.StateFoundWarming)
	second, err := newEvaluator(t).Evaluate(context.Background(), next)
	if err != nil {
		t.Fatalf("next Evaluate()=%v", err)
	}
	secondPlan := second.Plans[0]
	if secondPlan.LevelOutcomes[0].Outcome != execution.LevelOutcomeRecovery || len(secondPlan.StateResults[0].Events) != 1 || secondPlan.StateResults[0].Mutation.Levels[0].HistoryCompleteness != execution.HistoryFull || secondPlan.StateResults[0].Mutation.Levels[0].GapReasonCode != "" {
		t.Fatalf("following FULL slot did not converge WARMING: %+v", secondPlan)
	}
}

func TestEvaluatorDoesNotClearLoadedGappedWithoutEvidence(t *testing.T) {
	req := requestFixture(t, json.RawMessage(`10`), nil)
	req.State.Items[0].Status = execution.StateFoundGapped
	req.State.Items[0].Levels[0].HistoryCompleteness = execution.HistoryGapped
	req.State.Items[0].Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonGapSkipped)
	result, err := newEvaluator(t).Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate()=%v", err)
	}
	plan := result.Plans[0]
	if plan.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown || plan.LevelOutcomes[0].ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) ||
		len(plan.StateResults) != 1 || plan.StateResults[0].Mutation.Levels[0].HistoryCompleteness != execution.HistoryGapped ||
		plan.StateResults[0].Mutation.Levels[0].GapReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) || len(plan.StateResults[0].Events) != 0 {
		t.Fatalf("GAPPED was cleared without evidence: %+v", plan)
	}
}

func TestEvaluatorPreservesLoadedPlanGapReasonInUnknownOutcomeAndState(t *testing.T) {
	req := requestFixture(t, json.RawMessage(`10`), nil)
	due := req.Header.DuePlans[0]
	applyVersion, err := execution.BuildApplyVersion(req.Header.Contract, due.StateApplyEpoch)
	if err != nil {
		t.Fatal(err)
	}
	req.Gaps.Items[0] = execution.GapGuardSnapshot{
		Identity:                execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
		MarkerRevision:          1,
		PersistedApplyVersion:   applyVersion,
		PersistedMutationDigest: "gap-digest",
		Status:                  execution.GapFound,
		LastScheduleRevision:    due.ScheduleRevision,
		Scopes: []execution.GapScopeState{{
			Status:            execution.GapStatusGapped,
			ReasonCode:        execution.ReasonCode(contract.ReasonGapSkipped),
			RequiredFullSlots: 1,
		}},
	}

	result, err := newEvaluator(t).Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("Evaluate()=%v", err)
	}
	plan := result.Plans[0]
	if len(plan.LevelOutcomes) != 1 || plan.LevelOutcomes[0].Outcome != execution.LevelOutcomeUnknown ||
		plan.LevelOutcomes[0].ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) {
		t.Fatalf("outcome did not preserve GAP_SKIPPED: %+v", plan.LevelOutcomes)
	}
	if len(plan.StateResults) != 1 || len(plan.StateResults[0].Mutation.Levels) != 1 ||
		plan.StateResults[0].Mutation.Levels[0].HistoryCompleteness != execution.HistoryGapped ||
		plan.StateResults[0].Mutation.Levels[0].GapReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) {
		t.Fatalf("state did not preserve GAP_SKIPPED: %+v", plan.StateResults)
	}
}

func TestEvaluatorAdvancesLoadedPlanGapAfterFullStateMutation(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   execution.GapStatus
		observed uint32
		wantKind execution.GapMutationKind
	}{
		{name: "warmup", status: execution.GapStatusGapped, observed: 0, wantKind: execution.GapWarmup},
		{name: "clear", status: execution.GapStatusWarming, observed: 2, wantKind: execution.GapClear},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := requestFixture(t, json.RawMessage(`10`), nil)
			due := req.Header.DuePlans[0]
			persistedVersion, err := execution.BuildApplyVersion(req.Header.Contract, due.StateApplyEpoch)
			if err != nil {
				t.Fatal(err)
			}
			req.Gaps.Items[0] = execution.GapGuardSnapshot{
				Identity:                execution.PlanGapIdentity{Plan: due.Identity, StateGeneration: due.StateGeneration},
				MarkerRevision:          7,
				PersistedApplyVersion:   persistedVersion,
				PersistedMutationDigest: "gap-digest",
				Status:                  execution.GapFound,
				LastScheduleRevision:    due.ScheduleRevision,
				Scopes: []execution.GapScopeState{{
					Status:            test.status,
					ReasonCode:        execution.ReasonCode(contract.ReasonGapSkipped),
					RequiredFullSlots: 3,
					ObservedFullSlots: test.observed,
				}},
			}

			result, err := newEvaluator(t).Evaluate(context.Background(), req)
			if err != nil {
				t.Fatalf("Evaluate()=%v", err)
			}
			plan := result.Plans[0]
			if len(plan.StateResults) != 1 {
				t.Fatalf("FULL input did not produce durable state before gap recovery: %+v", plan.StateResults)
			}
			if len(plan.GuardAfterState) != 1 || len(plan.GuardAfterState[0].Scopes) != 1 {
				t.Fatalf("FULL input did not produce one post-state gap mutation: %+v", plan.GuardAfterState)
			}
			mutation := plan.GuardAfterState[0]
			if mutation.Identity != req.Gaps.Items[0].Identity || mutation.ExpectedMarkerRevision != 7 ||
				mutation.ScheduleRevision != due.ScheduleRevision || mutation.Scopes[0].Kind != test.wantKind {
				t.Fatalf("post-state gap mutation = %+v", mutation)
			}
			if test.wantKind == execution.GapWarmup {
				if mutation.Scopes[0].ReasonCode != execution.ReasonCode(contract.ReasonGapSkipped) ||
					mutation.Scopes[0].RequiredFullSlots != 3 {
					t.Fatalf("warmup mutation lost recovery contract: %+v", mutation.Scopes[0])
				}
			} else if mutation.Scopes[0].ReasonCode != "" || mutation.Scopes[0].RequiredFullSlots != 0 {
				t.Fatalf("clear mutation retained stale recovery payload: %+v", mutation.Scopes[0])
			}
			if err := mutation.ValidateDigest(); err != nil {
				t.Fatalf("gap mutation digest is invalid: %v", err)
			}
		})
	}
}

func stateViewFromMutation(mutation execution.StateMutation, status execution.StateLoadStatus) execution.RuntimeStateView {
	levels := make([]execution.RuntimeLevelStateView, len(mutation.Levels))
	for i, level := range mutation.Levels {
		levels[i] = execution.RuntimeLevelStateView{LevelID: level.LevelID, LevelStateCompatibility: level.LevelStateCompatibility, HistoryCompleteness: level.HistoryCompleteness, GapReasonCode: level.GapReasonCode, WarmupRequirementRef: level.WarmupRequirementRef, LastProcessedEventTime: level.LastProcessedEventTime}
	}
	return execution.RuntimeStateView{Identity: mutation.Identity, Status: status, BlobRevision: mutation.ExpectedBlobRevision + 1, VersionComparison: execution.ApplyVersionPersistedOlder, History: append([]execution.StateHistoryPoint(nil), mutation.Points...), Levels: levels, SeriesGuard: mutation.SeriesGuard}
}

func TestEvaluatorWarmingAndGappedNeverEmitNormalOrRecovery(t *testing.T) {
	for _, completeness := range []execution.HistoryCompleteness{execution.HistoryWarming, execution.HistoryGapped} {
		t.Run(string(completeness), func(t *testing.T) {
			req := requestFixture(t, json.RawMessage(`10`), nil)
			req.State.Items[0].Levels[0].HistoryCompleteness = completeness
			if completeness == execution.HistoryWarming {
				req.State.Items[0].Status = execution.StateFoundWarming
				req.State.Items[0].Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonHistoryWarming)
			} else {
				req.State.Items[0].Status = execution.StateFoundGapped
				req.State.Items[0].Levels[0].GapReasonCode = execution.ReasonCode(contract.ReasonHistoryGapped)
			}
			evaluated, err := newEvaluator(t).Evaluate(context.Background(), req)
			if err != nil {
				t.Fatalf("Evaluate()=%v", err)
			}
			result := evaluated.Plans[0]
			out := result.LevelOutcomes[0]
			if out.Outcome == execution.LevelOutcomeNormal || out.Outcome == execution.LevelOutcomeRecovery || len(result.StateResults) != 1 || len(result.StateResults[0].Mutation.Points) != 1 {
				t.Fatalf("unsafe result=%+v", result)
			}
		})
	}
}

func newEvaluator(t *testing.T) *Evaluator {
	d, err := detect.NewEvaluator(detect.NewDefaultRegistry(), nil)
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(d, Limits{MaxPlans: 4, MaxRecords: 16, MaxLevels: 16, Trigger: trigger.EvaluationLimitsV2{MaxLevels: 16, MaxTriggerWindowSize: 16, MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32, MaxLevelResultsPerEvent: 16, MaxEvidenceBytesPerEvent: 1 << 20, MaxComputeCost: 1 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func requestFixture(t *testing.T, value json.RawMessage, history []execution.StateHistoryPoint) execution.EvaluationRequest {
	return requestFixtureForPlan(t, compiled(t), []contract.CanonicalRecordV2{{RecordID: strings.Repeat("b", 64), SourceTime: 100, BusinessID: "2", DimensionIdentity: contract.DimensionIdentityV2{Digest: strings.Repeat("c", 64)}, Values: map[string]json.RawMessage{"value": value}, Dimensions: map[string]json.RawMessage{}, ReceivedTime: 100}}, history)
}

func requestFixtureForPlan(t *testing.T, plan *strategy.CompiledPlan, records []contract.CanonicalRecordV2, history []execution.StateHistoryPoint) execution.EvaluationRequest {
	id := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	due := execution.DuePlan{Identity: id, CompiledPlan: plan, StateGeneration: "state-v1", StateApplyEpoch: 1, ScheduleRevision: "plan-schedule", CompletionDeadlineUnixMilli: 200000}
	consumer := execution.ConsumerRef{Plan: id, LevelID: 5, HasLevel: true}
	requirement := execution.DataRequirement{RequirementID: "main", DatasetName: "main", Role: execution.InputRolePrimary, LogicalQueryRef: "q", RelativeWindow: execution.RelativeQueryWindow{StartOffsetSeconds: -60, EndOffsetSeconds: 0, HalfOpen: true}, StepMillis: 60000, AlignmentMillis: 60000, ResultWindowPolicy: execution.ResultWindowExactHalfOpen, ReadinessClass: execution.ReadinessEager, RequiredColumns: []string{"value"}, Consumers: []execution.DataRequirementConsumer{{Consumer: consumer, ConsumerDeadlineUnixMilli: 200000, DownstreamExecutionReserveMilliSec: 1000}}}
	digest, err := execution.DeriveDuePlanSetDigest([]execution.DuePlan{due}, []execution.DataRequirement{requirement})
	if err != nil {
		t.Fatal(err)
	}
	contractRef := execution.FrozenExecutionContractRef{Slot: execution.SlotIdentity{QueryGroup: "qg", EvaluationTime: 100}, SnapshotRevision: "snapshot", QueryRevision: "query", ScheduleRevision: "schedule", ScheduleSegmentStart: 100, DuePlanSetDigest: digest}
	series := execution.SeriesIdentityDigest(records[0].DimensionIdentity.Digest)
	dataset := execution.NewDataset(records)
	ordinals := make([]uint32, len(records))
	for i := range records {
		ordinals[i] = uint32(i)
	}
	view, _ := execution.NewDatasetView(dataset, ordinals)
	binding := execution.NamedInputBinding{Consumer: consumer, RequirementID: "main", DatasetName: "main", Role: execution.InputRolePrimary, ProviderResult: "provider", QueryWindow: execution.QueryWindow{Start: 40, End: 100}, Dataset: dataset, View: view, Completeness: execution.CompletenessFull, DataState: execution.DataStateData, Disposition: execution.AccessAvailable, ImpactScope: execution.ImpactPlan, Provenance: execution.InputProvenance{PhysicalQuery: "physical", AttemptNo: 1}}
	provider := strategy.NewStaticScheduleProvider(strategy.TimezoneResolverFunc(func(context.Context, string, string, string) (*time.Location, error) {
		return time.UTC, nil
	}))
	facts, err := provider.Resolve(context.Background(), []strategy.EffectiveTimeRequest{{TenantID: "tenant", BusinessID: "2", EvaluationTime: 100, Requirement: plan.Levels()[0].EffectiveTimeRequirement()}})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := execution.DeriveRuntimeLevelContractRefs(plan)
	if err != nil {
		t.Fatal(err)
	}
	stateView := execution.RuntimeStateView{Identity: execution.StateKeyIdentity{Plan: id, StateGeneration: "state-v1", SeriesIdentityDigest: series}, Status: execution.StateFoundReady, BlobRevision: 1, VersionComparison: execution.ApplyVersionPersistedOlder, History: append([]execution.StateHistoryPoint(nil), history...), Levels: []execution.RuntimeLevelStateView{{LevelID: 5, LevelStateCompatibility: refs[0].LevelStateCompatibility, WarmupRequirementRef: refs[0].WarmupRequirementRef, HistoryCompleteness: execution.HistoryFull}}}
	return execution.EvaluationRequest{Header: execution.InternalExecutionHeader{ExecutionID: "execution", Contract: contractRef, DuePlans: []execution.DuePlan{due}, Requirements: []execution.DataRequirement{requirement}, EffectiveTimeFacts: []execution.BoundEffectiveTimeFact{{Consumer: consumer, SeriesIdentity: series, Fact: facts[0]}}, RequiredPhysicalQueries: []execution.PlannedPhysicalQueryRef{{Digest: "physical", QueryRevision: "query"}}, DeadlineUnixMilli: 200000}, Inputs: []execution.SeriesEvaluationInputRequest{{Contract: contractRef, Consumer: consumer, SeriesIdentity: series, RequirementIDs: []execution.RequirementID{"main"}, Inputs: []execution.NamedInputBinding{binding}}}, State: execution.StatePreflightResult{Items: []execution.RuntimeStateView{stateView}}, Gaps: execution.GapLoadResult{Items: []execution.GapGuardSnapshot{{Identity: execution.PlanGapIdentity{Plan: id, StateGeneration: "state-v1"}, Status: execution.GapMissing}}}}
}

func compiled(t *testing.T) *strategy.CompiledPlan {
	return compiledWindow(t, 1, 1)
}

func compiledWindow(t *testing.T, windowSize, requiredAnomalies uint32) *strategy.CompiledPlan {
	return compiledWindowWithUptime(t, windowSize, requiredAnomalies, false)
}

func compiledWindowWithUptime(t *testing.T, windowSize, requiredAnomalies uint32, uptime bool) *strategy.CompiledPlan {
	c, err := strategy.NewCompiler(strategy.NewDefaultAlgorithmCompilerRegistry(), strategy.Limits{MaxPlanBytes: 1 << 20, MaxLevelsPerPlan: 16, MaxAlgorithmsPerLevel: 8, MaxGroupsPerAlgorithm: 16, MaxConditionsPerAlgorithm: 64, MaxASTNodesPerLevel: 256, MaxTriggerWindowSize: 16, MaxRecoveryConsecutiveWindows: 16, MaxRequiredHistoryPoints: 32, MaxTriggerComputeCost: 1 << 20, MaxCompiledPlanBytes: 1 << 20, MaxCacheEntries: 16, MaxCacheBytes: 1 << 20, NegativeCacheTTL: time.Minute, BudgetRevision: "test"})
	if err != nil {
		t.Fatal(err)
	}
	ref := contract.StrategyRefV2{TenantID: "tenant", StrategyID: "7", Revision: "r1"}
	projection := contract.InputProjectionV2{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id", MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired}
	triggerBody := map[string]any{"window_size": windowSize, "required_anomalies": requiredAnomalies, "step_seconds": 60}
	if uptime {
		triggerBody["timezone_ref"] = "BUSINESS_LOCAL"
		triggerBody["uptime"] = map[string]any{"time_ranges": []any{map[string]any{"start": "09:00", "end": "17:00"}}, "active_calendars": []any{}, "calendars": []any{}}
	}
	triggerPayload, err := json.Marshal(triggerBody)
	if err != nil {
		t.Fatal(err)
	}
	triggerConfig := json.RawMessage(triggerPayload)
	level := contract.LevelIRV2{Definition: contract.LevelDefinitionV2{LevelID: 5, Priority: 1}, Connector: contract.LevelConnectorAND, DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: "Threshold", Version: 1, Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`)}}}, TriggerPlan: contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: triggerConfig}, RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)}}
	p := contract.EvaluationPlanV2{PlanID: "7", StrategyRef: ref, InputProjection: projection, StrategyIR: contract.StrategyIRV2{Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref, InputProjection: projection, ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120}, Levels: []contract.LevelIRV2{level}}}
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

var _ = observability.ReasonNone
