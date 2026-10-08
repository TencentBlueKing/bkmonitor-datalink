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
	"encoding/json"
	"reflect"
	"time"

	"linkd/internal/domain"
	"linkd/internal/projection"
)

const (
	// MaxPendingPerTenant 为每租户未完成自动待办上限；失败历史保留但不占准入名额。
	MaxPendingPerTenant = 1024
	// MaxAttempts 为每个人工恢复周期的失败/发送尝试上限，正常等待投影不消耗次数。
	MaxAttempts = 8
	// MaxTaskBytes 包含固定请求、两个有界确认及最近恢复记录。
	MaxTaskBytes = MaxRequestBytes + 16<<10
)

// Task 固定获准动作、不可变来源发布和首次排队时间；不保存连接凭据。
type Task struct {
	// ID 按租户/Alert/目标/revision 唯一；同一版本不同原因冲突，不能创建两个不同动作。
	ID string `json:"id"`
	// Request 是不可改写的动作与原始快照。
	Request Request `json:"request"`
	// SourceID 是动作所属的真实来源。
	SourceID string `json:"source_id"`
	// SourceVersion 保存 opening 来源版本，用于业务溯源，不选择出口或凭据。
	SourceVersion int64 `json:"source_version"`
	// CreatedAt 为首次排队时间，重投不更新。
	CreatedAt time.Time `json:"created_at"`
	// Progress 只允许带 CAS 的执行推进。
	Progress Progress `json:"progress"`
}

// Progress 保存最近一次投递状态，投影确认和动作受理确认分开记录。
type Progress struct {
	// State 为 pending/waiting_projection/sending/retry/succeeded/skipped/failed。
	State string `json:"state"`
	// Attempts 为当前周期的投递/失败尝试数，正常等待投影不递增。
	Attempts int `json:"attempts"`
	// TotalAttempts 跨人工恢复保留全部周期的尝试数。
	TotalAttempts int64 `json:"total_attempts"`
	// Generation 仅人工恢复失败任务时递增，初始为 1。
	Generation int64 `json:"generation"`
	// LastRetry 保留最近一次完整恢复命令，重复命令不再次重置预算。
	LastRetry *RetryRecord `json:"last_retry,omitempty"`
	// UpdatedAt 是单调前进的执行元数据时间。
	UpdatedAt time.Time `json:"updated_at"`
	// DueAt 是待执行或重试的到期时间。
	DueAt *time.Time `json:"due_at,omitempty"`
	// LeaseUntil 是本次预留尝试的截止时间，不证明 HTTP 已实际发出。
	LeaseUntil *time.Time `json:"lease_until,omitempty"`
	// ErrorCode 只保存固定安全分类。
	ErrorCode string `json:"error_code,omitempty"`
	// Projection 是该次尝试真实取得的可见性证明，不是 ActionReceipt。
	Projection *projection.Receipt `json:"projection,omitempty"`
	// Receipt 是接收端对原动作最近一次成功的 Celery 投递结果。
	Receipt *Receipt `json:"receipt,omitempty"`
	// PreviousUnconfirmed 保留早先发送结果不确定的事实，之后本地跳过不能解释成从未处置。
	PreviousUnconfirmed bool `json:"previous_unconfirmed"`
}

// StoredTask 的版本只用于同任务 CAS，不能参与业务顺序比较。
type StoredTask struct {
	// Task 是经验证且独立的完整快照。
	Task Task
	// Version 是同一任务的后端条件写令牌。
	Version string
}

// Query 提供租户管理或内部待办扫描，最多 16 项，After 为上一条任务 ID。
type Query struct {
	// TenantID 对管理查询必填，内部待办扫描可为空。
	TenantID string
	// After 是前一页最后的任务 ID，首次为空。
	After string
	// WorkOnly 仅枚举自动待办；失败任务另由目标顺序查询保留屏障。
	WorkOnly bool
	// Limit 必须处于 1..16。
	Limit int
}

// Store 原子写入任务、work/unsettled 和目标版本索引；查询必须检查部分失败。
// OldestUnsettled 包含 failed，使后续动作不能跨过尚未处理的旧动作；精确执行前仍要实时读取。
type Store interface {
	Get(context.Context, string, string) (StoredTask, error)
	Put(context.Context, Task, string) (StoredTask, error)
	List(context.Context, Query) ([]StoredTask, error)
	CountWork(context.Context, string, int) (int, error)
	// ConfirmVisible 确认此不可变任务已进入排序查询；结果不确定后的重复生产也必须调用。
	ConfirmVisible(context.Context, Task) error
	OldestUnsettled(context.Context, string, string, string) (StoredTask, error)
}

