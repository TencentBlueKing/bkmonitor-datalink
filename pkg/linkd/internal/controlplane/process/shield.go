// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/telemetry"
)

type shieldChecker interface {
	CheckShield(context.Context, string, string) error
}

// runShieldChecks 独立扫描持久化 Alert，不依赖新 Event 或 Redis 到期索引。
// 每页十六条、四路执行、单项十秒；行失败不阻塞后续行，下一轮仍能发现未解除或待补齐的意图。
func runShieldChecks(ctx context.Context, cfg config.Config, checker shieldChecker, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) (runErr error) {
	runtime, err := storeassembly.OpenExisting(ctx, *cfg.Storage, 6)
	if err != nil {
		return err
	}
	defer storeassembly.JoinCloseError(&runErr, runtime)
	work, ok := runtime.Repository.(store.ShieldWorkStore)
	if !ok {
		return fmt.Errorf("repository does not support shield work")
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	after := store.ShieldWorkCursor{}
	for ctx.Err() == nil {
		started := time.Now()
		registry.Begin("shield-check", "", "")
		call, cancel := context.WithTimeout(ctx, 45*time.Second)
		next, count, failed, err := checkShieldPage(call, work, checker, after, started)
		cancel()
		code := ""
		if err != nil {
			code = "shield_check_failed"
			if ctx.Err() == nil {
				logger.WarnContext(ctx, "shield check failed", "error_code", code, "failed", failed)
			}
		}
		registry.Finish(ctx, "shield-check", "", "", time.Since(started), count, failed, code)
		if ctx.Err() == nil {
			metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskShieldCheck).RunFinished(ctx, time.Since(started), err == nil)
		}
		after = next
		if err == nil && next.TenantID != "" {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

func checkShieldPage(ctx context.Context, work store.ShieldWorkStore, checker shieldChecker, after store.ShieldWorkCursor, at time.Time) (store.ShieldWorkCursor, int, int, error) {
	page, err := work.ListShieldWork(ctx, after, at, 16)
	if err != nil {
		return after, 0, 1, err
	}
	if len(page.Alerts) > 16 || (page.Next.TenantID != "" && page.Next.Compare(after) <= 0) {
		return after, 0, 1, fmt.Errorf("invalid shield work page")
	}
	jobs := make(chan store.StoredAlert, len(page.Alerts))
	for _, item := range page.Alerts {
		jobs <- item
	}
	close(jobs)
	var wg sync.WaitGroup
	var failed atomic.Int64
	for range min(4, len(page.Alerts)) {
		wg.Go(func() {
			for item := range jobs {
				if ctx.Err() != nil {
					failed.Add(1)
					continue
				}
				if err := item.Alert.Validate(); err != nil || item.Version.IsZero() {
					failed.Add(1)
					continue
				}
				if err := checker.CheckShield(ctx, item.Alert.BKTenantID, item.Alert.AlertID); err != nil {
					failed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	n := int(failed.Load())
	if n > 0 {
		err = errors.New("shield row checks failed")
	}
	return page.Next, len(page.Alerts), n, err
}
