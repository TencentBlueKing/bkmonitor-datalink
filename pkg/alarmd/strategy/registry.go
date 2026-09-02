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
	"fmt"
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type AlgorithmCompileContext struct {
	Projection         contract.InputProjectionV2
	ExecutionSemantics contract.ExecutionSemanticsV2
	Limits             Limits
}

type AlgorithmCompileResult struct {
	Detector   DetectorSpec
	Normalizer NumericNormalizerSpec
	ASTNodes   int
}

type AlgorithmCapability struct {
	Kind                string `json:"kind"`
	Version             uint32 `json:"version"`
	EvaluationScope     string `json:"evaluation_scope"`
	InputShape          string `json:"input_shape"`
	RequiredHistoryKind string `json:"required_history_kind"`
	StateSchemaVersion  string `json:"state_schema_version"`
	Deterministic       bool   `json:"deterministic"`
	ExternalCallBudget  uint32 `json:"external_call_budget"`
	FixedComputeCost    uint64 `json:"fixed_compute_cost"`
	CostPerRecord       uint64 `json:"cost_per_record"`
}

type AlgorithmCompiler interface {
	Capability() AlgorithmCapability
	Compile(context.Context, AlgorithmCompileContext, contract.AlgorithmIRV2) (AlgorithmCompileResult, error)
}

type algorithmKey struct {
	kind    string
	version uint32
}

type registeredAlgorithm struct {
	compiler   AlgorithmCompiler
	capability AlgorithmCapability
}

type AlgorithmCompilerRegistry struct {
	compilers map[algorithmKey]registeredAlgorithm
	digest    string
}

func NewAlgorithmCompilerRegistry(compilers ...AlgorithmCompiler) (*AlgorithmCompilerRegistry, error) {
	registry := &AlgorithmCompilerRegistry{compilers: make(map[algorithmKey]registeredAlgorithm, len(compilers))}
	capabilities := make([]AlgorithmCapability, 0, len(compilers))
	for _, compiler := range compilers {
		if compiler == nil {
			return nil, fmt.Errorf("strategy: invalid algorithm compiler")
		}
		capability := compiler.Capability()
		if capability.Kind == "" || capability.Version == 0 || capability.EvaluationScope == "" || capability.InputShape == "" ||
			capability.RequiredHistoryKind == "" || capability.StateSchemaVersion == "" || !capability.Deterministic ||
			capability.FixedComputeCost == 0 || capability.CostPerRecord == 0 {
			return nil, fmt.Errorf("strategy: invalid algorithm capability")
		}
		key := algorithmKey{kind: capability.Kind, version: capability.Version}
		if _, exists := registry.compilers[key]; exists {
			return nil, fmt.Errorf("strategy: duplicate algorithm compiler %s@%d", key.kind, key.version)
		}
		registry.compilers[key] = registeredAlgorithm{compiler: compiler, capability: capability}
		capabilities = append(capabilities, capability)
	}
	sort.Slice(capabilities, func(left, right int) bool {
		if capabilities[left].Kind == capabilities[right].Kind {
			return capabilities[left].Version < capabilities[right].Version
		}
		return capabilities[left].Kind < capabilities[right].Kind
	})
	digest, err := contract.DeriveCanonicalDigestV2("algorithm-compiler-capabilities-v1", struct {
		PredicateSemantics  string                `json:"predicate_semantics"`
		NormalizerSemantics string                `json:"normalizer_semantics"`
		Capabilities        []AlgorithmCapability `json:"capabilities"`
	}{"threshold-predicate-v1", "numeric-normalizer-v1", capabilities})
	if err != nil {
		return nil, fmt.Errorf("strategy: derive capability digest: %w", err)
	}
	registry.digest = digest
	return registry, nil
}

func NewDefaultAlgorithmCompilerRegistry() *AlgorithmCompilerRegistry {
	registry, err := NewAlgorithmCompilerRegistry(thresholdAlgorithmCompiler{})
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *AlgorithmCompilerRegistry) lookup(kind string, version uint32) (registeredAlgorithm, bool) {
	if r == nil {
		return registeredAlgorithm{}, false
	}
	registration, ok := r.compilers[algorithmKey{kind: kind, version: version}]
	return registration, ok
}

