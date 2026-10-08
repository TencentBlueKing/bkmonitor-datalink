// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package controlplaneprocess

import (
	"context"
	"log/slog"
	"time"

	"linkd/internal/controlplane/taskstate"
	"linkd/internal/telemetry"
)

// runKACIndexMaintenance 周期检查兼容索引；失败保留运行和任务，错误仅输出固定分类。
func runKACIndexMaintenance(ctx context.Context, client interface{ Maintain(context.Context) error }, registry *taskstate.Registry, metrics *telemetry.Runtime, logger *slog.Logger) error {
	const id = "kac-index-maintenance"
	observation, err := newDeliveryTaskObservation(registry, metrics, id)
	if err != nil {
		return err
	}
	observation.setRunning(ctx, id, true)
	defer observation.setRunning(context.WithoutCancel(ctx), id, false)
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		observation.started(id)
		started := time.Now()
		err = client.Maintain(ctx)
		code := ""
		failed := 0
		if err != nil {
			code = "item_failed"
			failed = 1
			if ctx.Err() == nil {
				logger.WarnContext(ctx, "KAC compatibility index maintenance failed", "error_code", code)
			}
		}
		observation.finished(ctx, id, time.Since(started), 1, failed, code)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}
