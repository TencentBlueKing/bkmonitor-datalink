// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package worker

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/admission"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/targetplan"
)

// What the Slot resolved is handed to the source for admission and to the
// no-data round for absence from one holder. Every resolution is handed to
// admission, the unavailable one included: the members its static list and
// its other selectors did resolve still admit their records, and only
// absence reads it as unavailable. A Plan nothing resolved at all is left
// out, and its filter admits nothing.
func TestOneResolutionServesAdmissionAndAbsence(t *testing.T) {
	complete := execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "1"}
	incomplete := execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "2"}
	unavailable := execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "3"}
	unresolved := execution.PlanIdentity{TenantID: "t", BusinessID: "2", StrategyID: "4"}
	stream := &streamedExecution{targetResolutions: map[execution.PlanIdentity]*resolvedTarget{
		complete:    {absence: nodata.TargetResolution{State: nodata.TargetResolutionComplete, Members: []string{"101"}}, members: map[string]struct{}{"101": {}}},
		incomplete:  {absence: nodata.TargetResolution{State: nodata.TargetResolutionIncomplete, Members: []string{"101"}}, members: map[string]struct{}{"101": {}}},
		unavailable: {absence: nodata.TargetResolution{State: nodata.TargetResolutionUnavailable, Members: []string{"101"}}, members: map[string]struct{}{"101": {}}},
		unresolved:  {absence: nodata.TargetResolution{State: nodata.TargetResolutionUnavailable}, unresolved: true},
	}}
	memberships := stream.ResolvedTargets()
	if len(memberships) != 3 || memberships[complete] == nil || memberships[incomplete] == nil || memberships[unavailable] == nil || memberships[unresolved] != nil {
		t.Fatalf("memberships = %v, want every resolution but the unresolved Plan", memberships)
	}
	if !memberships[unavailable].Contains("101") {
		t.Fatal("the members an unavailable plan did resolve do not admit their records")
	}
	if !memberships[complete].Contains("101") || memberships[complete].Contains("102") {
		t.Fatal("membership does not answer from the resolved members")
	}
	if view := stream.targetResolutions[unavailable].absenceView(); view == nil || view.State != nodata.TargetResolutionUnavailable {
		t.Fatalf("absence view of the unavailable resolution = %+v", view)
	}
	if view := stream.targetResolutions[execution.PlanIdentity{StrategyID: "none"}].absenceView(); view != nil {
		t.Fatalf("absence view of nothing = %+v, want nil", view)
	}
	if (&streamedExecution{}).ResolvedTargets() != nil {
		t.Fatal("a Slot that resolved nothing hands out memberships")
	}
}

type scriptedTargetResolver struct {
	calls       []*contract.TargetPlanV1
	intervals   []time.Duration
	resolutions map[string]*targetplan.Resolution
}

func (resolver *scriptedTargetResolver) Resolve(_ context.Context, plan *contract.TargetPlanV1, interval time.Duration) *targetplan.Resolution {
	resolver.calls = append(resolver.calls, plan)
	resolver.intervals = append(resolver.intervals, interval)
	return resolver.resolutions[plan.StaticKeys[0]]
}

