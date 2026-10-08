// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package actiondelivery

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"linkd/internal/store"
	"linkd/internal/taskgroup"
)

// RunnerPhase 为独立补扫与发送循环的固定身份，不使用租户或来源作为指标标签。
type RunnerPhase string

const (
	// PhaseEnqueue 从 Alert 的持久动作意图补齐入队。
	PhaseEnqueue RunnerPhase = "action-enqueue"
	// PhaseDeliver 从动作任务推进投影等待、发送、重试或过期跳过。
	PhaseDeliver RunnerPhase = "action-delivery"
)

// Valid 限制运行器及观测端可接受的固定阶段。
func (p RunnerPhase) Valid() bool { return p == PhaseEnqueue || p == PhaseDeliver }

// WorkOutcome 是本轮观察结果，不是唯一动作数，也不表示业务处置执行完成。
type WorkOutcome string

const (
	// OutcomeEnqueued 表示当前意图已确认全部入队，也可包含已由其他执行者完成的项。
	OutcomeEnqueued WorkOutcome = "enqueued"
	// OutcomeAccepted 表示返回任务已有持久受理确认，不表示本轮新增受理或处置执行完成。
	OutcomeAccepted WorkOutcome = "accepted"
	// OutcomeSkipped 表示返回任务已按较新终态跳过。
	OutcomeSkipped WorkOutcome = "skipped"
	// OutcomeWaitingProjection 表示任务仍等待投影可见。
	OutcomeWaitingProjection WorkOutcome = "waiting_projection"
	// OutcomeBlocked 表示前序失败动作仍占据顺序屏障。
	OutcomeBlocked WorkOutcome = "blocked"
	// OutcomeRetrying 表示一次临时失败已持久安排退避。
	OutcomeRetrying WorkOutcome = "retrying"
	// OutcomeDeferred 表示锁忙、未到执行时间或索引尚未收敛。
	OutcomeDeferred WorkOutcome = "deferred"
	// OutcomeCapacity 表示新任务预算已满，原意图继续保留。
	OutcomeCapacity WorkOutcome = "capacity"
	// OutcomeFailed 表示本次永久失败或基础设施错误。
	OutcomeFailed WorkOutcome = "failed"
	// OutcomeUnstarted 表示取消或共享名额超时前尚未执行的项。
	OutcomeUnstarted WorkOutcome = "unstarted"
)

// Valid 拒绝把任意依赖错误或业务身份用作结果标签。
func (o WorkOutcome) Valid() bool {
	switch o {
	case OutcomeEnqueued, OutcomeAccepted, OutcomeSkipped, OutcomeWaitingProjection, OutcomeBlocked, OutcomeRetrying, OutcomeDeferred, OutcomeCapacity, OutcomeFailed, OutcomeUnstarted:
		return true
	}
	return false
}

// WorkFailure 仅含已验证的定位身份及固定原因，不保存原始错误或载荷。
type WorkFailure struct {
	// TenantID 为本页已验证租户，仅供日志定位，不作为指标标签。
	TenantID string
	// AlertID 为输入项关联的 Alert 身份。
	AlertID string
	// TaskID 仅发送阶段有值；若推进的是同目标前序任务，则使用其已验证身份。
	TaskID string
	// Code 是固定失败分类，不得放入底层错误正文。
	Code string
}

// RoundResult 描述一页实际观察；同任务重复扫描可能再次计数，不能当成 HTTP 请求数。
type RoundResult struct {
	// Scanned 包含本页未开始执行的项，最多 16。
	Scanned int
	// Visited 是本轮已判断执行/延后的项数，不等于连续完成前缀。
	Visited int
	// Outcomes 是互斥分类，总数等于 Scanned；扫描/校验失败没有业务项。
	Outcomes map[WorkOutcome]int
	// OldestObservedAge 只表示本页已观察待办的最大年龄，不是全局积压年龄。
	OldestObservedAge time.Duration
	// Unconfirmed 是本页返回状态仍保留此前结果不确定标记的项数。
	Unconfirmed int
	// Failures 最多保留四个安全定位样本，完整任务仍由仓储保存。
	Failures []WorkFailure
	// ErrorCode 只允许 scan_failed/invalid_page/item_failed/cancelled/round_timeout 或空。
	ErrorCode string
	// ObservedAt 为扫描页的观察时间；失败/非法页为空。
	ObservedAt time.Time
	// Duration 为本页墙钟耗时，不参与业务身份。
	Duration time.Duration
}

