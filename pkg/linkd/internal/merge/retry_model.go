// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

// RetryCommand 固定用户观察到的裁决或关系版本，不提供重置阶段、重选成员或强制合并字段。
type RetryCommand struct {
	// TenantID 是明确租户作用域。
	TenantID string `json:"bk_tenant_id"`
	// Kind 只允许 decisions 或 relations，与管理资源路径一致。
	Kind string `json:"kind"`
	// TargetID 是原持久裁决/关系身份，不是临时 Redis 窗口。
	TargetID string `json:"target_id"`
	// ExpectedToken 来自精确控制点，执行前在正式窗口租约内再次比较。
	ExpectedToken string `json:"expected_token"`
	// OperationID 标识一次显式意图，相同身份必须重投完全相同内容。
	OperationID string `json:"operation_id"`
	// OperatorID 由可信管理调用者提供；Console 从服务端认证取值。
	OperatorID string `json:"operator_id"`
	// Reason 原样保存非空白原因，最多 1024 字节。
	Reason string `json:"reason"`
}

func retryHash(s string) bool { return validHash(s) && strings.ToLower(s) == s }

func retryScope(tenant, kind, id string) bool {
	return domain.ValidateIdentityPart("tenant", tenant, 64) == nil && (kind == "decisions" || kind == "relations") && retryHash(id)
}

// Validate 拒绝不完整意图和无效作用域。
func (c RetryCommand) Validate() error {
	if !retryScope(c.TenantID, c.Kind, c.TargetID) || !retryHash(c.ExpectedToken) || domain.ValidateIdentityPart("operation", c.OperationID, 128) != nil || strings.TrimSpace(c.OperatorID) == "" || len(c.OperatorID) > 256 || strings.TrimSpace(c.Reason) == "" || len(c.Reason) > 1024 {
		return policy.ErrInvalid
	}
	return nil
}

func (c RetryCommand) id() string {
	return hash("merge-request:v1", c.TenantID, c.Kind, c.TargetID, c.OperationID)
}

func retryPrefix(tenant, kind, id string) string { return prefix(tenant) + hash(kind, id) + ":" }

// ControlPoint 是实时持久记录的有界观察，不复制配置、成员 payload 或父 Event。
// Token 绑定真实存储版本；不能把进度计数相同解释为仍是用户观察到的版本。
type ControlPoint struct {
	// TenantID 是精确记录所属租户。
	TenantID string `json:"bk_tenant_id"`
	// Kind 区分 decisions 和 relations，二者不能互换 token。
	Kind string `json:"kind"`
	// TargetID 是已验证存在的持久记录身份。
	TargetID string `json:"target_id"`
	// WindowID 选择与自动任务共用的窗口租约。
	WindowID string `json:"window_id"`
	// Token 是包含存储版本的摘要，不允许客户端生成新版本代替后台复核。
	Token string `json:"token"`
	// Phase 是裁决阶段或关系状态，不是 Alert 生命周期。
	Phase string `json:"phase"`
	// Outcome 仅用于裁决；failed 表示业务条件未满足，不表示执行异常。
	Outcome string `json:"outcome,omitempty"`
	// Complete 表示裁决及窗口清理已完成，或关系已 ended。
	Complete bool `json:"complete"`
	// UpdatedAt 是真实记录更新时间，UTC。
	UpdatedAt time.Time `json:"updated_at"`
	// CaptureOffset 是裁决已捕获成员快照的前缀，最多 256。
	CaptureOffset int `json:"capture_offset"`
	// MemberOffset 是裁决已处理等待成员的前缀，最多 256。
	MemberOffset int `json:"member_offset"`
	// IndexOffset 是父及成员反查引用已建立的前缀，最多 257。
	IndexOffset int `json:"index_offset"`
	// EndOffset 是关系已解除等待成员的前缀，最多 256。
	EndOffset int `json:"end_offset"`
}

