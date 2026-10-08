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
	"errors"
	"fmt"
	"slices"
	"time"

	"linkd/internal/domain"
	"linkd/internal/eventsource"
	"linkd/internal/policy"
	policyruntime "linkd/internal/policy/runtime"
	"linkd/internal/runtimeconfig"
	"linkd/internal/store"
)

// Operations 把每个单 Alert 副作用放回正式 fingerprint lease，窗口调度不持有多个 Alert 锁。
type Operations interface {
	RelationOperator
	FinishMergeChanges(context.Context, string, string) (store.StoredAlert, error)
}

// WindowJudge 使用冻结策略和真实成员对一个窗口裁决，不在事件处理线程等待窗口到期。
type WindowJudge interface {
	Evaluate(context.Context, string, string, time.Time, func(string) (string, error)) (policyruntime.MergeJudgment, error)
}

// WindowState 保留未裁决窗口读取和已完成提示清理，窗口丢失不负责重建历史。
type WindowState interface {
	WindowReader
	FinishMergeWindow(context.Context, string, string) error
}

// Executor 推进独立合并任务的业务步骤；调用方必须按租户/窗口串行并限制执行时间。
// 当前成员/事件读取必须是实时仓储读取，不能用有 refresh 延迟的搜索结果确认业务完成。
type Executor struct {
	Journal      *Journal
	Publisher    *Publisher
	Judge        WindowJudge
	Windows      WindowState
	Policies     policyruntime.ReleaseReader
	Operations   Operations
	CurrentAlert func(context.Context, string, string) (store.StoredAlert, error)
	Event        func(context.Context, string, string) (store.StoredEvent, error)
	Source       func(context.Context) (eventsource.Release, error)
	Severity     func() runtimeconfig.Snapshot
}

func (e *Executor) validate() error {
	if e.Journal == nil || e.Publisher == nil || e.Judge == nil || e.Windows == nil || e.Policies == nil || e.Operations == nil || e.CurrentAlert == nil || e.Event == nil || e.Source == nil || e.Severity == nil {
		return policy.ErrInvalid
	}
	return nil
}

func (e *Executor) alert(ctx context.Context, tenant, id string) (store.StoredAlert, error) {
	a, err := e.CurrentAlert(ctx, tenant, id)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if a.Alert.BKTenantID != tenant || a.Alert.AlertID != id {
		return store.StoredAlert{}, policy.ErrAccess
	}
	if a.Version.IsZero() || a.Alert.Validate() != nil {
		return store.StoredAlert{}, policy.ErrInvalid
	}
	return a, nil
}

// CheckWork 重读扫描项并处理一个窗口，先完成旧意图；有持久化裁决时绝不再次选择成员。
func (e *Executor) CheckWork(ctx context.Context, item store.MergeWorkItem, at time.Time) error {
	if err := e.validate(); err != nil {
		return err
	}
	if at.IsZero() || item.Alert.Version.IsZero() || item.Alert.Alert.Validate() != nil {
		return policy.ErrInvalid
	}
	tenant, id := item.Alert.Alert.BKTenantID, item.Alert.Alert.AlertID
	current, err := e.alert(ctx, tenant, id)
	if err != nil {
		return err
	}
	if current.Alert.MergeChange != nil || current.Alert.PolicyChange != nil {
		current, err = e.Operations.FinishMergeChanges(ctx, tenant, id)
		if err != nil {
			return err
		}
	}
	if item.WindowID == "" {
		return nil
	}
	a := current.Alert
	if a.Merge == nil || a.Status.Terminal() {
		return nil
	}
	var wait *domain.MergeWait
	for _, w := range a.Merge.Pending {
		if w.WindowID == item.WindowID {
			v := w.Clone()
			wait = &v
			break
		}
	}
	if wait == nil {
		return nil
	}
	existing, err := e.Journal.GetByWindow(ctx, tenant, wait.WindowID)
	if err == nil {
		if existing.Decision.Progress.Phase == "completed" {
			return e.completeLateMember(ctx, existing, current, at)
		}
		return e.StepDecision(ctx, tenant, existing.Decision.ID, at)
	}
	if !errors.Is(err, policy.ErrNotFound) {
		return err
	}
	level := e.Severity()
	result, err := e.Judge.Evaluate(ctx, tenant, wait.WindowID, at, level.KACLevel)
	if err != nil {
		return err
	}
	if result.Lost {
		_, err := e.Journal.ReleaseLostWait(ctx, current, wait.WindowID, e.Windows, e.Operations, at)
		return err
	}
	if !result.Frozen {
		return nil
	}
	if result.Window.TenantID != tenant || result.Window.ID != wait.WindowID || result.Window.Policy != wait.Policy || result.Window.GroupKey != wait.GroupKey {
		return policy.ErrAccess
	}
	return e.claim(ctx, result, at)
}

