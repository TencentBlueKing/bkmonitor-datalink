// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/enrich"
)

// AlertContentBuilder 为当前触发生成 content；默认仅创建时调用。
// 实现可通过 RebuildContentOnTrigger(string) bool 声明指定来源持续触发时也需要渲染。
// 实现必须无写副作用、可取消且可重试；不得修改来源 Event 或已有 Alert。
type AlertContentBuilder interface {
	BuildContent(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error)
}

// SourceContentBuilder 明确采用来源 content，供无需 bk-monitor 文案的入口使用。
// 配置了 bk-monitor 模式的运行入口必须注入按来源装配的构建器。
type SourceContentBuilder struct{}

// BuildContent 返回来源文本，不执行普通 Enrich。
func (SourceContentBuilder) BuildContent(ctx context.Context, event domain.Event, _ domain.EventEvaluation, _ domain.Alert) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return event.Content, nil
}

// WithAlertContentBuilder 指定内容生成入口；nil 由构造函数拒绝。
func WithAlertContentBuilder(builder AlertContentBuilder) ProcessorOption {
	return func(p *Processor) { p.contentBuilder = builder }
}

// 持续触发只替换 content，不能把渲染所用的本次身份写回首次创建锚点或丰富快照。
// source 模式保留直接采用原文的语义，包括显式空串；描述模式复用创建时的降级和取消规则。
func (p *Processor) buildUpdatedAlertContent(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, current domain.Alert) (string, error) {
	policy, ok := p.contentBuilder.(interface{ RebuildContentOnTrigger(string) bool })
	if !ok || !policy.RebuildContentOnTrigger(event.EventSourceID) {
		return event.Content, nil
	}
	candidate := current.Clone()
	candidate.TriggerEventID, candidate.EventSourceVersion = event.EventID, event.EventSourceVersion
	candidate.Severity, candidate.Content = evaluation.Severity, event.Content
	latest, ok := event.ForSeverity(evaluation.Severity)
	if !ok {
		return "", fmt.Errorf("trigger event has no enrich result for selected severity")
	}
	candidate.EnrichStatus, candidate.Enrich = latest.Status, latest.Data.Clone()
	return p.buildAlertContent(ctx, event, evaluation, candidate)
}

func (p *Processor) buildAlertContent(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (string, error) {
	defer p.measureStep(phasePlan, "content")()
	result, err := enrich.BuildAlertContent(ctx, p.contentBuilder, event, evaluation, opening)
	if err != nil {
		return "", err
	}
	if diagnostic := result.Diagnostic; diagnostic != nil {
		p.logger.WarnContext(ctx, "alert content generation degraded", "module", "lifecycle", "operation", "build_content",
			"bk_tenant_id", event.BKTenantID, "event_source_id", event.EventSourceID, "event_source_version", event.EventSourceVersion,
			"event_id", event.EventID, "alert_id", opening.AlertID, "severity", evaluation.Severity, "reason_code", diagnostic.ReasonCode, "content_mode", diagnostic.ContentMode)
	}
	return result.Content, nil
}
