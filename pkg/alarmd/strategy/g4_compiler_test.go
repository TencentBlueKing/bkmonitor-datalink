package strategy

import (
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestG4DefaultRegistryCompilesFourIndependentAlgorithmKinds(t *testing.T) {
	tests := []struct {
		kind         string
		config       map[string]any
		projection   AlgorithmInputProjection
		requirements []AlgorithmInputRequirement
		assert       func(*testing.T, CompiledAlgorithmPlan)
	}{
		{
			kind: DetectorKindSimpleRingRatio, config: map[string]any{"floor": 50, "ceil": nil},
			projection:   AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}},
			requirements: g4Requirements(t, "previous"),
			assert: func(t *testing.T, algorithm CompiledAlgorithmPlan) {
				config, ok := algorithm.SimpleRingRatioConfig()
				if !ok || !config.FloorEnabled || config.FloorDecimal != "50.000000" || config.CeilEnabled {
					t.Fatalf("SimpleRingRatio config = %+v, ok=%v", config, ok)
				}
			},
		},
		{
			kind: DetectorKindOsRestart, config: map[string]any{},
			projection:   AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}},
			requirements: g4Requirements(t, "uptime_history"),
			assert: func(t *testing.T, algorithm CompiledAlgorithmPlan) {
				config, ok := algorithm.OsRestartConfig()
				if !ok || config.PrimaryExpression != "a <= 3600" || config.HistoryExpression != "a" ||
					!reflect.DeepEqual(config.HistoryOffsetsSeconds, []int64{60, 600, 1500}) {
					t.Fatalf("OsRestart config = %+v, ok=%v", config, ok)
				}
			},
		},
		{
			kind: DetectorKindProcPort, config: map[string]any{},
			projection: AlgorithmInputProjection{
				ValueFields:     []string{"value"},
				DimensionFields: []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"},
				IdentityFields:  []string{"bk_target_cloud_id", "bk_target_ip", "display_name"},
			},
			requirements: g4PrimaryRequirements(t, AlgorithmInputProjection{
				ValueFields:     []string{"value"},
				DimensionFields: []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"},
				IdentityFields:  []string{"bk_target_cloud_id", "bk_target_ip", "display_name"},
			}),
			assert: func(t *testing.T, algorithm CompiledAlgorithmPlan) {
				config, ok := algorithm.ProcPortConfig()
				if !ok || config.ValueField != "value" || config.SourceMetric != "proc_exists" || config.NonListenField != "nonlisten" ||
					config.NotAccurateListenField != "not_accurate_listen" {
					t.Fatalf("ProcPort config = %+v, ok=%v", config, ok)
				}
			},
		},
		{
			kind: DetectorKindPingUnreachable, config: map[string]any{},
			projection:   AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}},
			requirements: g4PrimaryRequirements(t, AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}),
			assert: func(t *testing.T, algorithm CompiledAlgorithmPlan) {
				config, ok := algorithm.PingUnreachableConfig()
				if !ok || config.ValueField != "value" || config.SourceMetric != "loss_percent" || config.ThresholdDecimal != "1.000000" {
					t.Fatalf("PingUnreachable config = %+v, ok=%v", config, ok)
				}
			},
		},
	}

	stateCompatibility := make(map[string]string, len(tests))
	for _, test := range tests {
		t.Run(test.kind, func(t *testing.T) {
			request := g4CompileRequest(t, test.kind, test.config, test.projection, test.requirements)
			compiled := mustCompileG4Request(t, newTestCompiler(t), request)
			stateCompatibility[test.kind] = compiled.StateCompatibilityHash()
			algorithms := compiled.Levels()[0].Algorithms()
			if len(algorithms) != 1 || algorithms[0].Kind() != test.kind || algorithms[0].Version() != 1 ||
				len(algorithms[0].AlgorithmPlanID()) != 64 || len(algorithms[0].CapabilityDigest()) != 64 ||
				len(algorithms[0].StateCompatibilityFingerprint()) != 64 {
				t.Fatalf("compiled algorithms = %+v", algorithms)
			}
			capability := algorithms[0].Capability()
			if capability.EvaluationScope != contract.EvaluationScopeSeries ||
				((test.kind == DetectorKindSimpleRingRatio || test.kind == DetectorKindOsRestart) && capability.RequiredHistoryKind != algorithmHistoryPlanLocalInput) ||
				((test.kind == DetectorKindProcPort || test.kind == DetectorKindPingUnreachable) && capability.RequiredHistoryKind != algorithmHistoryNone) {
				t.Fatalf("capability = %+v", capability)
			}
			if got := algorithms[0].InputRequirements(); !reflect.DeepEqual(got, test.requirements) {
				t.Fatalf("InputRequirements() = %+v, want %+v", got, test.requirements)
			}
			if got := algorithms[0].InputProjection(); !reflect.DeepEqual(got, test.projection) {
				t.Fatalf("InputProjection() = %+v, want %+v", got, test.projection)
			}
			test.assert(t, algorithms[0])
		})
	}
	seenState := make(map[string]string, len(stateCompatibility))
	for kind, digest := range stateCompatibility {
		if previous, duplicate := seenState[digest]; duplicate {
			t.Fatalf("algorithm kinds %s and %s reused state compatibility %q", previous, kind, digest)
		}
		seenState[digest] = kind
	}
}

