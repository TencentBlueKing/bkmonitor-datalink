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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// Resolver 验证租户、业务溯源与全局目标；连接配置不随 EventSource 发布。
type Resolver interface {
	ResolveProjection(context.Context, string, string, int64, string) (Destination, error)
}

// Alerts 使用实时读取和原子 Alert CAS；ACK 不持有业务 fingerprint lease，不推进业务版本。
type Alerts interface {
	GetAlertCurrent(context.Context, string, string) (store.StoredAlert, error)
	CompareAndSetAlert(context.Context, string, string, store.VersionToken, domain.Alert) (store.StoredAlert, error)
}

// TargetLocker 按稳定用途键提供至少 30 秒的跨实例租约；目标投递与租户准入使用不同键。
// 单次用例限制 10 秒，成功必须返回 owner-safe 释放函数；释放不确定不能假报调度成功。
type TargetLocker interface {
	Acquire(context.Context, string) (func(context.Context) error, error)
}

// Service 编排持久任务、单次外部确认和本地水位；同一 Alert/目标串行，每实例最多四项执行。
type Service struct {
	tasks    Store
	alerts   Alerts
	resolver Resolver
	sender   Sender
	now      func() time.Time
	locker   TargetLocker
	slots    chan struct{}
}

// New 注入全部 I/O 和时钟，不创建 goroutine；生产/人工重试取得租户准入租约，投递取得 Alert/目标租约。
// 三种用例共享四个执行名额，各自的十秒总预算包含排队时间。
func New(tasks Store, alerts Alerts, resolver Resolver, sender Sender, locker TargetLocker, now func() time.Time) (*Service, error) {
	if tasks == nil || alerts == nil || resolver == nil || sender == nil || locker == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Service{tasks: tasks, alerts: alerts, resolver: resolver, sender: sender, now: now, locker: locker, slots: make(chan struct{}, 4)}, nil
}

// Produce 实时重读补扫候选并幂等建任务，允许合并到当前最新业务版本；失败不能推进水位。
func (s *Service) Produce(ctx context.Context, tenant, alert, target string) (StoredTask, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || alert == "" || len(alert) > domain.EntityIDMaxBytes || domain.ValidateIdentityPart("target", target, 64) != nil {
		return StoredTask{}, ErrInvalid
	}
	return s.withAdmission(ctx, tenant, func(call context.Context) (StoredTask, error) {
		a, err := s.alerts.GetAlertCurrent(call, tenant, alert)
		if err != nil {
			return StoredTask{}, err
		}
		if a.Alert.BKTenantID != tenant || a.Alert.AlertID != alert || a.Version.IsZero() {
			return StoredTask{}, ErrInvalid
		}
		ref, exists := a.Alert.Projection.Targets[target]
		if !exists || ref.SyncedRevision >= ref.RequiredRevision {
			return StoredTask{}, ErrBusy
		}
		t, err := NewTask(a.Alert, target, s.now())
		if err != nil {
			return StoredTask{}, err
		}
		// 同一任务的重投不占用新配额，满载时仍能复用已创建或已失败的原记录。
		current, err := s.tasks.Get(call, tenant, t.ID)
		if err == nil {
			return sameProducedTask(current, t)
		}
		if !errors.Is(err, ErrNotFound) {
			return StoredTask{}, err
		}
		count, err := s.tasks.CountWork(call, tenant, MaxPendingPerTenant)
		if err != nil {
			return StoredTask{}, err
		}
		if count < 0 || count > MaxPendingPerTenant {
			return StoredTask{}, ErrInvalid
		}
		if count == MaxPendingPerTenant {
			return StoredTask{}, ErrCapacity
		}
		saved, err := s.tasks.Put(call, t, "")
		if err == nil {
			return saved, nil
		}
		if !errors.Is(err, ErrConflict) {
			return StoredTask{}, err
		}
		current, err = s.tasks.Get(call, tenant, t.ID)
		if err != nil {
			return StoredTask{}, err
		}
		return sameProducedTask(current, t)
	})
}

// withAdmission 串行化同租户的新任务与人工重试准入，释放和已发送任务推进不会增加待办量。
// 容量来自持久任务索引，Redis 只保护正常并发的检查/写入；租约异常直接返回，保留 Alert 未确认水位供后续补扫。
func (s *Service) withAdmission(ctx context.Context, tenant string, run func(context.Context) (StoredTask, error)) (result StoredTask, err error) {
	return withProjectionAdmission(ctx, s.locker, s.slots, tenant, run)
}

