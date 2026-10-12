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

	"linkd/internal/domain"
	"linkd/internal/enrich/custom"
)

// AssignmentResult 保持独立 Processor 调试入口的补丁协议，整条链内部使用 Go 值。
func (s *Scope) AssignmentResult(status domain.EnrichStatus, assignments []domain.EnrichAssignment) (ProcessorResult, error) {
	result := ProcessorResult{Status: status, RuntimePatches: assignments}
	if !s.running {
		result.Patches = make([]domain.EnrichPatch, 0, len(assignments))
		for _, assignment := range assignments {
			patch, err := assignment.Patch()
			if err != nil {
				return ProcessorResult{}, err
			}
			result.Patches = append(result.Patches, patch)
		}
	}
	return result, nil
}

// ExecuteRules 在本等级共享状态上运行自定义规则，主链只提交结果、不再次应用补丁。
func (s *Scope) ExecuteRules(ctx context.Context, program *custom.Program) (ProcessorResult, error) {
	state, err := s.EvaluationState()
	if err != nil {
		return ProcessorResult{}, err
	}
	runtime, err := program.ExecuteEvaluation(ctx, state, s.original.BKTenantID, s.RuleSources())
	if err != nil {
		return ProcessorResult{}, err
	}
	result, err := s.AssignmentResult(runtime.Status, runtime.Patches)
	if err != nil {
		return ProcessorResult{}, err
	}
	result.applied = true
	result.Trace = make([]Trace, len(runtime.Trace))
	for i, t := range runtime.Trace {
		result.Trace[i] = Trace{RuleID: t.RuleID, OperationID: t.OperationID, Status: t.Status, Code: t.Code, Matches: t.Matches, DurationMilliseconds: t.DurationMilliseconds}
	}
	return result, nil
}

func (s *Scope) rollback() {
	if s.state != nil {
		s.state.Rollback(0)
	}
}

func (s *Scope) commit() error {
	if s.state != nil {
		return s.state.Commit()
	}
	return nil
}
