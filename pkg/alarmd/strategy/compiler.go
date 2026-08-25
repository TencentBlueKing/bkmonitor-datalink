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
	"time"

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
	registry         *AlgorithmCompilerRegistry
	limits           Limits
	cache            *compileCache
	capabilityDigest string
	budgetDigest     string
}

func NewCompiler(registry *AlgorithmCompilerRegistry, limits Limits) (*PlanCompiler, error) {
	if registry == nil || len(registry.compilers) == 0 {
		return nil, errors.New("strategy: algorithm registry is empty")
	}
	if limits.MaxPlanBytes <= 0 || limits.MaxLevelsPerPlan <= 0 || limits.MaxAlgorithmsPerLevel <= 0 ||
		limits.MaxGroupsPerAlgorithm <= 0 || limits.MaxConditionsPerAlgorithm <= 0 || limits.MaxASTNodesPerLevel <= 0 ||
		limits.MaxRequiredHistoryPoints == 0 || limits.MaxCompiledPlanBytes <= 0 || limits.MaxCacheEntries <= 0 ||
		limits.MaxCacheBytes <= 0 || limits.NegativeCacheTTL <= 0 || limits.BudgetRevision == "" {
		return nil, errors.New("strategy: deployment budgets must be positive")
	}
	budgetDigest, err := contract.DeriveCanonicalDigestV2("strategy-compiler-budget-v1", struct {
		MaxPlanBytes              int    `json:"max_plan_bytes"`
		MaxLevelsPerPlan          int    `json:"max_levels_per_plan"`
		MaxAlgorithmsPerLevel     int    `json:"max_algorithms_per_level"`
		MaxGroupsPerAlgorithm     int    `json:"max_groups_per_algorithm"`
		MaxConditionsPerAlgorithm int    `json:"max_conditions_per_algorithm"`
		MaxASTNodesPerLevel       int    `json:"max_ast_nodes_per_level"`
		MaxRequiredHistoryPoints  uint32 `json:"max_required_history_points"`
		MaxCompiledPlanBytes      int    `json:"max_compiled_plan_bytes"`
		BudgetRevision            string `json:"budget_revision"`
	}{
		limits.MaxPlanBytes, limits.MaxLevelsPerPlan, limits.MaxAlgorithmsPerLevel, limits.MaxGroupsPerAlgorithm,
		limits.MaxConditionsPerAlgorithm, limits.MaxASTNodesPerLevel, limits.MaxRequiredHistoryPoints,
		limits.MaxCompiledPlanBytes, limits.BudgetRevision,
	})
	if err != nil {
		return nil, fmt.Errorf("strategy: derive compiler budget digest: %w", err)
	}
	return &PlanCompiler{
		registry: registry, limits: limits,
		cache:            newCompileCache(limits.MaxCacheEntries, limits.MaxCacheBytes, limits.NegativeCacheTTL),
		capabilityDigest: registry.CapabilityDigest(), budgetDigest: budgetDigest,
	}, nil
}

func (c *PlanCompiler) Compile(ctx context.Context, request CompileRequest) (CompileResult, error) {
	if err := ctx.Err(); err != nil {
		return CompileResult{}, err
	}
	key, err := c.compileCacheKey(request)
	if err != nil {
		return c.compileUncached(ctx, request)
	}
	if result, ok := c.cache.get(key, time.Now()); ok {
		return result, nil
	}
	result, err := c.compileUncached(ctx, request)
	if err != nil {
		return CompileResult{}, err
	}
	negative := result.planTerminal != nil || len(result.levelTerminals) > 0
	c.cache.put(key, result, estimateCompileResultBytes(result), negative, time.Now())
	return result, nil
}

func (c *PlanCompiler) CacheStats() CacheStats {
	if c == nil || c.cache == nil {
		return CacheStats{}
	}
	return c.cache.snapshot()
}

