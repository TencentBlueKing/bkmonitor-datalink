// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package strategy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestCompilerCompilesDynamicLevelsInDeterministicOrders(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	plan.StrategyIR.Levels = []contract.LevelIRV2{
		validLevel(5, 1, "50"),
		validLevel(1, 20, "50"),
	}

	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("Compile() plan terminal = %+v", result.PlanTerminal())
	}
	levels := compiled.Levels()
	if got := []uint32{levels[0].Definition().LevelID, levels[1].Definition().LevelID}; got[0] != 1 || got[1] != 5 {
		t.Fatalf("Levels() IDs = %v, want [1 5]", got)
	}
	priority := compiled.LevelsByPriority()
	if got := []uint32{priority[0].Definition().LevelID, priority[1].Definition().LevelID}; got[0] != 5 || got[1] != 1 {
		t.Fatalf("LevelsByPriority() IDs = %v, want [5 1]", got)
	}
	if ref := compiled.PlanRef(); ref.StrategyID != "1001" || ref.StrategyRevision != "strategy-r1" || len(ref.StateCompatibilityHash) != 64 {
		t.Fatalf("PlanRef() = %+v", ref)
	}
}

func TestCompilerIsolatesInvalidSiblingLevel(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	bad := validLevel(5, 1, "50")
	bad.DetectPlan.Algorithms[0].Type = "Unknown"
	plan.StrategyIR.Levels = append(plan.StrategyIR.Levels, bad)

	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	compiled, ok := result.Plan()
	if !ok || len(compiled.Levels()) != 1 {
		t.Fatalf("Compile() valid levels = %d, terminal = %+v", len(compiled.Levels()), result.PlanTerminal())
	}
	terminals := result.LevelTerminals()
	if len(terminals) != 1 || terminals[0].LevelID != 5 || terminals[0].ReasonCode != contract.ReasonAlgorithmUnsupported {
		t.Fatalf("LevelTerminals() = %+v", terminals)
	}
}

func TestCompilerRejectsDuplicateLevelIDsAtPlanScope(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	plan.StrategyIR.Levels = append(plan.StrategyIR.Levels, validLevel(1, 30, "80"))

	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if _, ok := result.Plan(); ok {
		t.Fatal("Compile() unexpectedly returned a plan")
	}
	terminal := result.PlanTerminal()
	if terminal == nil || terminal.ReasonCode != contract.ReasonPlanDuplicateLevelID {
		t.Fatalf("PlanTerminal() = %+v", terminal)
	}
}

