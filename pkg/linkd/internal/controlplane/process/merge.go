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
	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/lifecycle/mailbox"
	lifecycleprocess "linkd/internal/lifecycle/process"
	"linkd/internal/lifecycle/scheduler"
	mergeflow "linkd/internal/merge"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/redisclient"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
	storeassembly "linkd/internal/store/assembly"
	"linkd/internal/taskgroup"
	"linkd/internal/telemetry"
)

type mergeJob struct {
	tenant, window string
	run            func(context.Context) error
	batch          func(context.Context) (bool, error)
}

type mergeJobRunner struct {
	slots  chan struct{}
	locker scheduler.Locker
	logger *slog.Logger
}

// 自动扫描与显式请求共用配置的执行名额和按租户/窗口隔离的租约；不同控制面实例也不能交叉裁决、建联或释放。
// 单项十秒小于三十秒租期；关系项可有界推进多个步骤，所有下游 I/O 共享该截止时间，释放失败仍报告结果不确定。
func (r *mergeJobRunner) run(ctx context.Context, job mergeJob) (err error) {
	key, err := domain.MergeDecisionID(job.tenant, job.window)
	if err != nil || job.run == nil {
		return policy.ErrInvalid
	}
	select {
	case r.slots <- struct{}{}:
		defer func() { <-r.slots }()
	case <-ctx.Done():
		return ctx.Err()
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	started := time.Now()
	lease, err := r.locker.Acquire(call, key)
	store.ObserveBatchPhase(ctx, "pool.lease_acquire", time.Since(started))
	if err != nil {
		return err
	}
	defer func() {
		release, stop := context.WithTimeout(context.WithoutCancel(call), 2*time.Second)
		defer stop()
		err = errors.Join(err, r.locker.Release(release, lease))
	}()
	return job.run(call)
}

// runBatch 只在实际推进且窗口租约成功释放后允许续跑；显式请求仍走 run。
func (r *mergeJobRunner) runBatch(ctx context.Context, job mergeJob) (bool, error) {
	if job.batch == nil {
		return false, r.run(ctx, job)
	}
	var more bool
	err := r.run(ctx, mergeJob{tenant: job.tenant, window: job.window, run: func(call context.Context) error {
		var err error
		more, err = job.batch(call)
		if err != nil {
			return err
		}
		return call.Err()
	}})
	return more && err == nil, err
}

// runMergeJobs 返回首次尝试前就耗尽页预算的窗口，供扫描循环续跑本页。
// 已执行、锁忙或明确失败的窗口交原扫描重试，不能因续页反复占用后排窗口的名额。
func runMergeJobs(ctx context.Context, runner *mergeJobRunner, jobs []mergeJob) (int, bool, []mergeJob, error) {
	if len(jobs) > store.MergeWorkBatchSize {
		return 1, false, nil, policy.ErrInvalid
	}
	var failed atomic.Int64
	var progressed atomic.Bool
	var failures []error
	// 1. 本页全部关系先各执行一批，避免单个大关系抢占同页其他关系。
	pending := jobs
	firstPass := true
	for len(pending) > 0 {
		if err := ctx.Err(); err != nil {
			var unstarted []mergeJob
			if firstPass {
				unstarted = pending
			}
			return int(failed.Load()), progressed.Load(), unstarted, mergePageError(failures, err)
		}
		queue := make(chan int, len(pending))
		for i := range pending {
			queue <- i
		}
		close(queue)
		unstarted := make([]bool, len(pending))
		var next []mergeJob
		var mu sync.Mutex
		var wg sync.WaitGroup
		for range min(cap(runner.slots), len(pending)) {
			wg.Go(func() {
				for i := range queue {
					if ctx.Err() != nil {
						unstarted[i] = true
						continue
					}
					job := pending[i]
					attempt, started := job, false
					if job.batch != nil {
						attempt.batch = func(call context.Context) (bool, error) {
							started = true
							return job.batch(call)
						}
					} else if job.run != nil {
						attempt.run = func(call context.Context) error {
							started = true
							return job.run(call)
						}
					}
					more, err := runner.runBatch(ctx, attempt)
					// 取全局名额或窗口租约时也可能耗尽页预算，此时仍未尝试业务步骤。
					if !started && ctx.Err() != nil {
						unstarted[i] = true
					}
					if err == nil {
						if more {
							progressed.Store(true)
							mu.Lock()
							next = append(next, job)
							mu.Unlock()
						}
						continue
					}
					// 三种扫描和其他控制面可能同时发现同一窗口；锁忙是正常延后，未执行项仍留在持久化扫描中。
					if mergeflow.RetryCanDefer(err) {
						continue
					}
					failed.Add(1)
					mu.Lock()
					failures = append(failures, err)
					mu.Unlock()
					if ctx.Err() == nil && runner.logger != nil {
						runner.logger.WarnContext(ctx, "merge control step failed", "module", "controlplane", "operation", "merge_step", "error", err, "bk_tenant_id", job.tenant, "window_id", job.window, "error_code", mergeStepCode(err))
					}
				}
			})
		}
		wg.Wait()
		if err := ctx.Err(); err != nil {
			var remaining []mergeJob
			if firstPass {
				for i, waiting := range unstarted {
					if waiting {
						remaining = append(remaining, pending[i])
					}
				}
			}
			return int(failed.Load()), progressed.Load(), remaining, mergePageError(failures, err)
		}
		// 2. 只轮转有进展且无错误的原关系；每批重取租约，共享外层45秒页预算。
		// 首轮已尝试完所有窗口，后续批预算耗尽仍交原扫描，不能阻塞新页。
		firstPass = false
		pending = next
	}
	if err := ctx.Err(); err != nil {
		return int(failed.Load()), progressed.Load(), nil, mergePageError(failures, err)
	}
	return int(failed.Load()), progressed.Load(), nil, errors.Join(failures...)
}

// mergePageError 保留真实失败，只避免把已经返回的同一context错误再添加一次。
func mergePageError(failures []error, contextErr error) error {
	err := errors.Join(failures...)
	if errors.Is(err, contextErr) {
		return err
	}
	return errors.Join(err, contextErr)
}

// mergePageCanContinue 只将持续推进后的页预算耗尽视为让出；混合的依赖/释放失败仍完整退避。
// 沿错误树检查所有原因，避免 errors.Is(deadline) 把 errors.Join 中的真实失败一并吞掉。
func mergePageCanContinue(parent, page context.Context, progressed bool, err error) bool {
	if parent.Err() != nil || !progressed {
		return false
	}
	if err == nil {
		// 成员批次把页尾一秒留给进度CAS，可能在真正deadline前主动停止。
		// 未完成项仍在持久work集合，继续原游标即可；完成页最多多一次空扫描。
		deadline, bounded := page.Deadline()
		return bounded && page.Err() == nil && time.Until(deadline) <= time.Second
	}
	if page.Err() != context.DeadlineExceeded {
		return false
	}
	var deadlineOnly func(error) bool
	deadlineOnly = func(err error) bool {
		if joined, ok := err.(interface{ Unwrap() []error }); ok {
			var cause error
			for _, next := range joined.Unwrap() {
				if next == nil {
					continue
				}
				// 多原因无法区分操作与归还窗口租约，双deadline也必须保守退避。
				if cause != nil {
					return false
				}
				cause = next
			}
			return cause != nil && deadlineOnly(cause)
		}
		if cause := errors.Unwrap(err); cause != nil {
			return deadlineOnly(cause)
		}
		return errors.Is(err, context.DeadlineExceeded)
	}
	return deadlineOnly(err)
}

func mergeStepCode(err error) string {
	switch {
	case mergeflow.RetryCanDefer(err):
		return "window_busy"
	case errors.Is(err, context.DeadlineExceeded):
		return "step_timeout"
	case errors.Is(err, context.Canceled):
		return "step_cancelled"
	case errors.Is(err, policy.ErrAccess):
		return "scope_mismatch"
	case errors.Is(err, store.ErrNotFound), errors.Is(err, policy.ErrNotFound):
		return "record_missing"
	case errors.Is(err, store.ErrVersionConflict), errors.Is(err, policy.ErrConflict):
		return "version_conflict"
	case errors.Is(err, store.ErrInvalidTransition), errors.Is(err, store.ErrInvalidArgument), errors.Is(err, policy.ErrInvalid):
		return "invalid_state"
	default:
		return "dependency_failed"
	}
}

type mergePageLoader func(context.Context) ([]mergeJob, bool, error)

func runMergeLoop(ctx context.Context, id telemetry.ControlPlaneTask, interval time.Duration, load mergePageLoader, runner *mergeJobRunner, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	observer := metrics.ControlPlaneTaskObserver(id)
	observer.SetActive(ctx, true)
	defer observer.SetActive(context.WithoutCancel(ctx), false)
	var pending []mergeJob
	var more bool
	for ctx.Err() == nil {
		started := time.Now()
		registry.Begin(string(id), "", "")
		call, cancel := context.WithTimeout(ctx, 45*time.Second)
		// loader 已推进存储游标，先续跑本页未开始的窗口，再读取下一页。
		// 最多保留一页短时工作；退出后由原持久扫描重建。
		jobs := pending
		var err error
		if len(jobs) == 0 {
			jobs, more, err = load(call)
		}
		pending = nil
		failed := 0
		progressed := false
		if err == nil {
			failed, progressed, pending, err = runMergeJobs(call, runner, jobs)
		} else {
			failed = 1
		}
		continuePage := mergePageCanContinue(ctx, call, progressed, err)
		cancel()
		code := ""
		if err != nil {
			code = "merge_check_failed"
			if ctx.Err() == nil {
				logger.WarnContext(ctx, "merge control page failed", "module", "controlplane", "operation", "merge_page", "error", err, "task", string(id), "error_code", code, "failed", failed)
			}
		}
		registry.Finish(ctx, string(id), "", "", time.Since(started), len(jobs), failed, code)
		if ctx.Err() == nil {
			observer.RunFinished(ctx, time.Since(started), err == nil)
		}
		// 持续推进的页只因预算耗尽时，沿已有游标继续；批间租约已释放，不额外等待扫描周期。
		if (err == nil && more) || continuePage {
			continue
		}
		// 长页可能已经错过 ticker 的到期点；完成后重新计时，失败/无进展仍完整退避。
		ticker.Reset(interval)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
	return nil
}

func runMergeTasks(ctx context.Context, cfg config.Config, sources *eventsource.Service, policies *policy.Service, journal *mergeflow.Journal, retries *mergeflow.RetryController, resources *policyruntime.Runtime, severity *runtimeconfig.Severity, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime, shared *lifecycleprocess.AlertProcessorResources) (runErr error) {
	runtime, err := storeassembly.OpenExisting(ctx, *cfg.Storage, 8)
	if err != nil {
		return err
	}
	defer storeassembly.JoinCloseError(&runErr, runtime)
	observed := metrics.ObserveRepository(runtime.Repository)
	work, ok := observed.(store.MergeWorkStore)
	if !ok {
		return fmt.Errorf("repository does not support merge work")
	}
	current := observed.GetAlert
	if reader, ok := observed.(store.LifecycleAlertStore); ok {
		current = reader.GetAlertCurrent
	}
	currentBatch := observed.GetAlerts
	if reader, ok := observed.(store.CurrentAlertBatchStore); ok {
		currentBatch = reader.GetAlertsCurrent
	}
	event := observed.GetEvent
	options := cfg.Storage.Redis.ClientOptions()
	options.ContextTimeoutEnabled = true
	options.PoolSize = 8
	client, err := redisclient.New(options)
	if err != nil {
		return err
	}
	defer func() { runErr = errors.Join(runErr, client.Close()) }()
	state, err := redisstate.New(client, cfg.Dispatch.WithDefaults().Deployment)
	if err != nil {
		return err
	}
	lc := cfg.Lifecycle.ForSource(cfg.Dispatch.WithDefaults().Deployment, domain.BuiltinMergeEventSourceID)
	inbox, err := mailbox.NewStore(client, lc.MailboxConfig())
	if err != nil {
		return err
	}
	lockConfig := lc.SchedulerConfig()
	lockConfig.LockKeyPrefix += ":merge-window"
	lockConfig.LockTTL = max(lockConfig.LockTTL, 30*time.Second)
	locker, err := scheduler.NewRedisLocker(client, lockConfig)
	if err != nil {
		return err
	}
	budget := cfg.MergeControlSettings()
	runner := &mergeJobRunner{slots: make(chan struct{}, budget.Concurrency), locker: locker, logger: logger}
	state.SetObserver(metrics)
	observations := policyruntime.NewObservations(state, metrics)
	defer observations.Close()
	catalog := resources.Catalog
	if catalog == nil {
		catalog = policy.NewCatalog(policies)
	}
	loader := &policyruntime.Suppressor{Observations: observations, Releases: policies, Catalog: catalog, Targets: resources.Dependencies}
	operations := lifecycleprocess.NewMergeOperator(cfg, sources, journal, severity, logger, metrics, loader)
	operations.UseResources(shared)
	engine := &mergeflow.Executor{
		Batch:   budget,
		Journal: journal, Publisher: &mergeflow.Publisher{Journal: journal, Events: observed, Mailboxes: inbox},
		Judge:   &policyruntime.MergeJudge{Loader: loader, Windows: state, CurrentAlert: current, CurrentAlerts: currentBatch},
		Windows: state, Policies: policies, CurrentAlert: current, Event: event, Severity: severity.SeveritySnapshot,
		CurrentAlerts: currentBatch,
		Operations:    operations,
		Source: func(ctx context.Context) (eventsource.Release, error) {
			record, err := sources.Get(ctx, domain.BuiltinMergeEventSourceID)
			if err != nil {
				return eventsource.Release{}, err
			}
			if record.Published < 1 {
				return eventsource.Release{}, policy.ErrUnavailable
			}
			return sources.GetRelease(ctx, record.ID, record.Published)
		},
	}
	engine.Members = mergeflow.NewMemberPool()
	var waitingAfter store.MergeWorkCursor
	waitingScan := &mergeWaitingScan{due: state}
	waiting := func(ctx context.Context) ([]mergeJob, bool, error) {
		page, err := work.ListMergeWork(ctx, waitingAfter, store.MergeWorkBatchSize)
		if err != nil {
			return nil, false, err
		}
		round := waitingScan.round
		items, err := waitingScan.selectWork(ctx, page, time.Now().UTC())
		if err != nil {
			return nil, false, err
		}
		jobs, err := mergeWaitingJobs(items, round, engine.CheckWorkBatch)
		if err != nil {
			return nil, false, err
		}
		waitingAfter = page.Next
		return jobs, page.Next.TenantID != "", nil
	}
	decisionAfter := ""
	decisions := func(ctx context.Context) ([]mergeJob, bool, error) {
		page, err := journal.ListWork(ctx, decisionAfter, 16)
		if err != nil {
			return nil, false, err
		}
		jobs := make([]mergeJob, 0, len(page.Decisions))
		for _, d := range page.Decisions {
			jobs = append(jobs, mergeJob{tenant: d.TenantID, window: d.WindowID, batch: func(ctx context.Context) (bool, error) {
				return engine.StepDecisionBatch(ctx, d.TenantID, d.ID, time.Now().UTC())
			}})
		}
		decisionAfter = page.Next
		return jobs, page.Next != "", nil
	}
	relationAfter := ""
	relations := func(ctx context.Context) ([]mergeJob, bool, error) {
		page, err := journal.ListRelationWork(ctx, relationAfter, 16)
		if err != nil {
			return nil, false, err
		}
		jobs := make([]mergeJob, 0, len(page.Relations))
		for _, r := range page.Relations {
			jobs = append(jobs, mergeJob{tenant: r.TenantID, window: r.WindowID, batch: func(ctx context.Context) (bool, error) {
				return engine.CheckRelationBatch(ctx, r.TenantID, r.ID, time.Now().UTC())
			}})
		}
		relationAfter = page.Next
		return jobs, page.Next != "", nil
	}
	tasks := []taskgroup.Task{
		{Name: "merge-batches", Run: func(ctx context.Context) error {
			return runMergeBatchPool(ctx, engine.Members, budget.Concurrency, engine.ProcessMemberBatch,
				func(ctx context.Context, b mergeflow.MemberBatch) error {
					return advanceMergeCompletion(ctx, b, engine, runner)
				}, logger, registry, metrics)
		}},
		{Name: "merge-requests", Run: func(ctx context.Context) error {
			return runMergeRequests(ctx, journal, mergeRequestRunner{controller: retries, windows: runner, engine: engine}, logger, registry, metrics)
		}},

		{Name: "merge-judge", Run: func(ctx context.Context) error {
			return runMergeLoop(ctx, telemetry.ControlPlaneTaskMergeJudge, time.Second, waiting, runner, logger, registry, metrics)
		}},
		{Name: "merge-decisions", Run: func(ctx context.Context) error {
			return runMergeLoop(ctx, telemetry.ControlPlaneTaskMergeDecisions, time.Second, decisions, runner, logger, registry, metrics)
		}},
		{Name: "merge-relations", Run: func(ctx context.Context) error {
			return runMergeLoop(ctx, telemetry.ControlPlaneTaskMergeRelations, 30*time.Second, relations, runner, logger, registry, metrics)
		}},
	}
	for i := range tasks {
		tasks[i] = registry.Wrap(tasks[i].Name, tasks[i])
	}
	return taskgroup.Run(ctx, tasks)
}
