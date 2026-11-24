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
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"linkd/internal/domain"
	"linkd/internal/enrich/view"
	"linkd/internal/processingerror"
)

// DefaultAlertContent 用于文案不可用且来源内容为空的合法触发事件，保持确定性且不补造检测事实。
const DefaultAlertContent = "告警已触发，详细内容暂不可用。"

const maxAlertContentBytes = 1 << 20

// ContentBuilder 为匹配本次 Event 的候选 Alert 生成展示文案，不改变事件判定或执行外部写操作。
type ContentBuilder interface {
	BuildContent(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error)
}

// ContentDiagnostic 描述文案降级；不携带来源内容、panic 值或依赖错误文本。
type ContentDiagnostic struct {
	// ReasonCode 仅包含有界的错误码，非结构化错误统一为 builder_error。
	ReasonCode string `json:"reason_code"`
	// ContentMode 标明使用来源原文或固定文案，对应 source_fallback/default_fallback。
	ContentMode string `json:"content_mode"`
}

// ContentResult 同时返回可冻结的文案和可选诊断，供创建与只读预览共用。
type ContentResult struct {
	// Content 可冻结到 Alert；自动文案已校验，显式提交的自定义空串保持原义。
	Content string
	// Diagnostic 在正常生成时为 nil，降级时说明原因和最终文案来源。
	Diagnostic *ContentDiagnostic
}

// BuildAlertContent 将任何生成错误、panic 或非法输出降级为来源原文；原文为空时
// 使用固定文案。输入身份、原始内容合法性和调用 Context 属于处理边界，仍返回错误。
// 显式提交的自定义文案优先，允许空串；控制面传输故障仍保留重试。
// 仅向 Builder 传独立副本，降级不会改写 Event 或借用其他版本的策略事实。
func BuildAlertContent(ctx context.Context, builder ContentBuilder, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (ContentResult, error) {
	if ctx == nil {
		return ContentResult{}, fmt.Errorf("content context is required")
	}
	if err := ctx.Err(); err != nil {
		return ContentResult{}, err
	}
	if err := event.ValidateIdentity(); err != nil {
		return ContentResult{}, err
	}
	if err := domain.ValidateEvaluations(event.Evaluations); err != nil {
		return ContentResult{}, err
	}
	if event.BKTenantID != opening.BKTenantID || event.EventSourceID != opening.EventSourceID || event.EventSourceVersion != opening.EventSourceVersion || event.EventID != opening.TriggerEventID || event.Fingerprint != opening.Fingerprint || evaluation.Severity != opening.Severity || evaluation.Action != domain.EventActionTriggered {
		return ContentResult{}, fmt.Errorf("content opening identity mismatch")
	}
	selected := false
	for _, candidate := range event.Evaluations {
		if candidate == evaluation {
			selected = true
			break
		}
	}
	if !selected {
		return ContentResult{}, fmt.Errorf("content opening evaluation mismatch")
	}
	if len(event.Content) > maxAlertContentBytes {
		return ContentResult{}, fmt.Errorf("event content is invalid")
	}
	// 自定义丰富已明确提交的 content 不交给自动描述重写，也不把显式空串当成缺省。
	if text, found, err := view.CustomText(opening.Enrich, "content"); err != nil || found {
		return ContentResult{Content: text}, err
	}
	content, reason, err := callContentBuilder(ctx, builder, event, evaluation, opening)
	if err != nil {
		return ContentResult{}, err
	}
	// 整个 Worker 被取消时不得冻结兜底或推进 mailbox；调度器保留本条事件重试。
	// 依赖自行返回的 timeout/canceled、但调用 Context 仍有效时只属于文案失败。
	if err := ctx.Err(); err != nil {
		return ContentResult{}, err
	}
	if reason == "" {
		switch {
		case len(content) > maxAlertContentBytes || !utf8.ValidString(content):
			reason = "content_invalid"
		case strings.TrimSpace(content) == "":
			reason = "content_empty"
		default:
			return ContentResult{Content: content}, nil
		}
	}
	mode := "source_fallback"
	content = event.Content
	if strings.TrimSpace(content) == "" || !utf8.ValidString(content) {
		content, mode = DefaultAlertContent, "default_fallback"
	}
	return ContentResult{Content: content, Diagnostic: &ContentDiagnostic{ReasonCode: reason, ContentMode: mode}}, nil
}

func callContentBuilder(ctx context.Context, builder ContentBuilder, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (content, reason string, controlErr error) {
	defer func() {
		if recover() != nil {
			content, reason = "", "builder_panic"
		}
	}()
	if builder == nil {
		return "", "builder_unavailable", nil
	}
	content, err := builder.BuildContent(ctx, event.Clone(), evaluation, opening.Clone())
	if err != nil {
		// 控制面连接失败是运行时依赖不可用，冻结 fallback 会使故障期间的事件丢失重试机会。
		if errors.Is(err, processingerror.ErrControlPlaneUnavailable) {
			return "", "", err
		}
		return "", contentFailureReason(err), nil
	}
	return content, "", nil
}

func contentFailureReason(err error) (reason string) {
	reason = "builder_error"
	// 诊断接口本身也不能把合法事件变成失败；禁止记录任意 error.Error() 文本。
	defer func() { _ = recover() }()
	var failure interface{ PermanentContentFailure() string }
	if !errors.As(err, &failure) {
		return reason
	}
	code := failure.PermanentContentFailure()
	if code == "" || len(code) > 128 {
		return reason
	}
	for _, char := range code {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' {
			return reason
		}
	}
	return code
}
