// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package enrich

import (
	"context"
	"fmt"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
)

// Processor 是一个可重复执行的只读丰富步骤。
type Processor interface {
	Name() string
	// Match 判断当前告警是否适用该 Processor；无法完成判断时返回错误。
	Match(ctx context.Context, scope *Scope) (bool, error)
	Process(ctx context.Context, scope *Scope) (ProcessorResult, error)
}

// Chain 按固定顺序执行一组 Processor，并编码为 Alert.enrich payload。
type Chain struct {
	processors []Processor
	sources    Sources
	observer   Observer
}

// NewChain 创建不可变的 Processor 执行链。
func NewChain(processors []Processor, sources Sources, options ...ChainOption) (*Chain, error) {
	copied := append([]Processor(nil), processors...)
	seen := make(map[string]struct{}, len(copied))
	for index, processor := range copied {
		if processor == nil {
			return nil, fmt.Errorf("enrich processor[%d] must not be nil", index)
		}
		name := processor.Name()
		if name == "" {
			return nil, fmt.Errorf("enrich processor[%d] name is required", index)
		}
		if _, exists := seen[name]; exists {
			return nil, fmt.Errorf("enrich processor %q is duplicated", name)
		}
		seen[name] = struct{}{}
	}
	chain := &Chain{processors: copied, sources: sources, observer: NoopObserver()}
	for _, option := range options {
		if option != nil {
			option(chain)
		}
	}
	return chain, nil
}

// Enrich 执行完整处理链；单步骤错误隔离后继续后续步骤，父 Context 取消立即停止。
func (c *Chain) Enrich(ctx context.Context, input lifecycle.EnrichInput) (lifecycle.EnrichResult, error) {
	if ctx == nil {
		return lifecycle.EnrichResult{}, fmt.Errorf("enrich: context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return lifecycle.EnrichResult{}, err
	}
	scope, err := NewScope(input.Alert, c.sources)
	if err != nil {
		return lifecycle.EnrichResult{}, err
	}
	results := make([]ProcessorResult, 0, len(c.processors))
	entries := make([]ProcessorEntry, 0, len(c.processors))
	for _, processor := range c.processors {
		if err := ctx.Err(); err != nil {
			return lifecycle.EnrichResult{}, err
		}
		startedAt := time.Now()
		result, outcome := runProcessor(ctx, processor, scope)
		result, valid := normalizeProcessorResult(result)
		if !valid {
			outcome = ProcessorOutcomeInvalidResult
		}
		c.observer.ProcessorFinished(ctx, ProcessorObservation{
			Processor: processor.Name(), Status: result.Status, Outcome: outcome,
			Duration: time.Since(startedAt), Diagnostics: cloneDiagnostics(result.Diagnostics),
		})
		if err := ctx.Err(); err != nil {
			return lifecycle.EnrichResult{}, err
		}
		results = append(results, result)
		entries = append(entries, ProcessorEntry{processor.Name(): ProcessorEnvelope{
			Status: result.Status, Value: result.Value, Diagnostics: cloneDiagnostics(result.Diagnostics),
		}})
	}
	status := aggregateStatus(results)
	payload := Payload{Processors: entries}
	data, err := payload.JSONObject()
	if err != nil {
		return lifecycle.EnrichResult{}, err
	}
	return lifecycle.EnrichResult{Status: status, Data: data}, nil
}

func runProcessor(ctx context.Context, processor Processor, scope *Scope) (result ProcessorResult, outcome string) {
	outcome = ProcessorOutcomeCompleted
	defer func() {
		if recover() != nil {
			result = failedProcessorResult(processor.Name())
			outcome = ProcessorOutcomePanic
		}
	}()
	matched, err := processor.Match(ctx, scope)
	if err != nil {
		return failedProcessorResult(processor.Name()), ProcessorOutcomeMatchError
	}
	if !matched {
		return ProcessorResult{Status: domain.EnrichStatusSkipped, Value: domain.JSONObject{}}, ProcessorOutcomeCompleted
	}
	result, err = processor.Process(ctx, scope)
	if err != nil {
		return failedProcessorResult(processor.Name()), ProcessorOutcomeProcessError
	}
	return result, outcome
}

func failedProcessorResult(name string) ProcessorResult {
	return ProcessorResult{
		Status: domain.EnrichStatusFailed,
		Value:  domain.JSONObject{},
		Diagnostics: []Diagnostic{{
			Code: DiagnosticCodeDependencyInvalid, Dependency: name,
		}},
	}
}

func normalizeProcessorResult(result ProcessorResult) (ProcessorResult, bool) {
	if !processorStatusValid(result.Status) {
		return invalidProcessorResult(), false
	}
	if result.Value == nil {
		result.Value = domain.JSONObject{}
	}
	normalized, err := result.Value.Normalize()
	if err != nil {
		return invalidProcessorResult(), false
	}
	result.Value = normalized
	return result, true
}

func invalidProcessorResult() ProcessorResult {
	return ProcessorResult{
		Status:      domain.EnrichStatusFailed,
		Value:       domain.JSONObject{},
		Diagnostics: []Diagnostic{{Code: DiagnosticCodeInvalidField, Fields: []string{"status"}}},
	}
}

func cloneDiagnostics(diagnostics []Diagnostic) []Diagnostic {
	if diagnostics == nil {
		return nil
	}
	cloned := make([]Diagnostic, len(diagnostics))
	for index, diagnostic := range diagnostics {
		cloned[index] = diagnostic
		cloned[index].Fields = append([]string(nil), diagnostic.Fields...)
	}
	return cloned
}