func TestCompilerThresholdNormalizerAndPredicate(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	plan.InputProjection.DataUnit = "percentunit"
	plan.StrategyIR.InputProjection.DataUnit = "percentunit"
	plan.StrategyIR.Levels[0] = validLevel(1, 1, "80")
	config := thresholdConfig("80")
	config["data_unit"] = "percentunit"
	config["threshold_unit_prefix"] = "%"
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config = mustJSON(config)

	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("Compile() terminal = %+v", result.PlanTerminal())
	}
	detector := compiled.Levels()[0].Detectors()[0]
	normalizer, ok := compiled.Normalizer(detector.NormalizerRef())
	if !ok {
		t.Fatalf("Normalizer(%q) not found", detector.NormalizerRef())
	}
	normalized := normalizer.Normalize(json.RawMessage(`0.8`))
	if !normalized.Available() || normalized.Value().CanonicalDecimal() != "80.000000" {
		t.Fatalf("Normalize(0.8) = %+v", normalized)
	}
	evaluation, err := detector.Predicate().Evaluate(normalized.Value())
	if err != nil || !evaluation.Matched() || evaluation.MatchedGroup() != 0 || len(evaluation.PredicateDigest()) != 64 {
		t.Fatal("Predicate() did not match the normalized threshold")
	}

	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{name: "absent", raw: nil, want: contract.ReasonRequiredValueMissing},
		{name: "null", raw: json.RawMessage(`null`), want: contract.ReasonRequiredValueMissing},
		{name: "whitespace only", raw: json.RawMessage(" \t\n"), want: contract.ReasonRequiredValueMissing},
		{name: "string", raw: json.RawMessage(`"0.8"`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "boolean", raw: json.RawMessage(`true`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "object", raw: json.RawMessage(`{}`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "array", raw: json.RawMessage(`[]`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "leading zero", raw: json.RawMessage(`01`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "negative leading zero", raw: json.RawMessage(`-01`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "trailing token", raw: json.RawMessage(`1 true`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "trailing decimal point", raw: json.RawMessage(`1.`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "missing integer", raw: json.RawMessage(`.1`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "missing exponent", raw: json.RawMessage(`1e`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "nan", raw: json.RawMessage(`NaN`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "positive infinity", raw: json.RawMessage(`Infinity`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "negative infinity", raw: json.RawMessage(`-Infinity`), want: contract.ReasonRequiredValueTypeMismatch},
		{name: "overflow", raw: json.RawMessage(`1e100`), want: contract.ReasonRequiredValueNormalizationFailed},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := normalizer.Normalize(test.raw)
			if got.ReasonCode() != test.want {
				t.Fatalf("Normalize(%s) reason = %q, want %q", test.raw, got.ReasonCode(), test.want)
			}
		})
	}

	withWhitespace := normalizer.Normalize(json.RawMessage(" \t0.8\n"))
	if !withWhitespace.Available() || withWhitespace.Value().CanonicalDecimal() != "80.000000" {
		t.Fatalf("Normalize() with JSON whitespace = %+v", withWhitespace)
	}
}

func TestCompilerCalculatesTriggerRecoveryAndFingerprints(t *testing.T) {
	compiler := newTestCompiler(t)
	base := validPlan()
	base.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":5,"required_anomalies":3,"step_seconds":60}`)
	base.StrategyIR.Levels[0].RecoveryPlan.Config = json.RawMessage(`{"enabled":true,"consecutive_windows":4}`)

	first := mustCompilePlan(t, compiler, base)
	level := first.Levels()[0]
	if got := level.RequiredDetectHistoryPoints(); got != 8 {
		t.Fatalf("RequiredDetectHistoryPoints() = %d, want 8", got)
	}
	requirement := level.StateRequirement()
	if requirement.RetentionPoints != 8 || requirement.RetentionPoints != requirement.RequiredDetectHistoryPoints {
		t.Fatalf("StateRequirement() = %+v, want equal 8-point history and retention", requirement)
	}
	if got := first.EvaluationSemantics().LatenessTolerance; got != 120 {
		t.Fatalf("EvaluationSemantics().LatenessTolerance = %d, want separate value 120", got)
	}
	if got := level.ResourceEstimate().StatePointsPerSeries; got != uint64(requirement.RetentionPoints) {
		t.Fatalf("ResourceEstimate().StatePointsPerSeries = %d, want %d", got, requirement.RetentionPoints)
	}
	firstFingerprints := level.Fingerprints()

	thresholdChanged := base
	thresholdChanged.StrategyIR.Levels = cloneLevels(base.StrategyIR.Levels)
	thresholdChanged.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config = mustJSON(thresholdConfig("60"))
	second := mustCompilePlan(t, compiler, thresholdChanged)
	secondFingerprints := second.Levels()[0].Fingerprints()
	if firstFingerprints.Detect == secondFingerprints.Detect || firstFingerprints.Trigger == secondFingerprints.Trigger {
		t.Fatalf("threshold change did not change both fingerprints: before=%+v after=%+v", firstFingerprints, secondFingerprints)
	}
	if first.StateCompatibilityHash() != second.StateCompatibilityHash() {
		t.Fatal("threshold change unexpectedly changed state compatibility hash")
	}

	triggerChanged := base
	triggerChanged.StrategyIR.Levels = cloneLevels(base.StrategyIR.Levels)
	triggerChanged.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":6,"required_anomalies":3,"step_seconds":60}`)
	third := mustCompilePlan(t, compiler, triggerChanged)
	thirdFingerprints := third.Levels()[0].Fingerprints()
	if firstFingerprints.Detect != thirdFingerprints.Detect || firstFingerprints.Trigger == thirdFingerprints.Trigger {
		t.Fatalf("trigger change fingerprints: before=%+v after=%+v", firstFingerprints, thirdFingerprints)
	}
}

func TestCompilerUsesTriggerWindowAsRetentionWhenRecoveryDisabled(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(`{"window_size":5,"required_anomalies":3,"step_seconds":60}`)
	plan.StrategyIR.Levels[0].RecoveryPlan.Config = json.RawMessage(`{"enabled":false,"consecutive_windows":99}`)

	compiled := mustCompilePlan(t, compiler, plan)
	level := compiled.Levels()[0]
	requirement := level.StateRequirement()
	if requirement.RetentionPoints != 5 || requirement.RequiredDetectHistoryPoints != 5 {
		t.Fatalf("StateRequirement() = %+v, want trigger window size 5", requirement)
	}
	if got := compiled.EvaluationSemantics().LatenessTolerance; got != 120 {
		t.Fatalf("EvaluationSemantics().LatenessTolerance = %d, want separate value 120", got)
	}
	if got := level.ResourceEstimate().StatePointsPerSeries; got != uint64(requirement.RetentionPoints) {
		t.Fatalf("ResourceEstimate().StatePointsPerSeries = %d, want %d", got, requirement.RetentionPoints)
	}
}

func TestCompilerIsolatesTriggerRecoveryDeploymentBudgets(t *testing.T) {
	tests := []struct {
		name      string
		trigger   string
		recovery  string
		fieldPath string
		terminal  bool
	}{
		{
			name:     "trigger window at limit",
			trigger:  `{"window_size":64,"required_anomalies":1,"step_seconds":60}`,
			recovery: `{"enabled":false,"consecutive_windows":0}`,
		},
		{
			name:      "trigger window",
			trigger:   `{"window_size":65,"required_anomalies":1,"step_seconds":60}`,
			recovery:  `{"enabled":false,"consecutive_windows":0}`,
			fieldPath: "level.trigger_plan",
			terminal:  true,
		},
		{
			name:     "recovery windows at limit",
			trigger:  `{"window_size":1,"required_anomalies":1,"step_seconds":60}`,
			recovery: `{"enabled":true,"consecutive_windows":64}`,
		},
		{
			name:      "recovery windows",
			trigger:   `{"window_size":1,"required_anomalies":1,"step_seconds":60}`,
			recovery:  `{"enabled":true,"consecutive_windows":65}`,
			fieldPath: "level.recovery_plan",
			terminal:  true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limits := testLimits()
			limits.MaxTriggerWindowSize = 64
			limits.MaxRecoveryConsecutiveWindows = 64
			compiler, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), limits)
			if err != nil {
				t.Fatal(err)
			}
			plan := validPlan()
			plan.StrategyIR.Levels[0].TriggerPlan.Config = json.RawMessage(test.trigger)
			plan.StrategyIR.Levels[0].RecoveryPlan.Config = json.RawMessage(test.recovery)

			result := mustCompileResult(t, compiler, plan)
			terminals := result.LevelTerminals()
			compiled, ok := result.Plan()
			if !test.terminal {
				if !ok || len(compiled.Levels()) != 1 || len(terminals) != 0 {
					t.Fatalf("Compile() plan=%#v terminals=%#v, want admitted Level", compiled, terminals)
				}
				return
			}
			if !ok || len(compiled.Levels()) != 0 || len(terminals) != 1 ||
				terminals[0].ReasonCode != contract.ReasonLevelBudgetExceeded || terminals[0].FieldPath != test.fieldPath {
				t.Fatalf("Compile() plan=%#v terminals=%#v, want isolated Level", compiled, terminals)
			}
		})
	}
}

func TestCompilerTerminalizesPlanAboveTriggerComputeBudget(t *testing.T) {
	limits := testLimits()
	limits.MaxTriggerComputeCost = 7
	compiler, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), limits)
	if err != nil {
		t.Fatal(err)
	}
	plan := validPlan()
	plan.StrategyIR.Levels = append(plan.StrategyIR.Levels, validLevel(2, 2, "60"))
	for index := range plan.StrategyIR.Levels {
		plan.StrategyIR.Levels[index].RecoveryPlan.Config = json.RawMessage(`{"enabled":true,"consecutive_windows":3}`)
	}

	result := mustCompileResult(t, compiler, plan)
	terminal := result.PlanTerminal()
	if terminal == nil || terminal.ReasonCode != contract.ReasonPlanBudgetExceeded || terminal.FieldPath != "trigger_compute" {
		t.Fatalf("PlanTerminal() = %#v", terminal)
	}
}

func TestCompilerReturnsImmutableViews(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	compiled := mustCompilePlan(t, compiler, plan)

	levels := compiled.Levels()
	levels[0] = CompiledLevel{}
	detectors := compiled.Levels()[0].Detectors()
	detectors[0] = DetectorSpec{}
	if compiled.Levels()[0].Definition().LevelID != 1 || compiled.Levels()[0].Detectors()[0].Kind() != "Threshold" {
		t.Fatal("caller mutation changed immutable compiled plan")
	}

	plan.StrategyIR.Levels[0].Definition.LevelID = 99
	if compiled.Levels()[0].Definition().LevelID != 1 {
		t.Fatal("source mutation changed compiled plan")
	}
}

func TestCompiledPlanReadOnlyViewsDoNotClonePredicateAST(t *testing.T) {
	config := thresholdConfig("50")
	groups := make([]any, 16)
	for group := range groups {
		conditions := make([]any, 4)
		for condition := range conditions {
			conditions[condition] = map[string]any{"operator": "GTE", "threshold_decimal": "50"}
		}
		groups[group] = map[string]any{"conditions": conditions}
	}
	config["groups"] = groups
	plan := validPlan()
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config = mustJSON(config)
	compiled := mustCompilePlan(t, newTestCompiler(t), plan)

	allocations := testing.AllocsPerRun(1000, func() {
		readOnlyLevelsSink = compiled.Levels()
		readOnlyDetectorsSink = readOnlyLevelsSink[0].Detectors()
		readOnlyPredicateSink = readOnlyDetectorsSink[0].Predicate()
	})
	if allocations > 2 {
		t.Fatalf("read-only views allocated %.1f objects per access, want at most 2 outer slice copies", allocations)
	}
}

func TestPredicateEvaluateRejectsUnvalidatedValue(t *testing.T) {
	predicate, normalizer := compileThresholdForTest(t, thresholdConfig("50"))
	unvalidated := Predicate{root: predicate.root, digest: predicate.digest}
	value := normalizer.Normalize(json.RawMessage(`60`))
	if _, err := unvalidated.Evaluate(value.Value()); err == nil {
		t.Fatal("Evaluate() accepted a Predicate without the compiled invariant marker")
	}
	if _, err := predicate.Evaluate(value.Value()); err != nil {
		t.Fatalf("compiled Predicate Evaluate() error = %v", err)
	}
}

func TestCompilerRejectsUnsupportedScopeAndBudget(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	plan.StrategyIR.ExecutionSemantics.EvaluationScope = contract.EvaluationScopeCrossSeries
	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if terminal := result.PlanTerminal(); terminal == nil || terminal.ReasonCode != contract.ReasonPlanInvalid {
		t.Fatalf("scope PlanTerminal() = %+v", terminal)
	}

	tiny := testLimits()
	tiny.MaxConditionsPerAlgorithm = 0
	if _, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), tiny); err == nil {
		t.Fatal("NewCompiler() accepted a zero deployment budget")
	}

	tiny = testLimits()
	tiny.MaxConditionsPerAlgorithm = 1
	compiler, err = NewCompiler(NewDefaultAlgorithmCompilerRegistry(), tiny)
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	config := thresholdConfig("50")
	config["groups"] = []any{map[string]any{"conditions": []any{
		map[string]any{"operator": "GT", "threshold_decimal": "50"},
		map[string]any{"operator": "LT", "threshold_decimal": "100"},
	}}}
	plan = validPlan()
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config = mustJSON(config)
	result, err = compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	terminals := result.LevelTerminals()
	if len(terminals) != 1 || terminals[0].ReasonCode != contract.ReasonLevelBudgetExceeded {
		t.Fatalf("budget LevelTerminals() = %+v", terminals)
	}
}

func TestCompilerDoesNotTerminalizeAlgorithmCompilerFailure(t *testing.T) {
	registry, err := NewAlgorithmCompilerRegistry(thresholdAlgorithmCompiler{}, brokenAlgorithmCompiler{})
	if err != nil {
		t.Fatalf("NewAlgorithmCompilerRegistry() error = %v", err)
	}
	compiler, err := NewCompiler(registry, testLimits())
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	plan := validPlan()
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0] = contract.AlgorithmIRV2{Type: "Broken", Version: 1, Config: json.RawMessage(`{}`)}

	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err == nil || !strings.Contains(err.Error(), "controlled compiler failure") {
		t.Fatalf("Compile() result = %+v, error = %v", result, err)
	}
}

