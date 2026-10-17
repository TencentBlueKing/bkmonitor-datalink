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

	"linkd/internal/config"
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
	Batch         config.MergeControlConfig
	Journal       *Journal
	Publisher     *Publisher
	Judge         WindowJudge
	Windows       WindowState
	Policies      policyruntime.ReleaseReader
	Operations    Operations
	CurrentAlert  func(context.Context, string, string) (store.StoredAlert, error)
	CurrentAlerts func(context.Context, string, []string) (store.AlertBatch, error)
	Event         func(context.Context, string, string) (store.StoredEvent, error)
	Source        func(context.Context) (eventsource.Release, error)
	Severity      func() runtimeconfig.Snapshot
	// Members 只保存本进程批次；重启从持久关系重建，成员操作可幂等重放。
	Members *MemberPool
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
	_, err := e.checkWork(ctx, item, at)
	return err
}

const waitingMemberTimeout = 3 * time.Second

// CheckWorkBatch 在同一窗口租约内处理本页成员：正常窗口只裁决一次，丢失等待和迟到成员逐项补偿。
// 一个成员失败不跳过其他成员；调用方按租户/窗口分组，页大小和租约期限限制总工作量。
func (e *Executor) CheckWorkBatch(ctx context.Context, items []store.MergeWorkItem, at time.Time) error {
	windowChecked := false
	var failures []error
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if windowChecked && item.WindowID != "" {
			continue
		}
		memberWork, err := e.checkWork(ctx, item, at)
		if err != nil {
			failures = append(failures, err)
		} else if !memberWork {
			windowChecked = true
		}
	}
	return errors.Join(failures...)
}

// checkWork 的 bool 表示仍需检查后续成员，false 表示本批窗口裁决已经完成。
func (e *Executor) checkWork(ctx context.Context, item store.MergeWorkItem, at time.Time) (bool, error) {
	if err := e.validate(); err != nil {
		return true, err
	}
	if at.IsZero() || item.Alert.Version.IsZero() || item.Alert.Alert.Validate() != nil {
		return true, policy.ErrInvalid
	}
	tenant, id := item.Alert.Alert.BKTenantID, item.Alert.Alert.AlertID
	// 成员读取/旧意图补齐单独限时，不能让一个坏成员耗尽整个窗口作业。
	call, cancel := context.WithTimeout(ctx, waitingMemberTimeout)
	current, err := e.alert(call, tenant, id)
	if err == nil && (current.Alert.MergeChange != nil || current.Alert.PolicyChange != nil) {
		current, err = e.Operations.FinishMergeChanges(call, tenant, id)
	}
	cancel()
	if err != nil {
		return true, err
	}
	if item.WindowID == "" {
		return true, nil
	}
	a := current.Alert
	if a.Merge == nil || a.Status.Terminal() {
		return true, nil
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
		return true, nil
	}
	existing, err := e.Journal.GetByWindow(ctx, tenant, wait.WindowID)
	if err == nil {
		if existing.Decision.Progress.Phase == "completed" {
			call, cancel := context.WithTimeout(ctx, waitingMemberTimeout)
			defer cancel()
			return true, e.completeLateMember(call, existing, current, at)
		}
		return false, e.StepDecision(ctx, tenant, existing.Decision.ID, at)
	}
	if !errors.Is(err, policy.ErrNotFound) {
		return true, err
	}
	level := e.Severity()
	result, err := e.Judge.Evaluate(ctx, tenant, wait.WindowID, at, level.KACLevel)
	if err != nil {
		return true, err
	}
	if result.Lost {
		call, cancel := context.WithTimeout(ctx, waitingMemberTimeout)
		defer cancel()
		_, err := e.Journal.ReleaseLostWait(call, current, wait.WindowID, e.Windows, e.Operations, at)
		return true, err
	}
	if !result.Frozen {
		return false, nil
	}
	if result.Window.TenantID != tenant || result.Window.ID != wait.WindowID || result.Window.Policy != wait.Policy || result.Window.GroupKey != wait.GroupKey {
		return true, policy.ErrAccess
	}
	return false, e.claim(ctx, result, at)
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
	// 先保留本轮裁决返回的全部首次快照；中断恢复才按成员预算补齐缺失项。
	// 完整集合受32 MiB预算约束，调用者的十秒期限仍适用；中断时已保存项保持create-only。
	snapshots := make(map[string]store.StoredAlert, len(result.Members))
	for _, m := range result.Members {
		_, selected := slices.BinarySearch(d.MemberIDs, m.Alert.AlertID)
		if m.Version.IsZero() || m.Alert.BKTenantID != w.TenantID || m.Alert.Validate() != nil || !selected {
			return policy.ErrInvalid
		}
		snapshots[m.Alert.AlertID] = m
	}
	first := make([]domain.Alert, 0, len(d.MemberIDs))
	for _, id := range d.MemberIDs {
		member, ok := snapshots[id]
		if !ok {
			return policy.ErrInvalid
		}
		first = append(first, member.Alert)
	}
	if err := e.Journal.captureMany(ctx, saved, first); err != nil {
		return err
	}
	if saved.Decision.Progress.CaptureOffset == len(d.MemberIDs) {
		return nil
	}
	// 全部 create-only 批次已经确认，直接保存完整前缀，不再逐块读回自己的写入。
	_, err = e.Journal.advanceCaptured(ctx, saved, len(d.MemberIDs), at)
	return err
}