// Validate 确保摘要可以安全用作请求依据，不把预计身份或未知阶段展示成有效控制点。
func (p ControlPoint) Validate() error {
	id, e := domain.MergeDecisionID(p.TenantID, p.WindowID)
	if !retryScope(p.TenantID, p.Kind, p.TargetID) || e != nil || id != p.TargetID || !retryHash(p.Token) || p.UpdatedAt.IsZero() || p.CaptureOffset < 0 || p.CaptureOffset > 256 || p.MemberOffset < 0 || p.MemberOffset > 256 || p.IndexOffset < 0 || p.IndexOffset > 257 || p.EndOffset < 0 || p.EndOffset > 256 {
		return policy.ErrInvalid
	}
	if p.Kind == "decisions" {
		if p.Outcome != "succeeded" && p.Outcome != "failed" {
			return policy.ErrInvalid
		}
		if p.IndexOffset != 0 || p.EndOffset != 0 || (p.Complete && p.Phase != "completed") {
			return policy.ErrInvalid
		}
		switch p.Phase {
		case "capturing", "prepared", "waiting_parent", "linking", "ending", "releasing", "completed":
		default:
			return policy.ErrInvalid
		}
	} else {
		if p.Outcome != "" || p.CaptureOffset != 0 || p.MemberOffset != 0 || p.Complete != (p.Phase == "ended") {
			return policy.ErrInvalid
		}
		switch p.Phase {
		case "preparing", "ready", "ending", "ended":
		default:
			return policy.ErrInvalid
		}
	}
	return nil
}

// ReadControlPoint 实时读取并验证原记录；不会按 Redis 状态重建丢失的业务裁决。
func (j *Journal) ReadControlPoint(ctx context.Context, tenant, kind, id string) (ControlPoint, error) {
	if !retryScope(tenant, kind, id) {
		return ControlPoint{}, policy.ErrInvalid
	}
	p := ControlPoint{TenantID: tenant, Kind: kind, TargetID: id}
	version := ""
	if kind == "decisions" {
		s, e := j.Get(ctx, tenant, id)
		if e != nil {
			return p, e
		}
		d := s.Decision
		p.WindowID, p.Phase, p.Outcome, p.UpdatedAt = d.WindowID, d.Progress.Phase, d.Outcome, d.Progress.UpdatedAt
		p.CaptureOffset, p.MemberOffset = d.Progress.CaptureOffset, d.Progress.MemberOffset
		p.Complete = d.Progress.Phase == "completed" && d.Progress.WindowFinished
		version = s.Version
	} else {
		s, e := j.GetRelation(ctx, tenant, id)
		if e != nil {
			return p, e
		}
		r := s.Relation
		p.WindowID, p.Phase, p.UpdatedAt = r.WindowID, r.State, r.UpdatedAt
		p.IndexOffset, p.EndOffset = r.IndexOffset, r.EndOffset
		p.Complete = r.State == "ended"
		version = s.Version
	}
	p.UpdatedAt = p.UpdatedAt.Round(0).UTC()
	p.Token = hash("merge-control:v1", tenant, kind, id, version)
	return p, p.Validate()
}

// RetryResult 是一次有界执行尝试的确认，不表示整项合并已完成。
type RetryResult struct {
	// CheckedAt 是本次结果确认时间。
	CheckedAt time.Time `json:"checked_at"`
	// Outcome 为 advanced/unchanged/superseded/failed。
	Outcome string `json:"outcome"`
	// Reason 只允许固定安全代码，不保存底层错误。
	Reason string `json:"reason"`
	// StepAttempted 只表明正式步骤被调用；失败时可能已经发生部分副作用。
	StepAttempted bool `json:"step_attempted"`
	// Before 是窗口租约内的执行前观察，读取失败时不得伪造。
	Before *ControlPoint `json:"before,omitempty"`
	// After 是同一窗口租约内的执行后观察，不替代所有业务副作用的事务确认。
	After *ControlPoint `json:"after,omitempty"`
}

