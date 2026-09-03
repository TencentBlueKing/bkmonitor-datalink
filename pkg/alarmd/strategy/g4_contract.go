package strategy

import (
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type AlgorithmInputRole string
type AlgorithmReadinessClass string

const (
	AlgorithmInputPrimary    AlgorithmInputRole = "PRIMARY"
	AlgorithmInputDependency AlgorithmInputRole = "ALGORITHM_DEPENDENCY"

	AlgorithmReadinessEager             AlgorithmReadinessClass = "EAGER"
	AlgorithmReadinessFinalizedRequired AlgorithmReadinessClass = "FINALIZED_REQUIRED"
)

type AlgorithmInputProjection struct {
	ValueFields     []string `json:"value_fields"`
	DimensionFields []string `json:"dimension_fields"`
	IdentityFields  []string `json:"identity_fields"`
}

func (projection AlgorithmInputProjection) validate() error {
	if len(projection.ValueFields) == 0 || !sortedUnique(projection.ValueFields, false) ||
		!sortedUnique(projection.DimensionFields, true) || !sortedUnique(projection.IdentityFields, true) {
		return errors.New("strategy: invalid algorithm input projection")
	}
	return nil
}

func cloneAlgorithmInputProjection(projection AlgorithmInputProjection) AlgorithmInputProjection {
	projection.ValueFields = append([]string(nil), projection.ValueFields...)
	projection.DimensionFields = append([]string(nil), projection.DimensionFields...)
	projection.IdentityFields = append([]string(nil), projection.IdentityFields...)
	return projection
}

type AlgorithmRelativeWindow struct {
	StartOffsetSeconds int64 `json:"start_offset_seconds"`
	EndOffsetSeconds   int64 `json:"end_offset_seconds"`
	HalfOpen           bool  `json:"half_open"`
}

type AlgorithmNamedInputPoint struct {
	Name          string `json:"name"`
	OffsetSeconds int64  `json:"offset_seconds"`
}

type AlgorithmInputRequirement struct {
	RequirementID       string                     `json:"requirement_id"`
	DatasetName         string                     `json:"dataset_name"`
	Role                AlgorithmInputRole         `json:"role"`
	ConsumerLevelID     uint32                     `json:"consumer_level_id"`
	LogicalQueryRef     string                     `json:"logical_query_ref"`
	RelativeWindow      AlgorithmRelativeWindow    `json:"relative_window"`
	StepMillis          int64                      `json:"step_millis"`
	AlignmentMillis     int64                      `json:"alignment_millis"`
	ReadinessClass      AlgorithmReadinessClass    `json:"readiness_class"`
	InputProjection     AlgorithmInputProjection   `json:"input_projection"`
	PointOffsetsSeconds []int64                    `json:"point_offsets_seconds,omitempty"`
	NamedPoints         []AlgorithmNamedInputPoint `json:"named_points,omitempty"`
}

func (requirement AlgorithmInputRequirement) validate() error {
	if len(requirement.RequirementID) != 64 || requirement.DatasetName == "" || requirement.ConsumerLevelID == 0 || requirement.LogicalQueryRef == "" ||
		(requirement.Role != AlgorithmInputPrimary && requirement.Role != AlgorithmInputDependency) ||
		!requirement.RelativeWindow.HalfOpen || requirement.RelativeWindow.StartOffsetSeconds >= requirement.RelativeWindow.EndOffsetSeconds ||
		requirement.StepMillis <= 0 || requirement.AlignmentMillis <= 0 ||
		(requirement.ReadinessClass != AlgorithmReadinessEager && requirement.ReadinessClass != AlgorithmReadinessFinalizedRequired) {
		return errors.New("strategy: invalid algorithm input requirement")
	}
	if err := requirement.InputProjection.validate(); err != nil {
		return err
	}
	var previous int64
	for index, offset := range requirement.PointOffsetsSeconds {
		if offset <= 0 || (index > 0 && offset <= previous) {
			return errors.New("strategy: invalid algorithm input point offsets")
		}
		previous = offset
	}
	if len(requirement.NamedPoints) != len(requirement.PointOffsetsSeconds) {
		return errors.New("strategy: named input points differ from offsets")
	}
	pointNames := make(map[string]struct{}, len(requirement.NamedPoints))
	for index, point := range requirement.NamedPoints {
		if point.Name == "" || point.OffsetSeconds != requirement.PointOffsetsSeconds[index] {
			return errors.New("strategy: invalid named input point")
		}
		if _, duplicate := pointNames[point.Name]; duplicate {
			return errors.New("strategy: duplicate named input point")
		}
		pointNames[point.Name] = struct{}{}
	}
	return nil
}

func cloneAlgorithmInputRequirements(source []AlgorithmInputRequirement) []AlgorithmInputRequirement {
	result := append([]AlgorithmInputRequirement(nil), source...)
	for index := range result {
		result[index].InputProjection = cloneAlgorithmInputProjection(result[index].InputProjection)
		result[index].PointOffsetsSeconds = append([]int64(nil), result[index].PointOffsetsSeconds...)
		result[index].NamedPoints = append([]AlgorithmNamedInputPoint(nil), result[index].NamedPoints...)
	}
	return result
}

type SimpleRingRatioConfig struct {
	ValueField      string `json:"value_field"`
	FloorConfigured bool   `json:"floor_configured"`
	FloorEnabled    bool   `json:"floor_enabled"`
	FloorDecimal    string `json:"floor_decimal"`
	CeilConfigured  bool   `json:"ceil_configured"`
	CeilEnabled     bool   `json:"ceil_enabled"`
	CeilDecimal     string `json:"ceil_decimal"`
}

type OsRestartConfig struct {
	ValueField            string  `json:"value_field"`
	PrimaryExpression     string  `json:"primary_expression"`
	HistoryExpression     string  `json:"history_expression"`
	HistoryOffsetsSeconds []int64 `json:"history_offsets_seconds"`
}

type ProcPortConfig struct {
	ValueField             string `json:"value_field"`
	SourceMetric           string `json:"source_metric"`
	NonListenField         string `json:"nonlisten_field"`
	NotAccurateListenField string `json:"not_accurate_listen_field"`
	BindIPField            string `json:"bind_ip_field"`
}

type PingUnreachableConfig struct {
	ValueField       string `json:"value_field"`
	SourceMetric     string `json:"source_metric"`
	ThresholdDecimal string `json:"threshold_decimal"`
}

type compiledAlgorithmConfig struct {
	Threshold       *detectorSemantic      `json:"threshold,omitempty"`
	SimpleRingRatio *SimpleRingRatioConfig `json:"simple_ring_ratio,omitempty"`
	OsRestart       *OsRestartConfig       `json:"os_restart,omitempty"`
	ProcPort        *ProcPortConfig        `json:"proc_port,omitempty"`
	PingUnreachable *PingUnreachableConfig `json:"ping_unreachable,omitempty"`
}

type CompiledAlgorithmPlan struct {
	algorithmPlanID               string
	kind                          string
	version                       uint32
	capability                    AlgorithmCapability
	capabilityDigest              string
	compilerVersion               string
	proofVersion                  string
	normalizedConfigDigest        string
	config                        compiledAlgorithmConfig
	inputProjection               AlgorithmInputProjection
	inputRequirements             []AlgorithmInputRequirement
	stateCompatibilityFingerprint string
}

func (plan CompiledAlgorithmPlan) AlgorithmPlanID() string         { return plan.algorithmPlanID }
func (plan CompiledAlgorithmPlan) Kind() string                    { return plan.kind }
func (plan CompiledAlgorithmPlan) Version() uint32                 { return plan.version }
func (plan CompiledAlgorithmPlan) Capability() AlgorithmCapability { return plan.capability }
func (plan CompiledAlgorithmPlan) CapabilityDigest() string        { return plan.capabilityDigest }
func (plan CompiledAlgorithmPlan) CompilerVersion() string         { return plan.compilerVersion }
func (plan CompiledAlgorithmPlan) ProofVersion() string            { return plan.proofVersion }
func (plan CompiledAlgorithmPlan) NormalizedConfigDigest() string  { return plan.normalizedConfigDigest }
func (plan CompiledAlgorithmPlan) StateCompatibilityFingerprint() string {
	return plan.stateCompatibilityFingerprint
}
func (plan CompiledAlgorithmPlan) InputProjection() AlgorithmInputProjection {
	return cloneAlgorithmInputProjection(plan.inputProjection)
}
func (plan CompiledAlgorithmPlan) InputRequirements() []AlgorithmInputRequirement {
	return cloneAlgorithmInputRequirements(plan.inputRequirements)
}
func (plan CompiledAlgorithmPlan) SimpleRingRatioConfig() (SimpleRingRatioConfig, bool) {
	if plan.config.SimpleRingRatio == nil {
		return SimpleRingRatioConfig{}, false
	}
	return *plan.config.SimpleRingRatio, true
}
func (plan CompiledAlgorithmPlan) OsRestartConfig() (OsRestartConfig, bool) {
	if plan.config.OsRestart == nil {
		return OsRestartConfig{}, false
	}
	config := *plan.config.OsRestart
	config.HistoryOffsetsSeconds = append([]int64(nil), config.HistoryOffsetsSeconds...)
	return config, true
}
func (plan CompiledAlgorithmPlan) ProcPortConfig() (ProcPortConfig, bool) {
	if plan.config.ProcPort == nil {
		return ProcPortConfig{}, false
	}
	return *plan.config.ProcPort, true
}
func (plan CompiledAlgorithmPlan) PingUnreachableConfig() (PingUnreachableConfig, bool) {
	if plan.config.PingUnreachable == nil {
		return PingUnreachableConfig{}, false
	}
	return *plan.config.PingUnreachable, true
}

type compiledAlgorithmSemantic struct {
	AlgorithmPlanID               string                      `json:"algorithm_plan_id"`
	Kind                          string                      `json:"kind"`
	Version                       uint32                      `json:"version"`
	CapabilityDigest              string                      `json:"capability_digest"`
	CompilerVersion               string                      `json:"compiler_version"`
	ProofVersion                  string                      `json:"proof_version"`
	NormalizedConfigDigest        string                      `json:"normalized_config_digest"`
	InputProjection               AlgorithmInputProjection    `json:"input_projection"`
	InputRequirements             []AlgorithmInputRequirement `json:"input_requirements"`
	StateCompatibilityFingerprint string                      `json:"state_compatibility_fingerprint"`
}

func (plan CompiledAlgorithmPlan) semantic() compiledAlgorithmSemantic {
	return compiledAlgorithmSemantic{
		AlgorithmPlanID: plan.algorithmPlanID, Kind: plan.kind, Version: plan.version,
		CapabilityDigest: plan.capabilityDigest, CompilerVersion: plan.compilerVersion,
		ProofVersion: plan.proofVersion, NormalizedConfigDigest: plan.normalizedConfigDigest,
		InputProjection:               cloneAlgorithmInputProjection(plan.inputProjection),
		InputRequirements:             cloneAlgorithmInputRequirements(plan.inputRequirements),
		StateCompatibilityFingerprint: plan.stateCompatibilityFingerprint,
	}
}

func buildCompiledAlgorithmPlan(
	levelID uint32,
	position int,
	raw contract.AlgorithmIRV2,
	capability AlgorithmCapability,
	capabilityDigest string,
	result AlgorithmCompileResult,
) (CompiledAlgorithmPlan, error) {
	configDigest, err := contract.DeriveCanonicalDigestV2("algorithm-normalized-config-v1", result.Config)
	if err != nil {
		return CompiledAlgorithmPlan{}, err
	}
	stateFingerprint, err := contract.DeriveCanonicalDigestV2("algorithm-state-compatibility-v1", struct {
		Kind                   string                      `json:"kind"`
		Version                uint32                      `json:"version"`
		CapabilityDigest       string                      `json:"capability_digest"`
		CompilerVersion        string                      `json:"compiler_version"`
		ProofVersion           string                      `json:"proof_version"`
		NormalizedConfigDigest string                      `json:"normalized_config_digest"`
		InputProjection        AlgorithmInputProjection    `json:"input_projection"`
		InputRequirements      []AlgorithmInputRequirement `json:"input_requirements"`
	}{raw.Type, raw.Version, capabilityDigest, result.CompilerVersion, result.ProofVersion, configDigest, result.InputProjection, result.InputRequirements})
	if err != nil {
		return CompiledAlgorithmPlan{}, err
	}
	algorithmPlanID, err := contract.DeriveCanonicalDigestV2("algorithm-plan-identity-v1", struct {
		LevelID                       uint32 `json:"level_id"`
		Position                      int    `json:"position"`
		StateCompatibilityFingerprint string `json:"state_compatibility_fingerprint"`
	}{levelID, position, stateFingerprint})
	if err != nil {
		return CompiledAlgorithmPlan{}, err
	}
	return CompiledAlgorithmPlan{
		algorithmPlanID: algorithmPlanID, kind: raw.Type, version: raw.Version,
		capability: capability, capabilityDigest: capabilityDigest,
		compilerVersion: result.CompilerVersion, proofVersion: result.ProofVersion,
		normalizedConfigDigest: configDigest, config: result.Config,
		inputProjection:               cloneAlgorithmInputProjection(result.InputProjection),
		inputRequirements:             cloneAlgorithmInputRequirements(result.InputRequirements),
		stateCompatibilityFingerprint: stateFingerprint,
	}, nil
}

func canonicalAlgorithmRequirements(requirements []AlgorithmInputRequirement) ([]AlgorithmInputRequirement, error) {
	result := cloneAlgorithmInputRequirements(requirements)
	seen := make(map[string]struct{}, len(result))
	primary := 0
	for _, requirement := range result {
		if err := requirement.validate(); err != nil {
			return nil, err
		}
		if _, duplicate := seen[requirement.RequirementID]; duplicate {
			return nil, errors.New("strategy: duplicate algorithm input requirement")
		}
		seen[requirement.RequirementID] = struct{}{}
		if requirement.Role == AlgorithmInputPrimary {
			primary++
		}
	}
	if primary != 1 {
		return nil, errors.New("strategy: one primary algorithm input is required")
	}
	return result, nil
}
