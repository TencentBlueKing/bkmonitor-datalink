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
	"encoding/hex"
	"fmt"
	"reflect"
	"slices"
	"time"
)

// MergeWait 是已经加入窗口的持久化引用；Deadline 不因重复触发或 Redis 丢失而延长。
// MemberEventID/Severity 指向首次入窗匹配的 Event，多个条件组不增加成员身份数量。
type MergeWait struct {
	WindowID      string        `json:"window_id"`
	Policy        PolicyVersion `json:"policy"`
	GroupKey      string        `json:"group_key"`
	MemberEventID string        `json:"member_event_id"`
	Severity      string        `json:"severity"`
	Groups        []int         `json:"groups"`
	StartedAt     time.Time     `json:"started_at"`
	Deadline      time.Time     `json:"deadline"`
}

// Clone 拷贝成员条件组，保证 Alert 快照之间没有共享切片。
func (w MergeWait) Clone() MergeWait {
	w.Groups = slices.Clone(w.Groups)
	w.StartedAt = normalizeTime(w.StartedAt)
	w.Deadline = normalizeTime(w.Deadline)
	return w
}

// Validate 检查身份、首次条件组与固定半开区间；最多 32 个条件组、一天窗口。
func (w MergeWait) Validate() error {
	for _, v := range []string{w.WindowID, w.GroupKey} {
		raw, err := hex.DecodeString(v)
		if err != nil || len(raw) != 32 {
			return fmt.Errorf("invalid merge identity")
		}
	}
	if err := w.Policy.Validate(); err != nil {
		return err
	}
	if w.MemberEventID == "" || len(w.MemberEventID) > EntityIDMaxBytes || w.Severity == "" || len(w.Severity) > 32 || w.StartedAt.IsZero() || !w.Deadline.After(w.StartedAt) || w.Deadline.Sub(w.StartedAt) > 24*time.Hour {
		return fmt.Errorf("invalid merge wait")
	}
	if len(w.Groups) < 1 || len(w.Groups) > 32 {
		return fmt.Errorf("invalid merge group count")
	}
	for i, g := range w.Groups {
		if g < 0 || g >= 32 || (i > 0 && w.Groups[i-1] >= g) {
			return fmt.Errorf("unordered merge groups")
		}
	}
	return nil
}

// AlertMerge 保存与生命周期独立的等待/已合并摘要；nil 表示原始告警尚未参与合并。
// 完整父子关系独立分页存储，这里只保留最多 32 个引用，不堆叠成员列表。
type AlertMerge struct {
	Role           string      `json:"role"`
	State          string      `json:"state"`
	Pending        []MergeWait `json:"pending,omitempty"`
	RelationIDs    []string    `json:"relation_ids,omitempty"`
	OperationID    string      `json:"operation_id,omitempty"`
	RelationsReady bool        `json:"relations_ready,omitempty"`
}

// Clone 深拷贝窗口和关系引用；nil 仍表示没有合并事实。
func (m *AlertMerge) Clone() *AlertMerge {
	if m == nil {
		return nil
	}
	v := *m
	v.Pending = slices.Clone(m.Pending)
	for i := range v.Pending {
		v.Pending[i] = v.Pending[i].Clone()
	}
	v.RelationIDs = slices.Clone(m.RelationIDs)
	if len(v.RelationIDs) == 0 {
		v.RelationIDs = nil
	}
	if len(v.Pending) == 0 {
		v.Pending = nil
	}
	return &v
}

// Blocking 表示处置被窗口/成功关系或待补齐父关系阻塞；不推导 Alert.status。
func (m *AlertMerge) Blocking() bool {
	return m != nil && ((m.Role == "original" && (len(m.Pending) > 0 || len(m.RelationIDs) > 0)) || (m.Role == "aggregate" && !m.RelationsReady))
}

// Validate 只接受明确的角色与状态，不借用 active/recovered/closed 表达合并。
func (m *AlertMerge) Validate(status AlertStatus) error {
	if m == nil {
		return nil
	}
	if m.Role != "original" && m.Role != "aggregate" {
		return fmt.Errorf("invalid merge role")
	}
	if len(m.Pending) > 32 || len(m.RelationIDs) > 32 {
		return fmt.Errorf("merge summary exceeds budget")
	}
	seen := map[string]bool{}
	for _, w := range m.Pending {
		if err := w.Validate(); err != nil {
			return err
		}
		if seen[w.WindowID] {
			return fmt.Errorf("duplicate merge wait")
		}
		seen[w.WindowID] = true
	}
	if status.Terminal() && len(m.Pending) > 0 {
		return fmt.Errorf("terminal alert cannot wait for merge")
	}
	for i, id := range m.RelationIDs {
		raw, err := hex.DecodeString(id)
		if err != nil || len(raw) != 32 || (i > 0 && m.RelationIDs[i-1] >= id) {
			return fmt.Errorf("invalid merge relation identity")
		}
	}
	switch m.State {
	case "none", "released":
		if len(m.Pending) > 0 || len(m.RelationIDs) > 0 {
			return fmt.Errorf("merge state differs from membership")
		}
	case "pending":
		if len(m.Pending) == 0 || len(m.RelationIDs) > 0 {
			return fmt.Errorf("pending merge requires waiting membership")
		}
	case "merged":
		if len(m.RelationIDs) == 0 {
			return fmt.Errorf("merged state requires real relations")
		}
	default:
		return fmt.Errorf("invalid merge state")
	}
	if m.Role == "original" && (m.OperationID != "" || m.RelationsReady) {
		return fmt.Errorf("original member cannot own merge operation")
	}
	if m.Role == "aggregate" {
		if m.RelationsReady && (m.State != "merged" || len(m.RelationIDs) == 0) {
			return fmt.Errorf("ready aggregate requires relation summary")
		}
		raw, err := hex.DecodeString(m.OperationID)
		if err != nil || len(raw) != 32 || len(m.Pending) > 0 {
			return fmt.Errorf("invalid aggregate origin")
		}
	}
	return nil
}

