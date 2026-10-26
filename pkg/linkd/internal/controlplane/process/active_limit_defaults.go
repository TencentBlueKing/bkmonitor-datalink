// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplaneprocess

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/eventsource"
)

// sourceLimitDefaults 只属于控制面配置发布入口；未来 KAC 发布完整规则后可整体删除。
// 规则身份取分组字段而非名称，避免覆盖调用方已经设置的策略阈值和通知。
func sourceLimitDefaults(spec config.EventSource) config.EventSource {
	if spec.EventSourceID != "built_in_bk" || !spec.Enabled {
		return spec
	}
	if spec.ActiveAlertLimits != nil {
		if !spec.ActiveAlertLimits.IsEnabled() {
			return spec
		}
		for _, r := range spec.ActiveAlertLimits.Rules {
			if slices.Equal(r.GroupBy, []string{"labels.strategy_id"}) {
				return spec
			}
		}
	}
	c := config.ActiveAlertLimits{}.WithDefaults()
	if spec.ActiveAlertLimits != nil {
		c = spec.ActiveAlertLimits.WithDefaults()
	}
	name := "strategy"
	for {
		used := false
		for _, r := range c.Rules {
			if r.Name == name {
				used = true
				break
			}
		}
		if !used {
			break
		}
		name += "-default"
	}
	c.Rules = append(c.Rules, config.ActiveAlertLimitRule{Name: name, GroupBy: []string{"labels.strategy_id"}, MaxActive: 5000, KeyPrefix: "alarmd:open_alerts", Notify: &config.ActiveAlertLimitNotify{Channel: "alarmd:open_alerts:changes", Fields: map[string]string{"bk_tenant_id": "bk_tenant_id", "strategy_id": "labels.strategy_id"}}})
	spec.ActiveAlertLimits = &c
	return spec
}

func runSourceLimitDefaults(ctx context.Context, sources *eventsource.Service, logger *slog.Logger, registry *taskstate.Registry) error {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		started := time.Now()
		if registry != nil {
			registry.Begin("source-limit-defaults", "", "")
		}
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		record, err := sources.Get(call, "built_in_bk")
		if err == nil && record.Pending != nil {
			record, err = sources.Recover(call, record.ID)
		}
		if err == nil && !record.Deleted && record.Spec.Enabled {
			// Apply 自身比较有效配置；重复轮次不增加 Release，也不触发任务重启。
			_, err = sources.Apply(call, record.Spec, record.Revision, false, "linkd-defaults")
		}
		cancel()
		if err != nil && !errors.Is(err, eventsource.ErrNotFound) && !errors.Is(err, eventsource.ErrConflict) && ctx.Err() == nil {
			logger.WarnContext(ctx, "source limit defaults failed", "module", "controlplane", "operation", "source_limit_defaults", "error", err, "event_source_id", "built_in_bk")
		}
		if registry != nil {
			code := ""
			if err != nil && !errors.Is(err, eventsource.ErrNotFound) && !errors.Is(err, eventsource.ErrConflict) {
				code = "defaults_failed"
			}
			registry.Finish(ctx, "source-limit-defaults", "", "", time.Since(started), 0, 0, code)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
