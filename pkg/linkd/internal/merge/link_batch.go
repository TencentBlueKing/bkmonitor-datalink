// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store"
)

type preparedRelationLinker interface {
	LinkMergeMemberPrepared(context.Context, string, string, domain.MergeRelation) (store.StoredAlert, error)
}

type preparedRelationBatchLinker interface {
	LinkMergeMembersPrepared(context.Context, string, []string, domain.MergeRelation) []store.AlertUpdateResult
}

// LinkMembersBatch 在反查引用完整后，按有界并发建立一个固定成员块。
// 每个成员仍经 Operations 的实时父检查、fingerprint lease 和 CAS；只有成功连续前缀推进持久进度。
// 全部成员处理后仍由 LinkStep 完成 Ready，不能依据本批开始时的活动父快照直接放行。
func (j *Journal) LinkMembersBatch(ctx context.Context, current StoredDecision, parent store.StoredAlert, ops RelationOperator, at time.Time, batchSize, concurrency int) (StoredDecision, error) {
	relation, err := j.GetRelation(ctx, current.Decision.TenantID, current.Decision.ID)
	if err != nil {
		return current, err
	}
	return j.linkMembersBatch(ctx, current, relation, parent, ops, at, batchSize, concurrency)
}

func (j *Journal) linkMembersBatch(ctx context.Context, current StoredDecision, relation StoredRelation, parent store.StoredAlert, ops RelationOperator, at time.Time, batchSize, concurrency int) (StoredDecision, error) {
	if err := ctx.Err(); err != nil {
		return current, err
	}
	if ops == nil || batchSize < 1 || concurrency < 1 || current.Version == "" {
		return current, policy.ErrInvalid
	}
	d := current.Decision
	if d.Progress.Phase == "completed" {
		return current, nil
	}
	if at.Before(d.Progress.UpdatedAt) {
		return current, policy.ErrInvalid
	}
	if d.Progress.Phase == "ending" || parent.Alert.Status.Terminal() {
		return j.LinkStep(ctx, d.TenantID, d.ID, parent, ops, at)
	}
	if d.Progress.Phase != "linking" {
		return current, policy.ErrConflict
	}
	// 1. 复用本次窗口租约内已经读到的关系；每个新批次仍实时读取，不跨批缓存。
	r := relation.Relation
	if (r.State != "preparing" && r.State != "ready") || r.IndexOffset != len(r.Members)+1 || at.Before(r.UpdatedAt) || parent.Alert.BKTenantID != d.TenantID || parent.Alert.AlertID != r.ParentAlertID {
		return current, policy.ErrInvalid
	}
	start := d.Progress.MemberOffset
	if start == len(d.WaitMemberIDs) {
		return current, nil
	}
	work, cancel, canStart := memberWorkContext(ctx)
	defer cancel()
	if !canStart {
		return current, nil
	}
	ids := d.WaitMemberIDs[start : start+min(batchSize, len(d.WaitMemberIDs)-start)]
	results := make([]error, len(ids))
	succeeded := make([]bool, len(ids))
	confirmed := make([]relationMemberConfirmation, len(ids))
	selected := make([]bool, len(ids))
	for i, id := range ids {
		_, selected[i] = slices.BinarySearch(d.MemberIDs, id)
	}
	prepared, canReuse := ops.(preparedRelationLinker)
	batch, canBatch := ops.(preparedRelationBatchLinker)
	if canBatch {
		batchIDs := []string{}
		positions := []int{}
		for i, id := range ids {
			if selected[i] {
				batchIDs = append(batchIDs, id)
				positions = append(positions, i)
			}
		}
		if len(batchIDs) > 0 {
			items := batch.LinkMergeMembersPrepared(work, d.TenantID, batchIDs, r)
			for j, i := range positions {
				if len(items) != len(positions) {
					results[i] = policy.ErrInvalid
					continue
				}
				err := items[j].Err
				if err == nil {
					confirmed[i], err = validateRelationMember(r, items[j].StoredAlert)
				}
				succeeded[i], results[i] = memberWorkResult(ctx, work, err)
			}
		}
	}
	jobs := make(chan int, len(ids))
	for i := range ids {
		if canBatch && selected[i] {
			continue
		}
		jobs <- i
	}
	close(jobs)
	// 2. 成员操作独立执行；活动父随时可能结束，不能把本批父快照注入子操作替代实时检查。
	var workers sync.WaitGroup
	for range min(concurrency, len(ids)) {
		workers.Go(func() {
			for i := range jobs {
				if err := work.Err(); err != nil {
					succeeded[i], results[i] = memberWorkResult(ctx, work, err)
					continue
				}
				if selected[i] {
					var linked store.StoredAlert
					var err error
					if canReuse {
						linked, err = prepared.LinkMergeMemberPrepared(work, d.TenantID, ids[i], r)
					} else {
						linked, err = ops.LinkMergeMember(work, d.TenantID, ids[i], d.ID)
					}
					if err == nil && linked.Alert.AlertID != ids[i] {
						err = policy.ErrAccess
					}
					if err == nil {
						confirmed[i], err = validateRelationMember(r, linked)
					}
					succeeded[i], results[i] = memberWorkResult(ctx, work, err)
				} else {
					released, err := ops.ReleaseMergeWindowWithReason(work, d.TenantID, ids[i], d.WindowID, "member_not_selected")
					succeeded[i], results[i] = memberWorkResult(ctx, work, validateReleasedMember(d.TenantID, ids[i], d.WindowID, released, err))
				}
			}
		})
	}
	workers.Wait()
	// 3. 只合并连续成功成员的关系确认，再一次保存决策前缀；中断后重放成员操作保持幂等。
	completed := 0
	for completed < len(results) && succeeded[completed] {
		completed++
	}
	batchErr := errors.Join(results...)
	if completed == 0 {
		return current, batchErr
	}
	confirmations := make([]relationMemberConfirmation, 0, completed)
	for i := range completed {
		if selected[i] {
			confirmations = append(confirmations, confirmed[i])
		}
	}
	if len(confirmations) > 0 {
		if _, err := j.saveRelationMemberConfirmations(ctx, relation, confirmations, at); err != nil {
			return current, errors.Join(batchErr, err)
		}
	}
	updated, err := j.AdvanceMembers(ctx, current, start+completed, at)
	if err != nil {
		return current, errors.Join(batchErr, err)
	}
	return updated, batchErr
}
