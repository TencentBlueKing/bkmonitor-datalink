// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package merge

import (
	"context"
	"encoding/json"
	"time"

	"linkd/internal/policy"
)

// IndexRelationBatch 固定一批父/成员引用，全部确认后只推进一次关系偏移量。
// 每个存储块最多128项；部分写入保留原偏移，重试幂等写入同一映射。
func (j *Journal) IndexRelationBatch(ctx context.Context, current StoredRelation, at time.Time, batchSize int) (StoredRelation, error) {
	if err := ctx.Err(); err != nil {
		return StoredRelation{}, err
	}
	r := current.Relation
	if r.Validate() != nil || current.Version == "" || at.Before(r.UpdatedAt) || batchSize < 1 {
		return StoredRelation{}, policy.ErrInvalid
	}
	end := r.IndexOffset + min(batchSize, len(r.Members)+1-r.IndexOffset)
	if end == r.IndexOffset {
		return current, nil
	}
	// 1. 引用前缀由关系内的既有顺序确定，不因部分存储失败跳过任何成员。
	for start := r.IndexOffset; start < end; start += documentBatchSize {
		if err := ctx.Err(); err != nil {
			return StoredRelation{}, err
		}
		documents := map[string]json.RawMessage{}
		for offset := start; offset < min(start+documentBatchSize, end); offset++ {
			ref := relationReference{TenantID: r.TenantID, AlertID: r.ParentAlertID, RelationID: r.ID, Role: "parent"}
			if offset > 0 {
				ref.AlertID, ref.Role = r.Members[offset-1].AlertID, "member"
			}
			scope, err := referenceScope(ref.TenantID, ref.AlertID)
			if err != nil {
				return StoredRelation{}, err
			}
			key := scope + ref.RelationID
			raw, err := json.Marshal(ref)
			if err != nil {
				return StoredRelation{}, err
			}
			documents[key] = raw
		}
		// 2. 固定引用允许幂等覆盖；写入失败不提交偏移，成功无需再读回。
		if err := j.docs.PutReferences(ctx, documents); err != nil {
			return StoredRelation{}, err
		}
	}
	// 3. 全部引用确认后以原关系CAS一次保存前缀，关系状态仍是唯一权威。
	next := r.Clone()
	next.IndexOffset, next.UpdatedAt = end, at
	return j.putRelation(ctx, current, next)
}
