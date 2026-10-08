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
	"fmt"
	"reflect"
	"time"

	"linkd/internal/domain"
)

var (
	// ErrInvalid 表示字段、预算或状态转换不合法。
	ErrInvalid = errors.New("invalid projection")
	// ErrNotFound 表示当前租户不存在指定任务。
	ErrNotFound = errors.New("projection task not found")
	// ErrConflict 表示 CAS 冲突或相同任务身份对应不同冻结内容。
	ErrConflict = errors.New("projection task version or identity conflict")
	// ErrCapacity 表示租户未完成投影已达到准入预算，稍后补扫而非丢弃业务水位。
	ErrCapacity = errors.New("projection pending task capacity reached")
	// ErrBusy 表示租约占用、退避未到期或目标已经确认。
	ErrBusy = errors.New("projection task not ready")
	// ErrInvalidReceipt 表示远端确认未满足身份、版本或可见性契约。
	ErrInvalidReceipt = errors.New("invalid projection receipt")
)

const (
	// MaxPendingPerTenant 限制每租户 pending/sending/retry/delivered 总量；历史 failed/succeeded 仍保留。
	MaxPendingPerTenant = 1024
	// MaxAttempts 是一次自动重试周期的上限；耗尽后保留 failed，人工重试开启新周期。
	MaxAttempts = 8
	// MaxTaskBytes 包含一个业务快照与有界任务信封，不允许嵌入无限错误历史。
	MaxTaskBytes = MaxSnapshotBytes + 16<<10
)

// Task 将稳定请求与目标的不可变来源发布引用一起保存，不保存连接凭据。
type Task struct {
	// ID 由租户、Alert、目标和业务版本确定，不包含排队时间。
	ID string `json:"id"`
	// Request 是首次创建后不可改写的协议请求。
	Request Request `json:"request"`
	// SourceID 指向该 Alert 的真实来源。
	SourceID string `json:"source_id"`
	// SourceVersion 保存 opening Event 的来源版本，不用于解析出口。
	SourceVersion int64 `json:"source_version"`
	// CreatedAt 固定首次排队时间，重试不刷新。
	CreatedAt time.Time `json:"created_at"`
	// Progress 保存可 CAS 推进的执行元数据。
	Progress Progress `json:"progress"`
}

// Progress 仅保存有界的最近尝试；Receipt 落库后重试只补本地 ACK。
type Progress struct {
	// State 只允许 pending/sending/retry/delivered/succeeded/failed。
	State string `json:"state"`
	// Attempts 是当前自动重试周期已预留的尝试数。
	Attempts int `json:"attempts"`
	// TotalAttempts 跨人工重试保留累计次数。
	TotalAttempts int64 `json:"total_attempts"`
	// Generation 从 1 开始，仅人工恢复失败任务时加一。
	Generation int64 `json:"generation"`
	// LastRetry 保存最近一次人工恢复的完整命令和时间，不能只按操作 ID 忽略不同操作者或原因。
	LastRetry *RetryRecord `json:"last_retry,omitempty"`
	// UpdatedAt 是非回退的任务元数据时间，不是 Alert 业务时间。
	UpdatedAt time.Time `json:"updated_at"`
	// DueAt 仅用于 pending/retry，到期后才能预留尝试。
	DueAt *time.Time `json:"due_at,omitempty"`
	// LeaseUntil 仅用于 sending，进程丢失后等待到期再重试。
	LeaseUntil *time.Time `json:"lease_until,omitempty"`
	// ErrorCode 只保存固定分类，不保存原始异常或远端响应。
	ErrorCode string `json:"error_code,omitempty"`
	// Receipt 是已验证远端应用且可搜索的确认，先于本地 ACK 持久化。
	Receipt *Receipt `json:"receipt,omitempty"`
}

// StoredTask 的 Version 只用于同一任务 CAS，不参与远端业务版本排序。
type StoredTask struct {
	// Task 是完整且独立的任务快照。
	Task Task
	// Version 是仅用于同一任务条件写的不透明后端 token。
	Version string
}

// Query 提供控制面待办扫描或租户管理查询；每页 1..16 项，游标是上一条任务 ID。
type Query struct {
	// TenantID 对管理查询必填；内部全局工作扫描可留空。
	TenantID string
	// After 是上一页最后的任务 ID；初始扫描为空。
	After string
	// WorkOnly 筛选尚未完成的持久任务，不包括 failed。
	WorkOnly bool
	// Limit 必须为 1..16。
	Limit int
}

