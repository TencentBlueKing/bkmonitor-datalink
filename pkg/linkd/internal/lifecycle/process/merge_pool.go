// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycleprocess

import (
	"context"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ProcessMergeMemberBatch 执行持久化批次，调用方仅持有该批租约，不持有整组窗口锁。
func (r *MergeOperator) ProcessMergeMemberBatch(ctx context.Context, facts domain.MergeMemberContext, ids []string, unlink bool) []store.AlertUpdateResult {
	results := make([]store.AlertUpdateResult, len(ids))
	var parent store.StoredAlert
	if unlink {
		repo := r.metrics.ObserveRepository(r.resources.repository.Repository)
		var err error
		parent, err = repo.(store.LifecycleAlertStore).GetAlertCurrent(ctx, facts.TenantID, facts.ParentAlertID)
		if err != nil {
			for i := range results {
				results[i].Err = err
			}
			return results
		}
	}
	for start := 0; start < len(ids); {
		n := min(store.MaxBatchSize-1, len(ids)-start)
		r.memberWave(r.resources.context(ctx), facts.TenantID, ids[start:start+n], facts, parent, unlink, results[start:start+n])
		start += n
	}
	return results
}