func TestCompilerCanonicalizesDeclaredExecutorErrors(t *testing.T) {
	registry, err := NewAlgorithmCompilerRegistry(declaredErrorsAlgorithmCompiler{reasons: []string{
		contract.ReasonRequiredValueTypeMismatch,
		contract.ReasonRecordInvalid,
		contract.ReasonRequiredValueTypeMismatch,
	}})
	if err != nil {
		t.Fatalf("NewAlgorithmCompilerRegistry() error = %v", err)
	}
	compiler, err := NewCompiler(registry, testLimits())
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	plan := validPlan()
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Type = "DeclaredErrors"
	compiled := mustCompilePlan(t, compiler, plan)
	reasons := compiled.Levels()[0].Detectors()[0].DeclaredExecutorErrors()
	if len(reasons) != 2 || reasons[0] != contract.ReasonRecordInvalid || reasons[1] != contract.ReasonRequiredValueTypeMismatch {
		t.Fatalf("DeclaredExecutorErrors() = %v", reasons)
	}
}

func TestCompilerRejectsDeclaredExecutorErrorOutsideReceiptObservation(t *testing.T) {
	registry, err := NewAlgorithmCompilerRegistry(declaredErrorsAlgorithmCompiler{reasons: []string{contract.ReasonAuditDrop}})
	if err != nil {
		t.Fatalf("NewAlgorithmCompilerRegistry() error = %v", err)
	}
	compiler, err := NewCompiler(registry, testLimits())
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	plan := validPlan()
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Type = "DeclaredErrors"
	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err == nil || !strings.Contains(err.Error(), "invalid compiler output") {
		t.Fatalf("Compile() result = %+v, error = %v", result, err)
	}
}

