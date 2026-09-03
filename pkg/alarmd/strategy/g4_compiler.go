package strategy

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

const (
	algorithmHistoryNone           = "NONE"
	algorithmHistoryPlanLocalInput = "PLAN_LOCAL_NAMED_INPUT"
)

type g4AlgorithmInputsV1 struct {
	InputProjection AlgorithmInputProjection    `json:"input_projection"`
	Requirements    []AlgorithmInputRequirement `json:"requirements"`
}

type simpleRingRatioConfigV1 struct {
	Floor           *json.Number                `json:"floor"`
	Ceil            *json.Number                `json:"ceil"`
	InputProjection AlgorithmInputProjection    `json:"input_projection"`
	Requirements    []AlgorithmInputRequirement `json:"requirements"`
}

type simpleRingRatioAlgorithmCompiler struct{}

func (simpleRingRatioAlgorithmCompiler) Capability() AlgorithmCapability {
	return AlgorithmCapability{
		Kind: DetectorKindSimpleRingRatio, Version: 1, EvaluationScope: contract.EvaluationScopeSeries,
		InputShape: "NAMED_SERIES", RequiredHistoryKind: algorithmHistoryPlanLocalInput,
		StateSchemaVersion: "simple-ring-ratio-v1", Deterministic: true,
		FixedComputeCost: 2, CostPerRecord: 2,
	}
}

func (simpleRingRatioAlgorithmCompiler) Compile(_ context.Context, compileContext AlgorithmCompileContext, raw contract.AlgorithmIRV2) (AlgorithmCompileResult, error) {
	var config simpleRingRatioConfigV1
	if err := decodeStrict(raw.Config, &config); err != nil {
		return AlgorithmCompileResult{}, configErrorf("SimpleRingRatio config: %v", err)
	}
	requirements, err := validateG4Inputs(compileContext, config.InputProjection, config.Requirements, "previous")
	if err != nil {
		return AlgorithmCompileResult{}, configErrorf("SimpleRingRatio config: %v", err)
	}
	if len(requirements) != 2 || requirements[1].Role != AlgorithmInputDependency ||
		!equalAlgorithmOffsets(requirements[1].PointOffsetsSeconds, []int64{int64(compileContext.ExecutionSemantics.AggregationInterval)}) ||
		!equalNamedPoints(requirements[1].NamedPoints, []AlgorithmNamedInputPoint{{Name: "previous", OffsetSeconds: int64(compileContext.ExecutionSemantics.AggregationInterval)}}) {
		return AlgorithmCompileResult{}, configErrorf("SimpleRingRatio config: previous input is required")
	}
	floor, floorConfigured, floorEnabled, err := normalizeOptionalNonNegative(config.Floor)
	if err != nil {
		return AlgorithmCompileResult{}, configErrorf("SimpleRingRatio config: floor %v", err)
	}
	ceil, ceilConfigured, ceilEnabled, err := normalizeOptionalNonNegative(config.Ceil)
	if err != nil {
		return AlgorithmCompileResult{}, configErrorf("SimpleRingRatio config: ceil %v", err)
	}
	if !floorEnabled && !ceilEnabled {
		return AlgorithmCompileResult{}, configErrorf("SimpleRingRatio config: floor or ceil is required")
	}
	normalized := &SimpleRingRatioConfig{
		ValueField:      config.InputProjection.ValueFields[0],
		FloorConfigured: floorConfigured, FloorEnabled: floorEnabled, FloorDecimal: floor,
		CeilConfigured: ceilConfigured, CeilEnabled: ceilEnabled, CeilDecimal: ceil,
	}
	return g4CompileResult(
		compiledAlgorithmConfig{SimpleRingRatio: normalized}, config.InputProjection, requirements,
		"simple-ring-ratio-compiler-v1", "exact-previous-point-v1", 1,
	), nil
}

type osRestartAlgorithmCompiler struct{}

func (osRestartAlgorithmCompiler) Capability() AlgorithmCapability {
	return AlgorithmCapability{
		Kind: DetectorKindOsRestart, Version: 1, EvaluationScope: contract.EvaluationScopeSeries,
		InputShape: "NAMED_SERIES", RequiredHistoryKind: algorithmHistoryPlanLocalInput,
		StateSchemaVersion: "os-restart-v1", Deterministic: true,
		FixedComputeCost: 3, CostPerRecord: 3,
	}
}

func (osRestartAlgorithmCompiler) Compile(_ context.Context, compileContext AlgorithmCompileContext, raw contract.AlgorithmIRV2) (AlgorithmCompileResult, error) {
	var config g4AlgorithmInputsV1
	if err := decodeStrict(raw.Config, &config); err != nil {
		return AlgorithmCompileResult{}, configErrorf("OsRestart config: %v", err)
	}
	requirements, err := validateG4Inputs(compileContext, config.InputProjection, config.Requirements, "uptime_history")
	if err != nil {
		return AlgorithmCompileResult{}, configErrorf("OsRestart config: %v", err)
	}
	offsets := []int64{int64(compileContext.ExecutionSemantics.AggregationInterval), 600, 1500}
	if len(requirements) != 2 || requirements[1].Role != AlgorithmInputDependency ||
		!equalAlgorithmOffsets(requirements[1].PointOffsetsSeconds, offsets) ||
		!equalNamedPoints(requirements[1].NamedPoints, []AlgorithmNamedInputPoint{
			{Name: "previous", OffsetSeconds: offsets[0]}, {Name: "previous_10m", OffsetSeconds: offsets[1]},
			{Name: "previous_25m", OffsetSeconds: offsets[2]},
		}) {
		return AlgorithmCompileResult{}, configErrorf("OsRestart config: exact uptime history offsets are required")
	}
	normalized := &OsRestartConfig{
		ValueField: config.InputProjection.ValueFields[0], PrimaryExpression: "a <= 3600",
		HistoryExpression: "a", HistoryOffsetsSeconds: append([]int64(nil), offsets...),
	}
	return g4CompileResult(
		compiledAlgorithmConfig{OsRestart: normalized}, config.InputProjection, requirements,
		"os-restart-compiler-v1", "raw-uptime-history-v1", 1,
	), nil
}

