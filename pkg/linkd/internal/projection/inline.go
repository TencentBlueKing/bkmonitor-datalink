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
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// InlineFailure 只描述同步投影及补偿入队结果，不携带远端响应或连接信息。
type InlineFailure struct {
	// Queued 表示已保存可由后台继续推进的补偿或确认记录。
	Queued bool
}

// Error 返回固定分类，不暴露远端错误或凭据。
func (e InlineFailure) Error() string {
	if e.Queued {
		return "KAC projection deferred to one compensation attempt"
	}
	return "KAC projection and compensation enqueue failed"
}

// CompensationQueued 供主流程记录脱敏结果，不能据此回滚已经提交的 Alert。
func (e InlineFailure) CompensationQueued() bool { return e.Queued }

// ProjectCurrent 按租户实时读取最新业务版本，供 Worker 的同步投影入口使用。
func (s *Service) ProjectCurrent(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	if ctx == nil || domain.ValidateIdentityPart("tenant", tenant, 64) != nil || id == "" || len(id) > domain.EntityIDMaxBytes {
		return store.StoredAlert{}, ErrInvalid
	}
	row, err := s.alerts.GetAlertCurrent(ctx, tenant, id)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if row.Alert.BKTenantID != tenant || row.Alert.AlertID != id {
		return store.StoredAlert{}, ErrInvalid
	}
	return s.Project(ctx, row)
}

// Project 直接投影已持久化的 Alert；成功只保存确认记录，失败才进入补偿待办。
// 调用者以 Linkd Alert 为成功依据，必须将本方法的失败与主业务写入失败分开处理。
func (s *Service) Project(ctx context.Context, row store.StoredAlert) (result store.StoredAlert, runErr error) {
	if ctx == nil {
		return row, ErrInvalid
	}
	a := row.Alert
	ref, enabled := a.Projection.Targets["kac"]
	if !enabled || ref.SyncedRevision >= a.Revision {
		return row, nil
	}
	task, err := NewTask(a, "kac", s.now())
	if err != nil || row.Version.IsZero() {
		return row, ErrInvalid
	}
	// 主流程没有额外等待队列；满额直接进入补偿，执行名额复用部署的 delivery 并发预算。
	if s.inlineSlots != nil {
		select {
		case s.inlineSlots <- struct{}{}:
			defer func() { <-s.inlineSlots }()
		default:
			return row, s.enqueueCompensation(ctx, task)
		}
	}
	call, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// 1. 与补偿和动作共用目标租约，旧版本不能穿透较新版本的确认。
	key, err := TargetLockKey(a.BKTenantID, a.AlertID, "kac")
	if err != nil {
		return row, err
	}
	release, err := s.locker.Acquire(call, key)
	if err != nil {
		return row, s.enqueueCompensation(ctx, task)
	}
	if release == nil {
		return row, ErrInvalid
	}
	defer func() {
		closeCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		if err := release(closeCtx); err != nil {
			runErr = errors.Join(runErr, InlineFailure{})
		}
	}()
	// 2. 已有补偿或终态不被主流程重入重新发送；成功记录只允许补本地水位。
	current, err := s.tasks.Get(call, a.BKTenantID, task.ID)
	if err == nil {
		current, err = sameProducedTask(current, task)
		if err != nil {
			return row, err
		}
		if current.Task.Progress.State != "succeeded" && current.Task.Progress.State != "delivered" {
			return row, InlineFailure{Queued: current.Task.HasWork()}
		}
		if _, err = s.confirm(call, current); err != nil {
			return row, err
		}
		return s.alerts.GetAlertCurrent(call, a.BKTenantID, a.AlertID)
	}
	if !errors.Is(err, ErrNotFound) {
		return row, InlineFailure{}
	}
	destination, err := s.resolver.ResolveProjection(call, a.BKTenantID, task.SourceID, task.SourceVersion, "kac")
	var receipt Receipt
	if err == nil {
		receipt, err = s.sender.Send(call, destination, task.Request.Clone())
		if err == nil {
			err = receipt.ValidateFor(task.Request)
		}
	}
	if err != nil {
		return row, s.enqueueCompensation(ctx, task)
	}
	// 3. 确认记录先于 ACK 持久化；正常成功不会经过后台投递。
	done := task.Clone()
	done.Progress.State, done.Progress.Attempts, done.Progress.TotalAttempts = "delivered", 1, 1
	done.Progress.DueAt, done.Progress.Receipt = nil, &receipt
	done.Progress.UpdatedAt = s.timeAfter(done.Progress.UpdatedAt)
	saved, err := s.tasks.Put(call, done, "")
	if errors.Is(err, ErrConflict) {
		current, err = s.tasks.Get(call, a.BKTenantID, task.ID)
		if err == nil {
			current, err = sameProducedTask(current, task)
		}
		if err == nil && current.Task.Progress.State == "pending" {
			done.CreatedAt = current.Task.CreatedAt
			done.Progress.UpdatedAt = s.timeAfter(current.Task.Progress.UpdatedAt)
			saved, err = s.tasks.Put(call, done, current.Version)
		} else if err == nil {
			return row, ErrBusy
		}
	}
	if err != nil {
		return row, s.enqueueCompensation(ctx, task)
	}
	if _, err := s.confirm(call, saved); err != nil {
		// delivered 只补 ACK，后台不会重复远端写入。
		return row, InlineFailure{Queued: true}
	}
	return s.alerts.GetAlertCurrent(call, a.BKTenantID, a.AlertID)
}

func (s *Service) enqueueCompensation(ctx context.Context, task Task) error {
	// 主调用的 I/O 截止时间已耗尽时仍给一次独立入队机会；进程取消不被无限延长。
	call, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	_, err := s.tasks.Put(call, task, "")
	if errors.Is(err, ErrConflict) {
		current, readErr := s.tasks.Get(call, task.Request.TenantID, task.ID)
		if readErr == nil {
			_, err = sameProducedTask(current, task)
		} else {
			err = readErr
		}
	}
	return InlineFailure{Queued: err == nil}
}