func (r *AlgorithmCompilerRegistry) CapabilityDigest() string {
	if r == nil {
		return ""
	}
	return r.digest
}

type thresholdAlgorithmCompiler struct{}

func (thresholdAlgorithmCompiler) Capability() AlgorithmCapability {
	return AlgorithmCapability{
		Kind: DetectorKindThreshold, Version: 1, EvaluationScope: contract.EvaluationScopeSeries,
		InputShape: "ROW", RequiredHistoryKind: "NONE", StateSchemaVersion: "threshold-stateless-v1", Deterministic: true,
		FixedComputeCost: 1, CostPerRecord: 1,
	}
}

func (thresholdAlgorithmCompiler) Compile(_ context.Context, compileContext AlgorithmCompileContext, raw contract.AlgorithmIRV2) (AlgorithmCompileResult, error) {
	var config thresholdConfigV1
	if err := decodeStrict(raw.Config, &config); err != nil {
		return AlgorithmCompileResult{}, configErrorf("threshold config: %v", err)
	}
	if config.ValueField == "" || !contains(compileContext.Projection.ValueFields, config.ValueField) ||
		config.DataUnit != compileContext.Projection.DataUnit || config.Precision.DecimalPlaces != 6 || config.Precision.Rounding != "HALF_EVEN" ||
		config.ThresholdUnitPrefix == nil || len(config.Groups) == 0 || len(config.Groups) > compileContext.Limits.MaxGroupsPerAlgorithm {
		return AlgorithmCompileResult{}, configErrorf("threshold config: invalid projection, precision, or groups")
	}
	normalizer, thresholdMultiplier, ok := compileUnitNormalizer(config.DataUnit, *config.ThresholdUnitPrefix)
	if !ok {
		return AlgorithmCompileResult{}, configErrorf("threshold config: unsupported unit")
	}
	root := predicateNode{kind: PredicateAny, children: make([]predicateNode, 0, len(config.Groups))}
	nodes := 1
	conditions := 0
	for _, rawGroup := range config.Groups {
		if len(rawGroup.Conditions) == 0 {
			return AlgorithmCompileResult{}, configErrorf("threshold config: empty group")
		}
		conditions += len(rawGroup.Conditions)
		if conditions > compileContext.Limits.MaxConditionsPerAlgorithm {
			return AlgorithmCompileResult{}, errAlgorithmBudget
		}
		group := predicateNode{kind: PredicateAll, children: make([]predicateNode, 0, len(rawGroup.Conditions))}
		nodes++
		for _, condition := range rawGroup.Conditions {
			if !validOperator(condition.Operator) {
				return AlgorithmCompileResult{}, configErrorf("threshold config: unsupported operator %q", condition.Operator)
			}
			value, ok := parseDecimalRational(condition.ThresholdDecimal, false)
			if !ok {
				return AlgorithmCompileResult{}, configErrorf("threshold config: invalid decimal")
			}
			normalized, ok := normalizeRational(value, thresholdMultiplier)
			if !ok {
				return AlgorithmCompileResult{}, configErrorf("threshold config: decimal normalization overflow")
			}
			group.children = append(group.children, predicateNode{kind: PredicateCompare, operator: condition.Operator, threshold: normalized})
			nodes++
		}
		root.children = append(root.children, group)
	}
	predicateDigest, err := contract.DeriveCanonicalDigestV2("threshold-predicate-v1", root.wire())
	if err != nil {
		return AlgorithmCompileResult{}, fmt.Errorf("threshold predicate digest: %w", err)
	}
	return AlgorithmCompileResult{
		Detector: DetectorSpec{
			kind: DetectorKindThreshold, version: 1, valueRef: config.ValueField, normalizerRef: normalizer.ref,
			predicate: Predicate{root: root, digest: predicateDigest}, declaredExecutorErrors: []string{},
		},
		Normalizer: normalizer,
		ASTNodes:   nodes,
	}, nil
}

func validOperator(operator string) bool {
	switch operator {
	case "GT", "GTE", "EQ", "NEQ", "LT", "LTE":
		return true
	default:
		return false
	}
}
