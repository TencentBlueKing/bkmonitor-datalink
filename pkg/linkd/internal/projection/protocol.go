// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package projection 管理 Alert 到兼容存储的有版本快照和可靠写入；不重复执行告警策略或处置。
package projection

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"linkd/internal/domain"
)

const (
	// SchemaVersion 标识持久任务的业务快照，与旧 KAC Kafka 输入协议分开，不是 HTTP 状态接收协议。
	SchemaVersion = "linkd.kac-projection.v1"
	// MaxSnapshotBytes 限制一个完整业务快照，任务和 HTTP 请求另有小幅信封预算。
	MaxSnapshotBytes = 1 << 20
)

// Request 是一个完整 Linkd 业务快照；接收端按字段所有权更新 KAC，不整篇覆盖本地处置字段。
type Request struct {
	// SchemaVersion 显式标识新协议，不能发送到旧 Kafka Alarm 入口。
	SchemaVersion string `json:"schema_version"`
	// TenantID 隔离文档身份、版本和接收端操作。
	TenantID string `json:"bk_tenant_id"`
	// TargetID 标识当前目的端，确认不能跨目标复用。
	TargetID string `json:"target_id"`
	// AlertID 是 Linkd 生命周期身份。
	AlertID string `json:"linkd_alert_id"`
	// AlarmID 是同一生命周期始终不变的 KAC 兼容 UUID。
	AlarmID string `json:"alarm_id"`
	// Revision 是本次完整业务快照的版本。
	Revision int64 `json:"linkd_revision"`
	// ContentHash 是规范业务 JSON 的 SHA-256 小写摘要。
	ContentHash string `json:"content_hash"`
	// Alert 只包含共享业务事实，不含投影水位、待输出意图、定时复查元数据。
	Alert json.RawMessage `json:"alert"`
}

// AlarmID 仅由租户与 Alert 身份生成；跨版本、恢复和关闭均不生成新文档身份。
func AlarmID(tenant, alert string) (string, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || alert == "" || len(alert) > domain.EntityIDMaxBytes {
		return "", ErrInvalid
	}
	raw, _ := json.Marshal([]string{"linkd:kac-alert-projection:v1", tenant, alert})
	return "linkd-" + uuid.NewSHA1(uuid.NameSpaceURL, raw).String(), nil
}

// BuildRequest 规范化业务 JSON 并冻结摘要；ACK 和定时复查不造成同 revision 内容冲突。
func BuildRequest(a domain.Alert, target string) (Request, error) {
	a, err := a.Normalize()
	if err != nil {
		return Request{}, fmt.Errorf("%w: alert snapshot", ErrInvalid)
	}
	if domain.ValidateIdentityPart("target", target, 64) != nil {
		return Request{}, ErrInvalid
	}
	raw, err := json.Marshal(a)
	if err != nil {
		return Request{}, ErrInvalid
	}
	var body map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&body) != nil {
		return Request{}, ErrInvalid
	}
	delete(body, "projection")
	delete(body, "policy_change")
	delete(body, "merge_change")
	delete(body, "action_pending")
	shield, ok := body["shield"].(map[string]any)
	if !ok {
		return Request{}, ErrInvalid
	}
	delete(shield, "next_check_at")
	raw, err = json.Marshal(body)
	if err != nil || len(raw) > MaxSnapshotBytes {
		return Request{}, ErrInvalid
	}
	id, err := AlarmID(a.BKTenantID, a.AlertID)
	if err != nil {
		return Request{}, err
	}
	sum := sha256.Sum256(raw)
	req := Request{SchemaVersion: SchemaVersion, TenantID: a.BKTenantID, TargetID: target, AlertID: a.AlertID, AlarmID: id, Revision: a.Revision, ContentHash: hex.EncodeToString(sum[:]), Alert: raw}
	return req, req.Validate()
}

// Clone 返回独立的快照字节，任务不能持有调用方可修改的原始数据。
func (r Request) Clone() Request { r.Alert = bytes.Clone(r.Alert); return r }

