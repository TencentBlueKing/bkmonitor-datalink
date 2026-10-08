// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package projection

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"linkd/internal/store"
	"linkd/internal/taskgroup"
)

// RunnerPhase 是控制面可以直接用于任务与指标名称的固定枚举。
type RunnerPhase string

const (
	// PhaseProduce 表示从 Alert 未确认水位补建任务。
	PhaseProduce RunnerPhase = "projection-producer"
	// PhaseDeliver 表示推进持久任务，包括补本地 ACK。
	PhaseDeliver RunnerPhase = "projection-delivery"
)

// Valid 限定运行器与观测共用的两个固定阶段，拒绝外部输入扩展指标标签。
func (p RunnerPhase) Valid() bool { return p == PhaseProduce || p == PhaseDeliver }

// RoundResult 记录一页的真实工作；延后不表示同步完成。
type RoundResult struct {
	// Scanned 是仓储本次返回的待办数，预算最多 16。
	Scanned int
	// Advanced 是成功建/复用任务或已完整确认的项数，不是新发送 HTTP 的次数。
	Advanced int
	// Deferred 包括锁忙、退避、未完成尝试以及容量不足。
	Deferred int
	// CapacityDeferred 是 Deferred 中由于新任务预算不足的数量。
	CapacityDeferred int
	// Failed 包括永久失败任务、基础设施错误和本轮取消的项数。
	Failed int
	// ErrorCode 为 scan_failed、invalid_page、item_failed、cancelled、round_timeout 或空，不回显依赖异常。
	ErrorCode string
	// ObservedAt 是完整合法页面读取后的观察时间；扫描失败保持零值，不伪造空页面。
	ObservedAt time.Time
	// OldestObservedAge 是该页当前业务 update_at 或任务首次 created_at 的最大年龄，不是全局最老待办。
	OldestObservedAge time.Duration
	// Duration 是本轮墙钟耗时，与业务版本无关。
	Duration time.Duration
}

// RunnerObserver 必须并发安全且不执行阻塞 I/O；两个循环同步汇报开始与结束。
// 所有高基数业务身份留在任务中，不通过这些指标标签暴露。
type RunnerObserver interface {
	// SetRunning 跟踪每个独立循环的生命周期，退出后必须清除执行中标记。
	SetRunning(context.Context, RunnerPhase, bool)
	// RoundStarted 在本轮读取前通知，不表示已取得任何业务租约。
	RoundStarted(context.Context, RunnerPhase)
	// RoundFinished 包含失败和取消轮次；调用者可结合 ctx 区分进程退出。
	RoundFinished(context.Context, RunnerPhase, RoundResult)
}

// Executor 由运行器消费，生产与发送的身份、租约、容量和错误持久化属于用例层。
type Executor interface {
	Produce(context.Context, string, string, string) (StoredTask, error)
	Deliver(context.Context, string, string) (StoredTask, error)
}

// Runner 独立运行补扫与投递循环；仅发现持久水位和任务，不创建目标绑定或重建策略 Redis 状态。
type Runner struct {
	work     store.ProjectionWorkStore
	tasks    Store
	executor Executor
	observer RunnerObserver
	now      func() time.Time
	running  atomic.Bool
}

// NewRunner 注入同部署的业务仓储、任务仓储和有界执行器；不启动 goroutine。
func NewRunner(work store.ProjectionWorkStore, tasks Store, executor Executor, observer RunnerObserver, now func() time.Time) (*Runner, error) {
	if work == nil || tasks == nil || executor == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Runner{work: work, tasks: tasks, executor: executor, observer: observer, now: now}, nil
}

// Run 每页至多 16 项、四路调用，整页至多一分钟。业务行失败仍推进游标，扫描失败保留原游标。
// 同一实例仅允许一个 Run；取消会等本轮 goroutine 退出，重启从头补扫已有持久事实。
func (r *Runner) Run(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if !r.running.CompareAndSwap(false, true) {
		return ErrBusy
	}
	defer r.running.Store(false)
	return taskgroup.Run(ctx, []taskgroup.Task{{Name: string(PhaseProduce), Run: func(ctx context.Context) error {
		after := store.ProjectionWorkCursor{}
		return r.loop(ctx, PhaseProduce, 5*time.Second, func(call context.Context) (bool, RoundResult) {
			var result RoundResult
			after, result = r.producePage(call, after)
			return after.TenantID != "", result
		})
	}}, {Name: string(PhaseDeliver), Run: func(ctx context.Context) error {
		after := ""
		return r.loop(ctx, PhaseDeliver, time.Second, func(call context.Context) (bool, RoundResult) {
			var result RoundResult
			after, result = r.deliverPage(call, after)
			return after != "", result
		})
	}}})
}