func (e *Executor) claim(ctx context.Context, result policyruntime.MergeJudgment, at time.Time) error {
	w := result.Window
	if w.Frozen == nil {
		return policy.ErrInvalid
	}
	release, err := e.Policies.GetRelease(ctx, policy.Scope{TenantID: w.TenantID, Kind: policy.Merge}, w.Policy.ID, w.Policy.Version)
	if err != nil {
		return err
	}
	if release.Compiled.Digest != w.Policy.Digest {
		return policy.ErrConflict
	}
	d := Decision{ID: w.Frozen.OperationID, TenantID: w.TenantID, WindowID: w.ID, GroupKey: w.GroupKey, Policy: release, FrozenAt: time.UnixMilli(w.Frozen.AtMillis).UTC(), StartedAt: time.UnixMilli(w.StartedAtMillis).UTC(), Deadline: time.UnixMilli(w.DeadlineMillis).UTC(), Outcome: w.Frozen.Outcome, MemberIDs: slices.Clone(w.Frozen.MemberIDs)}
	for _, m := range w.Members {
		d.WaitMemberIDs = append(d.WaitMemberIDs, m.Main.AlertID)
	}
	slices.Sort(d.WaitMemberIDs)
	saved, err := e.Journal.Claim(ctx, d)
	if err != nil {
		return err
	}
	if saved.Decision.Progress.Phase != "capturing" {
		return nil
	}
	// 先保留本轮裁决返回的首次快照，不能因后续前缀预算为 16 而丢弃余下成员的原始结果。
	// 集合最多 256 项/32 MiB，调用者的十秒期限仍适用；中断时已保存项保持 create-only。
	snapshots := make(map[string]store.StoredAlert, len(result.Members))
	for _, m := range result.Members {
		if m.Version.IsZero() || m.Alert.BKTenantID != w.TenantID || m.Alert.Validate() != nil || !slices.Contains(d.MemberIDs, m.Alert.AlertID) {
			return policy.ErrInvalid
		}
		snapshots[m.Alert.AlertID] = m
	}
	for _, id := range d.MemberIDs {
		member, ok := snapshots[id]
		if !ok {
			return policy.ErrInvalid
		}
		if _, err := e.Journal.Capture(ctx, d.TenantID, d.ID, member.Alert); err != nil {
			return err
		}
	}
	return e.capture(ctx, saved, snapshots, at)
}

