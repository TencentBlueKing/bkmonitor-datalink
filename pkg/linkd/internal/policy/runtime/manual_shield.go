// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// BindManual 直接核对发布指针，避免五秒目录缓存把刚发布的快捷策略误判为未发布。
// 指定告警不重跑普通目标和条件；只允许时间屏蔽，依赖屏蔽仍由正式匹配建立。
func (s *Shielder) BindManual(ctx context.Context, a domain.Alert, c lifecycle.ShieldCommand, now time.Time) (lifecycle.ShieldEvaluation, error) {
	if s.Loader == nil || s.Loader.Releases == nil {
		return lifecycle.ShieldEvaluation{}, policy.ErrUnavailable
	}
	records, ok := s.Loader.Releases.(interface {
		Get(context.Context, policy.Scope, string) (policy.Record, error)
	})
	if !ok {
		return lifecycle.ShieldEvaluation{}, policy.ErrUnavailable
	}
	scope := policy.Scope{TenantID: c.TenantID, Kind: policy.Shield}
	record, err := records.Get(ctx, scope, c.Policy.ID)
	if err != nil {
		return lifecycle.ShieldEvaluation{}, err
	}
	if record.Scope != scope || record.ID != c.Policy.ID {
		return lifecycle.ShieldEvaluation{}, policy.ErrAccess
	}
	if record.Deleted || record.Published != c.Policy.Version || record.Compiled.Digest != c.Policy.Digest {
		return lifecycle.ShieldEvaluation{}, policy.ErrConflict
	}
	frozen, err := s.Loader.load(ctx, c.TenantID, store.PolicyReleaseRef{Kind: "shield", ID: c.Policy.ID, Version: c.Policy.Version, Digest: c.Policy.Digest})
	if err != nil {
		return lifecycle.ShieldEvaluation{}, err
	}
	if frozen.Compiled.Shield.ShieldType != "time_shield" || !frozen.Compiled.Active(now) || !frozen.Compiled.Active(c.EffectiveAt) {
		return lifecycle.ShieldEvaluation{}, policy.ErrInvalid
	}
	activation, err := frozen.Compiled.Schedule.Occurrence(now)
	if err != nil {
		return lifecycle.ShieldEvaluation{}, err
	}
	raw, _ := json.Marshal([]string{"manual-shield-binding", c.TenantID, c.AlertID, c.OperationID})
	sum := sha256.Sum256(raw)
	binding := domain.ShieldBinding{BindingID: hex.EncodeToString(sum[:]), Origin: "manual", OperationID: c.OperationID, OperatorID: c.OperatorID, Policy: c.Policy, Type: "time_shield", SourceEventID: a.TriggerEventID, Severity: a.Severity, BoundAt: c.EffectiveAt, ActivationID: activation, Reason: frozen.Compiled.Shield.Reason}
	shield := a.Shield.Clone()
	shield.Bindings = append(shield.Bindings, binding)
	shield.Active = true
	next := now.Add(5 * time.Second)
	shield.NextCheckAt = &next
	if err = shield.Validate(a.AlertID, a.Status); err != nil {
		return lifecycle.ShieldEvaluation{}, policy.ErrInvalid
	}
	return lifecycle.ShieldEvaluation{Shield: shield, Tags: slices.Clone(frozen.Compiled.Shield.AlarmTags)}, nil
}

// 手动绑定直接读取发布指针，不借用可能尚未包含新策略的目录缓存。普通编辑不改变冻结有效期。
func (s *Shielder) keepManualBinding(ctx context.Context, tenant string, b domain.ShieldBinding, at time.Time) (bool, string, error) {
	records, ok := s.Loader.Releases.(interface {
		Get(context.Context, policy.Scope, string) (policy.Record, error)
	})
	if !ok {
		return true, "release_unavailable", nil
	}
	scope := policy.Scope{TenantID: tenant, Kind: policy.Shield}
	current, err := records.Get(ctx, scope, b.Policy.ID)
	if errors.Is(err, policy.ErrNotFound) {
		return false, "policy_disabled", nil
	}
	if err != nil {
		return true, "release_unavailable", nil
	}
	if current.Scope != scope || current.ID != b.Policy.ID {
		return true, "", policy.ErrAccess
	}
	if current.Deleted {
		return false, "policy_disabled", nil
	}
	compiled, err := policy.Compile(policy.Shield, current.Spec)
	if err != nil {
		return true, "release_unavailable", nil
	}
	if !compiled.Common.Enabled {
		return false, "policy_disabled", nil
	}
	frozen, err := s.Loader.load(ctx, tenant, store.PolicyReleaseRef{Kind: "shield", ID: b.Policy.ID, Version: b.Policy.Version, Digest: b.Policy.Digest})
	if err != nil {
		return true, "release_unavailable", nil
	}
	if !frozen.Compiled.Active(at) {
		return false, "policy_inactive", nil
	}
	activation, err := frozen.Compiled.Schedule.Occurrence(at)
	if err != nil {
		return true, "activation_unavailable", nil
	}
	if activation != b.ActivationID {
		return false, "policy_inactive", nil
	}
	return true, "", nil
}