func TestCompilerRequiresRecoveryEnabledField(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	plan.StrategyIR.Levels[0].RecoveryPlan.Config = json.RawMessage(`{"consecutive_windows":1}`)

	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	terminals := result.LevelTerminals()
	if len(terminals) != 1 || terminals[0].ReasonCode != contract.ReasonLevelInvalid {
		t.Fatalf("LevelTerminals() = %+v", terminals)
	}
}

func TestCompilerRequiresThresholdUnitPrefixField(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	config := thresholdConfig("50")
	delete(config, "threshold_unit_prefix")
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config = mustJSON(config)

	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	terminals := result.LevelTerminals()
	if len(terminals) != 1 || terminals[0].ReasonCode != contract.ReasonLevelInvalid {
		t.Fatalf("LevelTerminals() = %+v", terminals)
	}
}

func TestCompilerAcceptsCompatibleHigherMinor(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	plan.StrategyIR.Schema.Minor = 1
	plan.StrategyIR.RequiredFeatures = []string{"reader-owned-future-feature"}
	if _, ok := mustCompileResult(t, compiler, plan).Plan(); !ok {
		t.Fatal("compatible higher minor accepted by M0 did not compile")
	}
}

func TestThresholdSupportsLargeNormalizedValues(t *testing.T) {
	config := thresholdConfig("10")
	config["data_unit"] = "tbytes"
	config["threshold_unit_prefix"] = "Ti"
	predicate, normalizer := compileThresholdForTest(t, config)
	value := normalizer.Normalize(json.RawMessage(`10`))
	if !value.Available() || value.Value().CanonicalDecimal() != "10995116277760.000000" {
		t.Fatalf("Normalize(10 TiB) = %+v", value)
	}
	evaluation, err := predicate.Evaluate(value.Value())
	if err != nil || !evaluation.Matched() {
		t.Fatalf("Predicate(10 TiB) = %+v, %v", evaluation, err)
	}
}

