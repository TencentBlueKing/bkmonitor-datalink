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
	if !normalized.Available() || normalized.Value().Micros() != 80_000_000 {
		t.Fatalf("Normalize(0.8) = %+v", normalized)
	}
	if !detector.Predicate().Evaluate(normalized.Value()) {
		t.Fatal("Predicate() did not match the normalized threshold")
	}

	cases := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{name: "absent", raw: nil, want: contract.ReasonRequiredValueMissing},
		{name: "null", raw: json.RawMessage(`null`), want: contract.ReasonRequiredValueMissing},
		{name: "type mismatch", raw: json.RawMessage(`"0.8"`), want: contract.ReasonRequiredValueTypeMismatch},
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
			if !value.Available() || predicate.Evaluate(value.Value()) != test.want {
				t.Fatalf("%s(%s, 5) = %v", test.operator, test.value, predicate.Evaluate(value.Value()))
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
		if got := predicate.Evaluate(value.Value()); got != test.want {
			t.Fatalf("OR-of-AND(%s) = %v, want %v", test.value, got, test.want)
		}
	}
}

func TestThresholdUsesSixPlaceHalfEvenRounding(t *testing.T) {
	config := thresholdConfig("1.234564")
	config["data_unit"] = "none"
	predicate, normalizer := compileThresholdForTest(t, config)
	if value := normalizer.Normalize(json.RawMessage(`1.2345645`)); value.Value().Micros() != 1_234_564 || !predicate.Evaluate(value.Value()) {
		t.Fatalf("Normalize(1.2345645) = %+v", value)
	}

	config = thresholdConfig("1.234566")
	config["data_unit"] = "none"
	predicate, normalizer = compileThresholdForTest(t, config)
	if value := normalizer.Normalize(json.RawMessage(`1.2345655`)); value.Value().Micros() != 1_234_566 || !predicate.Evaluate(value.Value()) {
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

func (brokenAlgorithmCompiler) Kind() string    { return "Broken" }
func (brokenAlgorithmCompiler) Version() uint32 { return 1 }
func (brokenAlgorithmCompiler) Compile(context.Context, AlgorithmCompileContext, contract.AlgorithmIRV2) (AlgorithmCompileResult, error) {
	return AlgorithmCompileResult{}, errors.New("controlled compiler failure")
}

func compileThresholdForTest(t *testing.T, config map[string]any) (Predicate, NumericNormalizerSpec) {
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

func newTestCompiler(t *testing.T) *PlanCompiler {
	t.Helper()
	compiler, err := NewCompiler(NewDefaultAlgorithmCompilerRegistry(), testLimits())
	if err != nil {
		t.Fatalf("NewCompiler() error = %v", err)
	}
	return compiler
}

func testLimits() Limits {
	return Limits{
		MaxPlanBytes:              64 << 10,
		MaxLevelsPerPlan:          16,
		MaxAlgorithmsPerLevel:     8,
		MaxGroupsPerAlgorithm:     16,
		MaxConditionsPerAlgorithm: 64,
		MaxASTNodesPerLevel:       256,
		MaxRequiredHistoryPoints:  4096,
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

func mustCompilePlan(t *testing.T, compiler *PlanCompiler, plan contract.EvaluationPlanV2) *CompiledPlan {
	t.Helper()
	result, err := compiler.Compile(context.Background(), validRequest(plan))
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("Compile() terminal = %+v, levels = %+v", result.PlanTerminal(), result.LevelTerminals())
	}
	return compiled
}

func cloneLevels(levels []contract.LevelIRV2) []contract.LevelIRV2 {
	payload := mustJSON(levels)
	var cloned []contract.LevelIRV2
	if err := json.Unmarshal(payload, &cloned); err != nil {
		panic(err)
	}
	return cloned
}
