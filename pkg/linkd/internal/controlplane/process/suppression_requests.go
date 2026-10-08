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
	"linkd/internal/suppressioncheck"
	"linkd/internal/telemetry"
)

type suppressionRequestWork interface {
	Work(context.Context, string, int) (suppressioncheck.Page, error)
}

type suppressionRequestExecutor interface {
	Execute(context.Context, suppressioncheck.Request) error
}

type loggedSuppressionExecutor struct {
	executor suppressionRequestExecutor
	logger   *slog.Logger
}

func (l loggedSuppressionExecutor) Execute(ctx context.Context, r suppressioncheck.Request) error {
	err := l.executor.Execute(ctx, r)
	if err != nil && ctx.Err() == nil && !suppressioncheck.CanDefer(err) {
		l.logger.WarnContext(ctx, "suppression request execution failed", "bk_tenant_id", r.Command.TenantID, "kind", r.Command.Kind, "window_id", r.Command.WindowID, "request_id", r.ID, "error_code", "suppression_execution_failed")
	}
	return err
}

// 每页 16 条、四路执行，取消时保留原游标重读本页；重复已完成项只精确确认，不重做删除。
func checkSuppressionRequestsPage(ctx context.Context, work suppressionRequestWork, executor suppressionRequestExecutor, after string) (string, int, int, error) {
	page, err := work.Work(ctx, after, 16)
	if err != nil {
		return after, 0, 1, err
	}
	if len(page.Items) > 16 {
		return after, 0, 1, errors.New("invalid suppression requests page")
	}
	last := after
	for _, r := range page.Items {
		if r.Validate() != nil || r.State != "pending" || r.Cursor() <= last {
			return after, 0, 1, errors.New("invalid suppression request scope/order")
		}
		last = r.Cursor()
	}
	if page.Next != "" && (page.Next != last || page.Next <= after) {
		return after, 0, 1, errors.New("invalid suppression request cursor")
	}
	jobs := make(chan suppressioncheck.Request, len(page.Items))
	for _, r := range page.Items {
		jobs <- r
	}
	close(jobs)
	var wg sync.WaitGroup
	var failures, attempted atomic.Int64
	for range min(4, len(page.Items)) {
		wg.Go(func() {
			for r := range jobs {
				if ctx.Err() != nil {
					return
				}
				attempted.Add(1)
				if err := executor.Execute(ctx, r); err != nil && !suppressioncheck.CanDefer(err) {
					failures.Add(1)
				}
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return after, int(attempted.Load()), int(failures.Load()), ctx.Err()
	}
	if failures.Load() > 0 {
		err = errors.New("suppression request executions failed")
	}
	return page.Next, int(attempted.Load()), int(failures.Load()), err
}

func runSuppressionRequests(ctx context.Context, work suppressionRequestWork, executor suppressionRequestExecutor, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) error {
	executor = loggedSuppressionExecutor{executor: executor, logger: logger}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	after := ""
	for ctx.Err() == nil {
		start := time.Now()
		registry.Begin("suppression-requests", "", "")
		call, cancel := context.WithTimeout(ctx, 90*time.Second)
		next, n, failed, err := checkSuppressionRequestsPage(call, work, executor, after)
		cancel()
		code := ""
		if err != nil {
			code = "suppression_requests_failed"
			if ctx.Err() == nil {
				logger.WarnContext(ctx, "suppression requests failed", "error_code", code, "failed", failed)
			}
		}
		registry.Finish(ctx, "suppression-requests", "", "", time.Since(start), n, failed, code)
		if ctx.Err() == nil {
			metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskSuppressionRequests).RunFinished(ctx, time.Since(start), err == nil)
		}
		after = next
		if err == nil && next != "" {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
	return nil
}
