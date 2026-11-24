// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package shieldcheck 保存屏蔽控制面诊断和显式复查请求；不修改 Alert、策略或处置资格。
package shieldcheck

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
)

// MaxDocumentBytes 同时限制最近诊断和包含诊断的请求记录；覆盖最多十六个绑定与二百五十六个候选步骤。
const MaxDocumentBytes = 256 << 10

// Documents 显式区分精确 CAS、历史分页和有界待办扫描；实现须同时保存 pending 索引。
type Documents interface {
	Get(context.Context, string, string) (json.RawMessage, string, error)
	Put(context.Context, string, string, string, json.RawMessage) error
	List(context.Context, string, string, string, int) ([]json.RawMessage, error)
	ListShieldCheckRequests(context.Context, string, string, int) ([]json.RawMessage, error)
	CountShieldCheckRequests(context.Context, string, int) (int, error)
}

// Command 的稳定 operation_id 在同一租户和 Alert 内唯一；重试不能更换版本、操作者或原因。
type Command struct {
	TenantID         string `json:"bk_tenant_id"`
	AlertID          string `json:"alert_id"`
	OperationID      string `json:"operation_id"`
	ExpectedRevision int64  `json:"expected_revision"`
	OperatorID       string `json:"operator_id"`
	Reason           string `json:"reason"`
}

// Validate 检查显式操作者、原因、稳定身份与安全整数业务版本。
func (c Command) Validate() error {
	if validScope(c.TenantID, c.AlertID) != nil || domain.ValidateIdentityPart("operation", c.OperationID, 128) != nil || c.ExpectedRevision < 1 || c.ExpectedRevision >= 1<<53 || strings.TrimSpace(c.OperatorID) == "" || len(c.OperatorID) > 256 || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 1024 {
		return policy.ErrInvalid
	}
	return nil
}

// Check 是一次检查的安全诊断。Report.Changed=false 且有错误时，不能推断状态没有部分生效。
type Check struct {
	TenantID   string                      `json:"bk_tenant_id"`
	AlertID    string                      `json:"alert_id"`
	Trigger    string                      `json:"trigger"`
	RequestID  string                      `json:"request_id,omitempty"`
	StartedAt  time.Time                   `json:"started_at"`
	FinishedAt time.Time                   `json:"finished_at"`
	ErrorCode  string                      `json:"error_code,omitempty"`
	Report     lifecycle.ShieldCheckReport `json:"report"`
}

