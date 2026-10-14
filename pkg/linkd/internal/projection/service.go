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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// TargetLocker 按稳定用途键提供至少 30 秒的跨实例租约；只用于同 Alert/目标投递排序。
// 单次用例限制 10 秒，成功必须返回 owner-safe 释放函数；释放不确定不能假报调度成功。
type TargetLocker interface {
	Acquire(context.Context, string) (func(context.Context) error, error)
}

// Service 编排持久任务、单次外部确认和本地水位；同一 Alert/目标串行，不同对象可独立执行。
type Service struct {
	tasks    Store
	alerts   Alerts
	resolver Resolver
	sender   Sender
	now      func() time.Time
	locker   TargetLocker
}

// New 注入全部 I/O 和时钟，不创建 goroutine；生产/人工重试使用任务 CAS，投递取得 Alert/目标租约。
// 各用例独立执行，不因同租户其他告警的积压拒绝入队。
func New(tasks Store, alerts Alerts, resolver Resolver, sender Sender, locker TargetLocker, now func() time.Time) (*Service, error) {
	if tasks == nil || alerts == nil || resolver == nil || sender == nil || locker == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Service{tasks: tasks, alerts: alerts, resolver: resolver, sender: sender, now: now, locker: locker}, nil
}

// errAlreadyProduced 仅表示稳定身份已持久化，不能据此宣称任务已投递。
var errAlreadyProduced = errors.New("projection task already recorded")

// ProduceCandidate 让补扫跳过已有任务；缺失项仍实时重读再冻结，失败项不自动重建。
func (s *Service) ProduceCandidate(ctx context.Context, row store.StoredAlert, target string) (StoredTask, error) {
	if lookup, ok := s.tasks.(interface {
		Exists(context.Context, string, string) (bool, error)
	}); ok {
		ref, exists := row.Alert.Projection.Targets[target]
		if !exists {
			return StoredTask{}, ErrInvalid
		}
		id, err := TaskID(row.Alert.BKTenantID, row.Alert.AlertID, target, ref.RequiredRevision)
		if err != nil {
			return StoredTask{}, err
		}
		known, err := lookup.Exists(ctx, row.Alert.BKTenantID, id)
		if err != nil {
			return StoredTask{}, err
		}
		if known {
			return StoredTask{}, errAlreadyProduced
		}
	}
	return s.Produce(ctx, row.Alert.BKTenantID, row.Alert.AlertID, target)
}