func withProjectionAdmission(ctx context.Context, locker TargetLocker, slots chan struct{}, tenant string, run func(context.Context) (StoredTask, error)) (result StoredTask, err error) {
	if ctx == nil {
		return StoredTask{}, ErrInvalid
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-call.Done():
		return StoredTask{}, call.Err()
	}
	raw, _ := json.Marshal([]string{"linkd:projection-admission:v1", tenant})
	key := sha256.Sum256(raw)
	release, err := locker.Acquire(call, hex.EncodeToString(key[:]))
	if err != nil {
		return StoredTask{}, err
	}
	if release == nil {
		return StoredTask{}, ErrInvalid
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(call), 2*time.Second)
		defer stop()
		err = errors.Join(err, release(cleanup))
	}()
	return run(call)
}

func sameProducedTask(current StoredTask, next Task) (StoredTask, error) {
	if current.Task.Validate() != nil || current.Version == "" {
		return StoredTask{}, ErrInvalid
	}
	old, candidate := current.Task.Clone(), next.Clone()
	old.Progress, candidate.Progress = Progress{}, Progress{}
	old.CreatedAt, candidate.CreatedAt = time.Time{}, time.Time{}
	if !reflect.DeepEqual(old, candidate) {
		return StoredTask{}, ErrConflict
	}
	return current, nil
}

// Deliver 在 10 秒上下文内推进一次尝试；持久 sending 预留 30 秒，进程丢失后按同一身份重试。
// 成功 Receipt 先持久化，再 ACK Alert；ACK 失败不重发已经确认的外部请求。
func (s *Service) Deliver(ctx context.Context, tenant, id string) (result StoredTask, err error) {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
		defer func() { <-s.slots }()
	case <-call.Done():
		return StoredTask{}, call.Err()
	}
	initial, err := s.tasks.Get(call, tenant, id)
	if err != nil {
		return StoredTask{}, err
	}
	if initial.Task.Validate() != nil || initial.Task.Request.TenantID != tenant || initial.Task.ID != id {
		return StoredTask{}, ErrInvalid
	}
	key, err := TargetLockKey(tenant, initial.Task.Request.AlertID, initial.Task.Request.TargetID)
	if err != nil {
		return StoredTask{}, err
	}
	release, err := s.locker.Acquire(call, key)
	if err != nil {
		return StoredTask{}, err
	}
	if release == nil {
		return StoredTask{}, ErrInvalid
	}
	defer func() {
		releaseCtx, stop := context.WithTimeout(context.WithoutCancel(call), 2*time.Second)
		defer stop()
		err = errors.Join(err, release(releaseCtx))
	}()
	// 获取同 Alert/目标的租约后重读，排队期间的确认或人工重试不能被首次读取覆盖。
	return s.deliver(call, tenant, id)
}

func (s *Service) deliver(call context.Context, tenant, id string) (StoredTask, error) {
	current, err := s.tasks.Get(call, tenant, id)
	if err != nil {
		return StoredTask{}, err
	}
	if current.Task.Validate() != nil || current.Task.Request.TenantID != tenant || current.Task.ID != id || current.Version == "" {
		return StoredTask{}, ErrInvalid
	}
	p := current.Task.Progress
	if p.State == "delivered" || p.State == "succeeded" {
		return s.confirm(call, current)
	}
	if p.State == "failed" {
		return current, nil
	}
	now := s.timeAfter(p.UpdatedAt)
	if p.DueAt != nil && now.Before(*p.DueAt) || p.LeaseUntil != nil && now.Before(*p.LeaseUntil) {
		return current, ErrBusy
	}
	if p.Attempts == MaxAttempts {
		return s.fail(call, current, Failure{"attempt_interrupted", false})
	}
	next := current.Task.Clone()
	lease := now.Add(30 * time.Second)
	next.Progress.State = "sending"
	next.Progress.Attempts++
	next.Progress.TotalAttempts++
	next.Progress.UpdatedAt = now
	next.Progress.DueAt = nil
	next.Progress.LeaseUntil = &lease
	next.Progress.ErrorCode = ""
	claimed, err := s.tasks.Put(call, next, current.Version)
	if err != nil {
		return StoredTask{}, err
	}
	target, err := s.resolver.ResolveProjection(call, tenant, next.SourceID, next.SourceVersion, next.Request.TargetID)
	if err != nil {
		if call.Err() != nil {
			return claimed, call.Err()
		}
		return s.fail(call, claimed, Failure{"target_unavailable", true})
	}
	ack, err := s.sender.Send(call, target, next.Request.Clone())
	if call.Err() != nil {
		return claimed, call.Err()
	}
	if err != nil {
		var failure Failure
		if !errors.As(err, &failure) || !validFailureCode(failure.Code) {
			failure = Failure{"transport_failed", true}
		}
		return s.fail(call, claimed, failure)
	}
	if ack.ValidateFor(next.Request) != nil {
		return s.fail(call, claimed, Failure{"response_invalid", false})
	}
	delivered := claimed.Task.Clone()
	delivered.Progress.State = "delivered"
	delivered.Progress.LeaseUntil = nil
	delivered.Progress.Receipt = &ack
	delivered.Progress.UpdatedAt = s.timeAfter(delivered.Progress.UpdatedAt)
	saved, err := s.tasks.Put(call, delivered, claimed.Version)
	if err != nil {
		return claimed, err
	}
	return s.confirm(call, saved)
}