func (e *Executor) capture(ctx context.Context, current StoredDecision, snapshots map[string]store.StoredAlert, at time.Time) error {
	for range 16 {
		d := current.Decision
		if d.Progress.CaptureOffset == len(d.MemberIDs) {
			break
		}
		id := d.MemberIDs[d.Progress.CaptureOffset]
		_, err := e.Journal.Snapshot(ctx, d.TenantID, d.ID, id)
		if errors.Is(err, policy.ErrNotFound) {
			a, ok := snapshots[id]
			if !ok {
				a, err = e.alert(ctx, d.TenantID, id)
				if err != nil {
					return err
				}
			}
			if _, err := e.Journal.Capture(ctx, d.TenantID, d.ID, a.Alert); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		current, err = e.Journal.AdvanceCapture(ctx, current, at)
		if err != nil {
			return err
		}
	}
	return nil
}

// StepDecision 只推进已经持久化的阶段，模板准备后不再读取可变配置来重写父 Event。
func (e *Executor) StepDecision(ctx context.Context, tenant, id string, at time.Time) error {
	if err := e.validate(); err != nil {
		return err
	}
	current, err := e.Journal.Get(ctx, tenant, id)
	if err != nil {
		return err
	}
	d := current.Decision
	if at.Before(d.Progress.UpdatedAt) {
		return policy.ErrInvalid
	}
	switch d.Progress.Phase {
	case "capturing":
		if d.Progress.CaptureOffset < len(d.MemberIDs) {
			return e.capture(ctx, current, nil, at)
		}
		source, err := e.Source(ctx)
		if err != nil {
			return err
		}
		members, err := e.Journal.Members(ctx, d)
		if err != nil {
			return err
		}
		parent, err := renderParent(ctx, d, members, source, e.Severity())
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, policy.ErrAccess) {
				return err
			}
			_, err = e.Journal.RejectBeforeEvent(ctx, current, "parent_template_unavailable", at)
			return err
		}
		_, err = e.Journal.Prepare(ctx, current, parent, at)
		return err
	case "prepared":
		_, err = e.Publisher.Submit(ctx, tenant, id, at)
		return err
	case "waiting_parent":
		return e.observeParent(ctx, current, at)
	case "linking", "ending":
		parent, err := e.alert(ctx, tenant, d.Progress.ParentAlertID)
		if err != nil {
			return err
		}
		_, err = e.Journal.LinkStep(ctx, tenant, id, parent, e.Operations, at)
		return err
	case "releasing":
		_, err = e.Journal.ReleaseStep(ctx, tenant, id, e.Operations, at)
		return err
	case "completed":
		if d.Progress.WindowFinished {
			return nil
		}
		if err := e.Windows.FinishMergeWindow(ctx, tenant, d.WindowID); err != nil {
			return err
		}
		_, err = e.Journal.MarkWindowFinished(ctx, current, at)
		return err
	default:
		return policy.ErrInvalid
	}
}

func (e *Executor) observeParent(ctx context.Context, current StoredDecision, at time.Time) error {
	d := current.Decision
	event, err := e.Event(ctx, d.TenantID, d.Progress.ParentEvent.EventID)
	if err != nil {
		return err
	}
	if event.Version.IsZero() || event.Validate() != nil {
		return policy.ErrInvalid
	}
	if event.Event.BKTenantID != d.TenantID || event.Event.EventID != d.Progress.ParentEvent.EventID {
		return policy.ErrAccess
	}
	if err := domain.ValidateEventRedelivery(*d.Progress.ParentEvent, event.Event); err != nil {
		return policy.ErrConflict
	}
	if event.Processing.State == domain.EventProcessStateUnprocessed {
		return nil
	}
	if noOwnParent(event) {
		_, err = e.Journal.RecordNoParent(ctx, current, event, at)
		return err
	}
	candidates := []store.StoredAlert{}
	for _, id := range event.Event.RelatedAlertIDs {
		a, err := e.alert(ctx, d.TenantID, id)
		if err != nil {
			return err
		}
		if a.Alert.EventSourceID != domain.BuiltinMergeEventSourceID || a.Alert.Fingerprint != d.Progress.ParentEvent.Fingerprint || a.Alert.Merge == nil || a.Alert.Merge.Role != "aggregate" {
			continue
		}
		// 等级升级关闭旧生命周期时，相关列表也包含旧父；本次内部触发形成或更新的父才是关系目标。
		if a.Alert.EndType == domain.AlertEndTypeSeverityUpgrade && a.Alert.LatestEventID == event.Event.EventID {
			continue
		}
		candidates = append(candidates, a)
	}
	if len(candidates) != 1 {
		return fmt.Errorf("internal merge Event does not identify exactly one real parent")
	}
	_, err = e.Journal.RecordParent(ctx, current, candidates[0], at)
	return err
}

// CheckRelation 既扫描活动关系，也补齐结束阶段；一次最多推进 16 个持久化步骤。
// 有进展时在同一窗口租约/调用期限内继续，避免每解除一个成员都等待下一轮 30 秒扫描；
// 每步最多确认 16 个成员终态；无进展和已结束关系立即停止，失败/取消后的确认由下次调用继续。
func (e *Executor) CheckRelation(ctx context.Context, tenant, id string, at time.Time) error {
	if err := e.validate(); err != nil {
		return err
	}
	current, err := e.Journal.GetRelation(ctx, tenant, id)
	if err != nil {
		return err
	}
	for range 16 {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, err := e.checkRelationStep(ctx, current, at)
		if err != nil {
			return err
		}
		if next.Version == current.Version || next.Relation.State == "ended" {
			return nil
		}
		current = next
	}
	return nil
}