// EndWaiting 保留已发生的关系摘要，终态只结束尚在等待的窗口。
func (m *AlertMerge) EndWaiting() *AlertMerge {
	v := m.Clone()
	if v == nil {
		return nil
	}
	v.Pending = nil
	if len(v.RelationIDs) > 0 {
		v.State = "merged"
	} else if v.State == "pending" {
		v.State = "released"
	}
	return v
}

func validateMergeReplacement(before, after *AlertMerge) error {
	if before == nil {
		return nil
	}
	if after == nil {
		return fmt.Errorf("merge history cannot be erased")
	}
	if before.Role != after.Role || before.OperationID != after.OperationID || (before.RelationsReady && !after.RelationsReady) {
		return fmt.Errorf("merge origin is immutable")
	}
	for _, old := range before.Pending {
		for _, next := range after.Pending {
			if old.WindowID == next.WindowID && !reflect.DeepEqual(old, next) {
				return fmt.Errorf("merge waiting reference is immutable")
			}
		}
	}
	return nil
}

// ReleaseWindow 只移除指定等待，不清理其他策略窗口或成功关系；返回是否发生变更。
func (m *AlertMerge) ReleaseWindow(id string) (*AlertMerge, bool) {
	if m == nil || m.Role != "original" {
		return m.Clone(), false
	}
	result := m.Clone()
	result.Pending = slices.DeleteFunc(result.Pending, func(w MergeWait) bool { return w.WindowID == id })
	if len(result.Pending) == len(m.Pending) {
		return result, false
	}
	if len(result.Pending) == 0 {
		result.Pending = nil
	}
	switch {
	case len(result.RelationIDs) > 0:
		result.State = "merged"
	case len(result.Pending) > 0:
		result.State = "pending"
	default:
		result.State = "released"
	}
	return result, true
}

// AlertMergeChange 保存控制面释放窗口、建立成员关系或开放父关系后尚待完成的流水/输出；与 Alert CAS 一起写入。
// 新事件和直接关闭必须先完成该意图，避免用后续快照重试旧操作。
type AlertMergeChange struct {
	Kind        string      `json:"kind"`
	RelationID  string      `json:"relation_id,omitempty"`
	OperationID string      `json:"operation_id"`
	WindowID    string      `json:"window_id"`
	EffectiveAt time.Time   `json:"effective_at"`
	Before      *AlertMerge `json:"before"`
	After       *AlertMerge `json:"after"`
	ActionReady bool        `json:"action_ready"`
}

// Clone 隔离等待列表和关系引用，固定本次操作的前后快照。
func (c *AlertMergeChange) Clone() *AlertMergeChange {
	if c == nil {
		return nil
	}
	v := *c
	v.Before = c.Before.Clone()
	v.After = c.After.Clone()
	v.EffectiveAt = normalizeTime(c.EffectiveAt)
	return &v
}

