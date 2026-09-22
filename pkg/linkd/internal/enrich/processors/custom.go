// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/custom"
)

// Rules 将一个 Processor 的多条自定义规则接入共享丰富链。
type Rules struct {
	name    string
	program *custom.Program
}

// NewRules 在来源发布/装配时编译全部规则，无外部副作用。
func NewRules(kind string, config map[string]any) (*Rules, error) {
	p, err := custom.Compile(kind, config)
	if err != nil {
		return nil, err
	}
	return &Rules{name: kind, program: p}, nil
}

// Name 返回稳定的类型名称，同一类型只需配置一次。
func (p *Rules) Name() string { return p.name }

// Match 将具体匹配交给各条规则，Processor 本身始终适用。
func (p *Rules) Match(context.Context, *enrich.Scope) (bool, error) { return true, nil }

// Process 顺序执行规则并返回已提交补丁；不直接修改 Scope 或 Alert。
func (p *Rules) Process(ctx context.Context, scope *enrich.Scope) (enrich.ProcessorResult, error) {
	original, err := domain.AlertDocument(scope.OriginalAlert())
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	current, err := domain.AlertDocument(scope.EffectiveAlert())
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	result, err := p.program.Execute(ctx, original, current, scope.EffectiveAlert().BKTenantID, scope.RuleSources())
	if err != nil {
		return enrich.ProcessorResult{}, err
	}
	trace := make([]enrich.Trace, len(result.Trace))
	for i, t := range result.Trace {
		trace[i] = enrich.Trace{RuleID: t.RuleID, OperationID: t.OperationID, Status: t.Status, Code: t.Code, Matches: t.Matches, DurationMilliseconds: t.DurationMilliseconds}
	}
	return enrich.ProcessorResult{Status: result.Status, Patches: result.Patches, Trace: trace}, nil
}
