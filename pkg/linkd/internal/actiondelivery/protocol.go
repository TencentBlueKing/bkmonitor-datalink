// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package actiondelivery 保存获准处置的独立冻结动作，并在投影可见后可靠投递。
// 本包不决定策略准入，不把实时 Alert 代替原动作快照，也不宣称接收确认等于处置执行完成。
package actiondelivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/projection"
)

const (
	// SchemaVersion 明确区分可靠动作协议、状态投影与旧 KAC Kafka 输入。
	SchemaVersion = "linkd.kac-action.v1"
	// MaxRequestBytes 包含一个固定业务快照及有界动作信封。
	MaxRequestBytes = projection.MaxSnapshotBytes + 4096
)

var (
	// ErrInvalid 表示作用域、准入证明或状态不合法。
	ErrInvalid = errors.New("invalid action delivery")
	// ErrNotFound 表示当前租户没有对应任务。
	ErrNotFound = errors.New("action delivery not found")
	// ErrConflict 表示任务身份/内容冲突或 CAS 失败。
	ErrConflict = errors.New("action delivery conflict")
	// ErrCapacity 表示租户待办预算已满，不能丢弃原动作意图。
	ErrCapacity = errors.New("action delivery capacity reached")
	// ErrBusy 表示投影尚不可见、退避/租约未到期或前序动作尚未完成。
	ErrBusy = errors.New("action delivery not ready")
	// ErrInvalidReceipt 表示未取得符合协议的持久受理证明。
	ErrInvalidReceipt = errors.New("invalid action receipt")
)

// Cause 固定原 Event 或控制操作身份，不能用发送时间兜底。
type Cause struct {
	// Type 只允许 source_event/user_operation/system_operation。
	Type string `json:"type"`
	// ID 是原操作身份，不是本次投递尝试身份。
	ID string `json:"id"`
}

// Validate 与 Lifecycle 的推动类型一致，并限制持久载荷预算。
func (c Cause) Validate() error {
	if c.ID == "" || len(c.ID) > domain.EntityIDMaxBytes {
		return ErrInvalid
	}
	switch c.Type {
	case "source_event", "user_operation", "system_operation":
		return nil
	default:
		return ErrInvalid
	}
}

// Request 是一次已获准动作的完整固定内容；Alert 字段与投影 V1 使用同一业务快照。
type Request struct {
	// SchemaVersion 仅允许新动作协议。
	SchemaVersion string `json:"schema_version"`
	// ActionID 包含稳定原因与版本，用于接收端的持久幂等受理。
	ActionID string `json:"action_id"`
	// TenantID 隔离文档和处置身份。
	TenantID string `json:"bk_tenant_id"`
	// TargetID 必须与目标投影的目的端一致。
	TargetID string `json:"target_id"`
	// AlertID 是 Linkd 生命周期身份。
	AlertID string `json:"linkd_alert_id"`
	// AlarmID 指向稳定 KAC 投影，不随动作变化。
	AlarmID string `json:"alarm_id"`
	// Revision 是动作对应版本，也是最低可见投影版本。
	Revision int64 `json:"linkd_revision"`
	// Action 只允许 firing/resolved/close，不从网络重试推导新动作。
	Action string `json:"action"`
	// Cause 是最初业务推动身份。
	Cause Cause `json:"cause"`
	// ContentHash 是 Alert 规范业务 JSON 的摘要。
	ContentHash string `json:"content_hash"`
	// Alert 保存原动作时的业务快照，之后状态变化不刷新它。
	Alert json.RawMessage `json:"alert"`
}

