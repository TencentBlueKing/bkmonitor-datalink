// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ReleaseMergeWindow 在 fingerprint lease 内释放一个失败/丢失窗口，不重跑任何策略或 Enrich。
// 调用方先确认裁决失败或原截止时间已过；成功关系及其他窗口继续阻塞。输出中断时意图保留在 Alert。
func (p *Processor) ReleaseMergeWindow(ctx context.Context, tenant, id, window string) (store.StoredAlert, error) {
	if ctx == nil {
		return store.StoredAlert{}, fmt.Errorf("merge release requires context")
	}
	raw, err := hex.DecodeString(window)
	if err != nil || len(raw) != 32 || domain.ValidateIdentityPart("tenant", tenant, 64) != nil || id == "" || len(id) > domain.EntityIDMaxBytes {
		return store.StoredAlert{}, store.ErrInvalidArgument
	}
	if provider, ok := p.severity.(interface {
		FreezeSeverity() (SeverityTable, string)
	}); ok {
		table, digest := provider.FreezeSeverity()
		local := *p
		local.severity = table
		local.configDigest = digest
		return local.ReleaseMergeWindow(ctx, tenant, id, window)
	}
	for range maxCASAttempts {
		if err := ctx.Err(); err != nil {
			return store.StoredAlert{}, err
		}
		current, err := p.readMergeAlert(ctx, tenant, id)
		if err != nil {
			return store.StoredAlert{}, err
		}
		current, err = p.finishPendingChanges(ctx, current)
		if err != nil {
			return store.StoredAlert{}, err
		}
		merge, changed := current.Alert.Merge.ReleaseWindow(window)
		if !changed || current.Alert.Status.Terminal() {
			return current, nil
		}
		now, err := p.now()
		if err != nil {
			return store.StoredAlert{}, err
		}
		replacement := current.Alert.Clone()
		replacement.Merge = merge
		replacement.UpdateAt = nextAlertUpdateTime(now, current.Alert.UpdateAt)
		operation := digestStrings("merge-release", tenant, id, window)
		ready := !replacement.Shield.Active && !replacement.Merge.Blocking() && (replacement.Admission.AdmittedAt == nil || replacement.Admission.Severity != replacement.Severity)
		// 已被移除的等级仍结束窗口等待，但不能借释放操作生成未知等级处置。
		if ready {
			if _, known := p.severity.Priority(replacement.Severity); !known {
				ready = false
				p.logger.WarnContext(ctx, "merge release cannot admit removed severity", "error_code", "unknown_severity", "bk_tenant_id", tenant, "alert_id", id, "window_id", window)
			}
		}
		if ready {
			at := replacement.UpdateAt
			replacement.Admission = domain.AlertAdmission{AdmittedAt: &at, Severity: replacement.Severity, CauseType: AlertChangeCauseSystemOperation, CauseID: operation}
		}
		replacement.MergeChange = &domain.AlertMergeChange{Kind: "release", OperationID: operation, WindowID: window, EffectiveAt: replacement.UpdateAt, Before: current.Alert.Merge.Clone(), After: replacement.Merge.Clone(), ActionReady: ready}
		if err := freezeAction(&replacement, current.Alert.Revision+1, AlertChangeCause{Type: AlertChangeCauseSystemOperation, ID: operation}, ready); err != nil {
			return store.StoredAlert{}, err
		}
		updated, err := p.compareAndSetAlert(ctx, current, replacement)
		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		if err != nil {
			return store.StoredAlert{}, err
		}
		if err := p.cacheMergeSnapshot(ctx, updated); err != nil {
			return store.StoredAlert{}, err
		}
		return p.finishMergeChange(ctx, updated)
	}
	return store.StoredAlert{}, store.ErrVersionConflict
}

// FinishMergeChanges 在 Alert 自身的 fingerprint lease 内补齐已经保存的意图，不开始新的合并裁决。
func (p *Processor) FinishMergeChanges(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	current, err := p.readMergeAlert(ctx, tenant, id)
	if err != nil {
		return store.StoredAlert{}, err
	}
	return p.finishPendingChanges(ctx, current)
}

// finishMergeChange 使用保存的稳定 cause 和首次快照重试输出；普通 Hook 失败语义仍由 P6 的可靠投递替换。
func (p *Processor) finishMergeChange(ctx context.Context, current store.StoredAlert) (store.StoredAlert, error) {
	var err error
	current, err = p.finishActionIntent(ctx, current)
	if err != nil {
		return store.StoredAlert{}, err
	}
	intent := current.Alert.MergeChange
	if intent == nil {
		return current, nil
	}
	// CAS 已完成但缓存写失败时，任何后续 Event 必须先修复当前版本再继续处理。
	if err := p.cacheMergeSnapshot(ctx, current); err != nil {
		return store.StoredAlert{}, err
	}
	operation, _ := json.Marshal(intent.OperationID)
	window, _ := json.Marshal(intent.WindowID)
	kind, outcome := domain.OperationKindMergeRelease, OutcomeAlertMergeReleased
	if intent.Kind != "release" {
		kind, outcome = domain.OperationKindMerge, OutcomeAlertMergeChanged
	}
	if intent.Kind == "member_unlink" {
		kind = domain.OperationKindMergeEnd
	}
	if intent.Kind == "parent_recover" {
		kind, outcome = domain.OperationKindRecover, OutcomeAlertRecovered
		if err := p.clearSuppression(ctx, current.Alert); err != nil {
			return store.StoredAlert{}, err
		}
	}
	relation, _ := json.Marshal(intent.RelationID)
	logs := []domain.AlertLog{{LogID: digestStrings("merge-change-log", intent.Kind, intent.OperationID), BKTenantID: current.Alert.BKTenantID, AlertID: current.Alert.AlertID, OperatorKind: domain.OperatorKindSystem, OperationKind: kind, CreatedTime: intent.EffectiveAt, Params: domain.JSONObject{"operation_id": operation, "window_id": window, "relation_id": relation}}}
	hookLogs, err := p.runHooks(ctx, AlertChangeCause{Type: AlertChangeCauseSystemOperation, ID: intent.OperationID}, current.Alert, outcome, intent.ActionReady)
	if err != nil {
		return store.StoredAlert{}, err
	}
	logs = append(logs, hookLogs...)
	if err := p.appendAlertLogs(ctx, logs); err != nil {
		return store.StoredAlert{}, err
	}
	next := current.Alert.Clone()
	next.MergeChange = nil
	updated, err := p.compareAndSetAlert(ctx, current, next)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if err := p.cacheMergeSnapshot(ctx, updated); err != nil {
		return store.StoredAlert{}, err
	}
	return updated, nil
}

// 终态意图重试不能用旧父快照覆盖同 fingerprint 已经开始的新生命周期缓存。
func (p *Processor) cacheMergeSnapshot(ctx context.Context, current store.StoredAlert) error {
	if current.Alert.Status.Terminal() {
		return p.repairClosedAlertCache(ctx, current)
	}
	return p.recentAlerts.PutCurrent(ctx, current)
}