type procPortAlgorithmCompiler struct{}

func (procPortAlgorithmCompiler) Capability() AlgorithmCapability {
	return AlgorithmCapability{
		Kind: DetectorKindProcPort, Version: 1, EvaluationScope: contract.EvaluationScopeSeries,
		InputShape: "ROW", RequiredHistoryKind: algorithmHistoryNone,
		StateSchemaVersion: "proc-port-v1", Deterministic: true,
		FixedComputeCost: 2, CostPerRecord: 2,
	}
}

func (procPortAlgorithmCompiler) Compile(_ context.Context, compileContext AlgorithmCompileContext, raw contract.AlgorithmIRV2) (AlgorithmCompileResult, error) {
	var config g4AlgorithmInputsV1
	if err := decodeStrict(raw.Config, &config); err != nil {
		return AlgorithmCompileResult{}, configErrorf("ProcPort config: %v", err)
	}
	requirements, err := validateG4Inputs(compileContext, config.InputProjection, config.Requirements, "")
	if err != nil {
		return AlgorithmCompileResult{}, configErrorf("ProcPort config: %v", err)
	}
	wantProjection := AlgorithmInputProjection{
		ValueFields:     []string{"value"},
		DimensionFields: []string{"bind_ip", "listen", "nonlisten", "not_accurate_listen", "protocol"},
		IdentityFields:  []string{"bk_target_cloud_id", "bk_target_ip", "display_name"},
	}
	if len(requirements) != 1 || !equalAlgorithmProjection(config.InputProjection, wantProjection) {
		return AlgorithmCompileResult{}, configErrorf("ProcPort config: frozen projection is required")
	}
	normalized := &ProcPortConfig{
		ValueField: "value", SourceMetric: "proc_exists", NonListenField: "nonlisten",
		NotAccurateListenField: "not_accurate_listen", BindIPField: "bind_ip",
	}
	return g4CompileResult(
		compiledAlgorithmConfig{ProcPort: normalized}, config.InputProjection, requirements,
		"proc-port-compiler-v1", "three-predicate-branches-v1", 1,
	), nil
}

func g4CompileResult(
	config compiledAlgorithmConfig,
	projection AlgorithmInputProjection,
	requirements []AlgorithmInputRequirement,
	compilerVersion string,
	proofVersion string,
	astNodes int,
) AlgorithmCompileResult {
	return AlgorithmCompileResult{
		Config: config, InputProjection: cloneAlgorithmInputProjection(projection),
		InputRequirements: cloneAlgorithmInputRequirements(requirements), CompilerVersion: compilerVersion,
		ProofVersion: proofVersion, ASTNodes: astNodes,
	}
}

func validateG4Inputs(
	compileContext AlgorithmCompileContext,
	projection AlgorithmInputProjection,
	requirements []AlgorithmInputRequirement,
	dependencyName string,
) ([]AlgorithmInputRequirement, error) {
	if err := projection.validate(); err != nil {
		return nil, err
	}
	want := AlgorithmInputProjection{
		ValueFields:     append([]string(nil), compileContext.Projection.ValueFields...),
		DimensionFields: append([]string(nil), compileContext.Projection.DimensionFields...),
		IdentityFields:  append([]string(nil), compileContext.IdentityFields...),
	}
	if !equalAlgorithmProjection(projection, want) {
		return nil, fmt.Errorf("input projection does not match plan and identity contracts")
	}
	canonical, err := canonicalAlgorithmRequirements(requirements)
	if err != nil {
		return nil, err
	}
	if len(canonical) == 0 || canonical[0].Role != AlgorithmInputPrimary || canonical[0].DatasetName != "primary" {
		return nil, fmt.Errorf("primary input must be first and named primary")
	}
	for index, requirement := range canonical {
		if !equalAlgorithmProjection(requirement.InputProjection, projection) {
			return nil, fmt.Errorf("input requirement projection mismatch")
		}
		if index > 0 && (requirement.Role != AlgorithmInputDependency || requirement.DatasetName != dependencyName) {
			return nil, fmt.Errorf("unexpected algorithm dependency")
		}
	}
	if dependencyName == "" && len(canonical) != 1 {
		return nil, fmt.Errorf("primary-only algorithm has dependency")
	}
	return canonical, nil
}

func normalizeOptionalNonNegative(raw *json.Number) (string, bool, bool, error) {
	if raw == nil {
		return "", false, false, nil
	}
	rational, ok := parseDecimalRational(raw.String(), true)
	if !ok || rational.Sign() < 0 {
		return "", true, false, fmt.Errorf("must be a non-negative decimal")
	}
	normalized, ok := normalizeRational(rational, 1)
	if !ok {
		return "", true, false, fmt.Errorf("normalization overflow")
	}
	if normalized.high == 0 && normalized.low == 0 {
		return normalized.CanonicalDecimal(), true, false, nil
	}
	return normalized.CanonicalDecimal(), true, true, nil
}

func equalAlgorithmProjection(left, right AlgorithmInputProjection) bool {
	return equalStrings(left.ValueFields, right.ValueFields) &&
		equalStrings(left.DimensionFields, right.DimensionFields) && equalStrings(left.IdentityFields, right.IdentityFields)
}

func equalAlgorithmOffsets(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalNamedPoints(left, right []AlgorithmNamedInputPoint) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
