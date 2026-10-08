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
}

type mergeJobRunner struct {
	slots  chan struct{}
	locker scheduler.Locker
	logger *slog.Logger
}

// 自动扫描与显式请求共用四个执行名额和按租户/窗口隔离的租约；不同控制面实例也不能交叉裁决、建联或释放。
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
	lease, err := r.locker.Acquire(call, key)
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

func runMergeJobs(ctx context.Context, runner *mergeJobRunner, jobs []mergeJob) (int, error) {
	if len(jobs) > 16 {
		return 1, policy.ErrInvalid
	}
	queue := make(chan mergeJob, len(jobs))
	for _, job := range jobs {
		queue <- job
	}
	close(queue)
	var failed atomic.Int64
	var wg sync.WaitGroup
	for range min(4, len(jobs)) {
		wg.Go(func() {
			for job := range queue {
				if err := runner.run(ctx, job); err != nil {
					// 三种扫描和其他控制面可能同时发现同一窗口；锁忙是正常延后，未执行项仍留在持久化扫描中。
					if mergeflow.RetryCanDefer(err) {
						continue
					}
					failed.Add(1)
					if ctx.Err() == nil && runner.logger != nil {
						runner.logger.WarnContext(ctx, "merge control step failed", "bk_tenant_id", job.tenant, "window_id", job.window, "error_code", mergeStepCode(err))
					}
				}
			}
		})
	}
	wg.Wait()
	if failed.Load() > 0 {
		return int(failed.Load()), fmt.Errorf("merge steps failed")
	}
	return 0, nil
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
	for ctx.Err() == nil {
		started := time.Now()
		registry.Begin(string(id), "", "")
		call, cancel := context.WithTimeout(ctx, 45*time.Second)
		jobs, more, err := load(call)
		failed := 0
		if err == nil {
			failed, err = runMergeJobs(call, runner, jobs)
		} else {
			failed = 1
		}
		cancel()
		code := ""
		if err != nil {
			code = "merge_check_failed"
			if ctx.Err() == nil {
				logger.WarnContext(ctx, "merge control page failed", "task", string(id), "error_code", code, "failed", failed)
			}
		}
		registry.Finish(ctx, string(id), "", "", time.Since(started), len(jobs), failed, code)
		if ctx.Err() == nil {
			observer.RunFinished(ctx, time.Since(started), err == nil)
		}
		if err == nil && more {
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

func runMergeTasks(ctx context.Context, cfg config.Config, sources *eventsource.Service, policies *policy.Service, journal *mergeflow.Journal, retries *mergeflow.RetryController, resources *policyruntime.Runtime, severity *runtimeconfig.Severity, logger *slog.Logger, registry *taskstate.Registry, metrics *telemetry.Runtime) (runErr error) {
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
	runner := &mergeJobRunner{slots: make(chan struct{}, 4), locker: locker, logger: logger}
	state.SetObserver(metrics)
	observations := policyruntime.NewObservations(state, metrics)
	defer observations.Close()
	loader := &policyruntime.Suppressor{Observations: observations, Releases: policies, Catalog: policy.NewCatalog(policies), Targets: resources.Targets}
	engine := &mergeflow.Executor{
		Journal: journal, Publisher: &mergeflow.Publisher{Journal: journal, Events: observed, Mailboxes: inbox},
		Judge:   &policyruntime.MergeJudge{Loader: loader, Windows: state, CurrentAlert: current},
		Windows: state, Policies: policies, CurrentAlert: current, Event: event, Severity: severity.SeveritySnapshot,
		Operations: lifecycleprocess.NewMergeOperator(cfg, sources, journal, severity, logger, metrics),
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
	var waitingAfter store.MergeWorkCursor
	waiting := func(ctx context.Context) ([]mergeJob, bool, error) {
		page, err := work.ListMergeWork(ctx, waitingAfter, 16)
		if err != nil {
			return nil, false, err
		}
		jobs := make([]mergeJob, 0, len(page.Items))
		for _, item := range page.Items {
			window := item.WindowID
			if window == "" {
				if item.Alert.Alert.MergeChange == nil {
					return nil, false, policy.ErrInvalid
				}
				window = item.Alert.Alert.MergeChange.WindowID
			}
			jobs = append(jobs, mergeJob{tenant: item.Alert.Alert.BKTenantID, window: window, run: func(ctx context.Context) error { return engine.CheckWork(ctx, item, time.Now().UTC()) }})
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
			jobs = append(jobs, mergeJob{tenant: d.TenantID, window: d.WindowID, run: func(ctx context.Context) error { return engine.StepDecision(ctx, d.TenantID, d.ID, time.Now().UTC()) }})
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
			jobs = append(jobs, mergeJob{tenant: r.TenantID, window: r.WindowID, run: func(ctx context.Context) error { return engine.CheckRelation(ctx, r.TenantID, r.ID, time.Now().UTC()) }})
		}
		relationAfter = page.Next
		return jobs, page.Next != "", nil
	}
	tasks := []taskgroup.Task{
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
