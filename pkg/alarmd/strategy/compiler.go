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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

var (
	errAlgorithmBudget = errors.New("algorithm budget exceeded")
	errAlgorithmConfig = errors.New("algorithm config invalid")
)

func configErrorf(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", errAlgorithmConfig, fmt.Sprintf(format, arguments...))
}

type PlanCompiler struct {
	registry *AlgorithmCompilerRegistry
	limits   Limits
}

func NewCompiler(registry *AlgorithmCompilerRegistry, limits Limits) (*PlanCompiler, error) {
	if registry == nil || len(registry.compilers) == 0 {
		return nil, errors.New("strategy: algorithm registry is empty")
	}
	if limits.MaxPlanBytes <= 0 || limits.MaxLevelsPerPlan <= 0 || limits.MaxAlgorithmsPerLevel <= 0 ||
		limits.MaxGroupsPerAlgorithm <= 0 || limits.MaxConditionsPerAlgorithm <= 0 || limits.MaxASTNodesPerLevel <= 0 ||
		limits.MaxRequiredHistoryPoints == 0 {
		return nil, errors.New("strategy: deployment budgets must be positive")
	}
	return &PlanCompiler{registry: registry, limits: limits}, nil
}

func (c *PlanCompiler) Compile(ctx context.Context, request CompileRequest) (CompileResult, error) {
	if err := ctx.Err(); err != nil {
		return CompileResult{}, err
	}
	if terminal := c.validatePlan(request); terminal != nil {
		return CompileResult{planTerminal: terminal}, nil
	}
	stateHash, err := c.deriveStateCompatibilityHash(request)
	if err != nil {
		return CompileResult{}, fmt.Errorf("strategy: derive state compatibility: %w", err)
	}
	levelsByID := make(map[uint32]struct{}, len(request.Plan.StrategyIR.Levels))
	for _, level := range request.Plan.StrategyIR.Levels {
		if _, duplicate := levelsByID[level.Definition.LevelID]; duplicate {
			return CompileResult{planTerminal: &Terminal{ReasonCode: contract.ReasonPlanDuplicateLevelID, FieldPath: "strategy_ir.levels"}}, nil
		}
		levelsByID[level.Definition.LevelID] = struct{}{}
	}

	compiled := &CompiledPlan{
		planRef: contract.RuntimePlanRefV1{
			StrategyID: request.Plan.StrategyRef.StrategyID, StrategyRevision: request.Plan.StrategyRef.Revision,
			StateCompatibilityHash: stateHash,
		},
		projection:          cloneProjection(request.Plan.InputProjection),
		evaluationSemantics: request.Plan.StrategyIR.ExecutionSemantics,
		normalizers:         make(map[string]NumericNormalizerSpec),
	}
	terminals := make([]Terminal, 0)
	for _, rawLevel := range request.Plan.StrategyIR.Levels {
		level, normalizers, terminal, err := c.compileLevel(ctx, request.Plan.InputProjection, request.Plan.StrategyIR.ExecutionSemantics, rawLevel)
		if err != nil {
			return CompileResult{}, err
		}
		if terminal != nil {
			terminals = append(terminals, *terminal)
			continue
		}
		compiled.levels = append(compiled.levels, level)
		for _, normalizer := range normalizers {
			compiled.normalizers[normalizer.ref] = normalizer
		}
	}
	sort.Slice(compiled.levels, func(left, right int) bool {
		return compiled.levels[left].definition.LevelID < compiled.levels[right].definition.LevelID
	})
	if err := compilePlanFingerprints(compiled); err != nil {
		return CompileResult{}, err
	}
	sort.Slice(terminals, func(left, right int) bool { return terminals[left].LevelID < terminals[right].LevelID })
	return CompileResult{plan: compiled, levelTerminals: terminals}, nil
}