// RunnerObserver 必须并发安全，不得执行远端 I/O；观测失败不能改变业务结果。
type RunnerObserver interface {
	SetRunning(context.Context, RunnerPhase, bool)
	RoundStarted(context.Context, RunnerPhase)
	RoundFinished(context.Context, RunnerPhase, RoundResult)
}

// IntentProducer 必须在正式 fingerprint lease 内实时重读、补齐原意图并返回确认后的 Alert。
// 不允许直接使用扫描快照写任务，或在这里重跑 Enrich/策略；成功必须证明当前意图已全部入队。
type IntentProducer interface {
	FinishActionDelivery(context.Context, string, string) (store.StoredAlert, error)
}

// Deliverer 返回实际推进的任务，可能是请求目标的更早未结清版本；失败不能吞成成功。
type Deliverer interface {
	Deliver(context.Context, string, string) (StoredTask, error)
}

// WorkReader 仅暴露有界内部任务扫描，不向运行器提供修改进度的旁路。
type WorkReader interface {
	List(context.Context, Query) ([]StoredTask, error)
}

// Runner 独立补扫动作意图和自动任务，两个循环共享四个执行名额，不创建或更换目标绑定。
type Runner struct {
	work      store.ActionWorkStore
	tasks     WorkReader
	producer  IntentProducer
	deliverer Deliverer
	observer  RunnerObserver
	now       func() time.Time
	slots     chan struct{}
	running   atomic.Bool
}

// NewRunner 注入全部业务和 I/O 端口；构造不启动 goroutine，也不隐式连接 KAC。
func NewRunner(work store.ActionWorkStore, tasks WorkReader, producer IntentProducer, deliverer Deliverer, observer RunnerObserver, now func() time.Time) (*Runner, error) {
	if work == nil || tasks == nil || producer == nil || deliverer == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Runner{work: work, tasks: tasks, producer: producer, deliverer: deliverer, observer: observer, now: now, slots: make(chan struct{}, 4)}, nil
}

