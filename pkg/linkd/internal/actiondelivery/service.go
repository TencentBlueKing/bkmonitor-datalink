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
	"time"

	"linkd/internal/projection"
)

// Resolver 从部署配置解析全局处置入口；来源版本仅用于校验业务溯源，不选择路由。
type Resolver interface {
	ResolveAction(context.Context, string, string, int64, string) (Destination, error)
}

// TargetLocker 必须与投影发送共用同一部署的跨进程锁空间，租期至少 30 秒。
// 成功必须返回 owner-safe 释放函数，释放错误需要向调用者传播。
type TargetLocker interface {
	Acquire(context.Context, string) (func(context.Context) error, error)
}

// Service 提供冻结动作入队与一次有界投递；投递不重新判断业务策略。
// 生产者须从持久化准入意图按同 Alert 版本顺序调用 Record；本包不能发现尚未落库的动作意图。
type Service struct {
	// Recorder 与发送共享四个执行名额；单独使用 Recorder 不需要接收端连接。
	*Recorder
	tasks    Store
	gate     Gate
	resolver Resolver
	sender   Sender
}

// New 注入全部 I/O，不启动任务或修改 Alert；每实例最多四项执行，十秒总预算包含排队。
func New(tasks Store, gate Gate, resolver Resolver, sender Sender, locker TargetLocker, now func() time.Time) (*Service, error) {
	if tasks == nil || gate == nil || resolver == nil || sender == nil || locker == nil || now == nil {
		return nil, ErrInvalid
	}
	recorder, err := NewRecorder(tasks, locker, now)
	if err != nil {
		return nil, err
	}
	return &Service{Recorder: recorder, tasks: tasks, gate: gate, resolver: resolver, sender: sender}, nil
}

// Deliver 优先推进该 Alert/目标最早未终结的持久动作，返回实际处理的任务。
// 失败的前序动作也是顺序屏障；只有可见的较新终态证明可让旧 firing 转为跳过。
// 此顺序仅覆盖已经持久化的动作，生产意图的原子性由业务接入层负责。
// nil error 只表示本次状态已保存，也可能是 retry/failed/skipped；调用方必须检查 Progress。
func (s *Service) Deliver(ctx context.Context, tenant, id string) (result StoredTask, err error) {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-call.Done():
		return StoredTask{}, call.Err()
	}
	initial, e := s.tasks.Get(call, tenant, id)
	if e != nil {
		return StoredTask{}, e
	}
	if !validStored(initial, tenant, id) {
		return StoredTask{}, ErrInvalid
	}
	if !initial.Task.Unsettled() {
		return initial, nil
	}
	key, e := projection.TargetLockKey(tenant, initial.Task.Request.AlertID, initial.Task.Request.TargetID)
	if e != nil {
		return StoredTask{}, ErrInvalid
	}
	release, e := s.locker.Acquire(call, key)
	if e != nil {
		return StoredTask{}, e
	}
	if release == nil {
		return StoredTask{}, ErrInvalid
	}
	defer func() {
		end, stop := context.WithTimeout(context.WithoutCancel(call), 2*time.Second)
		defer stop()
		err = errors.Join(err, release(end))
	}()
	head, e := s.tasks.OldestUnsettled(call, tenant, initial.Task.Request.AlertID, initial.Task.Request.TargetID)
	if errors.Is(e, ErrNotFound) {
		return initial, ErrBusy
	}
	if e != nil {
		return StoredTask{}, e
	}
	if !validStored(head, tenant, head.Task.ID) || head.Task.Request.AlertID != initial.Task.Request.AlertID || head.Task.Request.TargetID != initial.Task.Request.TargetID || head.Task.Request.Revision > initial.Task.Request.Revision {
		return StoredTask{}, ErrInvalid
	}
	current, e := s.tasks.Get(call, tenant, head.Task.ID)
	if e != nil {
		return StoredTask{}, e
	}
	if !validStored(current, tenant, head.Task.ID) || current.Task.Request.AlertID != head.Task.Request.AlertID || current.Task.Request.TargetID != head.Task.Request.TargetID {
		return StoredTask{}, ErrInvalid
	}
	if !current.Task.Unsettled() {
		return current, ErrBusy
	} // ES 工作索引可滞后，不按过期搜索结果发送。
	return s.deliver(call, current)
}

func validStored(s StoredTask, tenant, id string) bool {
	return s.Version != "" && s.Task.Validate() == nil && s.Task.ID == id && s.Task.Request.TenantID == tenant
}

func (s *Service) timeAfter(previous time.Time) time.Time {
	now := s.now().Round(0).UTC()
	if now.Before(previous) {
		return previous
	}
	return now
}