func TestG4StateCompatibilityBindsAlgorithmConfigDependencyAndIdentity(t *testing.T) {
	projection := AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	requirements := g4Requirements(t, "previous")
	type digests struct{ state, detect string }
	compile := func(config map[string]any, requirements []AlgorithmInputRequirement, projection AlgorithmInputProjection) digests {
		request := g4CompileRequest(t, DetectorKindSimpleRingRatio, config, projection, requirements)
		compiled := mustCompileG4Request(t, newTestCompiler(t), request)
		return digests{state: compiled.StateCompatibilityHash(), detect: compiled.Fingerprints().Detect}
	}
	base := compile(map[string]any{"floor": 50, "ceil": nil}, requirements, projection)
	changedConfig := compile(map[string]any{"floor": 60, "ceil": nil}, requirements, projection)
	changedRequirements := append([]AlgorithmInputRequirement(nil), requirements...)
	changedRequirements[1].RelativeWindow.StartOffsetSeconds--
	changedRequirements[1] = withG4RequirementID(t, changedRequirements[1])
	changedDependency := compile(map[string]any{"floor": 50, "ceil": nil}, changedRequirements, projection)
	changedProjection := projection
	changedProjection.IdentityFields = []string{"host", "namespace"}
	changedProjectionRequirements := cloneAlgorithmInputRequirements(requirements)
	for index := range changedProjectionRequirements {
		changedProjectionRequirements[index].InputProjection = changedProjection
		changedProjectionRequirements[index] = withG4RequirementID(t, changedProjectionRequirements[index])
	}
	changedIdentity := compile(map[string]any{"floor": 50, "ceil": nil}, changedProjectionRequirements, changedProjection)
	for name, changed := range map[string]digests{"config": changedConfig, "dependency": changedDependency, "identity": changedIdentity} {
		if changed.state == base.state || changed.detect == base.detect {
			t.Fatalf("%s change reused state or detect plan digest: base=%+v changed=%+v", name, base, changed)
		}
	}
}

func TestG4SimpleRingRatioPreservesUnconfiguredAndNumericZero(t *testing.T) {
	projection := AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	requirements := g4Requirements(t, "previous")
	compile := func(floor any) *CompiledPlan {
		request := g4CompileRequest(t, DetectorKindSimpleRingRatio, map[string]any{"floor": floor, "ceil": 10}, projection, requirements)
		return mustCompileG4Request(t, newTestCompiler(t), request)
	}
	unconfigured := compile(nil)
	zero := compile(0)
	zeroConfig, ok := zero.Levels()[0].Algorithms()[0].SimpleRingRatioConfig()
	if !ok || !zeroConfig.FloorConfigured || zeroConfig.FloorEnabled || zeroConfig.FloorDecimal != "0.000000" {
		t.Fatalf("zero config = %+v, ok=%v", zeroConfig, ok)
	}
	unconfiguredConfig, ok := unconfigured.Levels()[0].Algorithms()[0].SimpleRingRatioConfig()
	if !ok || unconfiguredConfig.FloorConfigured || unconfiguredConfig.FloorDecimal != "" {
		t.Fatalf("unconfigured config = %+v, ok=%v", unconfiguredConfig, ok)
	}
	if unconfigured.StateCompatibilityHash() == zero.StateCompatibilityHash() {
		t.Fatal("unconfigured floor and numeric zero reused state compatibility")
	}
}

