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
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store"
)

// Documents 使用部署隔离的合并裁决、快照、关系与显式请求集合；Put 为空 token 时必须 create-only。
// 非空 token 必须真实 CAS；List 完整有界并按 ID 排序，不能把部分查询结果当作成功。
type Documents interface {
	Get(context.Context, string, string) (json.RawMessage, string, error)
	Put(context.Context, string, string, string, json.RawMessage) error
	List(context.Context, string, string, string, int) ([]json.RawMessage, error)
}

// Journal 将已冻结窗口提升为持久化执行记录；不持有 Redis 窗口重建逻辑。
type Journal struct{ docs Documents }

// NewJournal 注入真实 CAS 文档端口；不访问数据库或启动后台任务。
func NewJournal(d Documents) (*Journal, error) {
	if d == nil {
		return nil, policy.ErrInvalid
	}
	return &Journal{docs: d}, nil
}

func prefix(tenant string) string { return base64.RawURLEncoding.EncodeToString([]byte(tenant)) + ":" }

func decisionKey(tenant, id string) (string, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || !validHash(id) {
		return "", policy.ErrInvalid
	}
	return prefix(tenant) + id, nil
}

func snapshotKey(tenant, id, alert string) (string, error) {
	key, err := decisionKey(tenant, id)
	if err != nil || alert == "" || len(alert) > domain.EntityIDMaxBytes {
		return "", policy.ErrInvalid
	}
	return key + ":" + hash("member", alert), nil
}

// Claim 创建固定候选结果；重试只核对事实并复用已经推进的状态，不重置捕获或投递进度。
func (j *Journal) Claim(ctx context.Context, d Decision) (StoredDecision, error) {
	if err := ctx.Err(); err != nil {
		return StoredDecision{}, err
	}
	phase, reason := "capturing", ""
	if d.Outcome == "failed" {
		phase = "releasing"
		reason = "conditions_not_met"
	}
	d.Progress = Progress{Phase: phase, ReasonCode: reason, UpdatedAt: d.FrozenAt}
	d, err := d.Normalize()
	if err != nil {
		return StoredDecision{}, err
	}
	key, err := decisionKey(d.TenantID, d.ID)
	if err != nil {
		return StoredDecision{}, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return StoredDecision{}, err
	}
	err = j.docs.Put(ctx, "merge_decisions", key, "", raw)
	if err != nil && !errors.Is(err, policy.ErrConflict) {
		return StoredDecision{}, err
	}
	actual, err := j.Get(ctx, d.TenantID, d.ID)
	if err != nil {
		return StoredDecision{}, err
	}
	before, wanted := actual.Decision.Clone(), d.Clone()
	before.Progress = Progress{}
	wanted.Progress = Progress{}
	if !reflect.DeepEqual(before, wanted) {
		return StoredDecision{}, policy.ErrConflict
	}
	return actual, nil
}

// GetByWindow 不依赖 Redis 或历史列表直接读取窗口裁决；NotFound 之外的错误不能用于失败释放。
func (j *Journal) GetByWindow(ctx context.Context, tenant, window string) (StoredDecision, error) {
	id, err := domain.MergeDecisionID(tenant, window)
	if err != nil {
		return StoredDecision{}, policy.ErrInvalid
	}
	return j.Get(ctx, tenant, id)
}

// Get 使用显式租户和操作 ID 实时读取，并核对文档内作用域。
func (j *Journal) Get(ctx context.Context, tenant, id string) (StoredDecision, error) {
	key, err := decisionKey(tenant, id)
	if err != nil {
		return StoredDecision{}, err
	}
	raw, token, err := j.docs.Get(ctx, "merge_decisions", key)
	if err != nil {
		return StoredDecision{}, err
	}
	var d Decision
	if len(raw) > 8<<20 || json.Unmarshal(raw, &d) != nil || token == "" {
		return StoredDecision{}, policy.ErrInvalid
	}
	if d.TenantID != tenant || d.ID != id {
		return StoredDecision{}, policy.ErrAccess
	}
	normalized, err := d.Normalize()
	if err != nil {
		return StoredDecision{}, err
	}
	return StoredDecision{Decision: normalized, Version: token}, nil
}

