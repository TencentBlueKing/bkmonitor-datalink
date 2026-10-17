// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"errors"
	"time"

	"linkd/internal/store"
)

// CompareAndSetAlertsFromSnapshots 计量显式批次调用，单项结果由批量契约原样返回。
func (r *observedRepository) CompareAndSetAlertsFromSnapshots(ctx context.Context, updates []store.AlertSnapshotUpdate) ([]store.AlertUpdateResult, error) {
	started := time.Now()
	if batch, ok := r.next.(store.AlertSnapshotBatchStore); ok {
		results, err := batch.CompareAndSetAlertsFromSnapshots(ctx, updates)
		r.record(ctx, "alert", "compare_and_set_batch", started, batchResultError(results, err))
		return results, err
	}
	results := make([]store.AlertUpdateResult, len(updates))
	for i, u := range updates {
		results[i].StoredAlert, results[i].Err = r.CompareAndSetAlertFromSnapshot(ctx, u.Current, u.Replacement)
	}
	return results, nil
}

// CompleteAlertChanges 计量显式局部确认批次；不会替失败项提前确认完成。
func (r *observedRepository) CompleteAlertChanges(ctx context.Context, current []store.StoredAlert, field store.AlertChangeField) ([]store.AlertUpdateResult, error) {
	started := time.Now()
	if batch, ok := r.next.(store.AlertSnapshotBatchStore); ok {
		results, err := batch.CompleteAlertChanges(ctx, current, field)
		r.record(ctx, "alert", "complete_change_batch", started, batchResultError(results, err))
		return results, err
	}
	results := make([]store.AlertUpdateResult, len(current))
	for i, c := range current {
		results[i].StoredAlert, results[i].Err = r.CompleteAlertChange(ctx, c, field)
	}
	return results, nil
}

func batchResultError(results []store.AlertUpdateResult, err error) error {
	for _, item := range results {
		err = errors.Join(err, item.Err)
	}
	return err
}
