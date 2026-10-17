// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store"
)

type MemberBatchItem struct {
	AlertID  string `json:"alert_id"`
	Selected bool   `json:"selected"`
	State    string `json:"state"`
}

// MemberBatch 是单进程内存批次。成员 State 为空时需要执行，进程丢失后允许重建并重放。
type MemberBatch struct {
	Key     string                    `json:"key"`
	Phase   string                    `json:"phase"`
	Facts   domain.MergeMemberContext `json:"facts"`
	Members []MemberBatchItem         `json:"members"`
	State   string                    `json:"state"`
}

type memberBatchOperator interface {
	ProcessMergeMemberBatch(context.Context, domain.MergeMemberContext, []string, bool) []store.AlertUpdateResult
}

func memberBatchKey(r domain.MergeRelation, phase string, offset int) string {
	return prefix(r.TenantID) + r.ID + ":" + phase + ":" + fmt.Sprintf("%09d", offset)
}

// memberPool 从关系固定成员生成本阶段批次；只有进程内结果，不读写批次存储。
func (e *Executor) memberPool(r domain.MergeRelation, phase string) ([]MemberBatch, bool) {
	ids := r.WaitMemberIDs
	if phase == "references" {
		ids = make([]string, 1, len(r.Members)+1)
		ids[0] = r.ParentAlertID
		for _, m := range r.Members {
			ids = append(ids, m.AlertID)
		}
	}
	wanted := []MemberBatch{}
	size := e.Batch.WithDefaults().MemberBatchSize
	for start := 0; start < len(ids); start += size {
		b := MemberBatch{Key: memberBatchKey(r, phase, start), Phase: phase, Facts: r.MemberContext(), State: "pending"}
		for _, id := range ids[start:min(start+size, len(ids))] {
			_, selected := r.Member(id)
			b.Members = append(b.Members, MemberBatchItem{AlertID: id, Selected: selected})
		}
		wanted = append(wanted, b)
	}
	return e.Members.Stage(wanted)
}

// ProcessMemberBatch 执行一个内存批次，直接返回逐项结果；不保存中间任务进度。
// 业务写入、日志和输出保持原幂等身份，允许进程重启后重放整个阶段。
func (e *Executor) ProcessMemberBatch(ctx context.Context, b MemberBatch) (MemberBatch, error) {
	if err := ctx.Err(); err != nil {
		return b, err
	}
	if b.State == "completed" {
		return b, nil
	}
	if b.Phase == "references" {
		if err := e.Journal.createBatchReferences(ctx, b); err != nil {
			return b, err
		}
		for i := range b.Members {
			b.Members[i].State = "referenced"
		}
	} else {
		// 1. 每批实时确认父，提前结束只取消新增关系，后续解除阶段覆盖固定成员。
		parent, err := e.alert(ctx, b.Facts.TenantID, b.Facts.ParentAlertID)
		if err != nil {
			return b, err
		}
		if b.Phase == "link" && parent.Alert.Status.Terminal() {
			for i := range b.Members {
				if b.Members[i].State == "" {
					b.Members[i].State = "canceled"
				}
			}
		} else if err := e.processBatchMembers(ctx, &b); err != nil {
			return b, err
		}
	}
	// 2. 返回完成结果供协调器直接汇总，缺项不放行父；失败结果保留成功成员。
	b.State = "completed"
	for _, member := range b.Members {
		if member.State == "" {
			b.State = "pending"
			break
		}
	}
	return b, nil
}

func (e *Executor) processBatchMembers(ctx context.Context, b *MemberBatch) error {
	ids, positions := []string{}, []int{}
	released := []int{}
	for i, m := range b.Members {
		if m.State != "" {
			continue
		}
		if b.Phase == "link" && !m.Selected {
			released = append(released, i)
			continue
		}
		ids, positions = append(ids, m.AlertID), append(positions, i)
	}
	failures := make([]error, len(b.Members))
	if len(ids) > 0 {
		results := e.Operations.(memberBatchOperator).ProcessMergeMemberBatch(ctx, b.Facts, ids, b.Phase == "unlink")
		if len(results) != len(ids) {
			return policy.ErrInvalid
		}
		for j, i := range positions {
			item, m := results[j], b.Members[i]
			err := item.Err
			if b.Phase == "unlink" && !m.Selected && errors.Is(err, store.ErrNotFound) {
				err = nil
			} else if err == nil {
				err = validatePoolMember(b.Facts, m.AlertID, item.StoredAlert, b.Phase == "unlink")
			}
			failures[i] = err
			if err == nil {
				b.Members[i].State = "linked"
				if b.Phase == "unlink" {
					b.Members[i].State = "unlinked"
				} else if item.Alert.Status.Terminal() {
					b.Members[i].State = "terminal"
				}
			}
		}
	}
	// 3. 未选中候选仍按原准入规则释放；与选中成员同样独立记录结果。
	var wg sync.WaitGroup
	for _, i := range released {
		wg.Go(func() {
			m := b.Members[i]
			actual, err := e.Operations.ReleaseMergeWindowWithReason(ctx, b.Facts.TenantID, m.AlertID, b.Facts.WindowID, "member_not_selected")
			err = validateReleasedMember(b.Facts.TenantID, m.AlertID, b.Facts.WindowID, actual, err)
			failures[i] = err
			if err == nil {
				b.Members[i].State = "released"
			}
		})
	}
	wg.Wait()
	return errors.Join(failures...)
}