// Validate 验证观察范围和结果分类；失败结果不能推断没有副作用。
func (r RetryResult) Validate(c RetryCommand, window string) error {
	if c.Validate() != nil || r.CheckedAt.IsZero() {
		return policy.ErrInvalid
	}
	for _, p := range []*ControlPoint{r.Before, r.After} {
		if p != nil && (p.Validate() != nil || p.TenantID != c.TenantID || p.Kind != c.Kind || p.TargetID != c.TargetID || p.WindowID != window) {
			return policy.ErrInvalid
		}
	}
	if r.After != nil && r.Before == nil {
		return policy.ErrInvalid
	}
	if r.StepAttempted && (r.Before == nil || r.Before.Token != c.ExpectedToken || r.Before.Complete) {
		return policy.ErrInvalid
	}
	switch r.Outcome {
	case "superseded":
		if r.Reason != "target_changed" || r.Before == nil || r.Before.Token == c.ExpectedToken || r.After != nil || r.StepAttempted {
			return policy.ErrInvalid
		}
	case "advanced", "unchanged":
		if r.Before == nil || r.After == nil || r.Before.Token != c.ExpectedToken {
			return policy.ErrInvalid
		}
		if r.Outcome == "advanced" {
			if r.Reason != "progressed" || !r.StepAttempted || r.Before.Token == r.After.Token {
				return policy.ErrInvalid
			}
		} else {
			if r.Before.Token != r.After.Token {
				return policy.ErrInvalid
			}
			if r.Before.Complete {
				if r.Reason != "already_complete" || r.StepAttempted {
					return policy.ErrInvalid
				}
			} else if r.Reason != "no_progress" || !r.StepAttempted {
				return policy.ErrInvalid
			}
		}
	case "failed":
		switch r.Reason {
		case "target_missing", "scope_mismatch", "invalid_state", "dependency_failed", "execution_unavailable", "step_timeout":
		default:
			return policy.ErrInvalid
		}
	default:
		return policy.ErrInvalid
	}
	return nil
}

// RetryRequest 保存完整命令、原窗口与执行前后诊断；终态不可覆盖。
type RetryRequest struct {
	// ID 是由租户、资源、目标及 operation 派生的稳定请求身份。
	ID string `json:"id"`
	// Command 是不可变的原始操作意图。
	Command RetryCommand `json:"command"`
	// WindowID 在请求首次接受时由真实目标记录确定，不由客户端指定。
	WindowID string `json:"window_id"`
	// State 为 pending/completed/failed/superseded，与业务 outcome 分开。
	State string `json:"state"`
	// CreatedAt 是第一次入队时间，重投不刷新。
	CreatedAt time.Time `json:"created_at"`
	// StartedAt 先于第一次执行保存，中断重试沿用原值。
	StartedAt *time.Time `json:"started_at,omitempty"`
	// CompletedAt 是本次最终结果保存的确认时间，pending 时缺省。
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	// PreviousUnconfirmed 表示此前已有开始记录但未保存最终结果。
	PreviousUnconfirmed bool `json:"previous_unconfirmed"`
	// Result 只在请求终态保存，完成后不覆盖。
	Result *RetryResult `json:"result,omitempty"`
}

// Cursor 是控制面内部稳定扫描键，管理接口另绑定查询作用域。
func (r RetryRequest) Cursor() string {
	return retryPrefix(r.Command.TenantID, r.Command.Kind, r.Command.TargetID) + r.ID
}

// Validate 校验持久命令身份、阶段和时间边界。
func (r RetryRequest) Validate() error {
	id, e := domain.MergeDecisionID(r.Command.TenantID, r.WindowID)
	if r.Command.Validate() != nil || r.ID != r.Command.id() || e != nil || id != r.Command.TargetID || r.CreatedAt.IsZero() {
		return policy.ErrInvalid
	}
	if r.StartedAt != nil && (r.StartedAt.IsZero() || r.StartedAt.Before(r.CreatedAt)) {
		return policy.ErrInvalid
	}
	if r.PreviousUnconfirmed && r.StartedAt == nil {
		return policy.ErrInvalid
	}
	if r.State == "pending" {
		if r.Result != nil || r.CompletedAt != nil {
			return policy.ErrInvalid
		}
		return nil
	}
	if r.StartedAt == nil || r.Result == nil || r.Result.Validate(r.Command, r.WindowID) != nil || r.CompletedAt == nil || !r.CompletedAt.Equal(r.Result.CheckedAt) || r.CompletedAt.Before(*r.StartedAt) {
		return policy.ErrInvalid
	}
	expected := "completed"
	if r.Result.Outcome == "failed" || r.Result.Outcome == "superseded" {
		expected = r.Result.Outcome
	}
	if r.State != expected {
		return policy.ErrInvalid
	}
	return nil
}

// StoredRetry 保留请求 CAS 版本，不复用业务裁决 token。
type StoredRetry struct {
	// Request 是已验证的持久记录。
	Request RetryRequest
	// Version 是请求记录的独立 CAS token。
	Version string
}

// RetryPage 是单目标历史页或控制面待办页；空页仍可能有 Next。
type RetryPage struct {
	// Items 最多 16 项，管理 API 进一步限制为四项。
	Items []RetryRequest
	// Next 为最后一个内部记录键，空值表示本轮没有后续页。
	Next string
}