// Validate 拒绝范围、阶段和版本相互矛盾的诊断。
func (c Check) Validate() error {
	if validScope(c.TenantID, c.AlertID) != nil || c.StartedAt.IsZero() || c.FinishedAt.Before(c.StartedAt) || (c.Trigger != "timer" && c.Trigger != "request" && c.Trigger != "hint") || (c.Trigger == "request") != (c.RequestID != "") || (c.RequestID != "" && !hashID(c.RequestID)) {
		return policy.ErrInvalid
	}
	if c.ErrorCode != "" && !slices.Contains([]string{"revision_changed", "scope_mismatch", "invalid_state", "record_missing", "configuration_unavailable", "alert_busy", "check_timeout", "check_cancelled", "version_conflict", "dependency_failed"}, c.ErrorCode) {
		return policy.ErrInvalid
	}
	r := c.Report
	if !slices.Contains([]string{"failed", "superseded", "inactive", "retained", "changed", "partial"}, r.Outcome) || r.ObservedRevision < 0 || r.ObservedRevision >= 1<<53 || r.ResultRevision < r.ObservedRevision || r.ResultRevision >= 1<<53 || r.RemainingBindings < 0 || r.RemainingBindings > 16 {
		return policy.ErrInvalid
	}
	if (r.Outcome == "failed" || r.Outcome == "superseded") != (c.ErrorCode != "") {
		return policy.ErrInvalid
	}
	if (r.Outcome == "superseded") != (c.ErrorCode == "revision_changed") || ((r.Outcome == "inactive" || r.Outcome == "retained" || r.Outcome == "superseded") && r.Changed) || (r.Outcome == "changed" && !r.Changed) {
		return policy.ErrInvalid
	}
	if r.ObservedRevision == 0 {
		if r.Changed || r.Decision != nil || r.ResultRevision != 0 || r.Outcome != "failed" {
			return policy.ErrInvalid
		}
	} else if r.CheckedAt.IsZero() || r.ResultRevision != r.ObservedRevision+int64(boolInt(r.Changed)) {
		return policy.ErrInvalid
	}
	if r.Decision != nil {
		if r.Decision.Validate() != nil {
			return policy.ErrInvalid
		}
		bound, candidates := 0, 0
		for _, step := range r.Decision.Steps {
			if step.Main != nil {
				return policy.ErrInvalid
			} // 复查不得登记新的依赖主。
			if step.FromBinding {
				bound++
				if !slices.Contains([]string{"retained", "released", "skipped"}, step.Outcome) {
					return policy.ErrInvalid
				}
			} else {
				candidates++
				if !slices.Contains([]string{"bound", "not_matched", "skipped"}, step.Outcome) || (step.Outcome == "bound") != (step.BindingID != "") {
					return policy.ErrInvalid
				}
			}
		}
		if bound > 16 || candidates > 256 {
			return policy.ErrInvalid
		}
	}
	return nil
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// Request 同时保存不可变命令、排队时间和最终诊断；失败请求不会自动创建新 operation。
type Request struct {
	ID          string     `json:"id"`
	Command     Command    `json:"command"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	Result      *Check     `json:"result,omitempty"`
}

// Validate 检查不可变命令身份、排队状态和最终结果的一致性。
func (r Request) Validate() error {
	if r.Command.Validate() != nil || r.ID != requestID(r.Command) || r.CreatedAt.IsZero() || !slices.Contains([]string{"pending", "completed", "failed", "superseded"}, r.State) {
		return policy.ErrInvalid
	}
	if r.State == "pending" {
		if r.Result != nil || r.CompletedAt != nil {
			return policy.ErrInvalid
		}
		return nil
	}
	if r.Result == nil || r.Result.Validate() != nil || r.CompletedAt == nil || r.CompletedAt.Before(r.CreatedAt) || !r.CompletedAt.Equal(r.Result.FinishedAt) || r.Result.TenantID != r.Command.TenantID || r.Result.AlertID != r.Command.AlertID || r.Result.Trigger != "request" || r.Result.RequestID != r.ID {
		return policy.ErrInvalid
	}
	want := "completed"
	if r.Result.Report.Outcome == "superseded" {
		want = "superseded"
	} else if r.Result.ErrorCode != "" {
		want = "failed"
	}
	if (r.State == "completed" && r.Result.Report.ObservedRevision != r.Command.ExpectedRevision) || (r.State == "superseded" && r.Result.Report.ObservedRevision == r.Command.ExpectedRevision) {
		return policy.ErrInvalid
	}
	if r.State != want {
		return policy.ErrInvalid
	}
	return nil
}

// StoredRequest 的 Version 只用于本存储 CAS，不是业务 Alert revision。
type StoredRequest struct {
	Request Request
	Version string
}

// Page 的 Next 是最后扫描身份；已完成记录仍在历史页，工作页只返回 pending。
type Page struct {
	Items []Request
	Next  string
}

// Journal 管理本部署的检查记录；调用者负责显式请求的租户准入锁和同请求执行锁。
type Journal struct{ docs Documents }

// NewJournal 绑定当前部署的持久化控制记录存储。
func NewJournal(docs Documents) (*Journal, error) {
	if docs == nil {
		return nil, policy.ErrInvalid
	}
	return &Journal{docs}, nil
}

func validScope(tenant, alert string) error {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || alert == "" || len(alert) > domain.EntityIDMaxBytes {
		return policy.ErrInvalid
	}
	return nil
}

func hash(parts ...string) string {
	raw, _ := json.Marshal(parts)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func hashID(v string) bool {
	raw, err := hex.DecodeString(v)
	return err == nil && len(raw) == 32 && strings.ToLower(v) == v
}

func tenantPrefix(tenant string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(tenant)) + ":"
}

func alertPrefix(tenant, alert string) string {
	return tenantPrefix(tenant) + hash("alert", alert) + ":"
}

func requestID(c Command) string {
	return hash("shield-check-request", c.TenantID, c.AlertID, c.OperationID)
}

func requestKey(r Request) string { return alertPrefix(r.Command.TenantID, r.Command.AlertID) + r.ID }

func decodeRequest(raw json.RawMessage) (Request, error) {
	var r Request
	if len(raw) > MaxDocumentBytes || json.Unmarshal(raw, &r) != nil || r.Validate() != nil {
		return r, policy.ErrInvalid
	}
	return r, nil
}

// FindCommand 优先识别重复提交，已有请求完成后也不能被新 Alert 版本重新执行。
func (j *Journal) FindCommand(ctx context.Context, c Command) (StoredRequest, error) {
	if c.Validate() != nil {
		return StoredRequest{}, policy.ErrInvalid
	}
	r, err := j.GetRequest(ctx, c.TenantID, c.AlertID, requestID(c))
	if err != nil {
		return r, err
	}
	if !reflect.DeepEqual(r.Request.Command, c) {
		return StoredRequest{}, policy.ErrConflict
	}
	return r, nil
}

// Enqueue 仅在租户准入锁内调用，读取真实 Alert 版本后才创建。相同命令重试复用首次 CreatedAt。
func (j *Journal) Enqueue(ctx context.Context, c Command, at time.Time) (StoredRequest, error) {
	if c.Validate() != nil || at.IsZero() {
		return StoredRequest{}, policy.ErrInvalid
	}
	if r, err := j.FindCommand(ctx, c); err == nil {
		return r, nil
	} else if !errors.Is(err, policy.ErrNotFound) {
		return r, err
	}
	n, err := j.docs.CountShieldCheckRequests(ctx, tenantPrefix(c.TenantID), 1024)
	if err != nil {
		return StoredRequest{}, err
	}
	if n >= 1024 {
		return StoredRequest{}, policy.ErrPreviewCapacity
	}
	r := Request{ID: requestID(c), Command: c, State: "pending", CreatedAt: at.Round(0).UTC()}
	raw, _ := json.Marshal(r)
	if len(raw) > MaxDocumentBytes {
		return StoredRequest{}, policy.ErrInvalid
	}
	if err := j.docs.Put(ctx, "shield_requests", requestKey(r), "", raw); err != nil && !errors.Is(err, policy.ErrConflict) {
		return StoredRequest{}, err
	}
	return j.FindCommand(ctx, c)
}

// GetRequest 按明确租户和 Alert 精确读取请求，不接受跨作用域记录。
func (j *Journal) GetRequest(ctx context.Context, tenant, alert, id string) (StoredRequest, error) {
	if validScope(tenant, alert) != nil || !hashID(id) {
		return StoredRequest{}, policy.ErrInvalid
	}
	raw, version, err := j.docs.Get(ctx, "shield_requests", alertPrefix(tenant, alert)+id)
	if err != nil {
		return StoredRequest{}, err
	}
	r, err := decodeRequest(raw)
	if err != nil || version == "" {
		return StoredRequest{}, policy.ErrInvalid
	}
	if r.Command.TenantID != tenant || r.Command.AlertID != alert || r.ID != id {
		return StoredRequest{}, policy.ErrAccess
	}
	return StoredRequest{r, version}, nil
}

// Finish 只完成当前 pending 请求，调用方须持有同请求租约；最终结果不允许再次覆写。
func (j *Journal) Finish(ctx context.Context, current StoredRequest, result Check) (StoredRequest, error) {
	if current.Version == "" || current.Request.Validate() != nil || result.Validate() != nil || current.Request.State != "pending" {
		return StoredRequest{}, policy.ErrInvalid
	}
	r := current.Request
	r.Result = &result
	at := result.FinishedAt
	r.CompletedAt = &at
	r.State = "completed"
	if result.Report.Outcome == "superseded" {
		r.State = "superseded"
	} else if result.ErrorCode != "" {
		r.State = "failed"
	}
	if r.Validate() != nil {
		return StoredRequest{}, policy.ErrInvalid
	}
	raw, _ := json.Marshal(r)
	if len(raw) > MaxDocumentBytes {
		return StoredRequest{}, policy.ErrInvalid
	}
	if err := j.docs.Put(ctx, "shield_requests", requestKey(r), current.Version, raw); err != nil {
		return StoredRequest{}, err
	}
	return j.GetRequest(ctx, r.Command.TenantID, r.Command.AlertID, r.ID)
}

// SaveLatest 只更新一个 Alert 的最新诊断，旧的慢请求不覆盖已经记录的较新检查。
func (j *Journal) SaveLatest(ctx context.Context, c Check) error {
	if c.Validate() != nil {
		return policy.ErrInvalid
	}
	key := alertPrefix(c.TenantID, c.AlertID) + "latest"
	for range 4 {
		old, version, err := j.docs.Get(ctx, "shield_checks", key)
		if err != nil && !errors.Is(err, policy.ErrNotFound) {
			return err
		}
		if errors.Is(err, policy.ErrNotFound) {
			version = ""
		}
		if err == nil {
			var found Check
			if len(old) > MaxDocumentBytes || json.Unmarshal(old, &found) != nil || found.Validate() != nil {
				return policy.ErrInvalid
			}
			if found.TenantID != c.TenantID || found.AlertID != c.AlertID {
				return policy.ErrAccess
			}
			if found.StartedAt.After(c.StartedAt) || reflect.DeepEqual(found, c) {
				return nil
			}
		}
		raw, _ := json.Marshal(c)
		if len(raw) > MaxDocumentBytes {
			return policy.ErrInvalid
		}
		if err := j.docs.Put(ctx, "shield_checks", key, version, raw); errors.Is(err, policy.ErrConflict) {
			continue
		} else {
			return err
		}
	}
	return policy.ErrConflict
}

// Latest 读取最近保存的检查事实；没有记录不等同于检查成功。
func (j *Journal) Latest(ctx context.Context, tenant, alert string) (Check, error) {
	if validScope(tenant, alert) != nil {
		return Check{}, policy.ErrInvalid
	}
	raw, _, err := j.docs.Get(ctx, "shield_checks", alertPrefix(tenant, alert)+"latest")
	if err != nil {
		return Check{}, err
	}
	var c Check
	if len(raw) > MaxDocumentBytes || json.Unmarshal(raw, &c) != nil || c.Validate() != nil {
		return Check{}, policy.ErrInvalid
	}
	if c.TenantID != tenant || c.AlertID != alert {
		return Check{}, policy.ErrAccess
	}
	return c, nil
}

// List 按不可变请求身份分页返回指定 Alert 的操作历史。
func (j *Journal) List(ctx context.Context, tenant, alert, after string, limit int) (Page, error) {
	if validScope(tenant, alert) != nil {
		return Page{}, policy.ErrInvalid
	}
	prefix := alertPrefix(tenant, alert)
	if after != "" && !strings.HasPrefix(after, prefix) {
		return Page{}, policy.ErrInvalid
	}
	return j.page(ctx, prefix, after, limit, false)
}

// Work 只供后台跨租户枚举 pending；执行前须重新获取实时记录。
func (j *Journal) Work(ctx context.Context, after string, limit int) (Page, error) {
	return j.page(ctx, "", after, limit, true)
}

func (j *Journal) page(ctx context.Context, prefix, after string, limit int, work bool) (Page, error) {
	if len(after) > 256 || limit < 1 || limit > 16 {
		return Page{}, policy.ErrInvalid
	}
	var raw []json.RawMessage
	var err error
	if work {
		raw, err = j.docs.ListShieldCheckRequests(ctx, prefix, after, limit)
	} else {
		raw, err = j.docs.List(ctx, "shield_requests", prefix, after, limit)
	}
	if err != nil {
		return Page{}, err
	}
	if len(raw) > limit {
		return Page{}, policy.ErrInvalid
	}
	p := Page{Items: []Request{}}
	last := after
	for _, b := range raw {
		r, err := decodeRequest(b)
		if err != nil {
			return Page{}, err
		}
		key := requestKey(r)
		if key <= last || !strings.HasPrefix(key, prefix) || (work && r.State != "pending") {
			return Page{}, fmt.Errorf("%w: invalid check request page", policy.ErrInvalid)
		}
		last = key
		p.Items = append(p.Items, r)
	}
	if len(raw) == limit {
		p.Next = last
	}
	return p, nil
}
