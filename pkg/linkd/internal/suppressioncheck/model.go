// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package suppressioncheck 对一个明确 owner/代次执行持久化受控复核，不创建窗口或改变处置资格。
package suppressioncheck

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/suppressioncleanup"
)

// Command 固定管理员实际观察到的窗口身份；不确定重投必须携带完整相同命令。
type Command struct {
	// TenantID 明确租户隔离。
	TenantID string `json:"bk_tenant_id"`
	// Kind 只允许 clip/aggregation。
	Kind string `json:"kind"`
	// WindowID 使用运行态返回的身份，不接受 Redis 键。
	WindowID string `json:"window_id"`
	// ExpectedEpoch 固定观察到的代次，旧操作不得修改新一代窗口。
	ExpectedEpoch string `json:"expected_epoch"`
	// ExpectedOwner 固定 owner；空值只允许只读复核尚未绑定的防抖计数。
	ExpectedOwner string `json:"expected_owner_alert_id"`
	// OperationID 在同一租户、方式和窗口内唯一，不能更换内容复用。
	OperationID string `json:"operation_id"`
	// OperatorID 来自受信管理调用者，Console 不允许浏览器伪造。
	OperatorID string `json:"operator_id"`
	// Reason 原样保存非空白原因，最多 1024 字节。
	Reason string `json:"reason"`
}

// Validate 拒绝缺少身份或不完整的用户意图。
func (c Command) Validate() error {
	if domain.ValidateIdentityPart("tenant", c.TenantID, 64) != nil || (suppressioncleanup.Window{ID: c.WindowID, Epoch: c.ExpectedEpoch}).Validate(c.Kind) != nil || len(c.ExpectedOwner) > domain.EntityIDMaxBytes || (c.Kind == "aggregation" && c.ExpectedOwner == "") || domain.ValidateIdentityPart("operation", c.OperationID, 128) != nil || strings.TrimSpace(c.OperatorID) == "" || len(c.OperatorID) > 256 || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 1024 {
		return policy.ErrInvalid
	}
	return nil
}

func digest(parts ...string) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func hashID(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

func (c Command) id() string {
	return digest("suppression-request:v1", c.TenantID, c.Kind, c.WindowID, c.OperationID)
}

func tenantPrefix(tenant string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(tenant)) + ":"
}

func windowPrefix(tenant, kind, id string) string {
	return tenantPrefix(tenant) + digest(kind, id) + ":"
}

func recordKey(r Request) string {
	return windowPrefix(r.Command.TenantID, r.Command.Kind, r.Command.WindowID) + r.ID
}

// Cursor 返回受租户与窗口约束的内部扫描键，不替代管理接口的作用域游标。
func (r Request) Cursor() string { return recordKey(r) }

// Check 保存本次实时复核及有限副作用；Observed 是删除前实际读取的窗口，不是删除后的推断。
type Check struct {
	// Changed 只在 Lua 确认删除时为真；后续租约释放失败仍保留已确认副作用。
	Changed bool `json:"changed"`
	// CheckedAt 为本次检查时间，不作为业务身份。
	CheckedAt time.Time `json:"checked_at"`
	// Outcome 为 retained/cleared/absent/superseded/failed。
	Outcome string `json:"outcome"`
	// Reason 只保存固定安全原因，不保存底层错误或凭据。
	Reason string `json:"reason"`
	// Observed 可为空；缺失窗口时不伪造原窗口快照。
	Observed *redisstate.SuppressionWindow `json:"observed,omitempty"`
	// OwnerRevision 仅在实际读取并校验 owner 时保存。
	OwnerRevision int64 `json:"owner_revision,omitempty"`
	// OwnerStatus 是真实 owner 生命周期，不能从 Redis admitted 推断。
	OwnerStatus domain.AlertStatus `json:"owner_status,omitempty"`
}