// Capture 只写首次成员快照；并发重试始终读取已保存快照，不刷新为后来的告警内容。
func (j *Journal) Capture(ctx context.Context, tenant, id string, alert domain.Alert) (Snapshot, error) {
	if alert.BKTenantID != tenant {
		return Snapshot{}, policy.ErrAccess
	}
	key, err := snapshotKey(tenant, id, alert.AlertID)
	if err != nil {
		return Snapshot{}, err
	}
	d, err := j.Get(ctx, tenant, id)
	if err != nil {
		return Snapshot{}, err
	}
	if !slices.Contains(d.Decision.MemberIDs, alert.AlertID) {
		return Snapshot{}, policy.ErrAccess
	}
	existing, err := j.Snapshot(ctx, tenant, id, alert.AlertID)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, policy.ErrNotFound) {
		return Snapshot{}, err
	}
	if d.Decision.Progress.Phase != "capturing" {
		return Snapshot{}, policy.ErrConflict
	}
	a, err := alert.Normalize()
	if err != nil {
		return Snapshot{}, err
	}
	value := Snapshot{TenantID: tenant, DecisionID: id, Alert: a}
	raw, err := json.Marshal(value)
	if err != nil {
		return Snapshot{}, err
	}
	if len(raw) > 8<<20 {
		return Snapshot{}, policy.ErrInvalid
	}
	err = j.docs.Put(ctx, "merge_members", key, "", raw)
	if err != nil && !errors.Is(err, policy.ErrConflict) {
		return Snapshot{}, err
	}
	return j.Snapshot(ctx, tenant, id, alert.AlertID)
}

// Snapshot 按原成员身份读取，绝不回退到实时 Alert 来补已经保存的快照。
func (j *Journal) Snapshot(ctx context.Context, tenant, id, alert string) (Snapshot, error) {
	key, err := snapshotKey(tenant, id, alert)
	if err != nil {
		return Snapshot{}, err
	}
	raw, _, err := j.docs.Get(ctx, "merge_members", key)
	if err != nil {
		return Snapshot{}, err
	}
	var value Snapshot
	if len(raw) > 8<<20 || json.Unmarshal(raw, &value) != nil {
		return Snapshot{}, policy.ErrInvalid
	}
	if value.TenantID != tenant || value.DecisionID != id || value.Alert.BKTenantID != tenant || value.Alert.AlertID != alert {
		return Snapshot{}, policy.ErrAccess
	}
	if err := value.Alert.Validate(); err != nil {
		return Snapshot{}, err
	}
	return value, nil
}

// Members 完整读取固定成员集合，缺失或超过 32 MiB 均失败，不能使用部分集合渲染主事件。
func (j *Journal) Members(ctx context.Context, d Decision) ([]Snapshot, error) {
	if err := d.Validate(); err != nil {
		return nil, err
	}
	result := make([]Snapshot, 0, len(d.MemberIDs))
	bytes := 0
	for _, id := range d.MemberIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		s, err := j.Snapshot(ctx, d.TenantID, d.ID, id)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		bytes += len(raw)
		if bytes > 32<<20 {
			return nil, policy.ErrUnavailable
		}
		result = append(result, s)
	}
	return result, nil
}