func (r *Runner) loop(ctx context.Context, phase RunnerPhase, interval time.Duration, step func(context.Context) (bool, RoundResult)) error {
	if r.observer != nil {
		r.observer.SetRunning(ctx, phase, true)
		defer r.observer.SetRunning(context.WithoutCancel(ctx), phase, false)
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		started := time.Now()
		if r.observer != nil {
			r.observer.RoundStarted(ctx, phase)
		}
		call, cancel := context.WithTimeout(ctx, time.Minute)
		more, result := step(call)
		if ctx.Err() != nil {
			result.ErrorCode = "cancelled"
		} else if call.Err() != nil {
			result.ErrorCode = "round_timeout"
		}
		cancel()
		result.Duration = time.Since(started)
		if r.observer != nil {
			r.observer.RoundFinished(ctx, phase, result)
		}
		if more && result.ErrorCode == "" {
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

func (r *Runner) producePage(ctx context.Context, after store.ProjectionWorkCursor) (store.ProjectionWorkCursor, RoundResult) {
	scan, cancel := context.WithTimeout(ctx, 5*time.Second)
	page, err := r.work.ListProjectionWork(scan, after, 16)
	cancel()
	if err != nil {
		return after, RoundResult{Failed: 1, ErrorCode: "scan_failed"}
	}
	if len(page.Items) > 16 || page.Next.Validate(16) != nil || (page.Next.TenantID != "" && page.Next.Compare(after) <= 0) {
		return after, RoundResult{Failed: 1, ErrorCode: "invalid_page"}
	}
	previous := after
	for _, v := range page.Items {
		a := v.Alert.Alert
		key := store.ProjectionWorkCursor{TenantID: a.BKTenantID, AlertID: a.AlertID, TargetID: v.TargetID}
		target, exists := a.Projection.Targets[v.TargetID]
		if key.Validate(16) != nil || key.Compare(previous) <= 0 || v.Alert.Version.IsZero() || a.Validate() != nil || !exists || target.RequiredRevision <= target.SyncedRevision {
			return after, RoundResult{Failed: 1, ErrorCode: "invalid_page"}
		}
		previous = key
	}
	if page.Next.TenantID != "" && page.Next != previous {
		return after, RoundResult{Failed: 1, ErrorCode: "invalid_page"}
	}
	scopes := make([]string, len(page.Items))
	at, age := r.now().UTC(), time.Duration(0)
	for i, v := range page.Items {
		scopes[i] = v.Alert.Alert.BKTenantID
		age = max(age, at.Sub(v.Alert.Alert.UpdateAt))
	}
	result, visited := runProjectionItems(ctx, len(page.Items), scopes, func(i int) (StoredTask, error) {
		v := page.Items[i]
		return r.executor.Produce(ctx, v.Alert.Alert.BKTenantID, v.Alert.Alert.AlertID, v.TargetID)
	}, true)
	result.ObservedAt, result.OldestObservedAge = at, age
	if visited < len(page.Items) {
		if visited == 0 {
			return after, result
		}
		v := page.Items[visited-1]
		return store.ProjectionWorkCursor{TenantID: v.Alert.Alert.BKTenantID, AlertID: v.Alert.Alert.AlertID, TargetID: v.TargetID}, result
	}
	return page.Next, result
}

func (r *Runner) deliverPage(ctx context.Context, after string) (string, RoundResult) {
	scan, cancel := context.WithTimeout(ctx, 5*time.Second)
	page, err := r.tasks.List(scan, Query{WorkOnly: true, After: after, Limit: 16})
	cancel()
	if err != nil {
		return after, RoundResult{Failed: 1, ErrorCode: "scan_failed"}
	}
	if len(page) > 16 {
		return after, RoundResult{Failed: 1, ErrorCode: "invalid_page"}
	}
	previous := after
	for _, v := range page {
		if v.Version == "" || v.Task.Validate() != nil || !v.Task.HasWork() || v.Task.ID <= previous {
			return after, RoundResult{Failed: 1, ErrorCode: "invalid_page"}
		}
		previous = v.Task.ID
	}
	at := r.now()
	if at.IsZero() {
		return after, RoundResult{Failed: 1, ErrorCode: "invalid_page"}
	}
	age := time.Duration(0)
	for _, v := range page {
		age = max(age, at.Sub(v.Task.CreatedAt))
	}
	result, visited := runProjectionItems(ctx, len(page), nil, func(i int) (StoredTask, error) {
		v := page[i]
		p := v.Task.Progress
		if (p.DueAt != nil && at.Before(*p.DueAt)) || (p.LeaseUntil != nil && at.Before(*p.LeaseUntil)) {
			return v, ErrBusy
		}
		return r.executor.Deliver(ctx, v.Task.Request.TenantID, v.Task.ID)
	}, false)
	result.ObservedAt, result.OldestObservedAge = at.UTC(), age
	if visited < len(page) {
		if visited == 0 {
			return after, result
		}
		return page[visited-1].Task.ID, result
	}
	next := ""
	if len(page) == 16 {
		next = previous
	}
	return next, result
}

// 生产器按页内租户串行，防止自己的四个 Worker 争抢同一准入锁；不同租户仍最多四路。
// 超时只推进真正尝试过的连续前缀，较后批次已做的工作可安全重读，未启动项不能被游标越过。
func runProjectionItems(ctx context.Context, count int, scopes []string, execute func(int) (StoredTask, error), produce bool) (RoundResult, int) {
	type batch struct{ start, end int }
	jobs := make(chan batch, count)
	batches := 0
	for i := 0; i < count; {
		end := i + 1
		if produce {
			for end < count && scopes[end] == scopes[i] {
				end++
			}
		}
		jobs <- batch{i, end}
		batches++
		i = end
	}
	close(jobs)
	visited := make([]atomic.Bool, count)
	var wg sync.WaitGroup
	var advanced, deferred, capacity, failed atomic.Int64
	for range min(4, batches) {
		wg.Go(func() {
			for group := range jobs {
				for i := group.start; i < group.end; i++ {
					if ctx.Err() != nil {
						failed.Add(1)
						continue
					}
					visited[i].Store(true)
					result, err := execute(i)
					switch {
					case CanDefer(err):
						deferred.Add(1)
						if errors.Is(err, ErrCapacity) {
							capacity.Add(1)
						}
					case err != nil:
						failed.Add(1)
					case result.Version == "" || result.Task.Validate() != nil:
						failed.Add(1)
					case !produce && result.Task.Progress.State == "failed":
						failed.Add(1)
					case !produce && result.Task.Progress.State != "succeeded":
						deferred.Add(1)
					default:
						advanced.Add(1)
					}
				}
			}
		})
	}
	wg.Wait()
	prefix := 0
	for prefix < count && visited[prefix].Load() {
		prefix++
	}
	result := RoundResult{Scanned: count, Advanced: int(advanced.Load()), Deferred: int(deferred.Load()), CapacityDeferred: int(capacity.Load()), Failed: int(failed.Load())}
	if result.Failed > 0 {
		result.ErrorCode = "item_failed"
	}
	return result, prefix
}

// CanDefer 仅在全部错误原因都是普通锁忙或容量不足时返回 true。
// errors.Join 同时包含释放失败等异常时，调用方必须按失败处理。
func CanDefer(err error) bool {
	if err == nil {
		return false
	}
	if many, ok := err.(interface{ Unwrap() []error }); ok {
		causes := many.Unwrap()
		if len(causes) == 0 {
			return false
		}
		for _, cause := range causes {
			if !CanDefer(cause) {
				return false
			}
		}
		return true
	}
	if one, ok := err.(interface{ Unwrap() error }); ok {
		return CanDefer(one.Unwrap())
	}
	return errors.Is(err, ErrBusy) || errors.Is(err, ErrCapacity)
}
