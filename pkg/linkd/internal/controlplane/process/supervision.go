// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplaneprocess

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"linkd/internal/controlplane/taskstate"
	"linkd/internal/processingerror"
	"linkd/internal/taskgroup"
)

const taskRetryInterval = 5 * time.Second

// runControlCall 在实际执行的 goroutine 捕获 panic，复用不回显业务载荷的错误与栈信息。
func runControlCall(run func() error) error {
	_, err := processingerror.Guard(func() (struct{}, error) { return struct{}{}, run() })
	return err
}

// superviseTask 仅重启失败的控制任务；普通任务故障不得传播到调度器和其他管理任务。
// 所有权失效与 API 监听器退出仍由所属 taskgroup 先完成取消和清理，再由外层重建运行时。
func superviseTask(logger *slog.Logger, registry *taskstate.Registry, id string, task taskgroup.Task) taskgroup.Task {
	run := task.Run
	task.Run = func(ctx context.Context) error {
		for ctx.Err() == nil {
			started := time.Now()
			err := runControlCall(func() error { return run(ctx) })
			if ctx.Err() != nil {
				return nil
			}
			code := "task_failed"
			if err == nil {
				err = fmt.Errorf("control plane task stopped unexpectedly")
				code = "task_stopped"
			} else if errors.Is(err, processingerror.ErrPanic) {
				code = "task_panic"
			}
			registry.Finish(ctx, id, "", "", time.Since(started), -1, 1, code)
			logger.ErrorContext(ctx, "control plane task failed; retrying", "module", "controlplane", "operation", "task_run", "task", task.Name, "reason_code", code, "error", err, "retry_interval", taskRetryInterval)
			if !waitForArchiveRetry(ctx, taskRetryInterval) {
				return nil
			}
		}
		return nil
	}
	return task
}

// afterTaskReady 只等待首次成功准备，后续运行错误由各任务独立观测与重试。
func afterTaskReady(ready <-chan struct{}, run func(context.Context) error) func(context.Context) error {
	return func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return nil
		case <-ready:
			return run(ctx)
		}
	}
}
