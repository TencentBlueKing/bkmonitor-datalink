// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/store"
)

type closeBatchNotificationsKey struct{}

// 关闭先完成同步投影，再通知后台输出动作，避免同一批通知提前触发重复投影并争抢通道。
// 成功项只发布后续动作，失败项沿用普通通知；通知仍是尽力发布，调用方依照原操作身份重试。
type closeBatchNotifications struct {
	mu      sync.Mutex
	target  lifecycle.AlertChangePublisher
	changes []domain.AlertChange
}

func (p *closeBatchNotifications) use(target lifecycle.AlertChangePublisher) lifecycle.AlertChangePublisher {
	p.mu.Lock()
	defer p.mu.Unlock()
	// 所有来源共用部署级 KAC 通知出口，只保存一个真实 publisher。
	p.target = target
	return p
}

func (p *closeBatchNotifications) Publish(ctx context.Context, change domain.AlertChange) error {
	return p.PublishBatch(ctx, []domain.AlertChange{change})[0]
}

func (p *closeBatchNotifications) PublishBatch(_ context.Context, changes []domain.AlertChange) []error {
	p.mu.Lock()
	p.changes = append(p.changes, changes...)
	p.mu.Unlock()
	return make([]error, len(changes))
}

type projectedChangePublisher interface {
	PublishProjectedChanges(context.Context, []domain.AlertChange) []error
}

func (p *closeBatchNotifications) flush(ctx context.Context, logger *slog.Logger, projectedPublisher projectedChangePublisher, projected map[string]bool) {
	if p.target == nil || len(p.changes) == 0 {
		return
	}
	started := time.Now()
	defer func() { store.ObserveBatchPhase(ctx, "batch.notify", time.Since(started)) }()
	errs := make([]error, len(p.changes))
	ordinary, actions := []domain.AlertChange{}, []domain.AlertChange{}
	ordinaryPositions, actionPositions := []int{}, []int{}
	for i, c := range p.changes {
		if projectedPublisher != nil && projected[c.AlertID] {
			actions = append(actions, c)
			actionPositions = append(actionPositions, i)
		} else {
			ordinary = append(ordinary, c)
			ordinaryPositions = append(ordinaryPositions, i)
		}
	}
	if len(actions) > 0 {
		out := projectedPublisher.PublishProjectedChanges(ctx, actions)
		for j, i := range actionPositions {
			errs[i] = out[j]
		}
	}
	if len(ordinary) > 0 {
		if batch, ok := p.target.(interface {
			PublishBatch(context.Context, []domain.AlertChange) []error
		}); ok {
			out := batch.PublishBatch(ctx, ordinary)
			for j, i := range ordinaryPositions {
				errs[i] = out[j]
			}
		} else {
			for j, c := range ordinary {
				errs[ordinaryPositions[j]] = p.target.Publish(ctx, c)
			}
		}
	}
	for i, err := range errs {
		if err != nil && logger != nil {
			change := p.changes[i]
			logger.WarnContext(ctx, "alert change enqueue failed", "module", "lifecycle", "operation", "publish_change", "error", err, "bk_tenant_id", change.BKTenantID, "alert_id", change.AlertID, "error_code", "queue_publish_failed")
		}
	}
}
