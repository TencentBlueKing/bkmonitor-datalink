package lifecycle

import (
	"context"
	"fmt"

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
	// panic 不包含敏感 payload，也不能变成来源文本回退或普通 Enrich 降级。
	defer func() {
		if recover() != nil {
			content, err = "", &contentFailure{code: "builder_panic"}
		}
	}()
	content, err = p.contentBuilder.BuildContent(ctx, event.Clone(), evaluation, opening.Clone())
	if err != nil {
		return "", fmt.Errorf("build alert content: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	opening.Content = content
	if _, err := opening.Normalize(); err != nil {
		return "", &contentFailure{code: "opening_alert_invalid"}
	}
	return content, nil
}

type contentFailure struct{ code string }

func (e *contentFailure) Error() string                   { return "build alert content: " + e.code }
func (e *contentFailure) PermanentContentFailure() string { return e.code }
