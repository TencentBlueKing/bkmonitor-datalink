// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package mysqlstore

import (
	"context"
	"fmt"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// CompareAndSetAlertsFromSnapshots 使用调用方已经形成的成员块，不再等待时间窗口收集。
func (r *Repository) CompareAndSetAlertsFromSnapshots(ctx context.Context, updates []store.AlertSnapshotUpdate) ([]store.AlertUpdateResult, error) {
	writes := make([]alertWrite, len(updates))
	for i, u := range updates {
		writes[i] = alertWrite{current: u.Current, next: u.Replacement}
	}
	return r.writeAlertBatch(ctx, writes)
}

func (r *Repository) CompleteAlertChanges(ctx context.Context, current []store.StoredAlert, field store.AlertChangeField) ([]store.AlertUpdateResult, error) {
	writes := make([]alertWrite, len(current))
	for i, c := range current {
		next, err := store.CompletedAlertChange(c, field)
		if err != nil {
			return nil, err
		}
		writes[i] = alertWrite{current: c, next: next, field: field}
	}
	return r.writeAlertBatch(ctx, writes)
}

func (r *Repository) writeAlertBatch(ctx context.Context, writes []alertWrite) ([]store.AlertUpdateResult, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if len(writes) == 0 || len(writes) > store.MaxBatchSize {
		return nil, store.ErrInvalidArgument
	}
	results := make([]store.AlertUpdateResult, len(writes))
	direct := *r
	direct.batch = nil
	batch := writeBatch{repository: &direct}
	calls, positions, size := []*writeCall{}, []int{}, 0
	flush := func() {
		if len(calls) == 0 {
			return
		}
		batch.writeAlerts(ctx, calls)
		for i, call := range calls {
			result := <-call.done
			results[positions[i]] = store.AlertUpdateResult{StoredAlert: result.alert, Err: result.err}
		}
		calls, positions, size = nil, nil, 0
	}
	for i := range writes {
		w := &writes[i]
		if _, ok := parseVersion(w.current.Version); !ok {
			results[i].Err = store.ErrVersionConflict
			continue
		}
		if w.field == "" {
			var err error
			w.next, err = domain.PrepareAlertReplacementFromSnapshot(w.current.Alert, w.next)
			if err != nil {
				results[i].Err = fmt.Errorf("%w: %w", store.ErrInvalidTransition, err)
				continue
			}
			w.payload, err = encodeAlert(w.next)
			if err != nil {
				results[i].Err = err
				continue
			}
		}
		if len(calls) > 0 && size+len(w.payload)+512 > 4<<20 {
			flush()
		}
		calls = append(calls, &writeCall{ctx: ctx, alert: w, done: make(chan writeResult, 1)})
		positions = append(positions, i)
		size += len(w.payload) + 512
	}
	flush()
	return results, nil
}