func (c *PlanCompiler) validatePlan(request CompileRequest) *Terminal {
	plan := request.Plan
	strategy := plan.StrategyIR
	if plan.PlanID == "" || plan.PlanID != plan.StrategyRef.StrategyID || plan.StrategyRef != strategy.StrategyRef ||
		plan.InputProjection.BusinessIdentityField == "" || !equalProjection(plan.InputProjection, strategy.InputProjection) ||
		strategy.Schema.Name != contract.StrategyIRSchemaV2 || strategy.Schema.Major != 2 || strategy.Schema.Minor != 0 ||
		len(strategy.Levels) == 0 {
		return &Terminal{ReasonCode: contract.ReasonPlanInvalid, FieldPath: "strategy_ir"}
	}
	if len(strategy.RequiredFeatures) > 0 {
		return &Terminal{ReasonCode: contract.ReasonRequiredFeatureUnsupported, FieldPath: "strategy_ir.required_features"}
	}
	if strategy.ExecutionSemantics.EvaluationScope != contract.EvaluationScopeSeries ||
		strategy.ExecutionSemantics.QueryWindow == 0 || strategy.ExecutionSemantics.AggregationInterval == 0 || strategy.ExecutionSemantics.EvaluationInterval == 0 {
		return &Terminal{ReasonCode: contract.ReasonPlanInvalid, FieldPath: "strategy_ir.execution_semantics"}
	}
	if len(strategy.Levels) > c.limits.MaxLevelsPerPlan {
		return &Terminal{ReasonCode: contract.ReasonPlanBudgetExceeded, FieldPath: "strategy_ir.levels"}
	}
	payload, err := contract.CanonicalJSONV2(plan)
	if err != nil {
		return &Terminal{ReasonCode: contract.ReasonPlanInvalid, FieldPath: "plan"}
	}
	if len(payload) > c.limits.MaxPlanBytes {
		return &Terminal{ReasonCode: contract.ReasonPlanBudgetExceeded, FieldPath: "plan"}
	}
	projection := plan.InputProjection
	if projection.MissingValuePolicy != contract.MissingValuePolicyRequired || projection.MultiValueAlignment != "SINGLE_VALUE" ||
		len(projection.ValueFields) == 0 || !sortedUnique(projection.ValueFields, false) || !sortedUnique(projection.DimensionFields, true) ||
		projection.BusinessIdentityField != "bk_biz_id" || !equalStrings(projection.DimensionFields, request.DatasetContract.IdentityFields) {
		return &Terminal{ReasonCode: contract.ReasonProjectionInvalid, FieldPath: "input_projection"}
	}
	return nil
}

func (c *PlanCompiler) deriveStateCompatibilityHash(request CompileRequest) (string, error) {
	semantics := request.StateSemantics
	return contract.DeriveStateCompatibilityHashV1(contract.StateCompatibilityInputV1{
		StateSchemaVersion: semantics.StateSchemaVersion, CodecSemanticsVersion: semantics.CodecSemanticsVersion,
		IdentitySchemaDigest:        semantics.IdentitySchemaDigest,
		EvaluationScope:             request.Plan.StrategyIR.ExecutionSemantics.EvaluationScope,
		AggregationInterval:         request.Plan.StrategyIR.ExecutionSemantics.AggregationInterval,
		EvaluationInterval:          request.Plan.StrategyIR.ExecutionSemantics.EvaluationInterval,
		SourceTimeSemanticsVersion:  semantics.SourceTimeSemanticsVersion,
		HistoryCellSemanticsVersion: semantics.HistoryCellSemanticsVersion,
	})
}