// Validate 确认结果与操作身份一致；clear 必须有相同 owner/代次的删除前观察。
func (c Check) Validate(command Command) error {
	if command.Validate() != nil || c.CheckedAt.IsZero() {
		return policy.ErrInvalid
	}
	valid := map[string][]string{
		"retained": {"unbound_counter", "active_owner", "candidate_pending"}, "cleared": {"terminal_owner", "owner_missing", "owner_not_admitted"},
		"absent": {"window_missing"}, "superseded": {"window_changed"},
		"failed": {"window_unavailable", "owner_unavailable", "scope_mismatch", "invalid_state", "cleanup_unavailable", "execution_unavailable"},
	}
	allowed := false
	for _, reason := range valid[c.Outcome] {
		if c.Reason == reason {
			allowed = true
		}
	}
	if !allowed {
		return policy.ErrInvalid
	}
	if c.OwnerRevision < 0 || c.OwnerRevision >= 1<<53 || (c.OwnerRevision == 0) != (c.OwnerStatus == "") {
		return policy.ErrInvalid
	}
	if c.OwnerRevision > 0 && c.OwnerStatus != domain.AlertStatusActive && !c.OwnerStatus.Terminal() {
		return policy.ErrInvalid
	}
	if c.Observed != nil {
		w := c.Observed
		if w.Validate() != nil || w.TenantID != command.TenantID || w.Kind != command.Kind || w.ID != command.WindowID {
			return policy.ErrInvalid
		}
		same := w.Epoch == command.ExpectedEpoch && w.OwnerAlertID == command.ExpectedOwner
		if (c.Outcome == "cleared" || c.Outcome == "retained") && !same {
			return policy.ErrInvalid
		}
	} else if c.Outcome == "cleared" || c.Outcome == "retained" {
		return policy.ErrInvalid
	}
	if (c.Outcome == "cleared" && !c.Changed) || (c.Changed && (c.Outcome != "cleared" && c.Outcome != "failed")) {
		return policy.ErrInvalid
	}
	if c.Changed && (c.Observed == nil || c.Observed.Epoch != command.ExpectedEpoch || c.Observed.OwnerAlertID != command.ExpectedOwner || command.ExpectedOwner == "") {
		return policy.ErrInvalid
	}
	if c.Outcome == "absent" && (c.Observed != nil || c.OwnerRevision != 0) {
		return policy.ErrInvalid
	}
	if c.Reason == "owner_missing" && c.OwnerRevision != 0 {
		return policy.ErrInvalid
	}
	if c.Reason == "terminal_owner" && (c.OwnerRevision == 0 || !c.OwnerStatus.Terminal()) {
		return policy.ErrInvalid
	}
	if c.Reason == "owner_not_admitted" && (c.OwnerRevision == 0 || c.OwnerStatus != domain.AlertStatusActive || command.Kind != "aggregation") {
		return policy.ErrInvalid
	}
	if c.Reason == "active_owner" && (c.OwnerRevision == 0 || c.OwnerStatus != domain.AlertStatusActive) {
		return policy.ErrInvalid
	}
	if c.Reason == "unbound_counter" && (command.Kind != "clip" || command.ExpectedOwner != "" || c.OwnerRevision != 0) {
		return policy.ErrInvalid
	}
	if c.Reason == "candidate_pending" && (c.OwnerRevision != 0 || c.Observed == nil || c.Observed.Kind != "aggregation" || c.Observed.State != "pending" || c.Observed.ObservedAtMillis > c.Observed.PendingUntilMillis) {
		return policy.ErrInvalid
	}
	return nil
}

// Request 保留首次完整命令和最终结果；pending 不等于未执行，PreviousUnconfirmed 明确保留中断边界。
type Request struct {
	// ID 由租户、方式、窗口和操作 ID 确定，不包含请求时间。
	ID string `json:"id"`
	// Command 保存完整原意图，不能在重试时刷新 owner 或代次。
	Command Command `json:"command"`
	// State 为 pending/completed/failed/superseded，最终状态不可再执行。
	State string `json:"state"`
	// CreatedAt 固定首次受理时间。
	CreatedAt time.Time `json:"created_at"`
	// StartedAt 在第一次执行前保存，pending 时可能已经发生副作用。
	StartedAt *time.Time `json:"started_at,omitempty"`
	// PreviousUnconfirmed 表示前次执行未完成结果持久化。
	PreviousUnconfirmed bool `json:"previous_unconfirmed"`
	// CompletedAt 是最终结果时间，只有最终状态存在。
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// Result 保存安全原因和删除前观察，pending 时为空。
	Result *Check `json:"result,omitempty"`
}

// Validate 校验阶段与结果，已完成请求不可重新变为 pending。
func (r Request) Validate() error {
	if r.Command.Validate() != nil || r.ID != r.Command.id() || r.CreatedAt.IsZero() || (r.StartedAt != nil && r.StartedAt.Before(r.CreatedAt)) || (r.PreviousUnconfirmed && r.StartedAt == nil) {
		return policy.ErrInvalid
	}
	if r.State == "pending" {
		if r.Result != nil || r.CompletedAt != nil {
			return policy.ErrInvalid
		}
		return nil
	}
	if r.Result == nil || r.Result.Validate(r.Command) != nil || r.StartedAt == nil || r.CompletedAt == nil || r.CompletedAt.Before(*r.StartedAt) || !r.CompletedAt.Equal(r.Result.CheckedAt) {
		return policy.ErrInvalid
	}
	want := "completed"
	if r.Result.Outcome == "failed" {
		want = "failed"
	}
	if r.Result.Outcome == "superseded" {
		want = "superseded"
	}
	if r.State != want {
		return policy.ErrInvalid
	}
	return nil
}

// Stored 的 Version 仅用于记录 CAS。
type Stored struct {
	// Request 为校验过的完整持久记录。
	Request Request
	// Version 是单对象 CAS token，不是窗口代次。
	Version string
}

// Page 是严格递增的请求身份页；Next 为最后扫描的完整存储键。
type Page struct {
	// Items 按完整存储身份严格递增。
	Items []Request
	// Next 是最后扫描键，满页时返回。
	Next string
}

// Documents 把 pending 索引与 payload 同次写入，容量/工作扫描不能全量加载已完成历史。
type Documents interface {
	Get(context.Context, string, string) (json.RawMessage, string, error)
	Put(context.Context, string, string, string, json.RawMessage) error
	List(context.Context, string, string, string, int) ([]json.RawMessage, error)
	CountSuppressionCheckRequests(context.Context, string, int) (int, error)
	ListSuppressionCheckRequests(context.Context, string, string, int) ([]json.RawMessage, error)
}
