// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"

	"linkd/internal/domain"
)

// ActiveAlertCache 只观察已经持久化的成员变化，失败不影响生命周期结果。
type ActiveAlertCache interface {
	ObserveAlert(context.Context, domain.Alert) error
}

// WithActiveAlertCache 注入来源活跃缓存；空实现表示没有配置规则。
func WithActiveAlertCache(c ActiveAlertCache) ProcessorOption {
	return func(p *Processor) { p.activeAlerts = c }
}

func (p *Processor) observeActiveAlert(ctx context.Context, a domain.Alert) {
	if p.activeAlerts == nil {
		return
	}
	// 升级轮换沿用占用；下一次创建的重复 SADD 保留同组成员，不发出先删除再加入的无意义通知。
	if a.EndType == domain.AlertEndTypeSeverityUpgrade {
		return
	}
	if err := p.activeAlerts.ObserveAlert(ctx, a); err != nil {
		p.logger.WarnContext(ctx, "active alert cache update failed", "module", "lifecycle", "operation", "active_alert_cache", "error", err, "bk_tenant_id", a.BKTenantID, "event_source_id", a.EventSourceID, "alert_id", a.AlertID, "fingerprint", a.Fingerprint)
	}
}
