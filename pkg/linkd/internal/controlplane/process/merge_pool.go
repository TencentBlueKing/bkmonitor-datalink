// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package controlplaneprocess

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"linkd/internal/controlplane/taskstate"
	"linkd/internal/lifecycle/scheduler"
	mergeflow "linkd/internal/merge"
	"linkd/internal/store"
	"linkd/internal/telemetry"
)

// runMergeBatchPool 只在有待处理批次和空闲名额时启动 goroutine；不扫描中间存储，也不预启空 Worker。
func runMergeBatchPool(ctx context.Context, pool *mergeflow.MemberPool, concurrency int,
	execute func(context.Context, mergeflow.MemberBatch) (mergeflow.MemberBatch, error),
	complete func(context.Context, mergeflow.MemberBatch) error,
	logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) error {
	ctx, cancel := context.WithCancel(ctx)
	ctx = store.WithBatchPhaseObserver(ctx, metrics.ObserveMergeBatchPhase)
	active := make(chan struct{}, concurrency)
	var running sync.WaitGroup
	defer func() { cancel(); running.Wait() }()
	observer := metrics.ControlPlaneTaskObserver(telemetry.ControlPlaneTaskMergeBatches)
	observer.SetActive(ctx, true)
	defer observer.SetActive(context.WithoutCancel(ctx), false)
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for ctx.Err() == nil {
		// 1. 每个名额对应一个实际批次；慢批不会阻止其他空闲名额继续执行。
		for _, batch := range pool.Take(cap(active)-len(active), time.Now()) {
			active <- struct{}{}
			running.Go(func() {
				defer func() { <-active; pool.Signal() }()
				started := time.Now()
				registry.Begin(string(telemetry.ControlPlaneTaskMergeBatches), "", "")
				call, stop := context.WithTimeout(ctx, 10*time.Second)
				result, err := execute(call, batch)
				stop()
				stageReady := pool.Finish(result, err, time.Now())
				store.ObserveBatchPhase(ctx, "pool.executed", time.Since(started))
				failed, code := 0, ""
				if err != nil && !mergeflow.RetryCanDefer(err) && ctx.Err() == nil {
					failed, code = 1, "merge_batch_failed"
					logger.WarnContext(ctx, "merge member batch failed", "error", err, "bk_tenant_id", batch.Facts.TenantID, "window_id", batch.Facts.WindowID)
				}
				registry.Finish(ctx, string(telemetry.ControlPlaneTaskMergeBatches), "", "", time.Since(started), 1, failed, code)
				observer.RunFinished(ctx, time.Since(started), failed == 0)
				// 2. 收齐结果立即推进关系/父准入，不查询完成状态；阶段提交失败由原恢复循环接续。
				if stageReady && complete != nil {
					if err := complete(ctx, result); err != nil && ctx.Err() == nil {
						logger.WarnContext(ctx, "advance completed merge batch failed", "error", err, "bk_tenant_id", batch.Facts.TenantID, "window_id", batch.Facts.WindowID)
					}
				}
			})
		}
		// 3. 新批次与名额释放直接唤醒，定时器仅负责失败批次退避后的重试。
		select {
		case <-ctx.Done():
			return nil
		case <-pool.Wake():
		case <-tick.C:
		}
	}
	return nil
}

func advanceMergeCompletion(ctx context.Context, b mergeflow.MemberBatch, engine *mergeflow.Executor, runner *mergeJobRunner) error {
	job := mergeJob{tenant: b.Facts.TenantID, window: b.Facts.WindowID, batch: func(call context.Context) (bool, error) {
		if b.Phase == "unlink" {
			return engine.CheckRelationBatch(call, b.Facts.TenantID, b.Facts.ID, time.Now().UTC())
		}
		return engine.StepDecisionBatch(call, b.Facts.TenantID, b.Facts.ID, time.Now().UTC())
	}}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for call.Err() == nil {
		more, err := runner.runBatch(call, job)
		if errors.Is(err, scheduler.ErrLockBusy) {
			select {
			case <-call.Done():
				return call.Err()
			case <-time.After(20 * time.Millisecond):
			}
			continue
		}
		if err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
	return call.Err()
}
