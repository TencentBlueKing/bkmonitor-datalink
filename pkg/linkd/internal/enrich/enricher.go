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
	"encoding/json"
	"fmt"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich/kingeye"
	"linkd/internal/processingerror"
)

// Processor 是可重复执行的丰富步骤；外部依赖只读，变更仅限本等级工作对象。
type Processor interface {
	Name() string
	// Match 判断当前告警是否适用该 Processor；无法完成判断时返回错误。
	Match(ctx context.Context, scope *Scope) (bool, error)
	Process(ctx context.Context, scope *Scope) (ProcessorResult, error)
}

// Chain 按固定顺序执行一组 Processor，并编码为 Event.enrich 的逐等级 payload。
type Chain struct {
	processors []Processor
	sources    Sources
	observer   Observer
	logger     Logger
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
func (c *Chain) Enrich(ctx context.Context, input Input) (Result, error) {
	if ctx == nil {
		return Result{}, fmt.Errorf("enrich: context must not be nil")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	event := input.Event.Clone()
	event.EventEnrichment = domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending}
	if !input.Preview {
		if err := event.ValidateIdentity(); err != nil {
			return Result{}, err
		}
	}
	if err := domain.ValidateEvaluations(event.Evaluations); err != nil {
		return Result{}, err
	}
	result := Result{Evaluations: map[string]*domain.EnrichEvaluation{}, Data: domain.EventEnrichData{Evaluations: make([]domain.EvaluationEnrich, 0, len(event.Evaluations))}}
	cache := newRequestCache()
	budget := &chainBudget{}
	statuses := make([]domain.EnrichStatus, 0, len(event.Evaluations))
	for _, evaluation := range event.Evaluations {
		scope := newOwnedScope(&event, evaluation, c.sources, cache)
		evaluated, err := c.enrichEvaluation(ctx, scope, budget)
		if err != nil {
			return Result{}, err
		}
		result.Data.Evaluations = append(result.Data.Evaluations, evaluated)
		if scope.state != nil {
			if err := scope.state.FreezeResult(evaluated.Data); err != nil {
				return Result{}, err
			}
			result.Evaluations[evaluation.Severity] = scope.state
		}
		statuses = append(statuses, evaluated.Status)
	}
	result.Status = domain.AggregateEnrichStatus(statuses)
	return result, nil
}

type chainBudget struct{ patchCount, payloadBytes int }

func (c *Chain) enrichEvaluation(ctx context.Context, scope *Scope, budget *chainBudget) (domain.EvaluationEnrich, error) {
	results := make([]ProcessorResult, 0, len(c.processors))
	entries := make([]map[string]json.RawMessage, 0, len(c.processors))
	for _, processor := range c.processors {
		if err := ctx.Err(); err != nil {
			return domain.EvaluationEnrich{}, fmt.Errorf("enrich processor %s result: %w", processor.Name(), err)
		}
		startedAt := time.Now()
		scope.dependencyErrors = nil
		scope.dependencies = nil
		scope.running = true
		result, outcome, failure := runProcessor(ctx, processor, scope)
		scope.running = false
		result, valid := normalizeProcessorResult(result)
		assignments := result.RuntimePatches
		if valid && assignments == nil {
			var err error
			if result.Patches != nil {
				assignments = make([]domain.EnrichAssignment, 0, len(result.Patches))
				for _, patch := range result.Patches {
					assignment, e := domain.PrepareEnrichPatch(patch)
					if e != nil {
						err = e
						break
					}
					assignments = append(assignments, assignment)
				}
			} else {
				assignments, err = kingeye.ProjectAssignments(processor.Name(), result.Value)
			}
			if err != nil {
				valid = false
				failure = fmt.Errorf("prepare processor patches: %w", err)
			}
		}
		applicable := valid && (result.Status == domain.EnrichStatusSucceeded || result.Status == domain.EnrichStatusPartial)
		if applicable && !result.applied {
			state, err := scope.EvaluationState()
			if err == nil {
				err = state.Apply(assignments)
			}
			if err != nil {
				valid, applicable = false, false
				failure = fmt.Errorf("apply processor patches: %w", err)
			}
		}
		if !applicable {
			scope.rollback()
			assignments = nil
		}
		if !valid {
			if failure == nil {
				failure = fmt.Errorf("invalid processor status or diagnostics")
			}
			result = invalidProcessorResult()
			outcome = ProcessorOutcomeInvalidResult
		}
		// 已求值补丁只在结果协议边界编码；状态仍留在同一个工作对象中。
		patches := make([]domain.EnrichPatch, 0, len(assignments))
		for _, assignment := range assignments {
			patch, err := assignment.Patch()
			if err != nil {
				return domain.EvaluationEnrich{}, fmt.Errorf("enrich processor %s result: %w", processor.Name(), err)
			}
			patches = append(patches, patch)
		}
		envelope := ProcessorEnvelope{Status: result.Status, Patches: patches, Trace: result.Trace, Diagnostics: cloneDiagnostics(result.Diagnostics)}
		encoded, err := json.Marshal(envelope)
		if err != nil {
			return domain.EvaluationEnrich{}, fmt.Errorf("enrich processor %s result: %w", processor.Name(), err)
		}
		if budget.patchCount+len(patches) > 4096 || budget.payloadBytes+len(encoded) > 900000 {
			failure = fmt.Errorf("processor output exceeds event patch or payload budget")
			scope.rollback()
			result = invalidProcessorResult()
			outcome = ProcessorOutcomeInvalidResult
			encoded, err = json.Marshal(ProcessorEnvelope{Status: result.Status, Patches: []domain.EnrichPatch{}, Diagnostics: result.Diagnostics})
			if err != nil {
				return domain.EvaluationEnrich{}, fmt.Errorf("enrich processor %s result: %w", processor.Name(), err)
			}
		} else {
			budget.patchCount += len(patches)
			budget.payloadBytes += len(encoded)
		}
		if err := ctx.Err(); err != nil {
			scope.rollback()
			return domain.EvaluationEnrich{}, fmt.Errorf("enrich processor %s result: %w", processor.Name(), err)
		}
		if err := scope.commit(); err != nil {
			failure = fmt.Errorf("commit processor patches: %w", err)
			scope.rollback()
			result = invalidProcessorResult()
			outcome = ProcessorOutcomeInvalidResult
			encoded, err = json.Marshal(ProcessorEnvelope{Status: result.Status, Patches: []domain.EnrichPatch{}, Diagnostics: result.Diagnostics})
			if err != nil {
				return domain.EvaluationEnrich{}, fmt.Errorf("enrich processor %s result: %w", processor.Name(), err)
			}
		}
		c.logProcessorFailure(ctx, scope, processor.Name(), outcome, result, failure)
		c.observer.ProcessorFinished(ctx, ProcessorObservation{Processor: processor.Name(), Status: result.Status, Outcome: outcome, Duration: time.Since(startedAt), Diagnostics: cloneDiagnostics(result.Diagnostics)})
		if err := ctx.Err(); err != nil {
			return domain.EvaluationEnrich{}, fmt.Errorf("enrich processor %s result: %w", processor.Name(), err)
		}
		results = append(results, result)
		entries = append(entries, map[string]json.RawMessage{processor.Name(): encoded})
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		return domain.EvaluationEnrich{}, err
	}
	data, err := (domain.JSONObject{"processors": encoded}).Normalize()
	if err != nil {
		return domain.EvaluationEnrich{}, err
	}
	return domain.EvaluationEnrich{Severity: scope.evaluation.Severity, Status: aggregateStatus(results), Data: data}, nil
}

func runProcessor(ctx context.Context, processor Processor, scope *Scope) (result ProcessorResult, outcome string, err error) {
	defer startProcessorTiming(ctx, processor.Name())()
	outcome = ProcessorOutcomeCompleted
	defer func() {
		if recover() != nil {
			result = failedProcessorResult(processor.Name())
			outcome = ProcessorOutcomePanic
			err = processingerror.CapturePanic()
		}
	}()
	matched, err := processor.Match(ctx, scope)
	if err != nil {
		return failedProcessorResult(processor.Name()), ProcessorOutcomeMatchError, err
	}
	if !matched {
		return ProcessorResult{Status: domain.EnrichStatusSkipped, Value: domain.JSONObject{}}, ProcessorOutcomeCompleted, nil
	}
	result, err = processor.Process(ctx, scope)
	if err != nil {
		return failedProcessorResult(processor.Name()), ProcessorOutcomeProcessError, err
	}
	return result, outcome, nil
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
	for _, diagnostic := range result.Diagnostics {
		if !diagnostic.Code.Valid() {
			return invalidProcessorResult(), false
		}
	}
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