func (c *PlanCompiler) compileUncached(ctx context.Context, request CompileRequest) (CompileResult, error) {
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

	datasetDigest, err := deriveDatasetContractDigest(request.DatasetContract)
	if err != nil {
		return CompileResult{}, fmt.Errorf("strategy: derive dataset contract digest: %w", err)
	}
	compiled := &CompiledPlan{
		planRef: contract.RuntimePlanRefV1{
			StrategyID: request.Plan.StrategyRef.StrategyID, StrategyRevision: request.Plan.StrategyRef.Revision,
			StateCompatibilityHash: stateHash,
		},
		projection:          cloneProjection(request.Plan.InputProjection),
		evaluationSemantics: request.Plan.StrategyIR.ExecutionSemantics,
		normalizers:         make(map[string]NumericNormalizerSpec),
		datasetDigest:       datasetDigest,
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
	if err := compileResourceEstimate(compiled); err != nil {
		return CompileResult{}, err
	}
	if compiled.resourceEstimate.CompiledBytes > c.limits.MaxCompiledPlanBytes {
		return CompileResult{planTerminal: &Terminal{ReasonCode: contract.ReasonPlanBudgetExceeded, FieldPath: "compiled_plan"}}, nil
	}
	sort.Slice(terminals, func(left, right int) bool { return terminals[left].LevelID < terminals[right].LevelID })
	return CompileResult{plan: compiled, levelTerminals: terminals}, nil
}

func (c *PlanCompiler) validatePlan(request CompileRequest) *Terminal {
	plan := request.Plan
	strategy := plan.StrategyIR
	if plan.PlanID == "" || plan.PlanID != plan.StrategyRef.StrategyID || plan.StrategyRef != strategy.StrategyRef ||
		plan.InputProjection.BusinessIdentityField == "" || !equalProjection(plan.InputProjection, strategy.InputProjection) ||
		strategy.Schema.Name != contract.StrategyIRSchemaV2 || strategy.Schema.Major != 2 || strategy.Schema.Minor < 0 ||
		len(strategy.Levels) == 0 {
		return &Terminal{ReasonCode: contract.ReasonPlanInvalid, FieldPath: "strategy_ir"}
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
		registration, ok := c.registry.lookup(algorithm.Type, algorithm.Version)
		if !ok {
			return terminal(contract.ReasonAlgorithmUnsupported, "level.detect_plan.algorithms")
		}
		if registration.capability.EvaluationScope != execution.EvaluationScope {
			return terminal(contract.ReasonAlgorithmUnsupported, "level.detect_plan.algorithms")
		}
		compiled, err := registration.compiler.Compile(ctx, AlgorithmCompileContext{
			Projection: projection, ExecutionSemantics: execution, Limits: c.limits,
		}, algorithm)
		if errors.Is(err, errAlgorithmBudget) {
			return terminal(contract.ReasonLevelBudgetExceeded, "level.detect_plan.algorithms")
		}
		if errors.Is(err, errAlgorithmConfig) {
			return terminal(contract.ReasonLevelInvalid, "level.detect_plan.algorithms")
		}
		if err != nil {
			return CompiledLevel{}, nil, nil, fmt.Errorf("strategy: compile %s@%d: %w", algorithm.Type, algorithm.Version, err)
		}
		if err := validateAlgorithmCompileResult(algorithm, registration.capability, projection, compiled); err != nil {
			return CompiledLevel{}, nil, nil, err
		}
		astNodes += compiled.ASTNodes
		if astNodes > c.limits.MaxASTNodesPerLevel {
			return terminal(contract.ReasonLevelBudgetExceeded, "level.detect_plan")
		}
		detectors = append(detectors, compiled.Detector)
		normalizers = append(normalizers, compiled.Normalizer)
	}
	trigger, effectiveTime, canonicalTrigger, err := compileTriggerPlan(raw.TriggerPlan, execution)
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
		effectiveTime:    effectiveTime,
		stateRequirement: StateRequirement{RequiredDetectHistoryPoints: requiredPoints},
	}
	level.resourceEstimate.Algorithms = len(detectors)
	level.resourceEstimate.ASTNodes = astNodes
	level.resourceEstimate.StatePointsPerSeries = uint64(requiredPoints)
	level.resourceEstimate.FixedComputeCost = 1
	for _, algorithm := range raw.DetectPlan.Algorithms {
		registration, _ := c.registry.lookup(algorithm.Type, algorithm.Version)
		capability := registration.capability
		level.resourceEstimate.FixedComputeCost += capability.FixedComputeCost
		level.resourceEstimate.CostPerRecord += capability.CostPerRecord
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

func validateAlgorithmCompileResult(
	raw contract.AlgorithmIRV2,
	capability AlgorithmCapability,
	projection contract.InputProjectionV2,
	result AlgorithmCompileResult,
) error {
	if capability.Kind != raw.Type || capability.Version != raw.Version || result.Detector.kind != raw.Type || result.Detector.version != raw.Version ||
		result.Detector.valueRef == "" || !contains(projection.ValueFields, result.Detector.valueRef) ||
		result.Detector.normalizerRef == "" || result.Detector.normalizerRef != result.Normalizer.ref || len(result.Normalizer.ref) != 64 ||
		result.Normalizer.sourceMultiplier <= 0 || result.Normalizer.decimalPlaces != 6 || result.Normalizer.rounding != "HALF_EVEN" ||
		result.ASTNodes <= 0 {
		return fmt.Errorf("strategy: invalid compiler output for %s@%d", raw.Type, raw.Version)
	}
	nodes, err := result.Detector.predicate.root.validate()
	if err != nil || nodes != result.ASTNodes || result.Detector.predicate.validate() != nil {
		return fmt.Errorf("strategy: invalid compiler output for %s@%d", raw.Type, raw.Version)
	}
	for _, reason := range result.Detector.declaredExecutorErrors {
		if !contract.IsKnownReasonV2(reason) {
			return fmt.Errorf("strategy: invalid compiler output for %s@%d", raw.Type, raw.Version)
		}
	}
	return nil
}

func compileTriggerPlan(
	raw contract.TypedPlanV1,
	execution contract.ExecutionSemanticsV2,
) (TriggerPlan, EffectiveTimeRequirement, contract.TypedPlanV1, error) {
	if raw.Type != TriggerPlanTypeNOfM || raw.Version != 1 {
		return TriggerPlan{}, EffectiveTimeRequirement{}, contract.TypedPlanV1{}, errors.New("unsupported trigger plan")
	}
	var config triggerPlanConfigV1
	if err := decodeStrict(raw.Config, &config); err != nil {
		return TriggerPlan{}, EffectiveTimeRequirement{}, contract.TypedPlanV1{}, err
	}
	if config.WindowSize == 0 || config.RequiredAnomalies == 0 || config.StepSeconds == 0 || config.StepSeconds != execution.EvaluationInterval {
		return TriggerPlan{}, EffectiveTimeRequirement{}, contract.TypedPlanV1{}, errors.New("invalid trigger plan")
	}
	requirement, err := compileEffectiveTimeRequirement(config.Uptime, config.TimezoneRef)
	if err != nil {
		return TriggerPlan{}, EffectiveTimeRequirement{}, contract.TypedPlanV1{}, err
	}
	trigger := TriggerPlan{
		WindowSize: config.WindowSize, RequiredAnomalies: config.RequiredAnomalies, StepSeconds: config.StepSeconds,
	}
	canonical, err := canonicalTypedPlan(raw.Type, raw.Version, trigger)
	return trigger, requirement, canonical, err
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

func (c *PlanCompiler) compileCacheKey(request CompileRequest) (string, error) {
	planDigest, err := contract.DeriveCanonicalDigestV2("strategy-plan-semantic-v1", request.Plan)
	if err != nil {
		return "", err
	}
	datasetDigest, err := deriveDatasetContractDigest(request.DatasetContract)
	if err != nil {
		return "", err
	}
	stateDigest, err := contract.DeriveCanonicalDigestV2("strategy-state-semantics-v1", struct {
		StateSchemaVersion          string `json:"state_schema_version"`
		CodecSemanticsVersion       string `json:"codec_semantics_version"`
		IdentitySchemaDigest        string `json:"identity_schema_digest"`
		SourceTimeSemanticsVersion  string `json:"source_time_semantics_version"`
		HistoryCellSemanticsVersion string `json:"history_cell_semantics_version"`
	}{
		request.StateSemantics.StateSchemaVersion, request.StateSemantics.CodecSemanticsVersion,
		request.StateSemantics.IdentitySchemaDigest, request.StateSemantics.SourceTimeSemanticsVersion,
		request.StateSemantics.HistoryCellSemanticsVersion,
	})
	if err != nil {
		return "", err
	}
	return contract.DeriveCanonicalDigestV2("strategy-compile-cache-key-v1", struct {
		PlanDigest       string `json:"plan_digest"`
		CapabilityDigest string `json:"capability_digest"`
		DatasetDigest    string `json:"dataset_digest"`
		StateDigest      string `json:"state_digest"`
		BudgetDigest     string `json:"budget_digest"`
	}{planDigest, c.capabilityDigest, datasetDigest, stateDigest, c.budgetDigest})
}

func deriveDatasetContractDigest(dataset contract.DatasetContractV2) (string, error) {
	return contract.DeriveCanonicalDigestV2("strategy-dataset-contract-v1", dataset)
}

type compiledLevelWire struct {
	Definition       contract.LevelDefinitionV2   `json:"definition"`
	Connector        string                       `json:"connector"`
	Detectors        []detectorSemantic           `json:"detectors"`
	Trigger          TriggerPlan                  `json:"trigger"`
	Recovery         RecoveryPlan                 `json:"recovery"`
	EffectiveTime    effectiveTimeRequirementWire `json:"effective_time"`
	StateRequirement StateRequirement             `json:"state_requirement"`
	Fingerprints     LevelFingerprints            `json:"fingerprints"`
}

func compileResourceEstimate(plan *CompiledPlan) error {
	aggregate := ResourceEstimate{FixedComputeCost: 1}
	levelWires := make([]compiledLevelWire, len(plan.levels))
	for levelIndex := range plan.levels {
		level := &plan.levels[levelIndex]
		detectors := make([]detectorSemantic, len(level.detectors))
		for detectorIndex, detector := range level.detectors {
			normalizer, ok := plan.normalizers[detector.normalizerRef]
			if !ok {
				return errors.New("strategy: compiled plan normalizer invariant violated")
			}
			detectors[detectorIndex] = detector.semantic(normalizer)
		}
		wire := compiledLevelWire{
			Definition: level.definition, Connector: level.connector, Detectors: detectors,
			Trigger: level.trigger, Recovery: level.recovery, EffectiveTime: level.effectiveTime.wire(),
			StateRequirement: level.stateRequirement, Fingerprints: level.fingerprints,
		}
		encoded, err := contract.CanonicalJSONV2(wire)
		if err != nil {
			return fmt.Errorf("strategy: estimate compiled Level bytes: %w", err)
		}
		level.resourceEstimate.CompiledBytes = len(encoded)
		levelWires[levelIndex] = wire
		aggregate.FixedComputeCost += level.resourceEstimate.FixedComputeCost
		aggregate.CostPerRecord += level.resourceEstimate.CostPerRecord
		aggregate.StatePointsPerSeries += level.resourceEstimate.StatePointsPerSeries
		aggregate.Algorithms += level.resourceEstimate.Algorithms
		aggregate.ASTNodes += level.resourceEstimate.ASTNodes
	}
	encoded, err := contract.CanonicalJSONV2(struct {
		PlanRef             contract.RuntimePlanRefV1     `json:"plan_ref"`
		Projection          contract.InputProjectionV2    `json:"projection"`
		EvaluationSemantics contract.ExecutionSemanticsV2 `json:"evaluation_semantics"`
		DatasetDigest       string                        `json:"dataset_digest"`
		Levels              []compiledLevelWire           `json:"levels"`
		Fingerprints        PlanFingerprints              `json:"fingerprints"`
	}{plan.planRef, plan.projection, plan.evaluationSemantics, plan.datasetDigest, levelWires, plan.fingerprints})
	if err != nil {
		return fmt.Errorf("strategy: estimate compiled Plan bytes: %w", err)
	}
	aggregate.CompiledBytes = len(encoded)
	plan.resourceEstimate = aggregate
	return nil
}

func estimateCompileResultBytes(result CompileResult) int {
	size := 64
	if result.plan != nil {
		size += result.plan.resourceEstimate.CompiledBytes
	}
	if result.planTerminal != nil {
		size += len(result.planTerminal.ReasonCode) + len(result.planTerminal.FieldPath) + 32
	}
	for _, terminal := range result.levelTerminals {
		size += len(terminal.ReasonCode) + len(terminal.FieldPath) + 32
	}
	return size
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