func TestCompilerRejectsInvalidRegistryOutputAsInternalError(t *testing.T) {
	registry, err := NewAlgorithmCompilerRegistry(thresholdAlgorithmCompiler{}, zeroAlgorithmCompiler{})
	if err != nil {
		t.Fatalf("NewAlgorithmCompilerRegistry() error = %v", err)
	}
	compiler, err := NewCompiler(registry, testLimits())
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	plan := validPlan()
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0] = contract.AlgorithmIRV2{Type: "Zero", Version: 1, Config: json.RawMessage(`{}`)}
	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err == nil || !strings.Contains(err.Error(), "invalid compiler output") {
		t.Fatalf("Compile() result = %+v, error = %v", result, err)
	}
}

func TestThresholdOperatorsAndOROfAND(t *testing.T) {
	operators := []struct {
		operator string
		value    string
		want     bool
	}{
		{operator: "GT", value: "6", want: true},
		{operator: "GTE", value: "5", want: true},
		{operator: "EQ", value: "5", want: true},
		{operator: "NEQ", value: "5", want: false},
		{operator: "LT", value: "4", want: true},
		{operator: "LTE", value: "5", want: true},
	}
	for _, test := range operators {
		t.Run(test.operator, func(t *testing.T) {
			config := thresholdConfig("5")
			config["data_unit"] = "none"
			config["groups"] = []any{map[string]any{"conditions": []any{
				map[string]any{"operator": test.operator, "threshold_decimal": "5"},
			}}}
			predicate, normalizer := compileThresholdForTest(t, config)
			value := normalizer.Normalize(json.RawMessage(test.value))
			evaluation, err := predicate.Evaluate(value.Value())
			if !value.Available() || err != nil || evaluation.Matched() != test.want {
				t.Fatalf("%s(%s, 5) = %+v, %v", test.operator, test.value, evaluation, err)
			}
		})
	}

	config := thresholdConfig("10")
	config["data_unit"] = "none"
	config["groups"] = []any{
		map[string]any{"conditions": []any{
			map[string]any{"operator": "GT", "threshold_decimal": "10"},
			map[string]any{"operator": "LT", "threshold_decimal": "20"},
		}},
		map[string]any{"conditions": []any{
			map[string]any{"operator": "EQ", "threshold_decimal": "30"},
		}},
	}
	predicate, normalizer := compileThresholdForTest(t, config)
	for _, test := range []struct {
		value string
		want  bool
	}{{"15", true}, {"25", false}, {"30", true}} {
		value := normalizer.Normalize(json.RawMessage(test.value))
		evaluation, err := predicate.Evaluate(value.Value())
		if err != nil || evaluation.Matched() != test.want {
			t.Fatalf("OR-of-AND(%s) = %+v, %v, want %v", test.value, evaluation, err, test.want)
		}
		if test.want && test.value == "30" && evaluation.MatchedGroup() != 1 {
			t.Fatalf("OR-of-AND(%s) matched group = %d, want 1", test.value, evaluation.MatchedGroup())
		}
	}
}