func (c *PlanCompiler) compileLevel(
	ctx context.Context,
	projection contract.InputProjectionV2,
	execution contract.ExecutionSemanticsV2,
	raw contract.LevelIRV2,
) (CompiledLevel, []NumericNormalizerSpec, *Terminal, error) {
	levelID := raw.Definition.LevelID
	terminal := func(reason, field string) (CompiledLevel, []NumericNormalizerSpec, *Terminal, error) {
		return CompiledLevel{}, nil, &Terminal{LevelID: levelID, ReasonCode: reason, FieldPath: field}, nil
	}
	if levelID == 0 || raw.Definition.Priority == 0 || (raw.Connector != contract.LevelConnectorAND && raw.Connector != contract.LevelConnectorOR) ||
		len(raw.DetectPlan.Algorithms) == 0 {
		return terminal(contract.ReasonLevelInvalid, "level")
	}
	if len(raw.DetectPlan.Algorithms) > c.limits.MaxAlgorithmsPerLevel {
		return terminal(contract.ReasonLevelBudgetExceeded, "level.detect_plan.algorithms")
	}
	detectors := make([]DetectorSpec, 0, len(raw.DetectPlan.Algorithms))
	normalizers := make([]NumericNormalizerSpec, 0, len(raw.DetectPlan.Algorithms))
	astNodes := 1
	for _, algorithm := range raw.DetectPlan.Algorithms {
		compiler, ok := c.registry.lookup(algorithm.Type, algorithm.Version)
		if !ok {
			return terminal(contract.ReasonAlgorithmUnsupported, "level.detect_plan.algorithms")
		}
		compiled, err := compiler.Compile(ctx, AlgorithmCompileContext{Projection: projection, Limits: c.limits}, algorithm)
		if errors.Is(err, errAlgorithmBudget) {
			return terminal(contract.ReasonLevelBudgetExceeded, "level.detect_plan.algorithms")
		}
		if errors.Is(err, errAlgorithmConfig) {
			return terminal(contract.ReasonLevelInvalid, "level.detect_plan.algorithms")
		}
		if err != nil {
			return CompiledLevel{}, nil, nil, fmt.Errorf("strategy: compile %s@%d: %w", algorithm.Type, algorithm.Version, err)
		}
		astNodes += compiled.ASTNodes
		if astNodes > c.limits.MaxASTNodesPerLevel {
			return terminal(contract.ReasonLevelBudgetExceeded, "level.detect_plan")
		}
		detectors = append(detectors, compiled.Detector)
		normalizers = append(normalizers, compiled.Normalizer)
	}
	trigger, canonicalTrigger, err := compileTriggerPlan(raw.TriggerPlan, execution)
	if err != nil {
		return terminal(contract.ReasonLevelInvalid, "level.trigger_plan")
	}
	recovery, canonicalRecovery, err := compileRecoveryPlan(raw.RecoveryPlan)
	if err != nil {
		return terminal(contract.ReasonLevelInvalid, "level.recovery_plan")
	}
	requiredPoints := trigger.WindowSize
	if recovery.Enabled {
		addition := recovery.ConsecutiveWindows - 1
		if addition > ^uint32(0)-requiredPoints {
			return terminal(contract.ReasonLevelBudgetExceeded, "level.recovery_plan")
		}
		requiredPoints += addition
	}
	if requiredPoints > c.limits.MaxRequiredHistoryPoints {
		return terminal(contract.ReasonLevelBudgetExceeded, "level.state_requirement")
	}
	level := CompiledLevel{
		definition: raw.Definition, connector: raw.Connector, detectors: detectors, trigger: trigger, recovery: recovery,
		stateRequirement: StateRequirement{RequiredDetectHistoryPoints: requiredPoints},
	}
	projectionDigest, err := contract.DeriveCanonicalDigestV2("level-projection-v1", projection)
	if err != nil {
		return CompiledLevel{}, nil, nil, fmt.Errorf("strategy: derive projection digest: %w", err)
	}
	semantics := make([]detectorSemantic, len(detectors))
	for index := range detectors {
		semantics[index] = detectors[index].semantic(normalizers[index])
	}
	detectorDigest, err := contract.DeriveCanonicalDigestV2("level-detector-semantics-v1", struct {
		Connector string             `json:"connector"`
		Detectors []detectorSemantic `json:"detectors"`
	}{raw.Connector, semantics})
	if err != nil {
		return CompiledLevel{}, nil, nil, fmt.Errorf("strategy: derive detector digest: %w", err)
	}
	level.fingerprints.Detect, err = contract.DeriveLevelDetectFingerprintV1(contract.LevelDetectSemanticV1{
		LevelID: levelID, ProjectionDigest: projectionDigest, DetectorSemanticDigest: detectorDigest,
	})
	if err != nil {
		return CompiledLevel{}, nil, nil, fmt.Errorf("strategy: derive Level detect fingerprint: %w", err)
	}
	level.fingerprints.Trigger, err = contract.DeriveLevelTriggerFingerprintV1(level.fingerprints.Detect, canonicalTrigger, canonicalRecovery)
	if err != nil {
		return CompiledLevel{}, nil, nil, fmt.Errorf("strategy: derive Level trigger fingerprint: %w", err)
	}
	return level, normalizers, nil, nil
}

