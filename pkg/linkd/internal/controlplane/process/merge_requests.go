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
	mergeflow "linkd/internal/merge"
	"linkd/internal/telemetry"
)

type mergeRequestWork interface {
	RetryWork(context.Context, string, int) (mergeflow.RetryPage, error)
}

type mergeRequestExecutor interface {
	Execute(context.Context, mergeflow.RetryRequest) error
}

type loggedMergeExecutor struct {
	executor mergeRequestExecutor
	logger   *slog.Logger
}

func (l loggedMergeExecutor) Execute(ctx context.Context, r mergeflow.RetryRequest) error {
	err := l.executor.Execute(ctx, r)
	if err != nil && ctx.Err() == nil && !mergeflow.RetryCanDefer(err) {
		l.logger.WarnContext(ctx, "merge request execution failed", "bk_tenant_id", r.Command.TenantID, "kind", r.Command.Kind, "target_id", r.Command.TargetID, "window_id", r.WindowID, "request_id", r.ID, "error_code", "merge_execution_failed")
	}
	return err
}

// 每页 16 条、四路执行，取消时保留原游标重读本页；重复已完成项只精确确认，不重复执行业务步骤。
func checkMergeRequestsPage(ctx context.Context, work mergeRequestWork, executor mergeRequestExecutor, after string) (string, int, int, error) {
	page, err := work.RetryWork(ctx, after, 16)
	if err != nil {
		return after, 0, 1, err
	}
	if len(page.Items) > 16 {
		return after, 0, 1, errors.New("invalid merge requests page")
	}
	last := after
	for _, r := range page.Items {
		if r.Validate() != nil || r.State != "pending" || r.Cursor() <= last {
			return after, 0, 1, errors.New("invalid merge request scope/order")
		}
		last = r.Cursor()
	}
	if page.Next != "" && (page.Next != last || page.Next <= after) {
		return after, 0, 1, errors.New("invalid merge request cursor")
	}
	jobs := make(chan mergeflow.RetryRequest, len(page.Items))
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
				if err := executor.Execute(ctx, r); err != nil && !mergeflow.RetryCanDefer(err) {
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
		err = errors.New("merge request executions failed")
	}
	return page.Next, int(attempted.Load()), int(failures.Load()), err
}

func runMergeRequests(ctx context.Context, work mergeRequestWork, executor mergeRequestExecutor, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) error {
	executor = loggedMergeExecutor{executor: executor, logger: logger}
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	after := ""
	for ctx.Err() == nil {
		start := time.Now()
		registry.Begin("merge-requests", "", "")
		call, cancel := context.WithTimeout(ctx, 90*time.Second)
		next, n, failed, err := checkMergeRequestsPage(call, work, executor, after)
		cancel()
		code := ""
		if err != nil {
			code = "merge_requests_failed"
			if ctx.Err() == nil {
				logger.WarnContext(ctx, "merge requests failed", "error_code", code, "failed", failed)
			}
		}
		registry.Finish(ctx, "merge-requests", "", "", time.Since(start), n, failed, code)
		if ctx.Err() == nil {
			metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskMergeRequests).RunFinished(ctx, time.Since(start), err == nil)
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

// 将显式请求接回同一执行器和共享窗口预算，不能另建绕开自动任务租约的执行路径。
type mergeRequestRunner struct {
	controller *mergeflow.RetryController
	windows    *mergeJobRunner
	engine     *mergeflow.Executor
}

func (r mergeRequestRunner) Execute(ctx context.Context, request mergeflow.RetryRequest) error {
	return r.controller.Execute(ctx, request, func(ctx context.Context, tenant, window string, run func(context.Context) error) error {
		return r.windows.run(ctx, mergeJob{tenant: tenant, window: window, run: run})
	}, r.engine)
}