func TestThresholdUsesSixPlaceHalfEvenRounding(t *testing.T) {
	config := thresholdConfig("1.234564")
	config["data_unit"] = "none"
	predicate, normalizer := compileThresholdForTest(t, config)
	if value := normalizer.Normalize(json.RawMessage(`1.2345645`)); value.Value().CanonicalDecimal() != "1.234564" || mustEvaluate(t, predicate, value.Value()).Matched() == false {
		t.Fatalf("Normalize(1.2345645) = %+v", value)
	}

	config = thresholdConfig("1.234566")
	config["data_unit"] = "none"
	predicate, normalizer = compileThresholdForTest(t, config)
	if value := normalizer.Normalize(json.RawMessage(`1.2345655`)); value.Value().CanonicalDecimal() != "1.234566" || mustEvaluate(t, predicate, value.Value()).Matched() == false {
		t.Fatalf("Normalize(1.2345655) = %+v", value)
	}
}

func TestCompilerKeepsMultipleAlgorithmsAndRejectsUnknownUnit(t *testing.T) {
	compiler := newTestCompiler(t)
	plan := validPlan()
	second := plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0]
	second.Config = mustJSON(thresholdConfig("90"))
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms = append(plan.StrategyIR.Levels[0].DetectPlan.Algorithms, second)
	compiled := mustCompilePlan(t, compiler, plan)
	if level := compiled.Levels()[0]; level.Connector() != contract.LevelConnectorAND || len(level.Detectors()) != 2 {
		t.Fatalf("compiled Level = connector %q, detectors %d", level.Connector(), len(level.Detectors()))
	}

	plan = validPlan()
	plan.InputProjection.DataUnit = "unknown-unit"
	plan.StrategyIR.InputProjection.DataUnit = "unknown-unit"
	config := thresholdConfig("50")
	config["data_unit"] = "unknown-unit"
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config = mustJSON(config)
	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if terminals := result.LevelTerminals(); len(terminals) != 1 || terminals[0].ReasonCode != contract.ReasonLevelInvalid {
		t.Fatalf("unknown unit terminals = %+v", terminals)
	}
}

func TestCompilerFingerprintIgnoresRevisionPriorityAndLevelCode(t *testing.T) {
	compiler := newTestCompiler(t)
	base := mustCompilePlan(t, compiler, validPlan())
	changed := validPlan()
	changed.StrategyRef.Revision = "strategy-r2"
	changed.StrategyIR.StrategyRef.Revision = "strategy-r2"
	changed.StrategyIR.Levels[0].Definition.Priority = 99
	changed.StrategyIR.Levels[0].Definition.LevelCode = "critical"
	compiled := mustCompilePlan(t, compiler, changed)
	if base.StateCompatibilityHash() != compiled.StateCompatibilityHash() || base.Fingerprints() != compiled.Fingerprints() ||
		base.Levels()[0].Fingerprints() != compiled.Levels()[0].Fingerprints() {
		t.Fatalf("non-state semantic change altered fingerprints: base=%+v changed=%+v", base.Fingerprints(), compiled.Fingerprints())
	}
}