// NewTask 只从本次动作快照及其已绑定目标创建记录，不回读后来配置或后来 Alert。
func NewTask(a domain.Alert, target string, cause Cause, at time.Time) (Task, error) {
	q, e := BuildRequest(a, target, cause)
	if e != nil {
		return Task{}, e
	}
	ref, ok := a.Projection.Targets[target]
	if !ok || ref.RequiredRevision != a.Revision || ref.SourceVersion < 1 || at.IsZero() {
		return Task{}, ErrInvalid
	}
	at = at.Round(0).UTC()
	t := Task{ID: digest("linkd:action-task:v1", q.TenantID, q.AlertID, q.TargetID, q.Revision), Request: q, SourceID: a.EventSourceID, SourceVersion: ref.SourceVersion, CreatedAt: at, Progress: Progress{State: "pending", Generation: 1, UpdatedAt: at, DueAt: &at}}
	return t, t.Validate()
}

// Clone 隔离动态快照、确认和时间指针。
func (t Task) Clone() Task {
	t.Request = t.Request.Clone()
	if t.Progress.LastRetry != nil {
		v := *t.Progress.LastRetry
		t.Progress.LastRetry = &v
	}
	if t.Progress.Receipt != nil {
		v := *t.Progress.Receipt
		t.Progress.Receipt = &v
	}
	if t.Progress.Projection != nil {
		v := *t.Progress.Projection
		t.Progress.Projection = &v
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

// HasWork 是否继续自动扫描；失败记录仍参与同目标顺序屏障。
func (t Task) HasWork() bool {
	return t.Progress.State != "succeeded" && t.Progress.State != "skipped" && t.Progress.State != "failed"
}

// Unsettled 是否仍阻止同目标较新版本动作先执行。
func (t Task) Unsettled() bool {
	return t.Progress.State != "succeeded" && t.Progress.State != "skipped"
}

// Validate 限制全部阶段、确认身份、重试计数和载荷预算。
func (t Task) Validate() error {
	q, p := t.Request, t.Progress
	if q.Validate() != nil || t.ID != digest("linkd:action-task:v1", q.TenantID, q.AlertID, q.TargetID, q.Revision) || domain.ValidateIdentityPart("source", t.SourceID, 32) != nil || t.SourceVersion < 1 || t.SourceVersion >= 1<<53 || t.CreatedAt.IsZero() {
		return ErrInvalid
	}
	var a struct {
		SourceID string `json:"event_source_id"`
	}
	if json.Unmarshal(q.Alert, &a) != nil || a.SourceID != t.SourceID {
		return ErrInvalid
	}
	if p.UpdatedAt.IsZero() || p.UpdatedAt.Before(t.CreatedAt) || p.Attempts < 0 || p.Attempts > MaxAttempts || p.TotalAttempts < int64(p.Attempts) || p.TotalAttempts >= 1<<53 || p.Generation < 1 || p.Generation >= 1<<53 || (p.Generation == 1) != (p.LastRetry == nil) {
		return ErrInvalid
	}
	if p.LastRetry != nil {
		r := p.LastRetry
		if r.Command.Validate() != nil || r.Command.TenantID != q.TenantID || r.Command.TaskID != t.ID || r.RequestedAt.Before(t.CreatedAt) || r.RequestedAt.After(p.UpdatedAt) {
			return ErrInvalid
		}
	}
	if p.Projection != nil && ValidateProjection(q, *p.Projection) != nil {
		return ErrInvalid
	}
	if p.Receipt != nil && p.Receipt.ValidateFor(q) != nil {
		return ErrInvalid
	}
	if p.ErrorCode != "" && !validFailureCode(p.ErrorCode) {
		return ErrInvalid
	}
	switch p.State {
	case "pending":
		if p.Attempts != 0 || p.DueAt == nil || p.LeaseUntil != nil || p.Receipt != nil || p.Projection != nil || p.ErrorCode != "" {
			return ErrInvalid
		}
	case "waiting_projection":
		if p.DueAt == nil || p.LeaseUntil != nil || p.Receipt != nil || p.Projection != nil || p.ErrorCode != "projection_pending" {
			return ErrInvalid
		}
	case "retry":
		if p.Attempts < 1 || p.Attempts >= MaxAttempts || p.DueAt == nil || p.LeaseUntil != nil || p.Receipt != nil || (p.ErrorCode == "" || p.ErrorCode == "projection_pending" || p.ErrorCode == "superseded_by_terminal") {
			return ErrInvalid
		}
	case "sending":
		if p.Attempts < 1 || p.DueAt != nil || p.LeaseUntil == nil || p.LeaseUntil.Sub(p.UpdatedAt) != 30*time.Second || p.Receipt != nil || p.ErrorCode != "" {
			return ErrInvalid
		}
	case "failed":
		if p.Attempts < 1 || p.DueAt != nil || p.LeaseUntil != nil || p.Receipt != nil || p.ErrorCode == "" || p.ErrorCode == "projection_pending" || p.ErrorCode == "superseded_by_terminal" {
			return ErrInvalid
		}
	case "succeeded":
		if p.Attempts < 1 || p.DueAt != nil || p.LeaseUntil != nil || p.Projection == nil || p.Receipt == nil || p.Receipt.Outcome != "queued" || p.ErrorCode != "" {
			return ErrInvalid
		}
	case "skipped":
		if p.DueAt != nil || p.LeaseUntil != nil || p.ErrorCode != "superseded_by_terminal" {
			return ErrInvalid
		}
		if p.Receipt != nil || p.Projection == nil || !stale(q, *p.Projection) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if p.DueAt != nil && (p.DueAt.IsZero() || p.DueAt.Before(p.UpdatedAt) || p.DueAt.Sub(p.UpdatedAt) > time.Minute || p.State == "pending" && !p.DueAt.Equal(p.UpdatedAt)) {
		return ErrInvalid
	}
	raw, e := json.Marshal(t)
	if e != nil || len(raw) > MaxTaskBytes {
		return ErrInvalid
	}
	return nil
}

// ValidateWrite 让存储 CAS 同时保护原动作及单调状态，不能借条件写重置业务内容。
func ValidateWrite(current *Task, next Task) error {
	if next.Validate() != nil {
		return ErrInvalid
	}
	p := next.Progress
	if current == nil {
		if p.State != "pending" || p.Generation != 1 || p.TotalAttempts != 0 || p.PreviousUnconfirmed || !p.UpdatedAt.Equal(next.CreatedAt) {
			return ErrInvalid
		}
		return nil
	}
	old, n := current.Clone(), next.Clone()
	old.Progress, n.Progress = Progress{}, Progress{}
	if !reflect.DeepEqual(old, n) {
		return ErrConflict
	}
	c := current.Progress
	if p.UpdatedAt.Before(c.UpdatedAt) || c.PreviousUnconfirmed && !p.PreviousUnconfirmed {
		return ErrInvalid
	}
	if !c.PreviousUnconfirmed && p.PreviousUnconfirmed && (c.State != "sending" || c.Projection == nil) {
		return ErrInvalid
	}
	if p.Receipt != nil && (c.State != "sending" || !reflect.DeepEqual(p.Projection, c.Projection)) {
		return ErrInvalid
	}
	if c.State == "sending" && c.Projection != nil && p.Receipt == nil && !p.PreviousUnconfirmed {
		if p.State == "sending" || p.State == "waiting_projection" || p.State == "skipped" || (p.State == "retry" || p.State == "failed") && uncertainFailure(p.ErrorCode) {
			return ErrInvalid
		}
	}
	sameCycle := p.Generation == c.Generation && reflect.DeepEqual(p.LastRetry, c.LastRetry)
	sameAttempts := p.Attempts == c.Attempts && p.TotalAttempts == c.TotalAttempts
	ready := c.DueAt != nil && !p.UpdatedAt.Before(*c.DueAt) || c.LeaseUntil != nil && !p.UpdatedAt.Before(*c.LeaseUntil)
	switch {
	case current.Unsettled() && p.State == "skipped" && sameCycle && sameAttempts:
		if c.State == "sending" && p.Receipt == nil && !ready {
			return ErrInvalid
		}
		if p.Receipt != nil && c.State != "sending" {
			return ErrInvalid
		}
		return nil
	case c.State == "failed" && p.State == "pending":
		if p.Generation == c.Generation+1 && p.Attempts == 0 && p.TotalAttempts == c.TotalAttempts && p.LastRetry != nil && p.LastRetry.RequestedAt.Equal(p.UpdatedAt) && (c.LastRetry == nil || p.LastRetry.Command.OperationID != c.LastRetry.Command.OperationID) {
			return nil
		}
	case current.HasWork() && (p.State == "sending" || p.State == "waiting_projection") && ready && sameCycle:
		if p.State == "sending" && p.Attempts == c.Attempts+1 && p.TotalAttempts == c.TotalAttempts+1 {
			return nil
		}
		if p.State == "waiting_projection" && sameAttempts {
			return nil
		}
	case c.State == "sending" && sameCycle && sameAttempts && (p.State == "retry" || p.State == "failed" || p.State == "succeeded"):
		if p.State == "succeeded" && !reflect.DeepEqual(p.Projection, c.Projection) {
			return ErrInvalid
		}
		return nil
	}
	return ErrInvalid
}

// Validate 限制管理范围与扫描页；跨租户扫描只允许内部待办模式。
func (q Query) Validate() error {
	if q.Limit < 1 || q.Limit > 16 || q.TenantID != "" && domain.ValidateIdentityPart("tenant", q.TenantID, 64) != nil || q.After != "" && !validHash(q.After) || q.TenantID == "" && !q.WorkOnly {
		return ErrInvalid
	}
	return nil
}

// Failure 保存固定错误码及是否可自动重试，禁止包含远端正文或连接凭据。
type Failure struct {
	// Code 是固定安全分类，不携带远端正文或凭据。
	Code string
	// Retryable 决定是否在本周期剩余次数内退避。
	Retryable bool
}

func (f Failure) Error() string { return "action delivery: " + f.Code }

func validFailureCode(code string) bool {
	switch code {
	case "projection_pending", "projection_unavailable", "target_unavailable", "transport_failed", "remote_unauthorized", "identity_conflict", "remote_unavailable", "remote_rejected", "response_too_large", "response_invalid", "visibility_pending", "attempt_interrupted", "superseded_by_terminal":
		return true
	}
	return false
}

func uncertainFailure(code string) bool {
	switch code {
	case "target_unavailable", "remote_unauthorized":
		return false
	}
	return true
}
