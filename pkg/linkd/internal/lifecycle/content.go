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

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/view"
)

// AlertContentBuilder 为尚未持久化的 Alert 生成初始 content；持续触发直接采用 Event.content。
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

// WithAlertContentBuilder 指定创建内容的入口；nil 由构造函数拒绝。
func WithAlertContentBuilder(builder AlertContentBuilder) ProcessorOption {
	return func(p *Processor) { p.contentBuilder = builder }
}

func (p *Processor) buildNewAlertContent(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (content string, err error) {
	defer p.measureStep(phasePlan, "content")()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if text, found, err := view.CustomText(opening.Enrich, "content"); err != nil || found {
		return text, err
	}
	// panic 不包含敏感 payload，也不能变成来源文本回退或普通 Enrich 降级。
	defer func() {
		if recover() != nil {
			content, err = "", &contentFailure{code: "builder_panic"}
		}
	}()
	content, err = p.contentBuilder.BuildContent(ctx, event.Clone(), evaluation, opening.Clone())
	// 描述转换失败只影响展示，保留来源原文，不阻断告警创建。
	fallbackReason := enrich.SourceContentFallbackReason(err)
	if err != nil {
		content = event.Content
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(content) > 1<<20 {
		return "", &contentFailure{code: "opening_alert_invalid"}
	}
	if fallbackReason != "" {
		p.logger.WarnContext(ctx, "alert content uses source text after description failure", "module", "lifecycle", "operation", "build_content", "error", err, "bk_tenant_id", event.BKTenantID,
			"event_source_id", event.EventSourceID, "event_source_version", event.EventSourceVersion,
			"event_id", event.EventID, "reason_code", fallbackReason, "content_mode", "source_fallback")
	}
	return content, nil
}

type contentFailure struct{ code string }

func (e *contentFailure) Error() string { return "build alert content: " + e.code }

func (e *contentFailure) PermanentContentFailure() string { return e.code }
