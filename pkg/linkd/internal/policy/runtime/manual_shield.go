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
	binding := domain.ShieldBinding{Periods: frozen.Compiled.Schedule.Periods(c.EffectiveAt), BindingID: hex.EncodeToString(sum[:]), Origin: "manual", OperationID: c.OperationID, OperatorID: c.OperatorID, OperatorDisplay: c.OperatorDisplay, Policy: c.Policy, Type: "time_shield", SourceEventID: a.TriggerEventID, Severity: a.Severity, BoundAt: c.EffectiveAt, ActivationID: activation, Reason: frozen.Compiled.Shield.Reason}
	shield := a.Shield.Clone()
	shield.Bindings = append(shield.Bindings, binding)
	shield.Active = true
	next := now.Add(5 * time.Second)
	shield.NextCheckAt = &next
	if err = shield.Validate(a.AlertID, a.Status); err != nil {
		return lifecycle.ShieldEvaluation{}, policy.ErrInvalid
	}
	// 刚发布的快捷策略可能尚未进入目录；下次统一复查必须重新读取发布集合。
	if s.Loader.Catalog != nil {
		s.Loader.Catalog.Invalidate(c.TenantID)
	}
	return lifecycle.ShieldEvaluation{Shield: shield, Tags: slices.Clone(frozen.Compiled.Shield.AlarmTags)}, nil
}
