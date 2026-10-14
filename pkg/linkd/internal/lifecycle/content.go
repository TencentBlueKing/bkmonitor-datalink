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
	"errors"
	"fmt"
	"strings"

	"linkd/internal/domain"
)

// AlertContentBuilder 为尚未持久化的 Alert 生成不可变 content。
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
	// panic 不包含敏感 payload，也不能变成来源文本回退或普通 Enrich 降级。
	defer func() {
		if recover() != nil {
			content, err = "", &contentFailure{code: "builder_panic"}
		}
	}()
	content, err = p.contentBuilder.BuildContent(ctx, event.Clone(), evaluation, opening.Clone())
	// 历史策略发布态已被覆盖时保留触发事件的原文，不用最新策略冒充历史配置。
	// 只有明确缺失允许降级，歧义、身份校验失败和依赖故障仍按原错误处理。
	fallback := false
	if err != nil {
		var failure interface{ PermanentContentFailure() string }
		if !errors.As(err, &failure) || failure.PermanentContentFailure() != "publication_missing" || strings.TrimSpace(event.Content) == "" {
			return "", fmt.Errorf("build alert content: %w", err)
		}
		content, fallback = event.Content, true
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if len(content) > 1<<20 {
		return "", &contentFailure{code: "opening_alert_invalid"}
	}
	if fallback {
		p.logger.WarnContext(ctx, "alert content uses source text after missing publication", "module", "lifecycle", "operation", "build_content", "error", err, "bk_tenant_id", event.BKTenantID,
			"event_source_id", event.EventSourceID, "event_source_version", event.EventSourceVersion,
			"event_id", event.EventID, "reason_code", "publication_missing", "content_mode", "source_fallback")
	}
	return content, nil
}

type contentFailure struct{ code string }

func (e *contentFailure) Error() string { return "build alert content: " + e.code }

func (e *contentFailure) PermanentContentFailure() string { return e.code }
