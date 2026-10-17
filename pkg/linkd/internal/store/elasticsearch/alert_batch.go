// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package elasticsearchstore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

type alertBulkWrite struct {
	current store.StoredAlert
	next    domain.Alert
	field   store.AlertChangeField
}

// CompareAndSetAlertsFromSnapshots 显式提交 Bulk index，每项保留自己的 ES 版本条件。
func (r *Repository) CompareAndSetAlertsFromSnapshots(ctx context.Context, updates []store.AlertSnapshotUpdate) ([]store.AlertUpdateResult, error) {
	writes := make([]alertBulkWrite, len(updates))
	for i, u := range updates {
		writes[i] = alertBulkWrite{current: u.Current, next: u.Replacement}
	}
	return r.writeAlertBulk(ctx, writes)
}

// CompleteAlertChanges 显式提交局部完成更新；不编码、覆盖未修改的告警载荷。
func (r *Repository) CompleteAlertChanges(ctx context.Context, current []store.StoredAlert, field store.AlertChangeField) ([]store.AlertUpdateResult, error) {
	writes := make([]alertBulkWrite, len(current))
	for i, c := range current {
		next, err := store.CompletedAlertChange(c, field)
		if err != nil {
			return nil, err
		}
		writes[i] = alertBulkWrite{current: c, next: next, field: field}
	}
	return r.writeAlertBulk(ctx, writes)
}

func (r *Repository) writeAlertBulk(ctx context.Context, writes []alertBulkWrite) ([]store.AlertUpdateResult, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if len(writes) == 0 || len(writes) > store.MaxBatchSize {
		return nil, store.ErrInvalidArgument
	}
	results := make([]store.AlertUpdateResult, len(writes))
	var body bytes.Buffer
	indices := []int{}
	flush := func() {
		if len(indices) == 0 {
			return
		}
		var response struct {
			Took  *int64 `json:"took"`
			Items []map[string]struct {
				indexResponse
				Status int `json:"status"`
			} `json:"items"`
		}
		err := r.performNDJSON(ctx, http.MethodPost, "/_bulk", url.Values{"refresh": {"false"}}, body.Bytes(), &response)
		if err == nil && response.Took != nil {
			store.ObserveBatchPhase(ctx, "es.bulk_took", time.Duration(*response.Took)*time.Millisecond)
		}
		if err == nil && len(response.Items) != len(indices) {
			err = fmt.Errorf("alert bulk returned incomplete results")
		}
		for position, i := range indices {
			if err != nil {
				results[i].Err = err
				continue
			}
			action := "index"
			if writes[i].field != "" {
				action = "update"
			}
			item := response.Items[position][action]
			switch item.Status {
			case http.StatusOK, http.StatusCreated:
				results[i].StoredAlert, results[i].Err = storedAlertFromIndexResponse(writes[i].next, alertDocumentID(writes[i].next), item.indexResponse)
			case http.StatusConflict:
				results[i].Err = store.ErrVersionConflict
			case http.StatusNotFound:
				results[i].Err = store.ErrNotFound
			default:
				results[i].Err = fmt.Errorf("alert bulk %s returned HTTP %d", action, item.Status)
			}
		}
		body.Reset()
		indices = indices[:0]
	}
	for i := range writes {
		w := &writes[i]
		version, err := alertSnapshotVersion(w.current)
		if err != nil {
			results[i].Err = err
			continue
		}
		action := "index"
		var source []byte
		if w.field == "" {
			w.next, err = domain.PrepareAlertReplacementFromSnapshot(w.current.Alert, w.next)
			if err != nil {
				results[i].Err = fmt.Errorf("%w: %w", store.ErrInvalidTransition, err)
				continue
			}
			source, err = encodeAlertDocument(w.next)
		} else {
			action = "update"
			source, err = json.Marshal(map[string]any{"doc": map[string]any{string(w.field): nil, "merge_work": store.HasMergeWork(w.next)}})
		}
		if err != nil {
			results[i].Err = err
			continue
		}
		if len(source) > r.config.MaxDocumentBytes {
			results[i].Err = store.ErrInvalidArgument
			continue
		}
		header, err := json.Marshal(map[string]any{action: map[string]any{"_index": version.Index, "_id": version.DocumentID, "if_seq_no": version.SeqNo, "if_primary_term": version.PrimaryTerm}})
		if err != nil {
			results[i].Err = err
			continue
		}
		if body.Len() > 0 && body.Len()+len(header)+len(source)+2 > r.config.MaxRequestBytes {
			flush()
		}
		body.Write(header)
		body.WriteByte('\n')
		body.Write(source)
		body.WriteByte('\n')
		indices = append(indices, i)
	}
	flush()
	return results, nil
}
