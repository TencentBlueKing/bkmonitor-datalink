// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package domain

import (
	"fmt"
	"reflect"
	"time"
)

// ProjectionTargetState 保存单个目标的来源溯源与同步水位，不保存 endpoint、token 或凭据。
// SourceVersion 指向当前 Alert 来源的不可变发布；投递任务另存当时的目标引用与快照。
type ProjectionTargetState struct {
	// ActionEnabled 显式启用该目标的获准动作；纯投影目标不能自动推导为处置目标。
	ActionEnabled bool `json:"action_enabled,omitempty"`
	// SourceVersion 保存创建 Alert 时的来源发布版本，仅用于业务溯源与 ACK 作用域。
	SourceVersion int64 `json:"source_version"`
	// RequiredRevision 是该目标需要同步的 Alert 业务版本。
	RequiredRevision int64 `json:"required_revision"`
	// SyncedRevision 是已验证远端应用并可搜索的版本；0 表示尚无确认。
	SyncedRevision int64 `json:"synced_revision"`
	// SyncedAt 是最近一次确认时间；未确认时为空，后续确认不回退。
	SyncedAt *time.Time `json:"synced_at,omitempty"`
}

// AlertProjection 与生命周期状态独立；每个目标只能确认自己的同步进度，最多 16 个目标。
type AlertProjection struct {
	// Targets 以稳定目标 ID 隔离各目的端的配置引用和同步水位。
	Targets map[string]ProjectionTargetState `json:"targets,omitempty"`
}

// Clone 深拷贝目标和时间，防止异步 ACK 或输出插件修改仓储快照。
func (p AlertProjection) Clone() AlertProjection {
	if len(p.Targets) == 0 {
		return AlertProjection{}
	}
	out := AlertProjection{Targets: make(map[string]ProjectionTargetState, len(p.Targets))}
	for id, t := range p.Targets {
		t.SyncedAt = normalizeOptionalTime(t.SyncedAt)
		out.Targets[id] = t
	}
	return out
}

// Validate 拒绝越过当前 Alert 版本的要求/ACK，以及缺少对应 ACK 的时间。
func (p AlertProjection) Validate(revision int64) error {
	if len(p.Targets) > 16 {
		return fmt.Errorf("projection target budget exceeded")
	}
	for id, t := range p.Targets {
		if ValidateIdentityPart("projection target", id, 64) != nil || t.SourceVersion < 1 || t.SourceVersion >= 1<<53 || t.RequiredRevision < 1 || t.RequiredRevision > revision || t.SyncedRevision < 0 || t.SyncedRevision > t.RequiredRevision {
			return fmt.Errorf("invalid projection target watermarks")
		}
		if (t.SyncedRevision == 0) != (t.SyncedAt == nil) || (t.SyncedAt != nil && t.SyncedAt.IsZero()) {
			return fmt.Errorf("projection ACK time differs from watermark")
		}
	}
	return nil
}

// Pending 表示仍有目标落后；源 Alert 的 active/recovered/closed 不影响待同步事实。
func (p AlertProjection) Pending() bool {
	for _, target := range p.Targets {
		if target.RequiredRevision > target.SyncedRevision {
			return true
		}
	}
	return false
}

// RequireRevision 将已绑定目标的同步要求推进到当前业务版本，不改已确认水位。
func (p AlertProjection) RequireRevision(revision int64) (AlertProjection, error) {
	if revision < 1 || revision >= 1<<53 {
		return AlertProjection{}, fmt.Errorf("invalid required alert revision")
	}
	if err := p.Validate(revision); err != nil {
		return AlertProjection{}, err
	}
	next := p.Clone()
	for id, t := range next.Targets {
		t.RequiredRevision = revision
		next.Targets[id] = t
	}
	return next, nil
}

// Acknowledge 只推进指定目标的已确认版本；过期配置引用或重复 ACK 无副作用，超前 ACK 拒绝。
// 调用者必须已确认接收端的租户、目标、Alert 身份、版本与搜索可见性，不能把 HTTP 200 当作证明。
func (p AlertProjection) Acknowledge(target string, sourceVersion, revision int64, at time.Time) (AlertProjection, bool, error) {
	next := p.Clone()
	value, ok := next.Targets[target]
	if !ok || revision < 1 || at.IsZero() {
		return AlertProjection{}, false, fmt.Errorf("invalid projection ACK identity/time")
	}
	if sourceVersion != value.SourceVersion {
		return next, false, nil
	}
	if revision > value.RequiredRevision {
		return AlertProjection{}, false, fmt.Errorf("projection ACK exceeds required revision")
	}
	if revision <= value.SyncedRevision {
		return next, false, nil
	}
	value.SyncedRevision = revision
	at = normalizeTime(at)
	if value.SyncedAt != nil && at.Before(*value.SyncedAt) {
		at = *value.SyncedAt
	}
	value.SyncedAt = &at
	next.Targets[target] = value
	return next, true, nil
}