func targetPlanCompiledPlan(t *testing.T, strategyID string, target *contract.TargetPlanV1) *strategy.CompiledPlan {
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
		PlanID: strategyID, StrategyRef: ref, InputProjection: projection, TargetPlan: target,
		StrategyIR: contract.StrategyIRV2{
			Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2}, StrategyRef: ref, InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300, AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120},
			Levels: []contract.LevelIRV2{{
				Definition: contract.LevelDefinitionV2{LevelID: 5, Priority: 1}, Connector: contract.LevelConnectorAND,
				DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{Type: "Threshold", Version: 1,
					Config: json.RawMessage(`{"value_field":"value","data_unit":"percent","threshold_unit_prefix":"","precision":{"decimal_places":6,"rounding":"HALF_EVEN"},"groups":[{"conditions":[{"operator":"GTE","threshold_decimal":"50"}]}]}`)}}},
				TriggerPlan:  contract.TypedPlanV1{Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`)},
				RecoveryPlan: contract.TypedPlanV1{Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`)},
			}},
		},
	}
	result, err := compiler.Compile(context.Background(), strategy.CompileRequest{
		Plan: plan,
		DatasetContract: contract.DatasetContractV2{SchemaDigest: strings.Repeat("1", 64), NormalizationDigest: strings.Repeat("2", 64),
			IdentityFields: []string{"host"}, SourceTimeField: "time", ReceivedTimeField: "received_time"},
		StateSemantics: strategy.StateSemantics{StateSchemaVersion: "window-state-v1", CodecSemanticsVersion: "window-state-codec-v1",
			IdentitySchemaDigest: strings.Repeat("3", 64), SourceTimeSemanticsVersion: "source-time-seconds-v1", HistoryCellSemanticsVersion: "detect-history-cell-v1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("test plan did not compile: %+v", result.PlanTerminal())
	}
	return compiled
}

// At Begin every due Plan with a target plan is resolved once through the
// port, and the one resolution feeds both views: a Complete or Incomplete
// resolution is the admission membership and the absence view, an
// Unavailable one is absent from the memberships and unavailable to
// absence; a Plan without a target plan is never resolved; a worker with no
// resolver resolves every target-plan Plan as unavailable by name. Each
// resolution leaves one target_resolved line carrying its selectors.
func TestBeginResolvesEachTargetPlanOnceForBothViews(t *testing.T) {
	host := func(static string) *contract.TargetPlanV1 {
		return &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID,
			Identity: contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}, StaticKeys: []string{static}, DynamicGroups: []string{"1001"}}
	}
	complete := &targetplan.Resolution{Static: map[string]struct{}{"1": {}}, Selectors: []targetplan.SelectorResult{
		{Kind: targetplan.SelectorKindGroup, ID: "1001", State: targetplan.SelectorOK, Reason: targetplan.ReasonNone, Members: map[string]struct{}{"101": {}}, Kept: 1}}}
	complete.Compose()
	unavailable := &targetplan.Resolution{Static: map[string]struct{}{"2": {}}, Selectors: []targetplan.SelectorResult{
		{Kind: targetplan.SelectorKindGroup, ID: "1001", State: targetplan.SelectorUnavailable, Reason: targetplan.ReasonKeyMissing}}}
	unavailable.Compose()
	resolver := &scriptedTargetResolver{resolutions: map[string]*targetplan.Resolution{"1": complete, "2": unavailable}}
	var observations []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observation)
	})
	one := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "1"}
	two := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "2"}
	plain := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "3"}
	stream := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{ports: Ports{Targets: resolver, Observer: observer}},
		header: execution.InternalExecutionHeader{DuePlans: []execution.DuePlan{
			{Identity: one, CompiledPlan: targetPlanCompiledPlan(t, "1", host("1"))},
			{Identity: two, CompiledPlan: targetPlanCompiledPlan(t, "2", host("2"))},
			{Identity: plain, CompiledPlan: targetPlanCompiledPlan(t, "3", nil)},
		}},
	}
	stream.resolveTargetPlans(context.Background())
	if len(resolver.calls) != 2 || resolver.intervals[0] != time.Minute || resolver.intervals[1] != time.Minute {
		t.Fatalf("resolver called %d times with intervals %v, want once per target-plan Plan with the Plan's evaluation interval", len(resolver.calls), resolver.intervals)
	}
	memberships := stream.ResolvedTargets()
	if len(memberships) != 2 || memberships[one] == nil || !memberships[one].Contains("101") || !memberships[one].Contains("1") || memberships[one].Contains("2") {
		t.Fatalf("memberships = %v, want both resolved Plans with their own members", memberships)
	}
	// The unavailable Plan's static member still admits its records: the
	// group that could not be read contributes nothing, the static list is
	// not thereby unknown.
	if memberships[two] == nil || !memberships[two].Contains("2") || memberships[two].Contains("101") {
		t.Fatalf("unavailable Plan's membership = %v, want its static member and nothing from the unread group", memberships[two])
	}
	if view := stream.targetResolutions[one].absenceView(); view.State != nodata.TargetResolutionComplete || len(view.Members) != 2 {
		t.Fatalf("absence view of the complete Plan = %+v", view)
	}
	if view := stream.targetResolutions[two].absenceView(); view.State != nodata.TargetResolutionUnavailable {
		t.Fatalf("absence view of the unavailable Plan = %+v", view)
	}
	if _, resolved := stream.targetResolutions[plain]; resolved {
		t.Fatal("a Plan without a target plan was resolved")
	}
	lines := 0
	for _, observation := range observations {
		if observation.Stage != observability.StageTargetResolved || observation.TargetResolution == nil {
			continue
		}
		lines++
		facts := observation.TargetResolution
		switch facts.StrategyID {
		case "1":
			if facts.State != "Complete" || len(facts.Selectors) != 1 || facts.Selectors[0].Kept != 1 || observation.Result != observability.ResultSuccess {
				t.Fatalf("line for the complete Plan = %+v (%s)", facts, observation.Result)
			}
		case "2":
			if facts.State != "Unavailable" || facts.Selectors[0].Reason != targetplan.ReasonKeyMissing || observation.Result != observability.ResultDegraded {
				t.Fatalf("line for the unavailable Plan = %+v (%s)", facts, observation.Result)
			}
		default:
			t.Fatalf("a line for a Plan that was not resolved: %+v", facts)
		}
	}
	if lines != 2 {
		t.Fatalf("target_resolved lines = %d, want one per resolution", lines)
	}

	// No resolver: every target-plan Plan is unavailable, by the source's name.
	unwired := &streamedExecution{
		coordinator: &SlotExecutionCoordinator{ports: Ports{Observer: observer}},
		header:      execution.InternalExecutionHeader{DuePlans: []execution.DuePlan{{Identity: one, CompiledPlan: targetPlanCompiledPlan(t, "1", host("1"))}}},
	}
	unwired.resolveTargetPlans(context.Background())
	if unwired.ResolvedTargets() != nil || unwired.targetResolutions[one].absenceView().State != nodata.TargetResolutionUnavailable {
		t.Fatalf("without a resolver: memberships %v absence %+v", unwired.ResolvedTargets(), unwired.targetResolutions[one].absenceView())
	}
	last := observations[len(observations)-1]
	if last.TargetResolution == nil || last.TargetResolution.Selectors[0].Reason != targetplan.ReasonSourceUnwired {
		t.Fatalf("the unwired resolution did not say so: %+v", last.TargetResolution)
	}
}

