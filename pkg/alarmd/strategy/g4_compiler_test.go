package strategy

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

func TestG4DefaultRegistryCompilesThreeIndependentAlgorithmKinds(t *testing.T) {
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
				(test.kind == DetectorKindProcPort && capability.RequiredHistoryKind != algorithmHistoryNone) {
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

// The ProcPort fold policy is derived from the compiled configuration and is
// never persisted or digested. The fingerprints, state compatibility, config
// digest and algorithm plan id of a ProcPort plan compiled by the fixed test
// fixtures are pinned to the values recorded before the policy existed, so a
// change that started to digest the policy (and would flip state generations
// in production) fails here. Other algorithm kinds declare no policy.
//
// The state hash was re-pinned once on purpose, when the Level contract
// inputs (detect and trigger fingerprints, state requirement) were folded
// into it so that a trigger, recovery or connector edit re-warms the Plan
// instead of failing its loaded state. The cost of moving this value is
// paid by the whole deployment at rollout: every Plan's state generation
// changes once, so every Plan is forced through WARMING once and detects
// nothing for RequiredFullSlots of its evaluation interval, and every
// event id changes namespace once, since it closes over the hash. Move it
// only with that cost in mind, and never alone in a release. The other
// five values did not move.
func TestG4ProcPortDeclaresSeriesFoldPolicyWithoutChangingFingerprints(t *testing.T) {
	projection := AlgorithmInputProjection{
		ValueFields:     []string{"value"},
		DimensionFields: []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"},
		IdentityFields:  []string{"bk_target_cloud_id", "bk_target_ip", "display_name"},
	}
	compiled := mustCompileG4Request(t, newTestCompiler(t),
		g4CompileRequest(t, DetectorKindProcPort, map[string]any{}, projection, g4PrimaryRequirements(t, projection)))
	level := compiled.Levels()[0]
	algorithm := level.Algorithms()[0]
	const (
		wantDetect         = "9218eb1b3507104a392a86d45a0314e6ac21a964ce39a420832412ba8f0b72ce"
		wantTrigger        = "5177aa852128e953a9ed687682e39d2d7cdb99d892678da8bd52df3416f202f1"
		wantStateHash      = "98c79a7d7ab6cdcba964bd22e00439a0b904c154807d0ff52d68d435e804f54b"
		wantAlgorithmState = "30a7dc0134d5035922831e1b21dd043aa702fd39466e098a072dc34f696620e7"
		wantConfigDigest   = "fb4176edfb5c4e8b3fc317904a61ba93fab57f132a9132225d68e1055890a06f"
		wantPlanID         = "f80484f463a5f2948e71bb9ef6e83c84687befe58d787db348be49d833510362"
	)
	if level.Fingerprints().Detect != wantDetect || level.Fingerprints().Trigger != wantTrigger ||
		compiled.StateCompatibilityHash() != wantStateHash || algorithm.StateCompatibilityFingerprint() != wantAlgorithmState ||
		algorithm.NormalizedConfigDigest() != wantConfigDigest || algorithm.AlgorithmPlanID() != wantPlanID {
		t.Fatalf("ProcPort fingerprints changed: detect=%s trigger=%s state=%s algorithm=%s config=%s plan=%s",
			level.Fingerprints().Detect, level.Fingerprints().Trigger, compiled.StateCompatibilityHash(),
			algorithm.StateCompatibilityFingerprint(), algorithm.NormalizedConfigDigest(), algorithm.AlgorithmPlanID())
	}
	policy, declared := algorithm.SeriesFoldPolicy()
	want := SeriesFoldPolicy{
		Values: map[string]SeriesFoldRule{"value": SeriesFoldMin},
		Dimensions: map[string]SeriesFoldRule{
			"bind_ip": SeriesFoldDistinctValues, "listen": SeriesFoldUnionSet, "nonlisten": SeriesFoldUnionSet,
			"not_accurate_listen": SeriesFoldUnionSet, "protocol": SeriesFoldDistinctValues,
		},
	}
	if !declared || !reflect.DeepEqual(policy, want) {
		t.Fatalf("ProcPort fold policy = %+v declared=%v, want %+v", policy, declared, want)
	}
	others := []struct {
		kind       string
		config     map[string]any
		dependency string
	}{
		{kind: DetectorKindSimpleRingRatio, config: map[string]any{"floor": 50, "ceil": nil}, dependency: "previous"},
		{kind: DetectorKindOsRestart, config: map[string]any{}, dependency: "uptime_history"},
	}
	for _, other := range others {
		compiled := mustCompileG4Request(t, newTestCompiler(t), g4CompileRequest(t, other.kind, other.config,
			AlgorithmInputProjection{ValueFields: []string{"value"}, IdentityFields: []string{"host"}}, g4Requirements(t, other.dependency)))
		levels := compiled.Levels()
		if len(levels) != 1 || len(levels[0].Algorithms()) != 1 {
			t.Fatalf("%s compiled %d levels", other.kind, len(levels))
		}
		if _, declared := levels[0].Algorithms()[0].SeriesFoldPolicy(); declared {
			t.Fatalf("%s declared a fold policy", other.kind)
		}
	}
}

func TestG4DefaultRegistryDoesNotRegisterPingDetector(t *testing.T) {
	if _, ok := NewDefaultAlgorithmCompilerRegistry().lookup("PingUnreachable", 1); ok {
		t.Fatal("PingUnreachable source type was registered as an independent detector")
	}
}

func TestThresholdSourceMappingProvenanceEntersCompatibility(t *testing.T) {
	compiler := newTestCompiler(t)
	canonical := mustCompileG4Request(t, compiler, validRequest(validPlan()))
	queryDigest := strings.Repeat("a", 64)
	projection := AlgorithmInputProjection{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, IdentityFields: []string{"host"}}
	requirements := g4PrimaryRequirements(t, projection)
	requirements[0].LogicalQueryRef = queryDigest
	requirements[0] = withG4RequirementID(t, requirements[0])
	config := thresholdConfig("1")
	config["source_algorithm_family"] = "ping_unreachable"
	config["source_mapping_version"] = "ping-unreachable-to-threshold-v1"
	config["canonical_query_digest"] = queryDigest
	config["input_projection"] = projection
	config["requirements"] = requirements
	plan := validPlan()
	plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config = mustJSON(config)
	mapped := mustCompileG4Request(t, compiler, validRequest(plan))
	algorithm := mapped.Levels()[0].Algorithms()[0]
	provenance, ok := algorithm.SourceProvenance()
	if !ok || provenance.SourceAlgorithmFamily != "ping_unreachable" || provenance.SourceMappingVersion != "ping-unreachable-to-threshold-v1" ||
		provenance.CanonicalQueryDigest != queryDigest {
		t.Fatalf("SourceProvenance() = %+v, ok=%v", provenance, ok)
	}
	if algorithm.Kind() != DetectorKindThreshold || len(algorithm.InputRequirements()) != 1 {
		t.Fatalf("mapped algorithm = %+v", algorithm)
	}
	if mapped.StateCompatibilityHash() == canonical.StateCompatibilityHash() || mapped.Fingerprints().Detect == canonical.Fingerprints().Detect {
		t.Fatal("source mapping provenance reused canonical Threshold compatibility")
	}
}

func TestThresholdRejectsUnregisteredSourceMappingProvenance(t *testing.T) {
	queryDigest := strings.Repeat("a", 64)
	projection := AlgorithmInputProjection{ValueFields: []string{"value"}, DimensionFields: []string{"host"}, IdentityFields: []string{"host"}}
	requirements := g4PrimaryRequirements(t, projection)
	requirements[0].LogicalQueryRef = queryDigest
	requirements[0] = withG4RequirementID(t, requirements[0])
	baseConfig := thresholdConfig("1")
	baseConfig["source_algorithm_family"] = "ping_unreachable"
	baseConfig["source_mapping_version"] = "ping-unreachable-to-threshold-v1"
	baseConfig["canonical_query_digest"] = queryDigest
	baseConfig["input_projection"] = projection
	baseConfig["requirements"] = requirements
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"family", func(config map[string]any) { config["source_algorithm_family"] = "proc_port" }},
		{"version", func(config map[string]any) { config["source_mapping_version"] = "ping-unreachable-to-threshold-v2" }},
		{"query", func(config map[string]any) { config["canonical_query_digest"] = strings.Repeat("b", 64) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := make(map[string]any, len(baseConfig))
			for key, value := range baseConfig {
				config[key] = value
			}
			test.mutate(config)
			plan := validPlan()
			plan.StrategyIR.Levels[0].DetectPlan.Algorithms[0].Config = mustJSON(config)
			result, err := newTestCompiler(t).Compile(context.Background(), validRequest(plan))
			if err != nil {
				t.Fatal(err)
			}
			if terminals := result.LevelTerminals(); len(terminals) != 1 || terminals[0].ReasonCode != contract.ReasonLevelInvalid {
				t.Fatalf("LevelTerminals() = %+v", terminals)
			}
		})
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