func (s *Service) deliver(ctx context.Context, current StoredTask) (StoredTask, error) {
	p := current.Task.Progress
	now := s.timeAfter(p.UpdatedAt)
	if p.DueAt != nil && now.Before(*p.DueAt) || p.LeaseUntil != nil && now.Before(*p.LeaseUntil) {
		return current, ErrBusy
	}
	proof, e := s.gate.Check(ctx, current.Task.Clone())
	if ctx.Err() != nil {
		return current, ctx.Err()
	}
	if e == nil && ValidateProjection(current.Task.Request, proof) != nil {
		e = ErrInvalidReceipt
	}
	if e == nil && stale(current.Task.Request, proof) {
		return s.skip(ctx, current, proof)
	}
	if p.State == "failed" {
		return current, ErrBusy
	}
	if p.Attempts == MaxAttempts {
		return s.fail(ctx, current, Failure{Code: "attempt_interrupted"})
	}
	if CanDefer(e) {
		return s.waitProjection(ctx, current)
	}
	var confirmed *projection.Receipt
	if e == nil {
		confirmed = &proof
	}
	claimed, saveErr := s.reserve(ctx, current, confirmed)
	if saveErr != nil {
		return StoredTask{}, saveErr
	}
	if e != nil {
		return s.fail(ctx, claimed, Failure{Code: "projection_unavailable", Retryable: !errors.Is(e, ErrInvalidReceipt)})
	}
	destination, e := s.resolver.ResolveAction(ctx, claimed.Task.Request.TenantID, claimed.Task.SourceID, claimed.Task.SourceVersion, claimed.Task.Request.TargetID)
	if ctx.Err() != nil {
		return claimed, ctx.Err()
	}
	if e != nil {
		return s.fail(ctx, claimed, Failure{Code: "target_unavailable", Retryable: true})
	}
	receipt, e := s.sender.Send(ctx, destination, claimed.Task.Request.Clone())
	if ctx.Err() != nil {
		return claimed, ctx.Err()
	}
	if e != nil {
		failure := Failure{Code: "transport_failed", Retryable: true}
		var typed Failure
		if errors.As(e, &typed) && validFailureCode(typed.Code) {
			failure = typed
		}
		return s.fail(ctx, claimed, failure)
	}
	if receipt.ValidateFor(claimed.Task.Request) != nil {
		return s.fail(ctx, claimed, Failure{Code: "response_invalid"})
	}
	next := claimed.Task.Clone()
	next.Progress.State = "succeeded"
	next.Progress.Receipt = &receipt
	next.Progress.LeaseUntil = nil
	next.Progress.UpdatedAt = s.timeAfter(next.Progress.UpdatedAt)
	return s.tasks.Put(ctx, next, claimed.Version)
}

func markUnconfirmed(next *Task, current Task) {
	if current.Progress.State == "sending" && current.Progress.Projection != nil {
		next.Progress.PreviousUnconfirmed = true
	}
}

func (s *Service) waitProjection(ctx context.Context, current StoredTask) (StoredTask, error) {
	next := current.Task.Clone()
	markUnconfirmed(&next, current.Task)
	now := s.timeAfter(next.Progress.UpdatedAt)
	due := now.Add(time.Second)
	next.Progress.State = "waiting_projection"
	next.Progress.ErrorCode = "projection_pending"
	next.Progress.UpdatedAt = now
	next.Progress.DueAt = &due
	next.Progress.LeaseUntil = nil
	next.Progress.Projection = nil
	saved, e := s.tasks.Put(ctx, next, current.Version)
	if e != nil {
		return saved, e
	}
	return saved, ErrBusy
}

func (s *Service) reserve(ctx context.Context, current StoredTask, proof *projection.Receipt) (StoredTask, error) {
	next := current.Task.Clone()
	markUnconfirmed(&next, current.Task)
	now := s.timeAfter(next.Progress.UpdatedAt)
	until := now.Add(30 * time.Second)
	next.Progress.State = "sending"
	next.Progress.Attempts++
	next.Progress.TotalAttempts++
	next.Progress.UpdatedAt = now
	next.Progress.DueAt = nil
	next.Progress.LeaseUntil = &until
	next.Progress.ErrorCode = ""
	next.Progress.Projection = proof
	return s.tasks.Put(ctx, next, current.Version)
}

func (s *Service) skip(ctx context.Context, current StoredTask, proof projection.Receipt) (StoredTask, error) {
	next := current.Task.Clone()
	markUnconfirmed(&next, current.Task)
	next.Progress.State = "skipped"
	next.Progress.ErrorCode = "superseded_by_terminal"
	next.Progress.UpdatedAt = s.timeAfter(next.Progress.UpdatedAt)
	next.Progress.DueAt = nil
	next.Progress.LeaseUntil = nil
	next.Progress.Projection = &proof
	return s.tasks.Put(ctx, next, current.Version)
}

func (s *Service) fail(ctx context.Context, current StoredTask, f Failure) (StoredTask, error) {
	next := current.Task.Clone()
	if uncertainFailure(f.Code) {
		markUnconfirmed(&next, current.Task)
	}
	now := s.timeAfter(next.Progress.UpdatedAt)
	next.Progress.State = "failed"
	next.Progress.ErrorCode = f.Code
	next.Progress.UpdatedAt = now
	next.Progress.DueAt = nil
	next.Progress.LeaseUntil = nil
	if f.Retryable && next.Progress.Attempts < MaxAttempts {
		next.Progress.State = "retry"
		due := now.Add(min(time.Minute, time.Second*time.Duration(1<<uint(next.Progress.Attempts-1))))
		next.Progress.DueAt = &due
	}
	return s.tasks.Put(ctx, next, current.Version)
}

// CanDefer 仅允许纯等待/预算/锁忙，混合存储或租约释放异常不能被当成普通延后。
func CanDefer(err error) bool {
	if err == nil {
		return false
	}
	if x, ok := err.(interface{ Unwrap() []error }); ok {
		es := x.Unwrap()
		if len(es) == 0 {
			return false
		}
		for _, e := range es {
			if !CanDefer(e) {
				return false
			}
		}
		return true
	}
	if x, ok := err.(interface{ Unwrap() error }); ok {
		return CanDefer(x.Unwrap())
	}
	return errors.Is(err, ErrBusy) || errors.Is(err, ErrCapacity) || errors.Is(err, projection.ErrBusy)
}