func (e *Executor) checkRelationStep(ctx context.Context, current StoredRelation, at time.Time) (StoredRelation, error) {
	r := current.Relation
	if at.Before(r.UpdatedAt) {
		return StoredRelation{}, policy.ErrInvalid
	}
	if r.State == "ending" || r.State == "ended" {
		return e.Journal.ReconcileRelationStep(ctx, r.TenantID, r.ID, e.Operations, at)
	}
	parent, err := e.alert(ctx, r.TenantID, r.ParentAlertID)
	if err != nil {
		return StoredRelation{}, err
	}
	if parent.Alert.EventSourceID != domain.BuiltinMergeEventSourceID || parent.Alert.Fingerprint != r.ParentFingerprint || parent.Alert.Merge == nil || parent.Alert.Merge.Role != "aggregate" {
		return StoredRelation{}, policy.ErrInvalid
	}
	if parent.Alert.Status.Terminal() {
		return e.Journal.ReconcileRelationStep(ctx, r.TenantID, r.ID, e.Operations, at)
	}
	if r.IndexOffset != len(r.Members)+1 {
		return current, nil // 创建任务先补齐关系引用，不能提前确认尚未建立的成员关系。
	}
	confirmed := 0
	for _, member := range r.Members {
		if member.State == "terminal" {
			continue
		}
		if confirmed == 16 {
			return current, nil
		}
		actual, err := e.alert(ctx, r.TenantID, member.AlertID)
		if err != nil {
			return StoredRelation{}, err
		}
		if !actual.Alert.Status.Terminal() {
			return current, nil
		}
		if actual.Alert.MergeChange != nil || actual.Alert.PolicyChange != nil {
			if _, err := e.Operations.FinishMergeChanges(ctx, r.TenantID, member.AlertID); err != nil {
				return StoredRelation{}, err
			}
			actual, err = e.alert(ctx, r.TenantID, member.AlertID)
			if err != nil {
				return StoredRelation{}, err
			}
		}
		// Alert 终态不可逆。逐项保存实时确认，避免十秒预算内读不完全组时每轮从头重读。
		// 使用本轮 at 维持 Journal 的单调时间；父端最终 CAS 仍由 Lifecycle 持有自己的锁。
		current, err = e.Journal.ConfirmRelationMember(ctx, current, actual, at)
		if err != nil {
			return StoredRelation{}, err
		}
		confirmed++
	}
	return e.Journal.ReconcileRelationStep(ctx, r.TenantID, r.ID, e.Operations, at)
}

func (e *Executor) completeLateMember(ctx context.Context, current StoredDecision, member store.StoredAlert, at time.Time) error {
	d := current.Decision
	if !slices.Contains(d.WaitMemberIDs, member.Alert.AlertID) {
		return policy.ErrAccess
	}
	if d.Progress.ParentAlertID != "" {
		parent, err := e.alert(ctx, d.TenantID, d.Progress.ParentAlertID)
		if err != nil {
			return err
		}
		r, err := e.Journal.GetRelation(ctx, d.TenantID, d.ID)
		if err != nil {
			return err
		}
		if parent.Alert.Status.Terminal() || r.Relation.State == "ending" || r.Relation.State == "ended" {
			r, err = e.Journal.ReconcileRelationStep(ctx, d.TenantID, d.ID, e.Operations, at)
			if err != nil {
				return err
			}
			if r.Relation.IndexOffset != len(r.Relation.Members)+1 {
				return nil
			}
			actual, err := e.Operations.UnlinkMergeMember(ctx, d.TenantID, member.Alert.AlertID, d.ID)
			return validateUnlinkedMember(r.Relation, member.Alert.AlertID, actual, err)
		}
		if r.Relation.State != "ready" {
			return policy.ErrConflict
		}
		if slices.Contains(d.MemberIDs, member.Alert.AlertID) {
			actual, err := e.Operations.LinkMergeMember(ctx, d.TenantID, member.Alert.AlertID, d.ID)
			if err != nil {
				return err
			}
			_, err = e.Journal.ConfirmRelationMember(ctx, r, actual, at)
			return err
		}
	}
	actual, err := e.Operations.ReleaseMergeWindow(ctx, d.TenantID, member.Alert.AlertID, d.WindowID)
	return validateReleasedMember(d.TenantID, member.Alert.AlertID, d.WindowID, actual, err)
}
