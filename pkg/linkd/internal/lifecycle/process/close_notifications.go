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
// 通知仍是尽力发布；投影或流水失败也尝试发布，调用方依照原操作身份重试。
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

func (p *closeBatchNotifications) flush(ctx context.Context, logger *slog.Logger) {
	if p.target == nil || len(p.changes) == 0 {
		return
	}
	started := time.Now()
	defer func() { store.ObserveBatchPhase(ctx, "batch.notify", time.Since(started)) }()
	errs := make([]error, len(p.changes))
	if batch, ok := p.target.(interface {
		PublishBatch(context.Context, []domain.AlertChange) []error
	}); ok {
		errs = batch.PublishBatch(ctx, p.changes)
	} else {
		for i, change := range p.changes {
			errs[i] = p.target.Publish(ctx, change)
		}
	}
	for i, err := range errs {
		if err != nil && logger != nil {
			change := p.changes[i]
			logger.WarnContext(ctx, "alert change enqueue failed", "module", "lifecycle", "operation", "publish_change", "error", err, "bk_tenant_id", change.BKTenantID, "alert_id", change.AlertID, "error_code", "queue_publish_failed")
		}
	}
}
