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
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"linkd/internal/controlplane/taskstate"
	"linkd/internal/shieldcheck"
	"linkd/internal/telemetry"
)

type shieldRequestWork interface {
	Work(context.Context, string, int) (shieldcheck.Page, error)
}

type shieldRequestExecutor interface {
	Execute(context.Context, shieldcheck.Request) error
}

// 显式请求独立排队，但与定时复查共用四个实际执行名额；已完成请求不再进入工作索引。
// 单页失败仍推进游标，避免坏记录饿死后面的请求，下一轮从头重新发现未完成项。
func runShieldRequests(ctx context.Context, work shieldRequestWork, executor shieldRequestExecutor, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	after := ""
	for ctx.Err() == nil {
		started := time.Now()
		registry.Begin("shield-requests", "", "")
		call, cancel := context.WithTimeout(ctx, 90*time.Second)
		next, count, failed, err := checkShieldRequestsPage(call, work, executor, after)
		cancel()
		code := ""
		if err != nil {
			code = "shield_requests_failed"
			if ctx.Err() == nil {
				logger.WarnContext(ctx, "shield requests failed", "error_code", code, "failed", failed)
			}
		}
		registry.Finish(ctx, "shield-requests", "", "", time.Since(started), count, failed, code)
		if ctx.Err() == nil {
			metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskShieldRequests).RunFinished(ctx, time.Since(started), err == nil)
		}
		after = next
		if err == nil && next != "" {
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

func checkShieldRequestsPage(ctx context.Context, work shieldRequestWork, executor shieldRequestExecutor, after string) (string, int, int, error) {
	page, err := work.Work(ctx, after, 16)
	if err != nil {
		return after, 0, 1, err
	}
	if len(page.Items) > 16 || (page.Next != "" && page.Next <= after) {
		return after, 0, 1, errors.New("invalid shield requests page")
	}
	jobs := make(chan shieldcheck.Request, len(page.Items))
	for _, request := range page.Items {
		jobs <- request
	}
	close(jobs)
	var wg sync.WaitGroup
	var failed atomic.Int64
	for range min(4, len(page.Items)) {
		wg.Go(func() {
			for request := range jobs {
				if ctx.Err() != nil || request.Validate() != nil || request.State != "pending" {
					failed.Add(1)
					continue
				}
				if err := executor.Execute(ctx, request); err != nil {
					failed.Add(1)
				}
			}
		})
	}
	wg.Wait()
	n := int(failed.Load())
	if n > 0 {
		err = errors.New("shield request executions failed")
	}
	return page.Next, len(page.Items), n, err
}