// Validate 验证信封、摘要和快照作用域；输入必须来自已验证 Alert 的 BuildRequest。
func (r Request) Validate() error {
	id, err := AlarmID(r.TenantID, r.AlertID)
	if err != nil || r.SchemaVersion != SchemaVersion || r.AlarmID != id || domain.ValidateIdentityPart("target", r.TargetID, 64) != nil || r.Revision < 1 || r.Revision >= 1<<53 || len(r.Alert) == 0 || len(r.Alert) > MaxSnapshotBytes {
		return ErrInvalid
	}
	sum := sha256.Sum256(r.Alert)
	if r.ContentHash != hex.EncodeToString(sum[:]) {
		return ErrInvalid
	}
	var p struct {
		// TenantID 隔离文档身份、版本和接收端操作。
		TenantID string `json:"bk_tenant_id"`
		// AlertID 是 Linkd 生命周期身份。
		AlertID string `json:"alert_id"`
		// Revision 是本次完整业务快照的版本。
		Revision int64                      `json:"revision"`
		Status   domain.AlertStatus         `json:"status"`
		SourceID string                     `json:"event_source_id"`
		Shield   map[string]json.RawMessage `json:"shield"`
	}
	if json.Unmarshal(r.Alert, &p) != nil || p.TenantID != r.TenantID || p.AlertID != r.AlertID || p.Revision != r.Revision ||
		domain.ValidateIdentityPart("source", p.SourceID, 32) != nil || (p.Status != domain.AlertStatusActive && !p.Status.Terminal()) {
		return ErrInvalid
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(r.Alert, &fields) != nil {
		return ErrInvalid
	}
	for _, key := range []string{"projection", "policy_change", "merge_change", "action_pending"} {
		if _, exists := fields[key]; exists {
			return ErrInvalid
		}
	}
	if _, exists := p.Shield["next_check_at"]; exists {
		return ErrInvalid
	}
	return nil
}

// Receipt 是接收端对当前文档及搜索可见性的明确确认；HTTP 200 本身不构成成功。
type Receipt struct {
	// SchemaVersion 显式标识新协议，不能发送到旧 Kafka Alarm 入口。
	SchemaVersion string `json:"schema_version"`
	// TenantID 隔离文档身份、版本和接收端操作。
	TenantID string `json:"bk_tenant_id"`
	// TargetID 标识当前目的端，确认不能跨目标复用。
	TargetID string `json:"target_id"`
	// AlertID 是 Linkd 生命周期身份。
	AlertID string `json:"linkd_alert_id"`
	// AlarmID 是同一生命周期始终不变的 KAC 兼容 UUID。
	AlarmID string `json:"alarm_id"`
	// AppliedRevision 是接收端当前文档已经应用的版本。
	AppliedRevision int64 `json:"applied_revision"`
	// ContentHash 是规范业务 JSON 的 SHA-256 小写摘要。
	ContentHash string `json:"content_hash"`
	// AppliedStatus 是接收端当前 Linkd 生命周期状态，不是 KAC 本地处置态。
	AppliedStatus domain.AlertStatus `json:"applied_status"`
	// SearchVisible 必须明确为 true 才能确认本地水位。
	SearchVisible bool `json:"search_visible"`
	// DocumentRef 是接收端提供的稳定文档定位引用，不作为任意 URL 或索引表达式执行。
	DocumentRef string `json:"document_ref"`
}

// ValidateFor 允许接收端已经应用更高版本；只确认原请求版本，不擅自提升其他本地版本的水位。
func (r Receipt) ValidateFor(q Request) error {
	if q.Validate() != nil || r.SchemaVersion != SchemaVersion || r.TenantID != q.TenantID || r.TargetID != q.TargetID || r.AlertID != q.AlertID || r.AlarmID != q.AlarmID ||
		r.AppliedRevision < q.Revision || r.AppliedRevision >= 1<<53 || !r.SearchVisible || !validHash(r.ContentHash) || strings.TrimSpace(r.DocumentRef) == "" || len(r.DocumentRef) > 512 ||
		(r.AppliedStatus != domain.AlertStatusActive && !r.AppliedStatus.Terminal()) {
		return ErrInvalidReceipt
	}
	var a struct {
		Status domain.AlertStatus `json:"status"`
	}
	if json.Unmarshal(q.Alert, &a) != nil {
		return ErrInvalidReceipt
	}
	if a.Status.Terminal() && r.AppliedStatus != a.Status {
		return ErrInvalidReceipt
	}
	if r.AppliedRevision == q.Revision && (r.ContentHash != q.ContentHash || r.AppliedStatus != a.Status) {
		return ErrInvalidReceipt
	}
	return nil
}

func validHash(s string) bool {
	raw, e := hex.DecodeString(s)
	return e == nil && len(raw) == 32 && hex.EncodeToString(raw) == s
}