func (e *Executor) capture(ctx context.Context, current StoredDecision, snapshots map[string]store.StoredAlert, at time.Time) error {
	// 1. 先确认已持久化的前缀，读取后续缺失项失败不丢掉已有进展。
	d := current.Decision
	end := d.Progress.CaptureOffset + min(e.Batch.WithDefaults().MemberBatchSize, len(d.MemberIDs)-d.Progress.CaptureOffset)
	ids := d.MemberIDs[d.Progress.CaptureOffset:end]
	existing, err := e.Journal.snapshots(ctx, d.TenantID, d.ID, ids)
	if err != nil {
		return err
	}
	through := d.Progress.CaptureOffset
	for _, id := range ids {
		if _, found := existing[id]; !found {
			break
		}
		through++
	}
	if through > d.Progress.CaptureOffset {
		current, err = e.Journal.advanceCaptured(ctx, current, through, at)
		if err != nil {
			return err
		}
	}
	if through == end {
		return nil
	}

	// 2. 只读取缺失的快照；实时批读与首次裁决快照不能覆盖已有文档。
	missing := []string{}
	alerts := []domain.Alert{}
	for _, id := range d.MemberIDs[through:end] {
		if _, found := existing[id]; found {
			continue
		}
		if a, found := snapshots[id]; found {
			alerts = append(alerts, a.Alert)
		} else {
			missing = append(missing, id)
		}
	}
	if e.CurrentAlerts != nil && len(missing) > 0 {
		found := map[string]store.StoredAlert{}
		for start := 0; start < len(missing); start += documentBatchSize {
			chunk := missing[start:min(start+documentBatchSize, len(missing))]
			batch, err := e.CurrentAlerts(ctx, d.TenantID, chunk)
			if err != nil {
				return err
			}
			for _, a := range batch.Alerts {
				if a.Alert.BKTenantID != d.TenantID || !slices.Contains(chunk, a.Alert.AlertID) {
					return policy.ErrAccess
				}
				found[a.Alert.AlertID] = a
			}
		}
		for _, id := range missing {
			a, ok := found[id]
			if !ok {
				return store.ErrNotFound
			}
			alerts = append(alerts, a.Alert)
		}
	} else {
		for _, id := range missing {
			a, err := e.alert(ctx, d.TenantID, id)
			if err != nil {
				return err
			}
			alerts = append(alerts, a.Alert)
		}
	}
	if err := e.Journal.captureMany(ctx, current, alerts); err != nil {
		return err
	}

	// 3. 完整块持久化后只写一次进度，失败按原 CAS 和 create-only 快照重试。
	_, err = e.Journal.advanceCaptured(ctx, current, end, at)
	return err
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
		_, err = e.Journal.prepare(ctx, current, parent, at)
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

// StepDecisionBatch 推进一个持久阶段或成员块，报告是否有进展可立即续批。
// 等待父 Event 与无进展时返回false；显式诊断请求仍保持 StepDecision 单步语义。
func (e *Executor) StepDecisionBatch(ctx context.Context, tenant, id string, at time.Time) (bool, error) {
	if err := e.validate(); err != nil {
		return false, err
	}
	current, err := e.Journal.Get(ctx, tenant, id)
	if err != nil {
		return false, err
	}
	if at.Before(current.Decision.Progress.UpdatedAt) {
		return false, policy.ErrInvalid
	}
	d := current.Decision
	budget := e.Batch.WithDefaults()
	// 1. 建联先固定本批关系；查询引用及成员分别批量保存，不能每个成员重走完整准备链。
	if d.Progress.Phase == "linking" || d.Progress.Phase == "ending" {
		parent, err := e.alert(ctx, tenant, d.Progress.ParentAlertID)
		if err != nil {
			return false, err
		}
		ending := parent.Alert.Status.Terminal() || d.Progress.Phase == "ending"
		relation, err := e.Journal.GetRelation(ctx, tenant, id)
		if errors.Is(err, policy.ErrNotFound) {
			relation, err = e.Journal.ensureRelation(ctx, tenant, id, parent, at, ending)
		}
		if err != nil {
			return false, err
		}
		if e.usesMemberPool() && (ending || relation.Relation.IndexOffset < len(relation.Relation.Members)+1 || d.Progress.MemberOffset < len(d.WaitMemberIDs)) {
			return e.stepMemberPool(ctx, current, relation, parent, at)
		}
		if ending {
			next, err := e.Journal.EndRelationBatchWithBudget(ctx, tenant, id, e.Operations, at, budget.MemberBatchSize, budget.Concurrency)
			return err == nil && next.Version != relation.Version, err
		}
		if relation.Relation.IndexOffset < len(relation.Relation.Members)+1 {
			next, err := e.Journal.IndexRelationBatch(ctx, relation, at, budget.MemberBatchSize)
			return err == nil && next.Version != relation.Version, err
		}
		if d.Progress.MemberOffset < len(d.WaitMemberIDs) {
			next, err := e.Journal.linkMembersBatch(ctx, current, relation, parent, e.Operations, at, budget.MemberBatchSize, budget.Concurrency)
			return err == nil && next.Version != current.Version, err
		}
	}
	// 2. 其余阶段及最后Ready仍走原业务步骤，父准入必须实时确认全体成员的持久结果。
	if err := e.StepDecision(ctx, tenant, id, at); err != nil {
		return false, err
	}
	next, err := e.Journal.Get(ctx, tenant, id)
	if err != nil {
		return false, err
	}
	return next.Version != current.Version && (next.Decision.Progress.Phase != "completed" || !next.Decision.Progress.WindowFinished), nil
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
// 每步按配置预算确认成员终态；无进展和已结束关系立即停止，失败/取消后的确认由下次调用继续。
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

// CheckRelationBatch 供自动任务推进一批，并报告是否可在新租约中立即续批。
// 无进展、结束、失败或取消都不请求续跑；显式诊断仍保留 CheckRelation 的原步骤契约。
func (e *Executor) CheckRelationBatch(ctx context.Context, tenant, id string, at time.Time) (bool, error) {
	if err := e.validate(); err != nil {
		return false, err
	}
	current, err := e.Journal.GetRelation(ctx, tenant, id)
	if err != nil {
		return false, err
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	var next StoredRelation
	if current.Relation.State == "ending" || current.Relation.State == "ended" {
		if e.usesMemberPool() {
			if current.Relation.State == "ended" {
				return false, e.Journal.completeEndedDecision(ctx, tenant, id, at)
			}
			decision, err := e.Journal.Get(ctx, tenant, id)
			if err != nil {
				return false, err
			}
			parent, err := e.alert(ctx, tenant, current.Relation.ParentAlertID)
			if err != nil {
				return false, err
			}
			return e.stepMemberPool(ctx, decision, current, parent, at)
		}
		budget := e.Batch.WithDefaults()
		next, err = e.Journal.EndRelationBatchWithBudget(ctx, tenant, id, e.Operations, at, budget.MemberBatchSize, budget.Concurrency)
	} else {
		// 活动关系先批量确认成员终态或发现父结束；后续 ending 直接走批量解除。
		next, err = e.checkRelationStep(ctx, current, at)
	}
	if err != nil {
		return false, err
	}
	return next.Version != current.Version && next.Relation.State != "ended", nil
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
	// 1. 只读取尚未确认的成员，实际批读与成员块使用相同预算。
	budget := e.Batch.WithDefaults().MemberBatchSize
	ids := make([]string, 0, min(budget, len(r.Members)))
	more := false
	for _, member := range r.Members {
		if member.State == "terminal" {
			continue
		}
		if len(ids) == budget {
			more = true
			break
		}
		ids = append(ids, member.AlertID)
	}
	confirmed := make([]store.StoredAlert, 0, len(ids))
	// 保留已验证终态的连续前缀；活动成员或后续读取失败不能丢掉前面已完成的真实确认。
	flush := func(cause error) (StoredRelation, error) {
		if len(confirmed) == 0 {
			return current, cause
		}
		next, err := e.Journal.ConfirmRelationMembers(ctx, current, confirmed, at)
		if err != nil {
			return current, errors.Join(cause, err)
		}
		current = next
		return current, cause
	}
	for start := 0; start < len(ids); start += documentBatchSize {
		chunk := ids[start:min(start+documentBatchSize, len(ids))]
		var found map[string]store.StoredAlert
		if e.CurrentAlerts != nil {
			batch, err := e.CurrentAlerts(ctx, r.TenantID, chunk)
			if err != nil {
				return flush(err)
			}
			found = make(map[string]store.StoredAlert, len(batch.Alerts))
			for _, actual := range batch.Alerts {
				if _, exists := found[actual.Alert.AlertID]; !exists {
					found[actual.Alert.AlertID] = actual
				}
			}
		}
		// 2. 批读只减少 I/O，仍按固定成员顺序确认；遇首个活动成员立即停止，不修复后序项。
		for _, id := range chunk {
			var actual store.StoredAlert
			if e.CurrentAlerts != nil {
				var exists bool
				actual, exists = found[id]
				if !exists {
					return flush(store.ErrNotFound)
				}
				if actual.Alert.BKTenantID != r.TenantID || actual.Alert.AlertID != id {
					return flush(policy.ErrAccess)
				}
				if actual.Version.IsZero() || actual.Alert.Validate() != nil {
					return flush(policy.ErrInvalid)
				}
			} else {
				actual, err = e.alert(ctx, r.TenantID, id)
				if err != nil {
					return flush(err)
				}
			}
			if !actual.Alert.Status.Terminal() {
				return flush(nil)
			}
			if actual.Alert.MergeChange != nil || actual.Alert.PolicyChange != nil {
				if _, err := e.Operations.FinishMergeChanges(ctx, r.TenantID, id); err != nil {
					return flush(err)
				}
				actual, err = e.alert(ctx, r.TenantID, id)
				if err != nil {
					return flush(err)
				}
				if !actual.Alert.Status.Terminal() {
					return flush(policy.ErrInvalid)
				}
			}
			confirmed = append(confirmed, actual)
		}
	}
	// 3. 本批一次关系 CAS 保存全部确认；到预算边界返回进展，由下一窗口租约继续。
	if _, err := flush(nil); err != nil {
		return current, err
	}
	if more {
		return current, nil
	}
	return e.Journal.ReconcileRelationStep(ctx, r.TenantID, r.ID, e.Operations, at)
}

func (e *Executor) completeLateMember(ctx context.Context, current StoredDecision, member store.StoredAlert, at time.Time) error {
	d := current.Decision
	if _, found := slices.BinarySearch(d.WaitMemberIDs, member.Alert.AlertID); !found {
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
		if _, found := slices.BinarySearch(d.MemberIDs, member.Alert.AlertID); found {
			actual, err := e.Operations.LinkMergeMember(ctx, d.TenantID, member.Alert.AlertID, d.ID)
			if err != nil {
				return err
			}
			_, err = e.Journal.ConfirmRelationMember(ctx, r, actual, at)
			return err
		}
	}
	reason := "conditions_not_met"
	if d.Outcome == "succeeded" {
		reason = "member_not_selected"
	}
	actual, err := e.Operations.ReleaseMergeWindowWithReason(ctx, d.TenantID, member.Alert.AlertID, d.WindowID, reason)
	return validateReleasedMember(d.TenantID, member.Alert.AlertID, d.WindowID, actual, err)
}
