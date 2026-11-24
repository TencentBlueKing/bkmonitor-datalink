// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package controlplaneprocess

import (
	"context"

	"linkd/internal/config"
	"linkd/internal/kaccompat"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
)

func hasKACDelivery(cfg config.Config) bool { return cfg.Lifecycle != nil && cfg.Plugins.KACEnabled() }

type kacAlertReader struct{ store.Repository }

func (r kacAlertReader) GetAlertCloseStates(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
	if reader, ok := r.Repository.(interface {
		GetAlertCloseProjectionStates(context.Context, string, []string) (store.AlertBatch, error)
	}); ok {
		return reader.GetAlertCloseProjectionStates(ctx, tenant, ids)
	}
	if states, ok := r.Repository.(store.AlertCloseStateBatchStore); ok {
		return states.GetAlertCloseStates(ctx, tenant, ids)
	}
	return r.GetAlertsCurrent(ctx, tenant, ids)
}

func (r kacAlertReader) GetAlertsCurrent(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
	if current, ok := r.Repository.(kaccompat.AlertReader); ok {
		return current.GetAlertsCurrent(ctx, tenant, ids)
	}
	return r.GetAlerts(ctx, tenant, ids)
}

func openKACProjectionSender(cfg config.Config, severity *runtimeconfig.Severity, relations kaccompat.Relations) (*kaccompat.Client, error) {
	return kaccompat.Open(*cfg.Plugins.KAC, func(name string) (string, error) {
		if severity.SeveritySnapshot().NativeNames {
			return name, nil
		}
		switch name {
		case "critical":
			return "fatal", nil
		case "info":
			return "remind", nil
		default:
			return name, nil
		}
	}, relations)
}
