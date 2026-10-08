package description

import (
	"context"
	"fmt"

	"linkd/internal/domain"
)

// FactsResolver 读取创建阶段的策略和检测事实。它必须核验租户、级别、
// 来源发布版本及触发时策略绑定；当前策略不能自动当作触发时策略。
// 输入是独立副本；返回值不能包含来源 content 的反向解析结果。
type FactsResolver interface {
	Resolve(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (Facts, error)
}

// Builder 连接事实读取与纯渲染，满足 Lifecycle 的 AlertContentBuilder 端口。
type Builder struct{ resolver FactsResolver }

// NewBuilder 拒绝缺失 Resolver，不提供来源文本回退。
func NewBuilder(resolver FactsResolver) (*Builder, error) {
	if resolver == nil {
		return nil, fmt.Errorf("description facts resolver must not be nil")
	}
	return &Builder{resolver: resolver}, nil
}

// BuildContent 只为匹配 opening Event 的新 Alert 生成核心内容。
// Resolver 错误保持原错误链，便于调用方区分依赖重试和确定性失败。
func (b *Builder) BuildContent(ctx context.Context, event domain.Event, evaluation domain.EventEvaluation, opening domain.Alert) (string, error) {
	if ctx == nil {
		return "", invalid("context_missing")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if b == nil || b.resolver == nil {
		return "", invalid("resolver_missing")
	}
	if event.BKTenantID == "" || event.EventID == "" || event.EventSourceID == "" || event.BKTenantID != opening.BKTenantID || event.EventSourceID != opening.EventSourceID || event.EventSourceVersion != opening.EventSourceVersion || event.EventID != opening.TriggerEventID || event.Fingerprint != opening.Fingerprint || evaluation.Severity != opening.Severity || evaluation.Action != domain.EventActionTriggered {
		return "", invalid("opening_identity_invalid")
	}
	found := false
	for _, candidate := range event.Evaluations {
		if candidate == evaluation {
			found = true
			break
		}
	}
	if !found {
		return "", invalid("opening_evaluation_invalid")
	}
	facts, err := b.resolver.Resolve(ctx, event.Clone(), evaluation, opening.Clone())
	if err != nil {
		return "", fmt.Errorf("resolve description facts: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return Render(facts)
}
