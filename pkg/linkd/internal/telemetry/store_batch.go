// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"context"
	"errors"
	"time"

	"linkd/internal/store"
)

// GetAlertCloseStates 保留关闭状态的窄批读；不支持的后端仍使用完整批读。
func (r *observedRepository) GetAlertCloseStates(ctx context.Context, tenant string, ids []string) (store.AlertBatch, error) {
	started := time.Now()
	if reader, ok := r.next.(store.AlertCloseStateBatchStore); ok {
		result, err := reader.GetAlertCloseStates(ctx, tenant, ids)
		r.record(ctx, "alert", "get_close_states", started, err)
		return result, err
	}
	return r.GetAlertsCurrent(ctx, tenant, ids)
}

// CloseAlertsFromSnapshots 记录只 patch 关闭字段的显式批写，不把它展开成单条请求。
func (r *observedRepository) CloseAlertsFromSnapshots(ctx context.Context, updates []store.AlertSnapshotUpdate) ([]store.AlertUpdateResult, error) {
	started := time.Now()
	if closer, ok := r.next.(store.AlertCloseBatchStore); ok {
		results, err := closer.CloseAlertsFromSnapshots(ctx, updates)
		r.record(ctx, "alert", "close_batch", started, batchResultError(results, err))
		return results, err
	}
	return r.next.(store.AlertSnapshotBatchStore).CompareAndSetAlertsFromSnapshots(ctx, updates)
}

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