func TestCompilerCompilesM0GoldenEnvelope(t *testing.T) {
	payload, err := os.ReadFile("../contract/testdata/go-v2/execution_envelope_v2.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var envelope contract.ExecutionEnvelopeV2
	if err := json.Unmarshal(payload, &envelope); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	compiled := mustCompilePlan(t, newTestCompiler(t), envelope.PlanSet.EvaluationPlans[0])
	levels := compiled.LevelsByPriority()
	if len(levels) != 2 || levels[0].Definition().LevelID != 5 || levels[1].Definition().LevelID != 1 {
		t.Fatalf("golden LevelsByPriority() = %+v", levels)
	}
	if len(compiled.Fingerprints().Detect) != 64 || len(compiled.Fingerprints().Trigger) != 64 {
		t.Fatalf("golden fingerprints = %+v", compiled.Fingerprints())
	}
}

type brokenAlgorithmCompiler struct{}

func (brokenAlgorithmCompiler) Capability() AlgorithmCapability {
	return AlgorithmCapability{
		Kind: "Broken", Version: 1, EvaluationScope: contract.EvaluationScopeSeries, InputShape: "ROW",
		RequiredHistoryKind: "NONE", StateSchemaVersion: "broken-v1", Deterministic: true,
		FixedComputeCost: 1, CostPerRecord: 1,
	}
}
func (brokenAlgorithmCompiler) Compile(context.Context, AlgorithmCompileContext, contract.AlgorithmIRV2) (AlgorithmCompileResult, error) {
	return AlgorithmCompileResult{}, errors.New("controlled compiler failure")
}

type zeroAlgorithmCompiler struct{}

func (zeroAlgorithmCompiler) Capability() AlgorithmCapability {
	return AlgorithmCapability{
		Kind: "Zero", Version: 1, EvaluationScope: contract.EvaluationScopeSeries, InputShape: "ROW",
		RequiredHistoryKind: "NONE", StateSchemaVersion: "zero-v1", Deterministic: true,
		FixedComputeCost: 1, CostPerRecord: 1,
	}
}
func (zeroAlgorithmCompiler) Compile(context.Context, AlgorithmCompileContext, contract.AlgorithmIRV2) (AlgorithmCompileResult, error) {
	return AlgorithmCompileResult{}, nil
}

type declaredErrorsAlgorithmCompiler struct {
	reasons []string
}

var (
	readOnlyLevelsSink    []CompiledLevel
	readOnlyDetectorsSink []DetectorSpec
	readOnlyPredicateSink Predicate
)

func (declaredErrorsAlgorithmCompiler) Capability() AlgorithmCapability {
	return AlgorithmCapability{
		Kind: "DeclaredErrors", Version: 1, EvaluationScope: contract.EvaluationScopeSeries, InputShape: "ROW",
		RequiredHistoryKind: "NONE", StateSchemaVersion: "declared-errors-v1", Deterministic: true,
		FixedComputeCost: 1, CostPerRecord: 1,
	}
}

func (c declaredErrorsAlgorithmCompiler) Compile(ctx context.Context, compileContext AlgorithmCompileContext, raw contract.AlgorithmIRV2) (AlgorithmCompileResult, error) {
	compiled, err := (thresholdAlgorithmCompiler{}).Compile(ctx, compileContext, raw)
	if err != nil {
		return AlgorithmCompileResult{}, err
	}
	compiled.Detector.kind = "DeclaredErrors"
	compiled.Detector.declaredExecutorErrors = append([]string(nil), c.reasons...)
	return compiled, nil
}

func compileThresholdForTest(t testing.TB, config map[string]any) (Predicate, NumericNormalizerSpec) {
	t.Helper()
	plan := validPlan()
	dataUnit := config["data_unit"].(string)
	plan.InputProjection.DataUnit = dataUnit
	plan.StrategyIR.InputProjection.DataUnit = dataUnit
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config = mustJSON(config)
	compiled := mustCompilePlan(t, newTestCompiler(t), plan)
	detector := compiled.Levels()[0].Detectors()[0]
	normalizer, ok := compiled.Normalizer(detector.NormalizerRef())
	if !ok {
		t.Fatalf("Normalizer(%q) not found", detector.NormalizerRef())
	}
	return detector.Predicate(), normalizer
}

func mustEvaluate(t *testing.T, predicate Predicate, value NormalizedNumber) PredicateEvaluation {
	t.Helper()
	evaluation, err := predicate.Evaluate(value)
	if err != nil {
		t.Fatalf("Predicate.Evaluate() error = %v", err)
	}
	return evaluation
}

func newTestCompiler(t testing.TB) *PlanCompiler {
	t.Helper()
	compiler, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), testLimits())
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	return compiler
}