func digest(parts ...any) string {
	b, _ := json.Marshal(parts)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func validHash(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 32 && hex.EncodeToString(b) == s
}

func (q Request) identity() string {
	return digest("linkd:action:v1", q.TenantID, q.AlertID, q.TargetID, q.Revision, q.Action, q.Cause.Type, q.Cause.ID)
}

// ProjectionRequest 返回可验证同一动作最低投影要求的信封，不触发旧版本状态写入。
func (q Request) ProjectionRequest() projection.Request {
	return projection.Request{SchemaVersion: projection.SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, Revision: q.Revision, ContentHash: q.ContentHash, Alert: bytes.Clone(q.Alert)}
}

// BuildRequest 只接受明确获准触发或曾获准告警的终态；解屏、解联和普通状态变化不能借此产生动作。
func BuildRequest(a domain.Alert, target string, cause Cause) (Request, error) {
	if cause.Validate() != nil || a.Validate() != nil {
		return Request{}, ErrInvalid
	}
	q, e := projection.BuildRequest(a, target)
	if e != nil {
		return Request{}, ErrInvalid
	}
	action := "firing"
	switch a.Status {
	case domain.AlertStatusRecovered:
		action = "resolved"
	case domain.AlertStatusClosed:
		action = "close"
	}
	out := Request{SchemaVersion: SchemaVersion, TenantID: q.TenantID, TargetID: q.TargetID, AlertID: q.AlertID, AlarmID: q.AlarmID, Revision: q.Revision, Action: action, Cause: cause, ContentHash: q.ContentHash, Alert: q.Alert}
	out.ActionID = out.identity()
	return out, out.Validate()
}

// Clone 隔离冻结 JSON，不向调用者共享任务内存。
func (q Request) Clone() Request { q.Alert = bytes.Clone(q.Alert); return q }

// Validate 同时校验信封、真实业务快照与准入；不能仅凭 active 或解除关系生成 firing。
func (q Request) Validate() error {
	if q.SchemaVersion != SchemaVersion || q.Cause.Validate() != nil || q.ActionID != q.identity() || q.ProjectionRequest().Validate() != nil {
		return ErrInvalid
	}
	var a domain.Alert
	decoder := json.NewDecoder(bytes.NewReader(q.Alert))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&a) != nil || a.Validate() != nil {
		return ErrInvalid
	}
	// RawMessage 再编码会压缩空白并转义字符；只接受构造器的规范快照，避免保存成功后摘要失配。
	canonical, err := projection.BuildRequest(a, q.TargetID)
	if err != nil || !bytes.Equal(canonical.Alert, q.Alert) {
		return ErrInvalid
	}
	if a.Admission.AdmittedAt == nil {
		return ErrInvalid
	}
	switch q.Action {
	case "firing":
		if !a.AdmittedActiveMain() || !a.Admission.AdmittedAt.Equal(a.UpdateAt) || (q.Cause.Type == "source_event" && q.Cause.ID != a.LatestEventID) || a.Admission.CauseID != q.Cause.ID || a.Admission.CauseType != q.Cause.Type {
			return ErrInvalid
		}
	case "resolved":
		if a.Status != domain.AlertStatusRecovered {
			return ErrInvalid
		}
	case "close":
		if a.Status != domain.AlertStatusClosed {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	b, e := json.Marshal(q)
	if e != nil || len(b) > MaxRequestBytes {
		return ErrInvalid
	}
	return nil
}

// Hash 绑定包括原因/动作在内的完整原请求，不能只比较 Alert 内容摘要。
func (q Request) Hash() string {
	b, _ := json.Marshal(q)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Receipt 证明接收端已幂等持久受理或明确跳过过期动作，不代表通知/工单已经执行。
type Receipt struct {
	// SchemaVersion 必须与动作协议一致。
	SchemaVersion string `json:"schema_version"`
	// TenantID 必须与原请求租户一致。
	TenantID string `json:"bk_tenant_id"`
	// TargetID 必须与目标投影的目的端一致。
	TargetID string `json:"target_id"`
	// AlertID 必须与原生命周期一致。
	AlertID string `json:"linkd_alert_id"`
	// AlarmID 必须与原稳定投影身份一致。
	AlarmID string `json:"alarm_id"`
	// ActionID 必须与原动作身份一致。
	ActionID string `json:"action_id"`
	// RequestHash 必须是包括原原因和动作的完整摘要。
	RequestHash string `json:"request_hash"`
	// Outcome 为 accepted 或 skipped；重复受理返回原持久结果。
	Outcome string `json:"outcome"`
	// Reason 仅 skipped 时为 superseded_by_terminal。
	Reason string `json:"reason,omitempty"`
	// AppliedRevision 是接收端作出原持久决定时的可见投影版本。
	AppliedRevision int64 `json:"applied_revision"`
	// AppliedStatus 是该决定时的 Linkd 生命周期，不是 KAC 本地处置状态。
	AppliedStatus domain.AlertStatus `json:"applied_status"`
	// SearchVisible 必须明确为 true。
	SearchVisible bool `json:"search_visible"`
	// AcceptanceID 是接收端持久受理引用，重复返回相同值；不是任意可访问 URL。
	AcceptanceID string `json:"acceptance_id"`
}

// ValidateFor 拒绝假成功和旧 firing 重新激活较新终态；终态动作也必须观察到同种真实终态。
func (r Receipt) ValidateFor(q Request) error {
	if q.Validate() != nil || r.SchemaVersion != SchemaVersion || r.TenantID != q.TenantID || r.TargetID != q.TargetID || r.AlertID != q.AlertID || r.AlarmID != q.AlarmID || r.ActionID != q.ActionID || r.RequestHash != q.Hash() || r.AppliedRevision < q.Revision || r.AppliedRevision >= 1<<53 || !r.SearchVisible || strings.TrimSpace(r.AcceptanceID) == "" || len(r.AcceptanceID) > 256 {
		return ErrInvalidReceipt
	}
	if r.AppliedStatus != domain.AlertStatusActive && !r.AppliedStatus.Terminal() {
		return ErrInvalidReceipt
	}
	if r.Outcome == "skipped" {
		if r.Reason != "superseded_by_terminal" || q.Action != "firing" || r.AppliedRevision <= q.Revision || !r.AppliedStatus.Terminal() {
			return ErrInvalidReceipt
		}
		return nil
	}
	if r.Outcome != "accepted" || r.Reason != "" {
		return ErrInvalidReceipt
	}
	expected := domain.AlertStatusActive
	if q.Action == "resolved" {
		expected = domain.AlertStatusRecovered
	}
	if q.Action == "close" {
		expected = domain.AlertStatusClosed
	}
	if r.AppliedStatus != expected {
		return ErrInvalidReceipt
	}
	return nil
}

// Gate 仅返回同一固定目的端已经应用且搜索可见的投影证明；未达到要求返回 ErrBusy。
// 实现必须核对来源 Release/目标路由；不能用其他目标或后来改指到另一接收端的 ACK 充当证明。
// 本地已有较新终态时，必须等待该终态的可见证明，再跳过旧 firing，不能只复用旧活动版本水位。
type Gate interface {
	Check(context.Context, Task) (projection.Receipt, error)
}

// ValidateProjection 校验动作最低投影要求；旧 firing 可据较新终态证明安全停止新发送。
func ValidateProjection(q Request, r projection.Receipt) error {
	if q.Validate() != nil || r.ValidateFor(q.ProjectionRequest()) != nil {
		return ErrInvalidReceipt
	}
	return nil
}

func stale(q Request, r projection.Receipt) bool {
	return q.Action == "firing" && r.AppliedRevision > q.Revision && r.AppliedStatus.Terminal()
}

// DecodeReceipt 严格读取有界接收端确认，不能以 HTTP 200 代替持久受理与搜索可见性。
func DecodeReceipt(raw []byte, q Request) (Receipt, error) {
	if len(raw) > 64<<10 {
		return Receipt{}, Failure{Code: "response_too_large"}
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var r Receipt
	if d.Decode(&r) != nil {
		return Receipt{}, Failure{Code: "response_invalid"}
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		return Receipt{}, Failure{Code: "response_invalid"}
	}
	visible := r.SearchVisible
	r.SearchVisible = true
	if r.ValidateFor(q) != nil {
		return Receipt{}, Failure{Code: "response_invalid"}
	}
	if !visible {
		return Receipt{}, Failure{Code: "visibility_pending", Retryable: true}
	}
	return r, nil
}