func TestG4RejectsNamedInputForDifferentConsumerLevel(t *testing.T) {
	projection := AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	requirements := g4Requirements(t, "previous")
	requirements[1].ConsumerLevelID = 2
	requirements[1] = withG4RequirementID(t, requirements[1])
	request := g4CompileRequest(t, DetectorKindSimpleRingRatio, map[string]any{"floor": 50, "ceil": nil}, projection, requirements)
	result, err := newTestCompiler(t).Compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if terminals := result.LevelTerminals(); len(terminals) != 1 || terminals[0].LevelID != 1 ||
		terminals[0].ReasonCode != contract.ReasonLevelInvalid {
		t.Fatalf("LevelTerminals() = %+v", terminals)
	}
}

func g4CompileRequest(t *testing.T, kind string, config map[string]any, projection AlgorithmInputProjection, requirements []AlgorithmInputRequirement) CompileRequest {
	t.Helper()
	config["input_projection"] = projection
	config["requirements"] = requirements
	plan := validPlan()
	plan.InputProjection.ValueFields = append([]string(nil), projection.ValueFields...)
	plan.InputProjection.DimensionFields = append([]string(nil), projection.DimensionFields...)
	plan.StrategyIR.InputProjection = plan.InputProjection
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms = []contract.AlgorithmIRV2{{Type: kind, Version: 1, Config: mustJSON(config)}}
	request := validRequest(plan)
	request.DatasetContract.IdentityFields = append([]string(nil), projection.IdentityFields...)
	return request
}

func mustCompileG4Request(t *testing.T, compiler *PlanCompiler, request CompileRequest) *CompiledPlan {
	t.Helper()
	result, err := compiler.Compile(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := result.Plan()
	if !ok {
		t.Fatalf("Compile() terminal = %+v levels = %+v", result.PlanTerminal(), result.LevelTerminals())
	}
	return compiled
}

func g4Requirements(t *testing.T, dependencyName string) []AlgorithmInputRequirement {
	t.Helper()
	projection := AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}
	primary := g4PrimaryRequirements(t, projection)[0]
	window := AlgorithmRelativeWindow{StartOffsetSeconds: -120, EndOffsetSeconds: -60, HalfOpen: true}
	if dependencyName == "uptime_history" {
		window = AlgorithmRelativeWindow{StartOffsetSeconds: -1560, EndOffsetSeconds: 0, HalfOpen: true}
	}
	dependency := withG4RequirementID(t, AlgorithmInputRequirement{
		DatasetName: dependencyName, Role: AlgorithmInputDependency, LogicalQueryRef: "query-" + dependencyName,
		ConsumerLevelID: 1,
		RelativeWindow:  window, StepMillis: 60_000, AlignmentMillis: 60_000,
		ReadinessClass: AlgorithmReadinessFinalizedRequired, InputProjection: projection,
	})
	if dependencyName == "previous" {
		dependency.PointOffsetsSeconds = []int64{60}
		dependency.NamedPoints = []AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: 60}}
	} else {
		dependency.PointOffsetsSeconds = []int64{60, 600, 1500}
		dependency.NamedPoints = []AlgorithmNamedInputPoint{
			{Name: "previous", OffsetSeconds: 60},
			{Name: "previous_10m", OffsetSeconds: 600},
			{Name: "previous_25m", OffsetSeconds: 1500},
		}
	}
	dependency = withG4RequirementID(t, dependency)
	return []AlgorithmInputRequirement{primary, dependency}
}

func g4PrimaryRequirements(t *testing.T, projection AlgorithmInputProjection) []AlgorithmInputRequirement {
	t.Helper()
	primary := withG4RequirementID(t, AlgorithmInputRequirement{
		DatasetName: "primary", Role: AlgorithmInputPrimary, LogicalQueryRef: "query-primary",
		ConsumerLevelID: 1,
		RelativeWindow:  AlgorithmRelativeWindow{StartOffsetSeconds: -60, EndOffsetSeconds: 0, HalfOpen: true},
		StepMillis:      60_000, AlignmentMillis: 60_000,
		ReadinessClass: AlgorithmReadinessEager, InputProjection: projection,
	})
	return []AlgorithmInputRequirement{primary}
}

func withG4RequirementID(t *testing.T, requirement AlgorithmInputRequirement) AlgorithmInputRequirement {
	t.Helper()
	requirement.RequirementID = ""
	digest, err := contract.DeriveCanonicalDigestV2("test-g4-algorithm-input-v1", requirement)
	if err != nil {
		t.Fatal(err)
	}
	requirement.RequirementID = digest
	return requirement
}