func validatePoolMember(f domain.MergeMemberContext, id string, actual store.StoredAlert, unlink bool) error {
	a := actual.Alert
	if actual.Version.IsZero() || a.Validate() != nil || a.MergeChange != nil || a.PolicyChange != nil {
		return policy.ErrInvalid
	}
	if a.BKTenantID != f.TenantID || a.AlertID != id {
		return policy.ErrAccess
	}
	if a.Status.Terminal() {
		return nil
	}
	linked := a.Merge != nil && slices.Contains(a.Merge.RelationIDs, f.ID)
	if linked == unlink {
		return policy.ErrConflict
	}
	if a.Merge != nil {
		for _, w := range a.Merge.Pending {
			if w.WindowID == f.WindowID {
				return policy.ErrConflict
			}
		}
	}
	return nil
}

func (j *Journal) createBatchReferences(ctx context.Context, b MemberBatch) error {
	documents := map[string]json.RawMessage{}
	keys := []string{}
	for _, m := range b.Members {
		ref := relationReference{TenantID: b.Facts.TenantID, AlertID: m.AlertID, RelationID: b.Facts.ID, Role: "member"}
		if m.AlertID == b.Facts.ParentAlertID {
			ref.Role = "parent"
		}
		scope, err := referenceScope(ref.TenantID, ref.AlertID)
		if err != nil {
			return err
		}
		key := scope + ref.RelationID
		raw, err := json.Marshal(ref)
		if err != nil {
			return err
		}
		documents[key], keys = raw, append(keys, key)
	}
	for start := 0; start < len(keys); start += documentBatchSize {
		part := keys[start:min(start+documentBatchSize, len(keys))]
		writes := map[string]json.RawMessage{}
		for _, key := range part {
			writes[key] = documents[key]
		}
		if err := j.docs.PutReferences(ctx, writes); err != nil {
			return err
		}
	}
	return nil
}

// stepMemberPool 在组租约内准备阶段并汇总结果，成员由本进程调度器按空闲名额执行。
func (e *Executor) stepMemberPool(ctx context.Context, current StoredDecision, relation StoredRelation, parent store.StoredAlert, at time.Time) (bool, error) {
	r := relation.Relation
	if r.State == "ended" {
		// 关系已结束但裁决提交可能失败；只补齐裁决，不能重建解联或改写结束事实。
		e.Members.forget(r.TenantID, r.ID, "unlink")
		err := e.Journal.completeEndedDecision(ctx, r.TenantID, r.ID, at)
		return err == nil, err
	}
	ending := parent.Alert.Status.Terminal() || current.Decision.Progress.Phase == "ending"
	// 1. 父提前结束优先进入清理；引用完成屏障仍早于成员建联或解除。
	if ending && r.State != "ending" && r.State != "ended" {
		var err error
		parent, err = e.Operations.RecoverMergeParent(ctx, r.TenantID, r.ParentAlertID, r.ID)
		if err != nil {
			return false, err
		}
		_, err = e.Journal.BeginEndRelation(ctx, r.TenantID, r.ID, parent, at)
		return err == nil, err
	}
	phase := "link"
	// 2. 当前阶段只在内存登记一次，执行结果由调度器直接返回。
	if r.IndexOffset < len(r.Members)+1 {
		phase = "references"
	} else if ending {
		e.Members.forget(r.TenantID, r.ID, "link")
		phase = "unlink"
	}
	batches, done := e.memberPool(r, phase)
	if !done {
		return false, nil
	}
	// 3. 全部内存完成结果收齐后汇总组进度，成员执行不竞争此处的组 CAS。
	next := r.Clone()
	next.UpdatedAt = at
	if phase == "references" {
		next.IndexOffset = len(r.Members) + 1
		_, err := e.Journal.putRelation(ctx, relation, next)
		if err == nil {
			e.Members.forget(r.TenantID, r.ID, phase)
		}
		return err == nil, err
	}
	if phase == "unlink" {
		next.EndOffset, next.State, next.EndedAt = len(r.WaitMemberIDs), "ended", &at
		if _, err := e.Journal.putRelation(ctx, relation, next); err != nil {
			return false, err
		}
		e.Members.forget(r.TenantID, r.ID, phase)
		return true, e.Journal.completeEndedDecision(ctx, r.TenantID, r.ID, at)
	}
	states := map[string]string{}
	for _, batch := range batches {
		for _, member := range batch.Members {
			if member.State == "canceled" {
				return false, nil
			} // 下轮父实时读取将转入结束阶段。
			if member.Selected {
				states[member.AlertID] = member.State
			}
		}
	}
	for i, m := range next.Members {
		// 终态检查可能晚于成员回报、早于本次汇总；保留已经确认的终态，不能被旧 linked 回报退回。
		if m.State != "terminal" {
			next.Members[i].State = states[m.AlertID]
		}
	}
	if _, err := e.Journal.putRelation(ctx, relation, next); err != nil {
		return false, err
	}
	_, err := e.Journal.AdvanceMembers(ctx, current, len(current.Decision.WaitMemberIDs), at)
	if err == nil {
		e.Members.forget(r.TenantID, r.ID, phase)
	}
	return err == nil, err
}

func (e *Executor) usesMemberPool() bool {
	_, operations := e.Operations.(memberBatchOperator)
	return operations && e.Members != nil
}