func (j *Journal) put(ctx context.Context, current StoredDecision, next Decision) (StoredDecision, error) {
	if current.Version == "" {
		return StoredDecision{}, policy.ErrInvalid
	}
	normalized, err := next.Normalize()
	if err != nil {
		return StoredDecision{}, err
	}
	if err := validateProgress(current.Decision, normalized); err != nil {
		return StoredDecision{}, err
	}
	key, err := decisionKey(next.TenantID, next.ID)
	if err != nil {
		return StoredDecision{}, err
	}
	raw, err := json.Marshal(normalized)
	if err != nil {
		return StoredDecision{}, err
	}
	if err := j.docs.Put(ctx, "merge_decisions", key, current.Version, raw); err != nil {
		return StoredDecision{}, err
	}
	return j.Get(ctx, next.TenantID, next.ID)
}

// Prepare 在完整成员快照存在后冻结父 Event；调用成功前禁止写 EventStore 或 Mailbox。
func (j *Journal) Prepare(ctx context.Context, current StoredDecision, event domain.Event, at time.Time) (StoredDecision, error) {
	if current.Decision.Progress.Phase != "capturing" {
		return StoredDecision{}, policy.ErrConflict
	}
	if _, err := j.Members(ctx, current.Decision); err != nil {
		return StoredDecision{}, err
	}
	next := current.Decision.Clone()
	next.Progress.Phase = "prepared"
	next.Progress.CaptureOffset = len(next.MemberIDs)
	next.Progress.ParentEvent = &event
	next.Progress.UpdatedAt = at
	return j.put(ctx, current, next)
}

// AdvanceCapture 只在下一个成员快照已经持久化后推进前缀；CAS 失败仍重用首次快照。
func (j *Journal) AdvanceCapture(ctx context.Context, current StoredDecision, at time.Time) (StoredDecision, error) {
	d := current.Decision
	if d.Progress.Phase != "capturing" || d.Progress.CaptureOffset >= len(d.MemberIDs) {
		return StoredDecision{}, policy.ErrConflict
	}
	if _, err := j.Snapshot(ctx, d.TenantID, d.ID, d.MemberIDs[d.Progress.CaptureOffset]); err != nil {
		return StoredDecision{}, err
	}
	next := d.Clone()
	next.Progress.CaptureOffset++
	next.Progress.UpdatedAt = at
	return j.put(ctx, current, next)
}

// MarkEnqueued 仅在 EventStore create 与 Mailbox 入队均确认后调用；失败时重试同一已冻结 Event。
func (j *Journal) MarkEnqueued(ctx context.Context, current StoredDecision, at time.Time) (StoredDecision, error) {
	if current.Decision.Progress.Phase != "prepared" {
		return StoredDecision{}, policy.ErrConflict
	}
	next := current.Decision.Clone()
	next.Progress.Phase = "waiting_parent"
	next.Progress.UpdatedAt = at
	return j.put(ctx, current, next)
}

// RecordParent 接收真实 Repository 读取的父 Alert；禁止仅用预期 ID 推进 linking。
func (j *Journal) RecordParent(ctx context.Context, current StoredDecision, parent store.StoredAlert, at time.Time) (StoredDecision, error) {
	d := current.Decision
	p := parent.Alert
	if d.Progress.Phase != "waiting_parent" || d.Progress.ParentEvent == nil || parent.Version.IsZero() || p.Validate() != nil || p.Merge == nil || p.Merge.Role != "aggregate" {
		return StoredDecision{}, policy.ErrInvalid
	}
	if p.BKTenantID != d.TenantID || p.EventSourceID != domain.BuiltinMergeEventSourceID || p.Fingerprint != d.Progress.ParentEvent.Fingerprint {
		return StoredDecision{}, policy.ErrAccess
	}
	next := d.Clone()
	next.Progress.Phase = "linking"
	next.Progress.ParentAlertID = p.AlertID
	if p.Status.Terminal() {
		next.Progress.Phase = "ending"
		next.Progress.ReasonCode = parentEndReason(p)
	}
	next.Progress.UpdatedAt = at
	return j.put(ctx, current, next)
}