func compileTriggerPlan(raw contract.TypedPlanV1, execution contract.ExecutionSemanticsV2) (TriggerPlan, contract.TypedPlanV1, error) {
	if raw.Type != TriggerPlanTypeNOfM || raw.Version != 1 {
		return TriggerPlan{}, contract.TypedPlanV1{}, errors.New("unsupported trigger plan")
	}
	var config triggerPlanConfigV1
	if err := decodeStrict(raw.Config, &config); err != nil {
		return TriggerPlan{}, contract.TypedPlanV1{}, err
	}
	if config.WindowSize == 0 || config.RequiredAnomalies == 0 || config.StepSeconds == 0 || config.StepSeconds != execution.EvaluationInterval {
		return TriggerPlan{}, contract.TypedPlanV1{}, errors.New("invalid trigger plan")
	}
	canonical, err := canonicalTypedPlan(raw.Type, raw.Version, config)
	return TriggerPlan(config), canonical, err
}

func compileRecoveryPlan(raw contract.TypedPlanV1) (RecoveryPlan, contract.TypedPlanV1, error) {
	if raw.Type != RecoveryPlanTypeContinuousTriggerMiss || raw.Version != 1 {
		return RecoveryPlan{}, contract.TypedPlanV1{}, errors.New("unsupported recovery plan")
	}
	var config recoveryPlanConfigV1
	if err := decodeStrict(raw.Config, &config); err != nil {
		return RecoveryPlan{}, contract.TypedPlanV1{}, err
	}
	if config.Enabled == nil || (*config.Enabled && config.ConsecutiveWindows == 0) {
		return RecoveryPlan{}, contract.TypedPlanV1{}, errors.New("invalid recovery plan")
	}
	if !*config.Enabled {
		config.ConsecutiveWindows = 0
	}
	normalized := struct {
		Enabled            bool   `json:"enabled"`
		ConsecutiveWindows uint32 `json:"consecutive_windows"`
	}{Enabled: *config.Enabled, ConsecutiveWindows: config.ConsecutiveWindows}
	canonical, err := canonicalTypedPlan(raw.Type, raw.Version, normalized)
	return RecoveryPlan(normalized), canonical, err
}

func canonicalTypedPlan(kind string, version uint32, config any) (contract.TypedPlanV1, error) {
	raw, err := json.Marshal(config)
	if err != nil {
		return contract.TypedPlanV1{}, err
	}
	canonical, err := contract.CanonicalJSONV2(raw)
	if err != nil {
		return contract.TypedPlanV1{}, err
	}
	return contract.TypedPlanV1{Type: kind, Version: version, Config: canonical}, nil
}

func compilePlanFingerprints(plan *CompiledPlan) error {
	detect := make(map[uint32]string, len(plan.levels))
	trigger := make(map[uint32]string, len(plan.levels))
	for _, level := range plan.levels {
		detect[level.definition.LevelID] = level.fingerprints.Detect
		trigger[level.definition.LevelID] = level.fingerprints.Trigger
	}
	if len(detect) == 0 {
		return nil
	}
	var err error
	plan.fingerprints.Detect, err = contract.DerivePlanFingerprintV1(contract.DetectPlanFingerprintDomainV1, detect)
	if err != nil {
		return fmt.Errorf("strategy: derive detect plan fingerprint: %w", err)
	}
	plan.fingerprints.Trigger, err = contract.DerivePlanFingerprintV1(contract.TriggerStateFingerprintDomainV1, trigger)
	if err != nil {
		return fmt.Errorf("strategy: derive trigger plan fingerprint: %w", err)
	}
	return nil
}

func decodeStrict(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func cloneProjection(projection contract.InputProjectionV2) contract.InputProjectionV2 {
	projection.ValueFields = append([]string(nil), projection.ValueFields...)
	projection.DimensionFields = append([]string(nil), projection.DimensionFields...)
	return projection
}

func equalProjection(left, right contract.InputProjectionV2) bool {
	return left.BusinessIdentityField == right.BusinessIdentityField && left.MultiValueAlignment == right.MultiValueAlignment &&
		left.DataUnit == right.DataUnit && left.MissingValuePolicy == right.MissingValuePolicy &&
		equalStrings(left.ValueFields, right.ValueFields) && equalStrings(left.DimensionFields, right.DimensionFields)
}

func equalStrings(left, right []string) bool {
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

func sortedUnique(values []string, allowEmpty bool) bool {
	previous := ""
	for index, value := range values {
		if (!allowEmpty && value == "") || (index > 0 && value <= previous) {
			return false
		}
		previous = value
	}
	return true
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