// The leader's three-way assertion for a plan one of whose selectors could
// not be read: the static member's record is still admitted, a record the
// unread group would have named is rejected as outside the target - not as
// unresolved - and absence is paused by the selector's name. All three read
// the same holder, handed to the filter directly: that the holder reaches
// the source through ResolvedTargets whatever its state is the two tests
// above's to guard, not this one's.
func TestAnUnreadableSelectorPausesAbsenceAndNotTheStaticMembers(t *testing.T) {
	resolution := &targetplan.Resolution{Static: map[string]struct{}{"101": {}}, Selectors: []targetplan.SelectorResult{
		{Kind: targetplan.SelectorKindGroup, ID: "1001", State: targetplan.SelectorUnavailable, Reason: targetplan.ReasonKeyMissing}}}
	resolution.Compose()
	target := newResolvedTarget(resolution)
	identity := contract.TargetPlanIdentityV1{Dimensions: []string{"bk_host_id"}, HostIdentity: true}
	filter := admission.TargetPlanFilter{}
	record := func(host string) *admission.Facts {
		return &admission.Facts{Dimensions: map[string]json.RawMessage{"bk_host_id": json.RawMessage(`"` + host + `"`)}}
	}
	context := admission.PlanContext{TargetPlan: &admission.TargetPlanContext{Identity: identity, Members: target}}
	if decision := filter.Admit(context, record("101")); !decision.Admit {
		t.Fatalf("the static member was not admitted: %+v", decision)
	}
	if decision := filter.Admit(context, record("202")); decision.Admit || decision.Reason != admission.TargetPlanReasonOutOfTarget {
		t.Fatalf("a record of the unread group = %+v, want out_of_target, not unresolved", decision)
	}
	plan := &contract.EvaluationPlanV2{PlanID: "1", NoData: &contract.NoDataConfigV1{Continuous: 3, Level: 2, AggDimension: []string{"bk_host_id"}},
		TargetPlan: &contract.TargetPlanV1{SchemaVersion: 1, ModelID: "cw-Host", Rule: contract.TargetPlanRuleHostID, Identity: identity, StaticKeys: []string{"101"}, DynamicGroups: []string{"1001"}}}
	result, outcome, err := nodata.EvaluateSlot(nodata.SlotInput{Plan: plan, EvaluationTime: 1000, PeriodSeconds: 60,
		Completeness: execution.CompletenessFull, TargetResolution: target.absenceView(), Memory: map[string]nodata.GroupMemory{}})
	if err != nil || outcome != nodata.OutcomeSkippedTargetSelectorUnavailable || len(result.Verdicts) != 0 {
		t.Fatalf("absence = %q %v verdicts %v, want paused by the selector's name", outcome, err, result.Verdicts)
	}
}