// Validate 确认意图准确描述一种合并状态变更；已放行的处置必须具有同一稳定操作身份。
func (c *AlertMergeChange) Validate(a Alert) error {
	if c == nil {
		return nil
	}
	if err := ValidateIdentityPart("merge operation", c.OperationID, 128); err != nil {
		return err
	}
	for _, id := range []string{c.WindowID} {
		raw, err := hex.DecodeString(id)
		if err != nil || len(raw) != 32 {
			return fmt.Errorf("invalid merge change identity")
		}
	}
	if (a.Status != AlertStatusActive && c.Kind != "parent_recover") || c.Before == nil || c.After == nil || c.EffectiveAt.IsZero() || !c.EffectiveAt.Equal(a.UpdateAt) || c.Before.Validate(AlertStatusActive) != nil || c.After.Validate(a.Status) != nil || !reflect.DeepEqual(c.After, a.Merge) {
		return fmt.Errorf("invalid merge change snapshots")
	}
	var expected *AlertMerge
	var changed bool
	var err error
	switch c.Kind {
	case "release":
		if c.RelationID != "" {
			return fmt.Errorf("release must not alter relations")
		}
		expected, changed = c.Before.ReleaseWindow(c.WindowID)
	case "member_unlink":
		if c.ActionReady {
			return fmt.Errorf("relation cleanup cannot admit a member")
		}
		expected, changed, err = c.Before.WithoutRelation(c.WindowID, c.RelationID)
	case "member_link":
		if c.ActionReady {
			return fmt.Errorf("linking a member cannot admit it")
		}
		expected, changed, err = c.Before.LinkRelation(c.WindowID, c.RelationID)
	case "parent_ready":
		expected, changed, err = c.Before.WithReadyRelation(c.RelationID)
	case "parent_recover":
		raw, identityErr := hex.DecodeString(c.RelationID)
		if identityErr != nil || len(raw) != 32 || c.Before.Role != "aggregate" || a.Status != AlertStatusRecovered || a.EndType != AlertEndTypeSystem || a.EndReason != "merge_members_ended" || a.EndAt == nil || !a.EndAt.Equal(c.EffectiveAt) || c.ActionReady != (a.Admission.AdmittedAt != nil) {
			return fmt.Errorf("invalid merge parent recovery")
		}
		expected, changed = c.Before.Clone(), true
	default:
		return fmt.Errorf("invalid merge change kind")
	}
	if err != nil || !changed || !reflect.DeepEqual(expected, c.After) {
		return fmt.Errorf("merge change does not match declared operation")
	}
	if c.ActionReady && c.Kind != "parent_recover" && (!a.AdmittedActiveMain() || a.Admission.CauseType != "system_operation" || a.Admission.CauseID != c.OperationID || !a.Admission.AdmittedAt.Equal(c.EffectiveAt)) {
		return fmt.Errorf("merge release admission differs from change")
	}
	return nil
}

// LinkRelation 将一个原始成员的指定等待转为关系引用，保留其他窗口和关系。
func (m *AlertMerge) LinkRelation(window, relation string) (*AlertMerge, bool, error) {
	raw, err := hex.DecodeString(relation)
	if err != nil || len(raw) != 32 || m == nil || m.Role != "original" {
		return nil, false, fmt.Errorf("invalid member relation")
	}
	result, removed := m.ReleaseWindow(window)
	exists := slices.Contains(result.RelationIDs, relation)
	if !removed && !exists {
		return nil, false, fmt.Errorf("member no longer waits for this relation")
	}
	if exists {
		return result, removed, nil
	}
	if len(result.RelationIDs) >= 32 {
		return nil, false, fmt.Errorf("merge relation budget exceeded")
	}
	result.RelationIDs = append(result.RelationIDs, relation)
	slices.Sort(result.RelationIDs)
	result.State = "merged"
	return result, true, nil
}

// WithReadyRelation 只更新 aggregate 的关系摘要；调用方须先确认完整持久化关系，不能用于原始成员。
func (m *AlertMerge) WithReadyRelation(relation string) (*AlertMerge, bool, error) {
	raw, err := hex.DecodeString(relation)
	if err != nil || len(raw) != 32 || m == nil || m.Role != "aggregate" {
		return nil, false, fmt.Errorf("invalid aggregate relation")
	}
	result := m.Clone()
	exists := slices.Contains(result.RelationIDs, relation)
	if exists && result.RelationsReady {
		return result, false, nil
	}
	if !exists {
		if len(result.RelationIDs) >= 32 {
			return nil, false, fmt.Errorf("merge relation budget exceeded")
		}
		result.RelationIDs = append(result.RelationIDs, relation)
		slices.Sort(result.RelationIDs)
	}
	result.State = "merged"
	result.RelationsReady = true
	return result, true, nil
}

// WithoutRelation 只解除指定关系及其窗口，不改变其他关系、窗口或原始告警的生命周期。
func (m *AlertMerge) WithoutRelation(window, relation string) (*AlertMerge, bool, error) {
	raw, err := hex.DecodeString(relation)
	if err != nil || len(raw) != 32 {
		return nil, false, fmt.Errorf("invalid relation identity")
	}
	if m == nil {
		return nil, false, nil
	}
	if m.Role != "original" {
		return nil, false, fmt.Errorf("only original members may be detached")
	}
	result, changed := m.ReleaseWindow(window)
	before := len(result.RelationIDs)
	result.RelationIDs = slices.DeleteFunc(result.RelationIDs, func(id string) bool { return id == relation })
	changed = changed || len(result.RelationIDs) != before
	if !changed {
		return result, false, nil
	}
	if len(result.RelationIDs) == 0 {
		result.RelationIDs = nil
	}
	switch {
	case len(result.RelationIDs) > 0:
		result.State = "merged"
	case len(result.Pending) > 0:
		result.State = "pending"
	default:
		result.State = "released"
	}
	return result, true, nil
}