// RejectBeforeEvent 只在尚未准备父 Event 时转为失败释放，不能取消可能已经入队的父事件。
func (j *Journal) RejectBeforeEvent(ctx context.Context, current StoredDecision, reason string, at time.Time) (StoredDecision, error) {
	if current.Decision.Progress.Phase != "capturing" {
		return StoredDecision{}, policy.ErrConflict
	}
	next := current.Decision.Clone()
	next.Progress.Phase = "releasing"
	next.Progress.ReasonCode = reason
	next.Progress.UpdatedAt = at
	return j.put(ctx, current, next)
}

// RecordNoParent 只接受已完成且未形成自身合并父告警的 Event；聚合抑制的其他关联不充当父。
// 不把基础设施处理失败或尚未完成视为已经被策略抑制。
func (j *Journal) RecordNoParent(ctx context.Context, current StoredDecision, event store.StoredEvent, at time.Time) (StoredDecision, error) {
	d := current.Decision
	if d.Progress.Phase != "waiting_parent" || d.Progress.ParentEvent == nil || event.Version.IsZero() || event.Validate() != nil || event.Processing.State == domain.EventProcessStateUnprocessed || !noOwnParent(event) {
		return StoredDecision{}, policy.ErrInvalid
	}
	if event.Event.BKTenantID != d.TenantID || event.Event.EventID != d.Progress.ParentEvent.EventID || !reflect.DeepEqual(event.Event.MergeOrigin, d.Progress.ParentEvent.MergeOrigin) {
		return StoredDecision{}, policy.ErrAccess
	}
	if err := domain.ValidateEventRedelivery(*d.Progress.ParentEvent, event.Event); err != nil {
		return StoredDecision{}, policy.ErrConflict
	}
	next := d.Clone()
	next.Progress.Phase = "releasing"
	next.Progress.ReasonCode = "parent_event_" + string(event.Processing.State)
	next.Progress.UpdatedAt = at
	return j.put(ctx, current, next)
}

// AdvanceMembers 只在前缀内每个成员的关系/释放步骤确认后调用；不允许回退或越过固定集合。
func (j *Journal) AdvanceMembers(ctx context.Context, current StoredDecision, through int, at time.Time) (StoredDecision, error) {
	if current.Decision.Progress.Phase != "linking" && current.Decision.Progress.Phase != "releasing" {
		return StoredDecision{}, policy.ErrConflict
	}
	next := current.Decision.Clone()
	next.Progress.MemberOffset = through
	next.Progress.UpdatedAt = at
	return j.put(ctx, current, next)
}

// Complete 仅在全部成员处理完后完成；成功分支还须核对真实父已完成关系准入。
func (j *Journal) Complete(ctx context.Context, current StoredDecision, parent *store.StoredAlert, at time.Time) (StoredDecision, error) {
	d := current.Decision
	if (d.Progress.Phase != "linking" && d.Progress.Phase != "releasing") || d.Progress.MemberOffset != len(d.WaitMemberIDs) {
		return StoredDecision{}, policy.ErrConflict
	}
	if d.Progress.Phase == "linking" {
		if parent == nil || parent.Version.IsZero() || parent.Alert.Validate() != nil || parent.Alert.BKTenantID != d.TenantID || parent.Alert.AlertID != d.Progress.ParentAlertID || parent.Alert.Merge == nil || parent.Alert.Merge.Role != "aggregate" || !parent.Alert.Merge.RelationsReady || !slices.Contains(parent.Alert.Merge.RelationIDs, d.ID) {
			return StoredDecision{}, policy.ErrInvalid
		}
	}
	next := d.Clone()
	next.Progress.Phase = "completed"
	next.Progress.UpdatedAt = at
	return j.put(ctx, current, next)
}

// WorkPage 用全局文档游标公平扫描执行记录；每条执行仍必须回到其明确租户和实时 CAS token。
type WorkPage struct {
	Decisions []Decision
	Next      string
}

