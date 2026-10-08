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
	"reflect"
	"strings"
	"time"

	"linkd/internal/domain"
)

// RetryCommand 标识一次人工恢复；ExpectedVersion 是任务 CAS token，不是 Alert 业务 revision。
type RetryCommand struct {
	// TenantID 使操作身份和读取始终落在明确租户内。
	TenantID string `json:"bk_tenant_id"`
	// TaskID 是已有任务的稳定哈希身份，不能由重试创建新任务。
	TaskID string `json:"task_id"`
	// ExpectedVersion 原样回传上次读取的任务 CAS token。
	ExpectedVersion string `json:"expected_version"`
	// OperationID 标识该任务最近一次恢复，不能在结果不确定时更换；更早操作由原 CAS 防止重复推进。
	OperationID string `json:"operation_id"`
	// OperatorID 应由已认证的管理入口提供。
	OperatorID string `json:"operator_id"`
	// Reason 保留操作者提交的原文，最多 1024 字节。
	Reason string `json:"reason"`
}

// Validate 拒绝不明确的作用域、版本、操作者和原因；命令中的文本不在重试时重新归一化。
func (c RetryCommand) Validate() error {
	if domain.ValidateIdentityPart("tenant", c.TenantID, 64) != nil || !validHash(c.TaskID) || c.ExpectedVersion == "" || len(c.ExpectedVersion) > 128 || strings.ContainsAny(c.ExpectedVersion, "\r\n") || domain.ValidateIdentityPart("operation", c.OperationID, 128) != nil || strings.TrimSpace(c.OperatorID) == "" || len(c.OperatorID) > 256 || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 1024 {
		return ErrInvalid
	}
	return nil
}

// RetryRecord 只保存最近一次人工恢复；历史次数保存在 Generation/TotalAttempts，不宣称保存完整操作历史。
type RetryRecord struct {
	// Command 固定原作用域、版本、操作身份、操作者和原因。
	Command RetryCommand `json:"command"`
	// RequestedAt 是首次接受恢复的服务端时间，后续发送不刷新。
	RequestedAt time.Time `json:"requested_at"`
}

// Retrier 只允许恢复已存在的失败任务；它不加载来源配置、不发送 HTTP，也不构造新业务快照。
type Retrier struct {
	tasks  Store
	locker TargetLocker
	now    func() time.Time
	slots  chan struct{}
}

// NewRetrier 为管理请求注入任务存储、租户准入租约和时钟，独立限制四项执行。
func NewRetrier(tasks Store, locker TargetLocker, now func() time.Time) (*Retrier, error) {
	if tasks == nil || locker == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Retrier{tasks: tasks, locker: locker, now: now, slots: make(chan struct{}, 4)}, nil
}

// Retry 保留第一次命令原文；同操作身份但版本、操作者或原因不同均返回冲突。
// 成功只代表恢复待办；后续投递器沿用原目标发布与请求，不能解释为已经同步。
func (r *Retrier) Retry(ctx context.Context, c RetryCommand) (StoredTask, error) {
	if c.Validate() != nil {
		return StoredTask{}, ErrInvalid
	}
	return withProjectionAdmission(ctx, r.locker, r.slots, c.TenantID, func(call context.Context) (StoredTask, error) { return retryTask(call, r.tasks, c, r.now) })
}

func retryTask(ctx context.Context, tasks Store, c RetryCommand, clock func() time.Time) (StoredTask, error) {
	current, err := tasks.Get(ctx, c.TenantID, c.TaskID)
	if err != nil {
		return StoredTask{}, err
	}
	if current.Task.Validate() != nil || current.Task.Request.TenantID != c.TenantID || current.Task.ID != c.TaskID || current.Version == "" {
		return StoredTask{}, ErrInvalid
	}
	if old := current.Task.Progress.LastRetry; old != nil && old.Command.OperationID == c.OperationID {
		if !reflect.DeepEqual(old.Command, c) {
			return StoredTask{}, ErrConflict
		}
		return current, nil
	}
	if current.Version != c.ExpectedVersion || current.Task.Progress.State != "failed" {
		return StoredTask{}, ErrConflict
	}
	count, err := tasks.CountWork(ctx, c.TenantID, MaxPendingPerTenant)
	if err != nil {
		return StoredTask{}, err
	}
	if count < 0 || count > MaxPendingPerTenant {
		return StoredTask{}, ErrInvalid
	}
	if count == MaxPendingPerTenant {
		return StoredTask{}, ErrCapacity
	}
	next := current.Task.Clone()
	now := clock().Round(0).UTC()
	if now.Before(next.Progress.UpdatedAt) {
		now = next.Progress.UpdatedAt
	}
	next.Progress.State = "pending"
	next.Progress.Generation++
	next.Progress.Attempts = 0
	next.Progress.LastRetry = &RetryRecord{Command: c, RequestedAt: now}
	next.Progress.DueAt = &now
	next.Progress.UpdatedAt = now
	next.Progress.ErrorCode = ""
	return tasks.Put(ctx, next, current.Version)
}