// Run 每页最多 16 项，页面执行上下文九十秒，扫描最多五秒，单项二十秒含等待共享名额。
// 取消后仍等待端口的有界租约清理，不把执行上下文期限当成无需清理的强制退出。
// 满页持续前进；单项失败不饿死后面的记录，扫描失败保留游标。取消等待已启动项退出后返回。
func (r *Runner) Run(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if !r.running.CompareAndSwap(false, true) {
		return ErrBusy
	}
	defer r.running.Store(false)
	return taskgroup.Run(ctx, []taskgroup.Task{{Name: string(PhaseEnqueue), Run: func(ctx context.Context) error {
		after := store.ActionWorkCursor{}
		return r.loop(ctx, PhaseEnqueue, 5*time.Second, func(call context.Context) (bool, RoundResult) {
			var result RoundResult
			after, result = r.enqueuePage(call, after)
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
		call, cancel := context.WithTimeout(ctx, 90*time.Second)
		more, result := step(call)
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

func (r *Runner) enqueuePage(ctx context.Context, after store.ActionWorkCursor) (store.ActionWorkCursor, RoundResult) {
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	page, err := r.work.ListActionWork(call, after, 16)
	cancel()
	if err != nil {
		return after, failedScan(ctx)
	}
	checked, err := store.ActionWorkPageFromAlerts(page.Items, after, 16)
	if err != nil || checked.Next != page.Next {
		return after, RoundResult{ErrorCode: "invalid_page"}
	}
	now := r.now()
	if now.IsZero() {
		return after, RoundResult{ErrorCode: "invalid_page"}
	}
	scopes := make([]string, len(page.Items))
	var oldest time.Duration
	for i, row := range page.Items {
		scopes[i] = row.Alert.BKTenantID
		oldest = max(oldest, now.Sub(row.Alert.UpdateAt))
	}
	result, prefix := r.runItems(ctx, scopes, func(ctx context.Context, i int) itemResult {
		input := page.Items[i].Alert
		row, err := r.producer.FinishActionDelivery(ctx, input.BKTenantID, input.AlertID)
		result := itemResult{outcome: OutcomeEnqueued, failure: WorkFailure{TenantID: input.BKTenantID, AlertID: input.AlertID}}
		if err != nil {
			return result.withError(err)
		}
		a := row.Alert
		if row.Version.IsZero() || a.Validate() != nil || a.BKTenantID != input.BKTenantID || a.AlertID != input.AlertID || a.EventSourceID != input.EventSourceID || a.Fingerprint != input.Fingerprint || a.Revision < input.Revision || a.ActionPending != nil {
			return result.invalid()
		}
		return result
	})
	result.OldestObservedAge = max(0, oldest)
	result.ObservedAt = now.Round(0).UTC()
	if prefix < len(page.Items) {
		if prefix == 0 {
			return after, result
		}
		a := page.Items[prefix-1].Alert
		return store.ActionWorkCursor{TenantID: a.BKTenantID, AlertID: a.AlertID}, result
	}
	return page.Next, result
}

func (r *Runner) deliverPage(ctx context.Context, after string) (string, RoundResult) {
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	page, err := r.tasks.List(call, Query{WorkOnly: true, After: after, Limit: 16})
	cancel()
	if err != nil {
		return after, failedScan(ctx)
	}
	if len(page) > 16 {
		return after, RoundResult{ErrorCode: "invalid_page"}
	}
	previous := after
	now := r.now()
	if now.IsZero() {
		return after, RoundResult{ErrorCode: "invalid_page"}
	}
	scopes := make([]string, len(page))
	var oldest time.Duration
	for i, row := range page {
		t := row.Task
		if row.Version == "" || t.Validate() != nil || !t.HasWork() || t.ID <= previous {
			return after, RoundResult{ErrorCode: "invalid_page"}
		}
		previous = t.ID
		scopes[i] = digest("linkd:action-runner-order:v1", t.Request.TenantID, t.Request.AlertID, t.Request.TargetID)
		oldest = max(oldest, now.Sub(t.CreatedAt))
	}
	result, prefix := r.runItems(ctx, scopes, func(ctx context.Context, i int) itemResult {
		input := page[i]
		q, p := input.Task.Request, input.Task.Progress
		result := itemResult{failure: WorkFailure{TenantID: q.TenantID, AlertID: q.AlertID, TaskID: input.Task.ID}}
		if p.DueAt != nil && now.Before(*p.DueAt) || p.LeaseUntil != nil && now.Before(*p.LeaseUntil) {
			result.outcome = OutcomeDeferred
			return result
		}
		row, err := r.deliverer.Deliver(ctx, q.TenantID, input.Task.ID)
		if err != nil && !CanDefer(err) {
			return result.withError(err)
		}
		if CanDefer(err) && row.Version == "" {
			return result.withError(err)
		}
		t := row.Task
		if row.Version == "" || t.Validate() != nil || t.Request.TenantID != q.TenantID || t.Request.AlertID != q.AlertID || t.Request.TargetID != q.TargetID || t.SourceID != input.Task.SourceID || t.Request.Revision > q.Revision {
			return result.invalid()
		}
		result.unconfirmed = t.Progress.PreviousUnconfirmed
		result.failure.TaskID = t.ID
		if CanDefer(err) {
			result.outcome = OutcomeDeferred
			if errors.Is(err, ErrCapacity) {
				result.outcome = OutcomeCapacity
			} else if t.Progress.State == "failed" {
				result.outcome = OutcomeBlocked
			} else if t.Progress.State == "waiting_projection" {
				result.outcome = OutcomeWaitingProjection
			}
			return result
		}
		switch t.Progress.State {
		case "succeeded":
			result.outcome = OutcomeAccepted
		case "skipped":
			result.outcome = OutcomeSkipped
		case "retry":
			result.outcome = OutcomeRetrying
		case "waiting_projection":
			result.outcome = OutcomeWaitingProjection
		case "failed":
			result.outcome = OutcomeFailed
			result.failure.Code = t.Progress.ErrorCode
		default:
			return result.invalid()
		}
		return result
	})
	result.OldestObservedAge = max(0, oldest)
	result.ObservedAt = now.Round(0).UTC()
	if prefix < len(page) {
		if prefix == 0 {
			return after, result
		}
		return page[prefix-1].Task.ID, result
	}
	if len(page) == 16 {
		return previous, result
	}
	return "", result
}

type itemResult struct {
	outcome     WorkOutcome
	unconfirmed bool
	failure     WorkFailure
}

// 仅父执行上下文决定退出分类；单次扫描自身超时仍是扫描失败，后续轮次可以重试。
func failedScan(ctx context.Context) RoundResult {
	code := "scan_failed"
	if errors.Is(ctx.Err(), context.Canceled) {
		code = "cancelled"
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		code = "round_timeout"
	}
	return RoundResult{ErrorCode: code}
}

func (v itemResult) invalid() itemResult {
	v.outcome = OutcomeFailed
	v.failure.Code = "invalid_result"
	return v
}

func (v itemResult) withError(err error) itemResult {
	v.outcome = OutcomeFailed
	v.failure.Code = "execution_failed"
	if CanDefer(err) {
		v.outcome = OutcomeDeferred
		v.failure.Code = ""
		if errors.Is(err, ErrCapacity) {
			v.outcome = OutcomeCapacity
		}
	} else if errors.Is(err, context.Canceled) {
		v.failure.Code = "cancelled"
	} else if errors.Is(err, context.DeadlineExceeded) {
		v.failure.Code = "timeout"
	}
	return v
}

// 同租户入队、同 Alert/目标发送在一页中各自串行；不同组最多四路，并跨两个循环共用名额。
// 取消只能推进已尝试的连续前缀；后来完成的组可重读，不能越过尚未启动的前面项。
func (r *Runner) runItems(ctx context.Context, scopes []string, execute func(context.Context, int) itemResult) (RoundResult, int) {
	groups := [][]int{}
	indexes := map[string]int{}
	for i, key := range scopes {
		g, ok := indexes[key]
		if !ok {
			g = len(groups)
			indexes[key] = g
			groups = append(groups, nil)
		}
		groups[g] = append(groups[g], i)
	}
	jobs := make(chan []int, len(groups))
	for _, group := range groups {
		jobs <- group
	}
	close(jobs)
	values := make([]itemResult, len(scopes))
	visited := make([]bool, len(scopes))
	var wg sync.WaitGroup
	for range min(4, len(groups)) {
		wg.Go(func() {
			for group := range jobs {
				for _, i := range group {
					if ctx.Err() != nil {
						continue
					}
					call, cancel := context.WithTimeout(ctx, 20*time.Second)
					select {
					case r.slots <- struct{}{}:
						if call.Err() == nil {
							visited[i] = true
							values[i] = execute(call, i)
						}
						<-r.slots
					case <-call.Done():
					}
					cancel()
				}
			}
		})
	}
	wg.Wait()
	result := RoundResult{Scanned: len(scopes), Outcomes: map[WorkOutcome]int{}}
	prefix := 0
	for prefix < len(visited) && visited[prefix] {
		prefix++
	}
	for i, v := range values {
		if !visited[i] {
			result.Outcomes[OutcomeUnstarted]++
			continue
		}
		result.Visited++
		result.Outcomes[v.outcome]++
		if v.unconfirmed {
			result.Unconfirmed++
		}
		if v.outcome == OutcomeFailed && len(result.Failures) < 4 {
			result.Failures = append(result.Failures, v.failure)
		}
	}
	if result.Outcomes[OutcomeFailed] > 0 || result.Outcomes[OutcomeUnstarted] > 0 {
		result.ErrorCode = "item_failed"
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		result.ErrorCode = "cancelled"
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		result.ErrorCode = "round_timeout"
	}
	return result, prefix
}
