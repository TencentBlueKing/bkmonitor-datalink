// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package suppressioncheck

import (
	"context"
	"errors"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/policy/redisstate"
	"linkd/internal/store"
)

// Windows 只复核已有窗口并按完整 owner/代次删除，不提供计数或新建入口。
type Windows interface {
	ReadSuppressionWindow(context.Context, string, string, string) (redisstate.SuppressionWindow, bool, error)
	DeleteSuppressionWindow(context.Context, redisstate.SuppressionWindow) (bool, error)
}

// OwnerLease 必须与正式 Lifecycle 共用 tenant/source/fingerprint lease；调用方不同时持有两个 owner 锁。
type OwnerLease func(context.Context, string, string, string, func() error) error

// Engine 只读真实 owner，并在相同 lease 内复核与删除；不修改 Alert、Event、admission 或 Hook。
type Engine struct {
	windows Windows
	alert   func(context.Context, string, string) (store.StoredAlert, error)
	lease   OwnerLease
	now     func() time.Time
}

// NewEngine 要求实时 Alert 读取，不能注入状态搜索或近期缓存代替。
func NewEngine(w Windows, a func(context.Context, string, string) (store.StoredAlert, error), l OwnerLease, now func() time.Time) (*Engine, error) {
	if w == nil || a == nil || l == nil || now == nil {
		return nil, policy.ErrInvalid
	}
	return &Engine{w, a, l, now}, nil
}

func ownerScope(w redisstate.SuppressionWindow) (string, string) {
	if w.Kind == "clip" {
		return w.SourceID, w.Fingerprint
	}
	return w.OwnerSourceID, w.OwnerFingerprint
}

// Check 每次都受原命令约束；nil error 只说明检查完成，Changed 才说明本次确认发生删除。
func (e *Engine) Check(ctx context.Context, c Command) (result Check, err error) {
	result = Check{Outcome: "failed", Reason: "execution_unavailable", CheckedAt: e.now().Round(0).UTC()}
	defer func() { result.CheckedAt = e.now().Round(0).UTC() }()
	if c.Validate() != nil {
		return result, policy.ErrInvalid
	}
	read := func() (redisstate.SuppressionWindow, bool, error) {
		w, found, err := e.windows.ReadSuppressionWindow(ctx, c.TenantID, c.Kind, c.WindowID)
		if err != nil {
			result.Outcome = "failed"
			result.Reason = "window_unavailable"
			return w, false, err
		}
		if !found {
			result = Check{Outcome: "absent", Reason: "window_missing"}
			return w, false, nil
		}
		if w.TenantID != c.TenantID || w.Kind != c.Kind || w.ID != c.WindowID {
			result.Outcome = "failed"
			result.Reason = "scope_mismatch"
			return w, false, policy.ErrAccess
		}
		if w.Validate() != nil {
			result.Outcome = "failed"
			result.Reason = "invalid_state"
			return w, false, policy.ErrInvalid
		}
		result.Observed = &w
		if w.Epoch != c.ExpectedEpoch || w.OwnerAlertID != c.ExpectedOwner {
			result.Outcome = "superseded"
			result.Reason = "window_changed"
			return w, false, nil
		}
		// 正常占位租期内候选可能尚未创建 Alert，不能当作失效 owner 强制释放。
		if w.Kind == "aggregation" && w.State == "pending" && w.ObservedAtMillis <= w.PendingUntilMillis {
			result.Outcome = "retained"
			result.Reason = "candidate_pending"
			return w, false, nil
		}
		return w, true, nil
	}
	first, ready, err := read()
	if err != nil || !ready {
		return result, err
	}
	if first.OwnerAlertID == "" {
		result.Outcome = "retained"
		result.Reason = "unbound_counter"
		return result, nil
	}
	source, fingerprint := ownerScope(first)
	err = e.lease(ctx, c.TenantID, source, fingerprint, func() error {
		current, ready, err := read()
		if err != nil || !ready {
			return err
		}
		actualSource, actualFingerprint := ownerScope(current)
		if actualSource != source || actualFingerprint != fingerprint {
			result.Outcome = "failed"
			result.Reason = "scope_mismatch"
			return policy.ErrAccess
		}
		owner, err := e.alert(ctx, c.TenantID, c.ExpectedOwner)
		reason := "owner_missing"
		if err != nil && !onlyNotFound(err) {
			result.Outcome = "failed"
			result.Reason = "owner_unavailable"
			return err
		}
		if err == nil {
			a := owner.Alert
			if a.BKTenantID != c.TenantID || a.AlertID != c.ExpectedOwner || a.EventSourceID != source || a.Fingerprint != fingerprint {
				result.Outcome = "failed"
				result.Reason = "scope_mismatch"
				return policy.ErrAccess
			}
			if owner.Version.IsZero() || a.Validate() != nil {
				result.Outcome = "failed"
				result.Reason = "invalid_state"
				return policy.ErrInvalid
			}
			result.OwnerRevision, result.OwnerStatus = a.Revision, a.Status
			if a.Status == domain.AlertStatusActive && (c.Kind == "clip" || a.AdmittedActiveMain()) {
				result.Outcome = "retained"
				result.Reason = "active_owner"
				return nil
			}
			reason = "terminal_owner"
			if a.Status == domain.AlertStatusActive {
				reason = "owner_not_admitted"
			}
		}
		deleted, err := e.windows.DeleteSuppressionWindow(ctx, current)
		if err != nil {
			result.Outcome = "failed"
			result.Reason = "cleanup_unavailable"
			return err
		}
		if !deleted {
			result.Outcome = "superseded"
			result.Reason = "window_changed"
			return nil
		}
		result.Outcome, result.Reason, result.Changed = "cleared", reason, true
		return nil
	})
	if err != nil && result.Outcome != "failed" {
		result.Outcome = "failed"
		result.Reason = "execution_unavailable"
	}
	return result, err
}

func onlyNotFound(err error) bool {
	if err == nil {
		return false
	}
	if x, ok := err.(interface{ Unwrap() []error }); ok {
		children := x.Unwrap()
		if len(children) == 0 {
			return false
		}
		for _, e := range children {
			if !onlyNotFound(e) {
				return false
			}
		}
		return true
	}
	if x, ok := err.(interface{ Unwrap() error }); ok {
		return onlyNotFound(x.Unwrap())
	}
	return errors.Is(err, store.ErrNotFound)
}