// Produce 实时重读补扫候选并幂等建任务，允许合并到当前最新业务版本；失败不能推进水位。
func (s *Service) Produce(ctx context.Context, tenant, alert, target string) (StoredTask, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || alert == "" || len(alert) > domain.EntityIDMaxBytes || domain.ValidateIdentityPart("target", target, 64) != nil {
		return StoredTask{}, ErrInvalid
	}
	return withProjectionCall(ctx, func(call context.Context) (StoredTask, error) {
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
		// 同一任务的重投复用原记录；创建冲突由任务身份和 CAS 处理。
		current, err := s.tasks.Get(call, tenant, t.ID)
		if err == nil {
			return sameProducedTask(current, t)
		}
		if !errors.Is(err, ErrNotFound) {
			return StoredTask{}, err
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

// withProjectionCall 仅约束一次调用时间，任务身份和 CAS 保证并发幂等。
func withProjectionCall(ctx context.Context, run func(context.Context) (StoredTask, error)) (StoredTask, error) {
	if ctx == nil {
		return StoredTask{}, ErrInvalid
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := call.Err(); err != nil {
		return StoredTask{}, err
	}
	return run(call)
}

func sameProducedTask(current StoredTask, next Task) (StoredTask, error) {
	if current.Task.Validate() != nil || current.Version == "" {
		return StoredTask{}, ErrInvalid
	}
	old, candidate := current.Task.Clone(), next.Clone()
	old.Progress, candidate.Progress = Progress{}, Progress{}
	old.CreatedAt, candidate.CreatedAt = time.Time{}, time.Time{}
	if old.ID != candidate.ID || old.SourceID != candidate.SourceID || old.SourceVersion != candidate.SourceVersion || old.Request.SchemaVersion != candidate.Request.SchemaVersion || old.Request.TenantID != candidate.Request.TenantID || old.Request.TargetID != candidate.Request.TargetID || old.Request.AlertID != candidate.Request.AlertID || old.Request.AlarmID != candidate.Request.AlarmID || old.Request.Revision != candidate.Request.Revision || old.Request.ContentHash != candidate.Request.ContentHash || !bytes.Equal(old.Request.Alert, candidate.Request.Alert) {
		return StoredTask{}, ErrConflict
	}
	return current, nil
}

// Synchronize 在调用方预算内同步推进持久任务，只有目标水位覆盖指定业务版本才成功。
// 与后台投递共用任务身份、目标租约及退避预算；失败保留待办，不绕过 failed 状态重发。
func (s *Service) Synchronize(ctx context.Context, tenant, alert, target string, revision int64) error {
	if ctx == nil || domain.ValidateIdentityPart("tenant", tenant, 64) != nil || alert == "" || len(alert) > domain.EntityIDMaxBytes || domain.ValidateIdentityPart("target", target, 64) != nil || revision < 1 || revision >= 1<<53 {
		return ErrInvalid
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	synced := func() (bool, error) {
		row, err := s.alerts.GetAlertCurrent(call, tenant, alert)
		if err != nil {
			return false, err
		}
		ref, exists := row.Alert.Projection.Targets[target]
		if row.Alert.BKTenantID != tenant || row.Alert.AlertID != alert || row.Version.IsZero() || !exists || row.Alert.Revision < revision {
			return false, ErrInvalid
		}
		return ref.SyncedRevision >= revision, nil
	}
	if done, err := synced(); err != nil || done {
		return err
	}
	task, err := s.Produce(call, tenant, alert, target)
	if err == nil {
		task, err = s.Deliver(call, tenant, task.Task.ID)
		if err == nil && task.Task.Progress.State != "succeeded" {
			return ErrBusy
		}
	}
	// Produce/Deliver 可能与后台确认竞争。仅锁忙允许核对已完成水位；
	// 其他错误（尤其租约释放不确定）不能被水位掩盖。
	if err != nil && (!errors.Is(err, ErrBusy) || !CanDefer(err)) {
		return err
	}
	if done, readErr := synced(); readErr != nil || done {
		return readErr
	}
	return ErrBusy
}

// Deliver 在 10 秒上下文内推进一次尝试；持久 sending 预留 30 秒，进程丢失后按同一身份重试。
// 成功 Receipt 先持久化，再 ACK Alert；ACK 失败不重发已经确认的外部请求。
func (s *Service) Deliver(ctx context.Context, tenant, id string) (result StoredTask, err error) {
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
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
	if p.Attempts >= MaxAttempts {
		return s.fail(call, current, Failure{Code: "attempt_interrupted"})
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
		due := now.Add(min(time.Minute, time.Second*time.Duration(1<<uint(min(next.Progress.Attempts-1, 6)))))
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
	if writer, ok := s.alerts.(store.ProjectionAcknowledger); ok {
		ack := store.ProjectionAcknowledgment{TenantID: t.Request.TenantID, AlertID: t.Request.AlertID, TargetID: t.Request.TargetID, SourceVersion: t.SourceVersion, Revision: t.Request.Revision, At: s.timeAfter(t.Progress.UpdatedAt)}
		if err := writer.AcknowledgeProjection(ctx, ack); err != nil {
			return current, err
		}
		if t.Progress.State == "succeeded" {
			return current, nil
		}
		done := t.Clone()
		done.Progress.State, done.Progress.UpdatedAt = "succeeded", s.timeAfter(t.Progress.UpdatedAt)
		return s.tasks.Put(ctx, done, current.Version)
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
	return withProjectionCall(ctx, func(call context.Context) (StoredTask, error) { return retryTask(call, s.tasks, command, s.now) })
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