// ListWork 仅供控制面扫描，不按最近天数截断；业务完成但缓存提示尚未清理的行仍保留。
func (j *Journal) ListWork(ctx context.Context, after string, limit int) (WorkPage, error) {
	if len(after) > 256 || limit < 1 || limit > 16 {
		return WorkPage{}, policy.ErrInvalid
	}
	rows, err := j.docs.List(ctx, "merge_decisions", "", after, limit)
	if err != nil {
		return WorkPage{}, err
	}
	if len(rows) > limit {
		return WorkPage{}, policy.ErrInvalid
	}
	page := WorkPage{}
	last := after
	for _, raw := range rows {
		var d Decision
		if len(raw) > 8<<20 || json.Unmarshal(raw, &d) != nil || d.Validate() != nil {
			return WorkPage{}, policy.ErrInvalid
		}
		key, err := decisionKey(d.TenantID, d.ID)
		if err != nil || key <= last {
			return WorkPage{}, policy.ErrInvalid
		}
		last = key
		if d.Progress.Phase != "completed" || !d.Progress.WindowFinished {
			page.Decisions = append(page.Decisions, d)
		}
	}
	if len(rows) == limit {
		page.Next = last
	}
	return page, nil
}

// MarkWindowFinished 只在 Redis 窗口提示清理成功后确认；业务结果保持不变。
func (j *Journal) MarkWindowFinished(ctx context.Context, current StoredDecision, at time.Time) (StoredDecision, error) {
	if current.Decision.Progress.Phase != "completed" {
		return StoredDecision{}, policy.ErrConflict
	}
	if current.Decision.Progress.WindowFinished {
		return current, nil
	}
	next := current.Decision.Clone()
	next.Progress.WindowFinished = true
	next.Progress.UpdatedAt = at
	return j.put(ctx, current, next)
}

// ListMembers 返回固定操作的快照页，游标只能来自同一租户/操作前缀。
func (j *Journal) ListMembers(ctx context.Context, tenant, id, after string, limit int) ([]Snapshot, string, error) {
	key, err := decisionKey(tenant, id)
	if err != nil {
		return nil, "", err
	}
	scope := key + ":"
	if limit < 1 || limit > 16 || len(after) > 256 || (after != "" && !strings.HasPrefix(after, scope)) {
		return nil, "", policy.ErrInvalid
	}
	rows, err := j.docs.List(ctx, "merge_members", scope, after, limit)
	if err != nil {
		return nil, "", err
	}
	if len(rows) > limit {
		return nil, "", policy.ErrInvalid
	}
	result := []Snapshot{}
	last := after
	for _, raw := range rows {
		var s Snapshot
		if len(raw) > 8<<20 || json.Unmarshal(raw, &s) != nil || s.Alert.Validate() != nil {
			return nil, "", policy.ErrInvalid
		}
		if s.TenantID != tenant || s.DecisionID != id || s.Alert.BKTenantID != tenant {
			return nil, "", policy.ErrAccess
		}
		k, err := snapshotKey(tenant, id, s.Alert.AlertID)
		if err != nil || k <= last {
			return nil, "", fmt.Errorf("unordered merge member page")
		}
		last = k
		result = append(result, s)
	}
	if len(rows) < limit {
		last = ""
	}
	return result, last, nil
}

// 聚合抑制可能关联另一条 Alert，但那不是本次成员集合所产生的合并父告警。
func noOwnParent(e store.StoredEvent) bool {
	if len(e.Event.RelatedAlertIDs) == 0 {
		return true
	}
	if e.Processing.State != domain.EventProcessStateSuppressed || e.Processing.ReasonCode != "aggregation_suppressed" || e.Processing.PolicyDecision == nil || e.Processing.PolicyDecision.Suppression == nil {
		return false
	}
	values := e.Processing.PolicyDecision.Suppression.Evaluations
	return len(values) == 1 && values[0].Suppressed && values[0].ReasonCode == "aggregation_suppressed"
}