// MergeAcknowledgments 在相同目标绑定和要求之间合并已经确认的水位，用于归档副本的元数据收敛。
// 不合并业务字段；相同版本保留接收副本的时间，较新确认按 Acknowledge 的单调规则推进。
func (p AlertProjection) MergeAcknowledgments(other AlertProjection) (AlertProjection, bool, error) {
	if err := p.Validate(1<<53 - 1); err != nil {
		return AlertProjection{}, false, err
	}
	if err := other.Validate(1<<53 - 1); err != nil {
		return AlertProjection{}, false, err
	}
	if len(p.Targets) != len(other.Targets) {
		return AlertProjection{}, false, fmt.Errorf("projection bindings differ")
	}
	next := p.Clone()
	changed := false
	for id, incoming := range other.Targets {
		current, ok := next.Targets[id]
		if !ok || current.ActionEnabled != incoming.ActionEnabled || current.SourceVersion != incoming.SourceVersion || current.RequiredRevision != incoming.RequiredRevision {
			return AlertProjection{}, false, fmt.Errorf("projection binding/requirement differs")
		}
		if incoming.SyncedRevision > current.SyncedRevision {
			updated, _, err := next.Acknowledge(id, incoming.SourceVersion, incoming.SyncedRevision, *incoming.SyncedAt)
			if err != nil {
				return AlertProjection{}, false, err
			}
			next = updated
			changed = true
		}
	}
	return next, changed, nil
}

// advanceProjectionRequirements 把保留/新绑定目标纳入同一次业务 CAS；同步水位必须来自已有确认。
func advanceProjectionRequirements(current Alert, next *Alert) error {
	for id, previous := range current.Projection.Targets {
		target, ok := next.Projection.Targets[id]
		if !ok {
			return fmt.Errorf("projection binding cannot be silently removed")
		}
		if target.SyncedRevision != previous.SyncedRevision || !reflect.DeepEqual(target.SyncedAt, previous.SyncedAt) {
			return fmt.Errorf("business update cannot change projection ACK")
		}
	}
	for id, target := range next.Projection.Targets {
		if target.RequiredRevision < 1 || target.RequiredRevision > next.Revision {
			return fmt.Errorf("invalid projection requirement")
		}
		if _, exists := current.Projection.Targets[id]; !exists && (target.SyncedRevision != 0 || target.SyncedAt != nil) {
			return fmt.Errorf("new projection target cannot start acknowledged")
		}
		target.RequiredRevision = next.Revision
		next.Projection.Targets[id] = target
	}
	return nil
}

func validateProjectionBusinessReplacement(current, next Alert) error {
	expected := next.Clone()
	if err := advanceProjectionRequirements(current, &expected); err != nil {
		return err
	}
	if !reflect.DeepEqual(expected.Projection, next.Projection) {
		return fmt.Errorf("business mutation must require current projection revision")
	}
	return nil
}

func projectionACKOnly(before, after AlertProjection) bool {
	if len(before.Targets) != len(after.Targets) {
		return false
	}
	for id, old := range before.Targets {
		next, ok := after.Targets[id]
		if !ok || old.ActionEnabled != next.ActionEnabled || old.SourceVersion != next.SourceVersion || old.RequiredRevision != next.RequiredRevision || next.SyncedRevision < old.SyncedRevision {
			return false
		}
		if next.SyncedRevision == old.SyncedRevision && !reflect.DeepEqual(next.SyncedAt, old.SyncedAt) {
			return false
		}
		if old.SyncedAt != nil && (next.SyncedAt == nil || next.SyncedAt.Before(*old.SyncedAt)) {
			return false
		}
	}
	return true
}

// SameAlertBusinessSnapshot 忽略已完成意图、复查时间和投影 ACK，供已落库计划的幂等恢复判断。
// revision、目标绑定和 required_revision 仍属于比较范围，不能用新业务快照确认旧计划完成。
func SameAlertBusinessSnapshot(a, b Alert) bool {
	strip := func(v Alert) Alert {
		v = v.Clone()
		v.PolicyChange = nil
		v.MergeChange = nil
		v.ActionPending = nil
		v.Shield.NextCheckAt = nil
		for id, t := range v.Projection.Targets {
			t.SyncedRevision = 0
			t.SyncedAt = nil
			v.Projection.Targets[id] = t
		}
		return v
	}
	return reflect.DeepEqual(strip(a), strip(b))
}