// Store 必须在单条写入内保存 payload 及待办索引；空版本只创建，非空版本真实 CAS。
// List 返回 ID 严格递增的完整页，检查部分查询失败；不允许用 Redis 代替这些持久事实。
type Store interface {
	Get(context.Context, string, string) (StoredTask, error)
	Put(context.Context, Task, string) (StoredTask, error)
	List(context.Context, Query) ([]StoredTask, error)
	// CountWork 在租户准入租约内有界计数，达到 limit 返回 limit；不能把部分搜索失败当作低计数。
	CountWork(context.Context, string, int) (int, error)
}

// TaskID 按租户、Alert、目标及原请求版本定位持久任务，不使用回执中的远端版本。
// ACK 只推进本次请求版本；其他边界读取可见性证明时必须复用同一身份规则。
func TaskID(tenant, alert, target string, revision int64) (string, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || alert == "" || len(alert) > domain.EntityIDMaxBytes ||
		domain.ValidateIdentityPart("target", target, 64) != nil || revision < 1 || revision >= 1<<53 {
		return "", ErrInvalid
	}
	raw, _ := json.Marshal([]any{"linkd:projection-task:v1", tenant, alert, target, revision})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// NewTask 从当前已绑定且落后的目标创建固定业务请求；排队时间不进入任务身份。
func NewTask(a domain.Alert, target string, at time.Time) (Task, error) {
	q, err := BuildRequest(a, target)
	if err != nil {
		return Task{}, err
	}
	ref, ok := a.Projection.Targets[target]
	if !ok || ref.RequiredRevision != a.Revision || ref.SyncedRevision >= ref.RequiredRevision || at.IsZero() {
		return Task{}, ErrInvalid
	}
	id, err := TaskID(a.BKTenantID, a.AlertID, target, a.Revision)
	if err != nil {
		return Task{}, err
	}
	at = at.Round(0).UTC()
	t := Task{ID: id, Request: q, SourceID: a.EventSourceID, SourceVersion: ref.SourceVersion, CreatedAt: at, Progress: Progress{State: "pending", Generation: 1, UpdatedAt: at, DueAt: &at}}
	return t, t.Validate()
}

// Clone 隔离快照、确认和时间指针。
func (t Task) Clone() Task {
	t.Request = t.Request.Clone()
	if t.Progress.LastRetry != nil {
		value := *t.Progress.LastRetry
		t.Progress.LastRetry = &value
	}
	if t.Progress.Receipt != nil {
		v := *t.Progress.Receipt
		t.Progress.Receipt = &v
	}
	if t.Progress.DueAt != nil {
		v := *t.Progress.DueAt
		t.Progress.DueAt = &v
	}
	if t.Progress.LeaseUntil != nil {
		v := *t.Progress.LeaseUntil
		t.Progress.LeaseUntil = &v
	}
	return t
}

// HasWork 包含已经收到远端确认但还未完成本地水位的任务，不扫描 succeeded/failed。
func (t Task) HasWork() bool { return t.Progress.State != "succeeded" && t.Progress.State != "failed" }

// Validate 校验稳定身份、目标引用和状态不变量，不接受调用方伪造初始确认。
func (t Task) Validate() error {
	if t.Request.Validate() != nil || domain.ValidateIdentityPart("source", t.SourceID, 32) != nil || t.SourceVersion < 1 || t.SourceVersion >= 1<<53 || t.CreatedAt.IsZero() {
		return ErrInvalid
	}
	var source struct {
		// ID 由租户、Alert、目标和业务版本确定，不包含排队时间。
		ID string `json:"event_source_id"`
	}
	if json.Unmarshal(t.Request.Alert, &source) != nil || source.ID != t.SourceID {
		return ErrInvalid
	}
	id, err := TaskID(t.Request.TenantID, t.Request.AlertID, t.Request.TargetID, t.Request.Revision)
	if err != nil || t.ID != id {
		return ErrInvalid
	}
	p := t.Progress
	if p.UpdatedAt.IsZero() || p.UpdatedAt.Before(t.CreatedAt) || p.Generation < 1 || p.Generation >= 1<<53 || p.Attempts < 0 || p.Attempts > MaxAttempts ||
		p.TotalAttempts < int64(p.Attempts) || p.TotalAttempts >= 1<<53 {
		return ErrInvalid
	}
	if (p.Generation == 1) != (p.LastRetry == nil) {
		return ErrInvalid
	}
	if p.LastRetry != nil {
		retry := p.LastRetry
		if retry.Command.Validate() != nil || retry.Command.TenantID != t.Request.TenantID || retry.Command.TaskID != t.ID || retry.RequestedAt.IsZero() || retry.RequestedAt.Before(t.CreatedAt) || retry.RequestedAt.After(p.UpdatedAt) {
			return ErrInvalid
		}
	}
	if p.ErrorCode != "" && !validFailureCode(p.ErrorCode) {
		return ErrInvalid
	}
	switch p.State {
	case "pending":
		if p.Attempts != 0 || p.DueAt == nil || p.LeaseUntil != nil || p.Receipt != nil || p.ErrorCode != "" {
			return ErrInvalid
		}
	case "retry":
		if p.Attempts < 1 || p.Attempts >= MaxAttempts || p.DueAt == nil || p.LeaseUntil != nil || p.Receipt != nil || p.ErrorCode == "" {
			return ErrInvalid
		}
	case "sending":
		if p.Attempts < 1 || p.DueAt != nil || p.LeaseUntil == nil || p.LeaseUntil.Sub(p.UpdatedAt) != 30*time.Second || p.Receipt != nil || p.ErrorCode != "" {
			return ErrInvalid
		}
	case "delivered", "succeeded":
		if p.Attempts < 1 || p.DueAt != nil || p.LeaseUntil != nil || p.Receipt == nil || p.Receipt.ValidateFor(t.Request) != nil || p.ErrorCode != "" {
			return ErrInvalid
		}
	case "failed":
		if p.Attempts < 1 || p.DueAt != nil || p.LeaseUntil != nil || p.Receipt != nil || p.ErrorCode == "" {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if p.DueAt != nil && (p.DueAt.IsZero() || p.DueAt.Before(p.UpdatedAt) || p.DueAt.Sub(p.UpdatedAt) > time.Minute || p.State == "pending" && !p.DueAt.Equal(p.UpdatedAt)) {
		return ErrInvalid
	}
	raw, err := json.Marshal(t)
	if err != nil || len(raw) > MaxTaskBytes {
		return ErrInvalid
	}
	return nil
}

// ValidateWrite 将转换校验放在持久化边界，避免 CAS 被用于重写已冻结内容或确认。
func ValidateWrite(current *Task, next Task) error {
	if err := next.Validate(); err != nil {
		return err
	}
	p := next.Progress
	if current == nil {
		if p.State != "pending" || p.Generation != 1 || p.TotalAttempts != 0 || p.LastRetry != nil || !p.UpdatedAt.Equal(next.CreatedAt) {
			return ErrInvalid
		}
		return nil
	}
	old := current.Clone()
	n := next.Clone()
	old.Progress = Progress{}
	n.Progress = Progress{}
	if !reflect.DeepEqual(old, n) {
		return ErrConflict
	}
	c := current.Progress
	if p.UpdatedAt.Before(c.UpdatedAt) {
		return ErrInvalid
	}
	sameCycle := p.Generation == c.Generation && reflect.DeepEqual(p.LastRetry, c.LastRetry)
	sameAttempts := p.Attempts == c.Attempts && p.TotalAttempts == c.TotalAttempts
	switch {
	case (c.State == "pending" || c.State == "retry" || c.State == "sending") && p.State == "sending":
		ready := c.DueAt != nil && !p.UpdatedAt.Before(*c.DueAt) || c.LeaseUntil != nil && !p.UpdatedAt.Before(*c.LeaseUntil)
		if sameCycle && ready && p.Attempts == c.Attempts+1 && p.TotalAttempts == c.TotalAttempts+1 {
			return nil
		}
	case c.State == "sending" && (p.State == "retry" || p.State == "failed" || p.State == "delivered"):
		if sameCycle && sameAttempts {
			return nil
		}
	case c.State == "delivered" && p.State == "succeeded":
		if sameCycle && sameAttempts && reflect.DeepEqual(c.Receipt, p.Receipt) {
			return nil
		}
	case c.State == "failed" && p.State == "pending":
		if p.Generation == c.Generation+1 && p.Attempts == 0 && p.TotalAttempts == c.TotalAttempts && p.LastRetry != nil && p.LastRetry.RequestedAt.Equal(p.UpdatedAt) && (c.LastRetry == nil || p.LastRetry.Command.OperationID != c.LastRetry.Command.OperationID) {
			return nil
		}
	}
	return ErrInvalid
}

// Validate 拒绝越过租户作用域的管理游标和过大的工作页。
func (q Query) Validate() error {
	if q.Limit < 1 || q.Limit > 16 || (q.TenantID != "" && domain.ValidateIdentityPart("tenant", q.TenantID, 64) != nil) || (q.After != "" && !validHash(q.After)) {
		return ErrInvalid
	}
	if q.TenantID == "" && !q.WorkOnly {
		return fmt.Errorf("%w: management list requires tenant", ErrInvalid)
	}
	return nil
}