func testLimits() Limits {
	return Limits{
		MaxPlanBytes:                  64 << 10,
		MaxLevelsPerPlan:              16,
		MaxAlgorithmsPerLevel:         8,
		MaxGroupsPerAlgorithm:         16,
		MaxConditionsPerAlgorithm:     64,
		MaxASTNodesPerLevel:           256,
		MaxTriggerWindowSize:          4096,
		MaxRecoveryConsecutiveWindows: 4096,
		MaxRequiredHistoryPoints:      4096,
		MaxTriggerComputeCost:         1 << 20,
		MaxCompiledPlanBytes:          64 << 10,
		MaxCacheEntries:               64,
		MaxCacheBytes:                 4 << 20,
		NegativeCacheTTL:              time.Minute,
		BudgetRevision:                "test-budget-v1",
	}
}

func validRequest(plan contract.EvaluationPlanV2) CompileRequest {
	return CompileRequest{
		Plan: plan,
		DatasetContract: contract.DatasetContractV2{
			SchemaDigest:        strings.Repeat("1", 64),
			NormalizationDigest: strings.Repeat("2", 64),
			IdentityFields:      []string{"host"},
			SourceTimeField:     "time",
			ReceivedTimeField:   "received_time",
		},
		StateSemantics: StateSemantics{
			StateSchemaVersion:          "window-state-v1",
			CodecSemanticsVersion:       "window-state-codec-v1",
			IdentitySchemaDigest:        strings.Repeat("3", 64),
			SourceTimeSemanticsVersion:  "source-time-seconds-v1",
			HistoryCellSemanticsVersion: "detect-history-cell-v1",
		},
	}
}

func validPlan() contract.EvaluationPlanV2 {
	ref := contract.StrategyRefV2{TenantID: "default", StrategyID: "1001", Revision: "strategy-r1"}
	projection := contract.InputProjectionV2{
		ValueFields: []string{"value"}, DimensionFields: []string{"host"}, BusinessIdentityField: "bk_biz_id",
		MultiValueAlignment: "SINGLE_VALUE", DataUnit: "percent", MissingValuePolicy: contract.MissingValuePolicyRequired,
	}
	return contract.EvaluationPlanV2{
		PlanID: ref.StrategyID, StrategyRef: ref, InputProjection: projection,
		StrategyIR: contract.StrategyIRV2{
			Schema: contract.Schema{Name: contract.StrategyIRSchemaV2, Major: 2, Minor: 0}, RequiredFeatures: []string{},
			StrategyRef: ref, InputProjection: projection,
			ExecutionSemantics: contract.ExecutionSemanticsV2{
				EvaluationScope: contract.EvaluationScopeSeries, QueryWindow: 300,
				AggregationInterval: 60, EvaluationInterval: 60, LatenessTolerance: 120,
			},
			Levels: []contract.LevelIRV2{validLevel(1, 1, "50")},
		},
	}
}

func validLevel(levelID, priority uint32, threshold string) contract.LevelIRV2 {
	return contract.LevelIRV2{
		Definition: contract.LevelDefinitionV2{LevelID: levelID, Priority: priority},
		Connector:  contract.LevelConnectorAND,
		DetectPlan: contract.DetectPlanV2{Algorithms: []contract.AlgorithmIRV2{{
			Type: "Threshold", Version: 1, Config: mustJSON(thresholdConfig(threshold)),
		}}},
		TriggerPlan: contract.TypedPlanV1{
			Type: "N_OF_M", Version: 1, Config: json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60}`),
		},
		RecoveryPlan: contract.TypedPlanV1{
			Type: "CONTINUOUS_TRIGGER_MISS", Version: 1, Config: json.RawMessage(`{"enabled":true,"consecutive_windows":1}`),
		},
	}
}

func thresholdConfig(threshold string) map[string]any {
	return map[string]any{
		"value_field": "value", "data_unit": "percent", "threshold_unit_prefix": "",
		"precision": map[string]any{"decimal_places": 6, "rounding": "HALF_EVEN"},
		"groups": []any{map[string]any{"conditions": []any{
			map[string]any{"operator": "GTE", "threshold_decimal": threshold},
		}}},
	}
}

func mustJSON(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func mustCompilePlan(t testing.TB, compiler *PlanCompiler, plan contract.EvaluationPlanV2) *CompiledPlan {
	t.Helper()
	result := mustCompileResult(t, compiler, plan)
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("Compile() terminal = %+v, levels = %+v", result.PlanTerminal(), result.LevelTerminals())
	}
	return compiled
}

func mustCompileResult(t testing.TB, compiler *PlanCompiler, plan contract.EvaluationPlanV2) CompileResult {
	t.Helper()
	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	return result
}

func cloneLevels(levels []contract.LevelIRV2) []contract.LevelIRV2 {
	payload := mustJSON(levels)
	var cloned []contract.LevelIRV2
	if err := json.Unmarshal(payload, &cloned); err != nil {
		panic(err)
	}
	return cloned
}