func (s *Service) fail(ctx context.Context, current StoredTask, f Failure) (StoredTask, error) {
	next := current.Task.Clone()
	now := s.timeAfter(next.Progress.UpdatedAt)
	next.Progress.State = "failed"
	next.Progress.ErrorCode = f.Code
	next.Progress.UpdatedAt = now
	next.Progress.LeaseUntil = nil
	next.Progress.DueAt = nil
	if f.Retryable && next.Progress.Attempts < MaxAttempts {
		next.Progress.State = "retry"
		// 固定有界退避使操作身份及重启行为可解释；同一目标的外层租约保证不会并行穿透重试。
		due := now.Add(min(time.Minute, time.Second*time.Duration(1<<uint(next.Progress.Attempts-1))))
		next.Progress.DueAt = &due
	}
	return s.tasks.Put(ctx, next, current.Version)
}

func (s *Service) timeAfter(previous time.Time) time.Time {
	now := s.now().Round(0).UTC()
	if now.Before(previous) {
		return previous
	}
	return now
}

func (s *Service) confirm(ctx context.Context, current StoredTask) (StoredTask, error) {
	t := current.Task
	if t.Progress.Receipt == nil || t.Progress.Receipt.ValidateFor(t.Request) != nil {
		return StoredTask{}, ErrInvalidReceipt
	}
	for attempt := 0; attempt < 4; attempt++ {
		row, err := s.alerts.GetAlertCurrent(ctx, t.Request.TenantID, t.Request.AlertID)
		if err != nil {
			return current, err
		}
		if row.Version.IsZero() || row.Alert.BKTenantID != t.Request.TenantID || row.Alert.AlertID != t.Request.AlertID {
			return current, ErrInvalid
		}
		next := row.Alert.Clone()
		projection, changed, err := next.Projection.Acknowledge(t.Request.TargetID, t.SourceVersion, t.Request.Revision, s.timeAfter(t.Progress.UpdatedAt))
		if err != nil {
			return current, err
		}
		if changed {
			next.Projection = projection
			_, err = s.alerts.CompareAndSetAlert(ctx, next.BKTenantID, next.AlertID, row.Version, next)
			if errors.Is(err, store.ErrVersionConflict) {
				continue
			}
			if err != nil {
				return current, err
			}
		}
		if t.Progress.State == "succeeded" {
			return current, nil
		}
		done := t.Clone()
		done.Progress.State = "succeeded"
		done.Progress.UpdatedAt = s.timeAfter(t.Progress.UpdatedAt)
		return s.tasks.Put(ctx, done, current.Version)
	}
	return current, store.ErrVersionConflict
}

// Retry 使用完整命令恢复失败任务，保留冻结请求、总尝试次数和最近操作者记录。
func (s *Service) Retry(ctx context.Context, command RetryCommand) (StoredTask, error) {
	if command.Validate() != nil {
		return StoredTask{}, ErrInvalid
	}
	return s.withAdmission(ctx, command.TenantID, func(call context.Context) (StoredTask, error) { return retryTask(call, s.tasks, command, s.now) })
}

// TargetLockKey 由状态投影与可靠动作共用，使同一目的端的版本应用和处置发送按目标串行。
// 调用者还必须共用相同部署的 Locker 命名空间；该键不替代接收端原子版本检查。
func TargetLockKey(tenant, alert, target string) (string, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || alert == "" || len(alert) > domain.EntityIDMaxBytes || domain.ValidateIdentityPart("target", target, 64) != nil {
		return "", ErrInvalid
	}
	raw, _ := json.Marshal([]string{"linkd:projection-target:v1", tenant, alert, target})
	hash := sha256.Sum256(raw)
	return hex.EncodeToString(hash[:]), nil
}
