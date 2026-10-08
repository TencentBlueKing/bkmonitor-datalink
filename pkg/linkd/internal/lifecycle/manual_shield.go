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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ShieldCommand 把一个已发布快捷屏蔽策略立即绑定到指定活动 Alert。
// ExpectedRevision 与完整命令固定重试身份，不能把旧命令套用到新的业务状态。
type ShieldCommand struct {
	TenantID         string               `json:"bk_tenant_id"`
	AlertID          string               `json:"alert_id"`
	OperationID      string               `json:"operation_id"`
	OperatorID       string               `json:"operator_id"`
	ExpectedRevision int64                `json:"expected_revision"`
	Policy           domain.PolicyVersion `json:"policy"`
	EffectiveAt      time.Time            `json:"effective_at"`
}

// Validate 校验命令身份和策略引用，不执行外部查询。
func (c ShieldCommand) Validate() error {
	if domain.ValidateIdentityPart("tenant", c.TenantID, 64) != nil || c.AlertID == "" || len(c.AlertID) > domain.EntityIDMaxBytes || domain.ValidateIdentityPart("operation", c.OperationID, 128) != nil || strings.TrimSpace(c.OperatorID) == "" || len(c.OperatorID) > 256 || c.ExpectedRevision < 1 || c.ExpectedRevision >= 1<<53 || c.Policy.Validate() != nil || c.EffectiveAt.IsZero() {
		return store.ErrInvalidArgument
	}
	return nil
}

// ShieldCommandResult 保存实际 Alert 和是否已应用；重复成功不重新绑定已解除的关系。
type ShieldCommandResult struct {
	Alert          domain.Alert `json:"alert"`
	AlreadyApplied bool         `json:"already_applied"`
}

// ManualShieldEvaluator 只校验已发布策略和有效期，不对指定告警重跑普通匹配条件。
type ManualShieldEvaluator interface {
	BindManual(context.Context, domain.Alert, ShieldCommand, time.Time) (ShieldEvaluation, error)
}

// BindShield 须持有 fingerprint lease。状态与输出意图同次 CAS，不生成 Event 或新动作。
func (p *Processor) BindShield(ctx context.Context, c ShieldCommand) (ShieldCommandResult, error) {
	if ctx == nil || c.Validate() != nil {
		return ShieldCommandResult{}, store.ErrInvalidArgument
	}
	c.EffectiveAt = c.EffectiveAt.Round(0).UTC()
	raw, _ := json.Marshal(c)
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	for range maxCASAttempts {
		current, err := p.getAlertCurrent(ctx, c.TenantID, c.AlertID)
		if err != nil {
			return ShieldCommandResult{}, err
		}
		if current.Alert.BKTenantID != c.TenantID || current.Alert.AlertID != c.AlertID || current.Version.IsZero() {
			return ShieldCommandResult{}, store.ErrInvalidArgument
		}
		current, err = p.finishPendingChanges(ctx, current)
		if err != nil {
			return ShieldCommandResult{}, err
		}
		a := current.Alert
		if old := a.LastShieldOperation; old != nil && old.ID == c.OperationID {
			if old.RequestHash != hash {
				return ShieldCommandResult{}, store.ErrVersionConflict
			}
			if err := p.recentAlerts.PutCurrent(ctx, current); err != nil {
				return ShieldCommandResult{}, err
			}
			return ShieldCommandResult{Alert: a.Clone(), AlreadyApplied: true}, nil
		}
		if a.Status != domain.AlertStatusActive || a.Revision != c.ExpectedRevision {
			return ShieldCommandResult{}, store.ErrInvalidTransition
		}
		evaluator, ok := p.shielder.(ManualShieldEvaluator)
		if !ok {
			return ShieldCommandResult{}, store.ErrInvalidArgument
		}
		now, err := p.now()
		if err != nil {
			return ShieldCommandResult{}, err
		}
		if c.EffectiveAt.After(now.Add(5 * time.Minute)) {
			return ShieldCommandResult{}, store.ErrInvalidArgument
		}
		evaluated, err := evaluator.BindManual(ctx, a, c, now)
		if err != nil {
			return ShieldCommandResult{}, err
		}
		next := a.Clone()
		next.Shield = evaluated.Shield.Clone()
		next.PolicyTags = append(next.PolicyTags, evaluated.Tags...)
		slices.Sort(next.PolicyTags)
		next.PolicyTags = slices.Compact(next.PolicyTags)
		next.LastShieldOperation = &domain.AlertShieldOperation{ID: c.OperationID, RequestHash: hash}
		next.UpdateAt = nextAlertUpdateTime(now, a.UpdateAt)
		next.PolicyChange = &domain.AlertPolicyChange{OperationID: digestStrings("manual-shield", c.TenantID, c.AlertID, c.OperationID), EffectiveAt: next.UpdateAt, Before: a.Shield.Clone().Bindings, After: next.Shield.Clone().Bindings}
		updated, err := p.compareAndSetAlert(ctx, current, next)
		if errors.Is(err, store.ErrVersionConflict) {
			continue
		}
		if err != nil {
			return ShieldCommandResult{}, err
		}
		updated, err = p.finishPendingChanges(ctx, updated)
		if err != nil {
			return ShieldCommandResult{}, err
		}
		if err = p.recentAlerts.PutCurrent(ctx, updated); err != nil {
			return ShieldCommandResult{}, err
		}
		return ShieldCommandResult{Alert: updated.Alert.Clone()}, nil
	}
	return ShieldCommandResult{}, store.ErrVersionConflict
}
