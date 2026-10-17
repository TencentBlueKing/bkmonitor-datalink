// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"errors"
	"sync"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/policy"
	"linkd/internal/store"
)

type preparedRelationUnlinker interface {
	UnlinkMergeMemberPrepared(context.Context, string, string, domain.MergeRelation, store.StoredAlert) (store.StoredAlert, error)
}

type preparedRelationBatchUnlinker interface {
	UnlinkMergeMembersPrepared(context.Context, string, []string, domain.MergeRelation, store.StoredAlert) []store.AlertUpdateResult
}

// EndRelationBatch 复用本批父/关系事实，按配置预算并发解除成员。
// 调用方持有窗口租约；每个成员仍由 Operations 获取自己的 fingerprint lease。
func (j *Journal) EndRelationBatch(ctx context.Context, tenant, id string, ops RelationEndOperator, at time.Time) (StoredRelation, error) {
	budget := (config.MergeControlConfig{}).WithDefaults()
	return j.EndRelationBatchWithBudget(ctx, tenant, id, ops, at, budget.MemberBatchSize, budget.Concurrency)
}

// EndRelationBatchWithBudget 在指定成员批次和并发预算内推进关系结束。
func (j *Journal) EndRelationBatchWithBudget(ctx context.Context, tenant, id string, ops RelationEndOperator, at time.Time, batchSize, concurrency int) (StoredRelation, error) {
	if ops == nil || batchSize < 1 || concurrency < 1 {
		return StoredRelation{}, policy.ErrInvalid
	}
	// 1. 每批只确认一次父终态并保存结束意图，不能从调用方的旧扫描快照直接解除成员。
	r, err := j.GetRelation(ctx, tenant, id)
	if err != nil {
		return r, err
	}
	if at.Before(r.Relation.UpdatedAt) {
		return r, policy.ErrInvalid
	}
	if r.Relation.State == "ended" {
		return r, j.completeEndedDecision(ctx, tenant, id, at)
	}
	work, cancel, canStart := memberWorkContext(ctx)
	defer cancel()
	if !canStart {
		return r, nil
	}
	parent, err := ops.RecoverMergeParent(ctx, tenant, r.Relation.ParentAlertID, id)
	if err != nil || parent.Alert.Status == domain.AlertStatusActive {
		return r, err
	}
	r, err = j.BeginEndRelation(ctx, tenant, id, parent, at)
	if err != nil {
		return r, err
	}
	// 2. 提前关闭的父可能尚未建立完整反查引用；先有界补齐，再进入成员批量解除。
	if r.Relation.IndexOffset < len(r.Relation.Members)+1 {
		r, err = j.IndexRelationBatch(ctx, r, at, batchSize)
		if err != nil {
			return r, err
		}
	}
	if r.Relation.IndexOffset < len(r.Relation.Members)+1 {
		return r, nil
	}
	start := r.Relation.EndOffset
	ids := r.Relation.WaitMemberIDs[start : start+min(batchSize, len(r.Relation.WaitMemberIDs)-start)]
	results := make([]error, len(ids))
	succeeded := make([]bool, len(ids))
	prepared, canReuse := ops.(preparedRelationUnlinker)
	batch, canBatch := ops.(preparedRelationBatchUnlinker)
	if canBatch && len(ids) > 0 {
		items := batch.UnlinkMergeMembersPrepared(work, tenant, ids, r.Relation, parent)
		for i, id := range ids {
			if len(items) != len(ids) {
				results[i] = policy.ErrInvalid
				continue
			}
			succeeded[i], results[i] = memberWorkResult(ctx, work, validateUnlinkedMember(r.Relation, id, items[i].StoredAlert, items[i].Err))
		}
	}
	jobs := make(chan int, len(ids))
	for i := range ids {
		if canBatch {
			continue
		}
		jobs <- i
	}
	close(jobs)
	var workers sync.WaitGroup
	for range min(concurrency, len(ids)) {
		workers.Go(func() {
			for i := range jobs {
				if err := work.Err(); err != nil {
					succeeded[i], results[i] = memberWorkResult(ctx, work, err)
					continue
				}
				var actual store.StoredAlert
				var err error
				if canReuse {
					actual, err = prepared.UnlinkMergeMemberPrepared(work, tenant, ids[i], r.Relation, parent)
				} else {
					actual, err = ops.UnlinkMergeMember(work, tenant, ids[i], id)
				}
				succeeded[i], results[i] = memberWorkResult(ctx, work, validateUnlinkedMember(r.Relation, ids[i], actual, err))
			}
		})
	}
	workers.Wait()
	// 3. 只推进全部确认成功的连续前缀；后序成功项在重试时按原关系身份幂等重放。
	completed := 0
	for completed < len(results) && succeeded[completed] {
		completed++
	}
	batchErr := errors.Join(results...)
	next := r.Relation.Clone()
	next.EndOffset += completed
	if next.EndOffset == len(next.WaitMemberIDs) {
		next.State, next.EndedAt = "ended", &at
	}
	if completed > 0 || next.State != r.Relation.State {
		next.UpdatedAt = at
		r, err = j.putRelation(ctx, r, next)
		if err != nil {
			return r, errors.Join(batchErr, err)
		}
	}
	if r.Relation.State == "ended" {
		return r, errors.Join(batchErr, j.completeEndedDecision(ctx, tenant, id, at))
	}
	return r, batchErr
}
