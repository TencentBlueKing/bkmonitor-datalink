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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type AlgorithmCompileContext struct {
	Projection contract.InputProjectionV2
	Limits     Limits
}

type AlgorithmCompileResult struct {
	Detector   DetectorSpec
	Normalizer NumericNormalizerSpec
	ASTNodes   int
}

type AlgorithmCompiler interface {
	Kind() string
	Version() uint32
	Compile(context.Context, AlgorithmCompileContext, contract.AlgorithmIRV2) (AlgorithmCompileResult, error)
}

type algorithmKey struct {
	kind    string
	version uint32
}

type AlgorithmCompilerRegistry struct {
	compilers map[algorithmKey]AlgorithmCompiler
}

func NewAlgorithmCompilerRegistry(compilers ...AlgorithmCompiler) (*AlgorithmCompilerRegistry, error) {
	registry := &AlgorithmCompilerRegistry{compilers: make(map[algorithmKey]AlgorithmCompiler, len(compilers))}
	for _, compiler := range compilers {
		if compiler == nil || compiler.Kind() == "" || compiler.Version() == 0 {
			return nil, fmt.Errorf("strategy: invalid algorithm compiler")
		}
		key := algorithmKey{kind: compiler.Kind(), version: compiler.Version()}
		if _, exists := registry.compilers[key]; exists {
			return nil, fmt.Errorf("strategy: duplicate algorithm compiler %s@%d", key.kind, key.version)
		}
		registry.compilers[key] = compiler
	}
	return registry, nil
}

func NewDefaultAlgorithmCompilerRegistry() *AlgorithmCompilerRegistry {
	registry, err := NewAlgorithmCompilerRegistry(thresholdAlgorithmCompiler{})
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *AlgorithmCompilerRegistry) lookup(kind string, version uint32) (AlgorithmCompiler, bool) {
	if r == nil {
		return nil, false
	}
	compiler, ok := r.compilers[algorithmKey{kind: kind, version: version}]
	return compiler, ok
}

type thresholdAlgorithmCompiler struct{}

func (thresholdAlgorithmCompiler) Kind() string {
	return DetectorKindThreshold
}

func (thresholdAlgorithmCompiler) Version() uint32 {
	return 1
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
	return AlgorithmCompileResult{
		Detector: DetectorSpec{
			kind: DetectorKindThreshold, version: 1, valueRef: config.ValueField, normalizerRef: normalizer.ref,
			predicate: Predicate{root: root}, declaredExecutorErrors: []string{},
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
