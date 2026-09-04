// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

type bulkEventCreateItem struct {
	event        domain.Event
	processing   store.EventProcessing
	documentID   string
	writeTarget  string
	requireAlias bool
	body         []byte
}

type bulkCreateResponse struct {
	Items []struct {
		Create struct {
			Index       string `json:"_index"`
			ID          string `json:"_id"`
			SeqNo       int64  `json:"_seq_no"`
			PrimaryTerm int64  `json:"_primary_term"`
			Status      int    `json:"status"`
			Error       *struct {
				Type   string `json:"type"`
				Reason string `json:"reason"`
			} `json:"error"`
		} `json:"create"`
	} `json:"items"`
}

// CreateEvent 委托 batch-of-one，保证单条与 Bulk create 使用完全相同的幂等和冲突语义。
func (r *Repository) CreateEvent(
	ctx context.Context,
	event domain.Event,
) (store.CreateEventResult, error) {
	items, err := r.CreateEvents(ctx, []domain.Event{event})
	if err != nil {
		return store.CreateEventResult{}, err
	}
	if len(items) != 1 {
		return store.CreateEventResult{}, fmt.Errorf("create event: bulk writer returned %d items", len(items))
	}
	return items[0].Result, items[0].Err
}

// CreateEvents 使用 Elasticsearch Bulk create API 按输入顺序返回逐项结果。
// refresh=false 只取消搜索可见性等待；成功返回前主分片仍已确认创建，后续 GetEvent 使用 realtime GET。
func (r *Repository) CreateEvents(
	ctx context.Context,
	events []domain.Event,
) ([]store.CreateEventItemResult, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if len(events) == 0 || len(events) > store.MaxBatchSize {
		return nil, fmt.Errorf("%w: event batch size must be between 1 and %d", store.ErrInvalidArgument, store.MaxBatchSize)
	}
	results := make([]store.CreateEventItemResult, len(events))
	prepared := make([]*bulkEventCreateItem, len(events))
	groups := map[bool][]int{false: {}, true: {}}
	for index, event := range events {
		item, err := r.prepareBulkEvent(ctx, event)
		if err != nil {
			results[index].Err = err
			continue
		}
		prepared[index] = &item
		groups[item.requireAlias] = append(groups[item.requireAlias], index)
	}
	for _, requireAlias := range []bool{false, true} {
		indices := groups[requireAlias]
		if len(indices) == 0 {
			continue
		}
		if err := r.createEventBulkGroup(ctx, prepared, indices, requireAlias, results); err != nil {
			for _, index := range indices {
				if results[index].Err == nil && results[index].Result.Event.EventID == "" {
					results[index].Err = err
				}
			}
		}
	}
	return results, nil
}

func (r *Repository) prepareBulkEvent(ctx context.Context, event domain.Event) (bulkEventCreateItem, error) {
	normalized, err := event.Normalize()
	if err != nil {
		return bulkEventCreateItem{}, fmt.Errorf("%w: normalize event: %w", store.ErrInvalidArgument, err)
	}
	if err := domain.ValidateNewEvent(normalized); err != nil {
		return bulkEventCreateItem{}, fmt.Errorf("%w: validate new event: %w", store.ErrInvalidArgument, err)
	}
	if err := validateIdentity(normalized.BKTenantID, "event_id", normalized.EventID); err != nil {
		return bulkEventCreateItem{}, err
	}
	if err := validateBucketEventIdentity(r.router, normalized); err != nil {
		return bulkEventCreateItem{}, fmt.Errorf("%w: %w", store.ErrInvalidArgument, err)
	}
	if err := validateDateNanos("event received_at", normalized.ReceivedAt); err != nil {
		return bulkEventCreateItem{}, err
	}
	route, err := r.router.EventRoute(ctx, normalized.EventID)
	if err != nil {
		return bulkEventCreateItem{}, fmt.Errorf("route event %q: %w", normalized.EventID, err)
	}
	route, err = normalizeRoute(route, r.config.MaxReadTargets)
	if err != nil {
		return bulkEventCreateItem{}, err
	}
	processing := store.NewUnprocessedEventProcessing()
	body, err := encodeEventDocument(normalized, processing)
	if err != nil {
		return bulkEventCreateItem{}, err
	}
	if len(body) > r.config.MaxDocumentBytes {
		return bulkEventCreateItem{}, fmt.Errorf("%w: event document exceeds %d bytes", store.ErrInvalidArgument, r.config.MaxDocumentBytes)
	}
	return bulkEventCreateItem{
		event: normalized, processing: processing, documentID: documentID(normalized.BKTenantID, normalized.EventID),
		writeTarget: route.WriteTarget, requireAlias: route.RequireAlias, body: body,
	}, nil
}

func (r *Repository) createEventBulkGroup(
	ctx context.Context,
	prepared []*bulkEventCreateItem,
	indices []int,
	requireAlias bool,
	results []store.CreateEventItemResult,
) error {
	var body bytes.Buffer
	for _, index := range indices {
		item := prepared[index]
		metadata, err := json.Marshal(map[string]any{"create": map[string]string{"_index": item.writeTarget, "_id": item.documentID}})
		if err != nil {
			return fmt.Errorf("encode elasticsearch event bulk metadata: %w", err)
		}
		body.Write(metadata)
		body.WriteByte('\n')
		body.Write(item.body)
		body.WriteByte('\n')
	}
	query := url.Values{"refresh": []string{"false"}}
	if requireAlias {
		query.Set("require_alias", "true")
	}
	var response bulkCreateResponse
	if err := r.performNDJSON(ctx, http.MethodPost, "/_bulk", query, body.Bytes(), &response); err != nil {
		return fmt.Errorf("bulk create events: %w", err)
	}
	if len(response.Items) != len(indices) {
		return fmt.Errorf("elasticsearch event bulk returned %d items for %d events", len(response.Items), len(indices))
	}
	for responseIndex, resultIndex := range indices {
		item := prepared[resultIndex]
		created := response.Items[responseIndex].Create
		switch created.Status {
		case http.StatusCreated:
			stored, err := storedEventFromIndexResponse(item.event, item.processing, item.documentID, indexResponse{
				Index: created.Index, ID: created.ID, SeqNo: created.SeqNo, PrimaryTerm: created.PrimaryTerm,
			})
			results[resultIndex] = store.CreateEventItemResult{Result: store.CreateEventResult{StoredEvent: stored, Created: true}, Err: err}
		case http.StatusConflict:
			existing, err := r.getEventFromTarget(ctx, item.writeTarget, item.event.BKTenantID, item.event.EventID)
			if err == nil {
				err = domain.ValidateEventReplacement(item.event, existing.Event)
				if err != nil {
					err = fmt.Errorf("%w: event %q already contains different content: %w", store.ErrIdentityConflict, item.event.EventID, err)
				}
			}
			results[resultIndex] = store.CreateEventItemResult{Result: store.CreateEventResult{StoredEvent: existing, Created: false}, Err: err}
		default:
			err := fmt.Errorf("elasticsearch bulk create event %q returned status %d", item.event.EventID, created.Status)
			if created.Error != nil {
				err = fmt.Errorf("elasticsearch bulk create event %q returned status %d: %s: %s", item.event.EventID, created.Status, created.Error.Type, created.Error.Reason)
			}
			if created.Status >= http.StatusBadRequest && created.Status < http.StatusInternalServerError && created.Status != http.StatusTooManyRequests {
				err = fmt.Errorf("%w: %w", store.ErrInvalidArgument, err)
			}
			results[resultIndex].Err = err
		}
	}
	return nil
}

// GetEvent 使用 EventID 的确定性写路由执行 realtime GET。
// Lifecycle 依赖该语义立即看到 refresh=false 的终态 CAS，避免重复 Mailbox 引用读取旧 processing。
func (r *Repository) GetEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (store.StoredEvent, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredEvent{}, err
	}
	if err := validateIdentity(bkTenantID, "event_id", eventID); err != nil {
		return store.StoredEvent{}, err
	}
	route, err := r.router.EventRoute(ctx, eventID)
	if err != nil {
		return store.StoredEvent{}, err
	}
	route, err = normalizeRoute(route, r.config.MaxReadTargets)
	if err != nil {
		return store.StoredEvent{}, err
	}
	return r.getEventFromTarget(ctx, route.WriteTarget, bkTenantID, eventID)
}

// GetEvents 按首次出现顺序批量查询 Event，并使用单次 _msearch 避免逐 ID 网络往返。
func (r *Repository) GetEvents(
	ctx context.Context,
	bkTenantID string,
	eventIDs []string,
) (store.EventBatch, error) {
	if err := contextError(ctx); err != nil {
		return store.EventBatch{}, err
	}
	ids, err := normalizeBatchIDs(bkTenantID, "event_id", eventIDs)
	if err != nil {
		return store.EventBatch{}, err
	}
	results, err := r.loadEvents(ctx, bkTenantID, ids)
	if err != nil {
		return store.EventBatch{}, err
	}
	batch := store.EventBatch{
		Events:   make([]store.StoredEvent, 0, len(results)),
		NotFound: make([]string, 0),
	}
	for _, eventID := range ids {
		if stored, ok := results[eventID]; ok {
			batch.Events = append(batch.Events, stored)
			continue
		}
		batch.NotFound = append(batch.NotFound, eventID)
	}
	return batch, nil
}

// ListEventsByAlert 按 related_alert_id 和 received_at 稳定分页读取一次 Alert 关联的 Event。
func (r *Repository) ListEventsByAlert(
	ctx context.Context,
	bkTenantID, alertID string,
	request store.EventByAlertRequest,
) (store.EventPage, error) {
	if err := contextError(ctx); err != nil {
		return store.EventPage{}, err
	}
	if err := validateIdentity(bkTenantID, "alert_id", alertID); err != nil {
		return store.EventPage{}, err
	}
	if err := validateBucketAlertReference(r.router, bkTenantID, alertID); err != nil {
		return store.EventPage{}, fmt.Errorf("%w: %w", store.ErrInvalidArgument, err)
	}
	alert, err := r.GetAlert(ctx, bkTenantID, alertID)
	if err != nil {
		return store.EventPage{}, err
	}
	from, to, page, err := store.ResolveEventByAlertRange(alert.Alert, request, time.Now())
	if err != nil {
		return store.EventPage{}, err
	}
	if to.Before(from) {
		return store.EventPage{Events: []store.StoredEvent{}}, nil
	}
	if err := validateDateNanos("received_from", from); err != nil {
		return store.EventPage{}, err
	}
	if err := validateDateNanos("received_to", to); err != nil {
		return store.EventPage{}, err
	}
	targets, err := r.router.EventRangeTargets(ctx, from, to)
	if err != nil {
		return store.EventPage{}, fmt.Errorf("route events for alert %q: %w", alertID, err)
	}
	if len(targets) > r.config.MaxReadTargets {
		targets, err = r.router.EventScanTargets(ctx, to)
		if err != nil {
			return store.EventPage{}, err
		}
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.EventPage{}, err
	}
	expectedCursor := cursorPayload{
		Kind: cursorKindEventByAlert, TenantID: bkTenantID, ParentID: alertID,
		RangeStartNS: from.UnixNano(), BoundaryNS: to.UnixNano(), TargetsDigest: targetsDigest(targets),
	}
	var cursor cursorPayload
	if page.Cursor != "" {
		cursor, err = decodeCursor(page.Cursor, expectedCursor)
		if err != nil {
			return store.EventPage{}, err
		}
	}
	body := map[string]any{
		"size": page.Limit + 1, "track_total_hits": false, "seq_no_primary_term": true,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"bk_tenant_id": bkTenantID}},
			map[string]any{"term": map[string]any{"related_alert_id": alertID}},
			map[string]any{"range": map[string]any{"received_at": map[string]any{
				"gte": from.Format(time.RFC3339Nano), "lte": to.Format(time.RFC3339Nano),
			}}},
		}}},
		"sort": []any{
			map[string]any{"received_at": map[string]any{"order": "asc", "format": "strict_date_optional_time_nanos"}},
			map[string]any{"event_id": map[string]any{"order": "asc"}},
			map[string]any{"_shard_doc": map[string]any{"order": "asc"}},
		},
	}
	if page.Cursor != "" {
		body["search_after"] = cursor.SearchAfter
	}
	response, pitID, err := r.searchWithPIT(ctx, targets, cursor.PITID, body)
	if err != nil {
		return store.EventPage{}, fmt.Errorf("list events for alert %q: %w", alertID, err)
	}
	count := min(len(response.Hits.Hits), page.Limit)
	result := store.EventPage{Events: make([]store.StoredEvent, 0, count)}
	for _, hit := range response.Hits.Hits[:count] {
		if len(hit.Sort) != 3 {
			return store.EventPage{}, fmt.Errorf("elasticsearch event-by-alert search returned invalid sort values")
		}
		stored, decodeErr := decodeEventHit(hit)
		if decodeErr != nil {
			return store.EventPage{}, decodeErr
		}
		if stored.Event.BKTenantID != bkTenantID || stored.Event.RelatedAlertID != alertID {
			return store.EventPage{}, fmt.Errorf("elasticsearch event-by-alert search returned an unexpected identity")
		}
		result.Events = append(result.Events, stored)
	}
	if len(response.Hits.Hits) > page.Limit {
		lastHit := response.Hits.Hits[page.Limit-1]
		last := result.Events[len(result.Events)-1].Event
		result.NextCursor, err = encodeCursor(cursorPayload{
			Kind: cursorKindEventByAlert, TenantID: bkTenantID, ParentID: alertID,
			RangeStartNS: from.UnixNano(), BoundaryNS: to.UnixNano(), TargetsDigest: targetsDigest(targets),
			TimeNS: last.ReceivedAt.UnixNano(), ObjectID: last.EventID, PITID: pitID,
			SearchAfter: append([]json.RawMessage(nil), lastHit.Sort...),
		})
		if err != nil {
			return store.EventPage{}, err
		}
	} else if err := r.closePIT(ctx, pitID); err != nil {
		return store.EventPage{}, err
	}
	return result, nil
}

// ScanUnprocessedEvents 在 Router 给出的有界索引集合中按 received_time、event_id 稳定扫描。
func (r *Repository) ScanUnprocessedEvents(
	ctx context.Context,
	bkTenantID string,
	receivedBefore time.Time,
	page store.PageRequest,
) (store.EventPage, error) {
	if err := contextError(ctx); err != nil {
		return store.EventPage{}, err
	}
	if bkTenantID == "" {
		return store.EventPage{}, fmt.Errorf("%w: bk_tenant_id must not be empty", store.ErrInvalidArgument)
	}
	if len(bkTenantID) > maxIdentityBytes {
		return store.EventPage{}, fmt.Errorf(
			"%w: bk_tenant_id must not exceed %d bytes",
			store.ErrInvalidArgument,
			maxIdentityBytes,
		)
	}
	if receivedBefore.IsZero() {
		return store.EventPage{}, fmt.Errorf("%w: received_before must not be zero", store.ErrInvalidArgument)
	}
	normalizedPage, err := page.Normalize()
	if err != nil {
		return store.EventPage{}, err
	}
	receivedBefore = receivedBefore.Round(0).UTC()
	if err := validateDateNanos("received_before", receivedBefore); err != nil {
		return store.EventPage{}, err
	}
	targets, err := r.router.EventScanTargets(ctx, receivedBefore)
	if err != nil {
		return store.EventPage{}, fmt.Errorf("route unprocessed event scan: %w", err)
	}
	if len(targets) == 0 {
		return store.EventPage{Events: []store.StoredEvent{}}, nil
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.EventPage{}, err
	}
	expectedCursor := cursorPayload{
		Kind:          cursorKindEvent,
		TenantID:      bkTenantID,
		BoundaryNS:    receivedBefore.UnixNano(),
		TargetsDigest: targetsDigest(targets),
	}
	var cursor cursorPayload
	if normalizedPage.Cursor != "" {
		cursor, err = decodeCursor(normalizedPage.Cursor, expectedCursor)
		if err != nil {
			return store.EventPage{}, err
		}
	}
	filters := []any{
		map[string]any{"term": map[string]any{"bk_tenant_id": bkTenantID}},
		map[string]any{"term": map[string]any{"processing.state": domain.EventProcessStateUnprocessed}},
		map[string]any{"range": map[string]any{
			"received_at": map[string]any{"lte": receivedBefore.Format(time.RFC3339Nano)},
		}},
	}
	body := map[string]any{
		"size":                normalizedPage.Limit + 1,
		"track_total_hits":    false,
		"seq_no_primary_term": true,
		"query": map[string]any{
			"bool": map[string]any{"filter": filters},
		},
		"sort": []any{
			map[string]any{"received_at": map[string]any{
				"order":  "asc",
				"format": "strict_date_optional_time_nanos",
			}},
			map[string]any{"event_id": map[string]any{"order": "asc"}},
			map[string]any{"_shard_doc": map[string]any{"order": "asc"}},
		},
	}
	if normalizedPage.Cursor != "" {
		body["search_after"] = cursor.SearchAfter
	}
	response, pitID, err := r.searchWithPIT(ctx, targets, cursor.PITID, body)
	if err != nil {
		return store.EventPage{}, fmt.Errorf("scan unprocessed events: %w", err)
	}
	count := min(len(response.Hits.Hits), normalizedPage.Limit)
	result := store.EventPage{Events: make([]store.StoredEvent, 0, count)}
	seen := make(map[string]struct{}, count)
	if normalizedPage.Cursor != "" {
		seen[cursor.ObjectID] = struct{}{}
	}
	for _, hit := range response.Hits.Hits[:count] {
		if len(hit.Sort) != 3 {
			return store.EventPage{}, fmt.Errorf("elasticsearch event search returned invalid sort values")
		}
		stored, err := decodeEventHit(hit)
		if err != nil {
			return store.EventPage{}, err
		}
		if _, exists := seen[stored.Event.EventID]; exists {
			return store.EventPage{}, fmt.Errorf(
				"%w: event %q exists in multiple elasticsearch indices",
				store.ErrIdentityConflict,
				stored.Event.EventID,
			)
		}
		seen[stored.Event.EventID] = struct{}{}
		result.Events = append(result.Events, stored)
	}
	if len(response.Hits.Hits) > normalizedPage.Limit {
		lastHit := response.Hits.Hits[normalizedPage.Limit-1]
		last := result.Events[len(result.Events)-1].Event
		result.NextCursor, err = encodeCursor(cursorPayload{
			Kind:          cursorKindEvent,
			TenantID:      bkTenantID,
			BoundaryNS:    receivedBefore.UnixNano(),
			TargetsDigest: targetsDigest(targets),
			TimeNS:        last.ReceivedAt.UnixNano(),
			ObjectID:      last.EventID,
			PITID:         pitID,
			SearchAfter:   append([]json.RawMessage(nil), lastHit.Sort...),
		})
		if err != nil {
			return store.EventPage{}, err
		}
	} else if err := r.closePIT(ctx, pitID); err != nil {
		return store.EventPage{}, err
	}
	return result, nil
}

// ScanAllUnprocessedEvents 在 Router 给出的有界索引集合中扫描全部租户的未处理事件。
// 该特权操作只供进程内恢复调度器使用；返回的每个 Event 仍携带权威 bk_tenant_id。
func (r *Repository) ScanAllUnprocessedEvents(
	ctx context.Context,
	receivedBefore time.Time,
	page store.PageRequest,
) (store.EventPage, error) {
	if err := contextError(ctx); err != nil {
		return store.EventPage{}, err
	}
	if receivedBefore.IsZero() {
		return store.EventPage{}, fmt.Errorf("%w: received_before must not be zero", store.ErrInvalidArgument)
	}
	normalizedPage, err := page.Normalize()
	if err != nil {
		return store.EventPage{}, err
	}
	receivedBefore = receivedBefore.Round(0).UTC()
	if err := validateDateNanos("received_before", receivedBefore); err != nil {
		return store.EventPage{}, err
	}
	targets, err := r.router.EventScanTargets(ctx, receivedBefore)
	if err != nil {
		return store.EventPage{}, fmt.Errorf("route all unprocessed event scan: %w", err)
	}
	if len(targets) == 0 {
		return store.EventPage{Events: []store.StoredEvent{}}, nil
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.EventPage{}, err
	}
	expectedCursor := cursorPayload{
		Kind:          cursorKindAllEvent,
		BoundaryNS:    receivedBefore.UnixNano(),
		TargetsDigest: targetsDigest(targets),
	}
	var cursor cursorPayload
	if normalizedPage.Cursor != "" {
		cursor, err = decodeCursor(normalizedPage.Cursor, expectedCursor)
		if err != nil {
			return store.EventPage{}, err
		}
		if cursor.ItemTenantID == "" {
			return store.EventPage{}, fmt.Errorf("%w: cursor does not contain item tenant", store.ErrInvalidCursor)
		}
	}
	body := map[string]any{
		"size":                normalizedPage.Limit + 1,
		"track_total_hits":    false,
		"seq_no_primary_term": true,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"processing.state": domain.EventProcessStateUnprocessed}},
			map[string]any{"range": map[string]any{
				"received_at": map[string]any{"lte": receivedBefore.Format(time.RFC3339Nano)},
			}},
		}}},
		"sort": []any{
			map[string]any{"received_at": map[string]any{
				"order": "asc", "format": "strict_date_optional_time_nanos",
			}},
			map[string]any{"bk_tenant_id": map[string]any{"order": "asc"}},
			map[string]any{"event_id": map[string]any{"order": "asc"}},
			map[string]any{"_shard_doc": map[string]any{"order": "asc"}},
		},
	}
	if normalizedPage.Cursor != "" {
		body["search_after"] = cursor.SearchAfter
	}
	response, pitID, err := r.searchWithPIT(ctx, targets, cursor.PITID, body)
	if err != nil {
		return store.EventPage{}, fmt.Errorf("scan all unprocessed events: %w", err)
	}
	count := min(len(response.Hits.Hits), normalizedPage.Limit)
	result := store.EventPage{Events: make([]store.StoredEvent, 0, count)}
	seen := make(map[string]struct{}, count)
	if normalizedPage.Cursor != "" {
		seen[cursor.ItemTenantID+"\x00"+cursor.ObjectID] = struct{}{}
	}
	for _, hit := range response.Hits.Hits[:count] {
		if len(hit.Sort) != 4 {
			return store.EventPage{}, fmt.Errorf("elasticsearch all-event search returned invalid sort values")
		}
		stored, err := decodeEventHit(hit)
		if err != nil {
			return store.EventPage{}, err
		}
		identity := stored.Event.BKTenantID + "\x00" + stored.Event.EventID
		if _, exists := seen[identity]; exists {
			return store.EventPage{}, fmt.Errorf(
				"%w: event %q exists in multiple elasticsearch indices",
				store.ErrIdentityConflict,
				stored.Event.EventID,
			)
		}
		seen[identity] = struct{}{}
		result.Events = append(result.Events, stored)
	}
	if len(response.Hits.Hits) > normalizedPage.Limit {
		lastHit := response.Hits.Hits[normalizedPage.Limit-1]
		last := result.Events[len(result.Events)-1].Event
		result.NextCursor, err = encodeCursor(cursorPayload{
			Kind:          cursorKindAllEvent,
			BoundaryNS:    receivedBefore.UnixNano(),
			TargetsDigest: targetsDigest(targets),
			TimeNS:        last.ReceivedAt.UnixNano(),
			ItemTenantID:  last.BKTenantID,
			ObjectID:      last.EventID,
			PITID:         pitID,
			SearchAfter:   append([]json.RawMessage(nil), lastHit.Sort...),
		})
		if err != nil {
			return store.EventPage{}, err
		}
	} else if err := r.closePIT(ctx, pitID); err != nil {
		return store.EventPage{}, err
	}
	return result, nil
}

// CompareAndSetEventResult 使用 _seq_no/_primary_term 写入 Event 的单向处理结果。
// refresh=false 保证写请求完成后即可返回，但调用方不能据此假设结果已经能被 search 查询到。
func (r *Repository) CompareAndSetEventResult(
	ctx context.Context,
	bkTenantID, eventID string,
	expected store.VersionToken,
	result store.EventResult,
) (store.StoredEvent, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredEvent{}, err
	}
	if err := validateIdentity(bkTenantID, "event_id", eventID); err != nil {
		return store.StoredEvent{}, err
	}
	if expected.IsZero() {
		return store.StoredEvent{}, fmt.Errorf("%w: expected event version must not be empty", store.ErrInvalidArgument)
	}
	version, ok := decodeVersion(expected)
	if !ok || version.DocumentID != documentID(bkTenantID, eventID) {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrVersionConflict, eventID)
	}
	current, err := r.GetEvent(ctx, bkTenantID, eventID)
	if err != nil {
		return store.StoredEvent{}, err
	}
	if current.Version != expected {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrVersionConflict, eventID)
	}
	if current.Processing.State != domain.EventProcessStateUnprocessed {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q is already processed", store.ErrInvalidTransition, eventID)
	}
	normalizedResult, err := result.Normalize()
	if err != nil {
		return store.StoredEvent{}, fmt.Errorf("%w: event result: %w", store.ErrInvalidArgument, err)
	}
	updated := current.Event.Clone()
	if normalizedResult.RelatedAlertID != "" {
		updated, err = updated.WithRelatedAlertID(normalizedResult.RelatedAlertID)
		if err != nil {
			return store.StoredEvent{}, fmt.Errorf("%w: %w", store.ErrInvalidTransition, err)
		}
	}
	processedAt := normalizedResult.ProcessedAt
	processing := store.EventProcessing{State: normalizedResult.State, Outcome: normalizedResult.Outcome,
		ReasonCode: normalizedResult.ReasonCode, ProcessedAt: &processedAt}
	body, err := encodeEventDocument(updated, processing)
	if err != nil {
		return store.StoredEvent{}, err
	}
	if len(body) > r.config.MaxDocumentBytes {
		return store.StoredEvent{}, fmt.Errorf(
			"%w: event document exceeds %d bytes",
			store.ErrInvalidArgument,
			r.config.MaxDocumentBytes,
		)
	}
	query := url.Values{
		"if_seq_no":       []string{strconv.FormatInt(version.SeqNo, 10)},
		"if_primary_term": []string{strconv.FormatInt(version.PrimaryTerm, 10)},
		"refresh":         []string{"false"},
	}
	var response indexResponse
	err = r.performJSON(
		ctx,
		http.MethodPut,
		"/"+version.Index+"/_doc/"+version.DocumentID,
		query,
		body,
		&response,
	)
	if err != nil {
		if responseErr, ok := asResponseError(err); ok && responseErr.StatusCode == http.StatusConflict {
			return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrVersionConflict, eventID)
		}
		if responseErr, ok := asResponseError(err); ok && responseErr.StatusCode == http.StatusNotFound {
			return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrNotFound, eventID)
		}
		return store.StoredEvent{}, fmt.Errorf("update event %q: %w", eventID, err)
	}
	return storedEventFromIndexResponse(updated, processing, version.DocumentID, response)
}

func (r *Repository) loadEvents(
	ctx context.Context,
	bkTenantID string,
	eventIDs []string,
) (map[string]store.StoredEvent, error) {
	headers := make([]map[string]any, len(eventIDs))
	bodies := make([]map[string]any, len(eventIDs))
	for index, eventID := range eventIDs {
		route, err := r.router.EventRoute(ctx, eventID)
		if err != nil {
			return nil, fmt.Errorf("route event %q: %w", eventID, err)
		}
		route, err = normalizeRoute(route, r.config.MaxReadTargets)
		if err != nil {
			return nil, err
		}
		headers[index] = map[string]any{
			"index":              route.ReadTargets,
			"ignore_unavailable": true,
		}
		bodies[index] = exactEntitySearchBody(
			bkTenantID,
			"event_id",
			eventID,
			documentID(bkTenantID, eventID),
		)
	}
	responses, err := r.multiSearch(ctx, headers, bodies)
	if err != nil {
		return nil, fmt.Errorf("batch search events: %w", err)
	}
	if len(responses) != len(eventIDs) {
		return nil, fmt.Errorf(
			"elasticsearch event multi-search returned %d responses for %d IDs",
			len(responses),
			len(eventIDs),
		)
	}
	results := make(map[string]store.StoredEvent, len(eventIDs))
	for index, hits := range responses {
		// multiSearch 和上面的长度校验保证 index 同时属于 eventIDs。
		eventID := eventIDs[index]
		switch len(hits) {
		case 0:
			continue
		case 1:
			stored, err := decodeEventHit(hits[0])
			if err != nil {
				return nil, err
			}
			if stored.Event.BKTenantID != bkTenantID || stored.Event.EventID != eventID {
				return nil, fmt.Errorf("elasticsearch event search returned an unexpected identity")
			}
			results[eventID] = stored
		default:
			return nil, fmt.Errorf(
				"%w: event %q exists in multiple elasticsearch indices",
				store.ErrIdentityConflict,
				eventID,
			)
		}
	}
	return results, nil
}

func (r *Repository) getEventFromTarget(
	ctx context.Context,
	target, bkTenantID, eventID string,
) (store.StoredEvent, error) {
	documentID := documentID(bkTenantID, eventID)
	var response getResponse
	err := r.performJSON(
		ctx,
		http.MethodGet,
		"/"+target+"/_doc/"+documentID,
		url.Values{"realtime": []string{"true"}},
		nil,
		&response,
	)
	if err != nil {
		if responseErr, ok := asResponseError(err); ok && responseErr.StatusCode == http.StatusNotFound {
			return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrNotFound, eventID)
		}
		return store.StoredEvent{}, err
	}
	stored, err := decodeEventHit(response.hit())
	if err != nil {
		return store.StoredEvent{}, err
	}
	if stored.Event.BKTenantID != bkTenantID || stored.Event.EventID != eventID {
		return store.StoredEvent{}, fmt.Errorf("elasticsearch event GET returned an unexpected identity")
	}
	return stored, nil
}

func exactEntitySearchBody(
	bkTenantID, idField, entityID, elasticsearchID string,
) map[string]any {
	return map[string]any{
		"size":                2,
		"track_total_hits":    false,
		"seq_no_primary_term": true,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"bk_tenant_id": bkTenantID}},
					map[string]any{"term": map[string]any{idField: entityID}},
					map[string]any{"ids": map[string]any{"values": []string{elasticsearchID}}},
				},
			},
		},
	}
}

func storedEventFromIndexResponse(
	event domain.Event,
	processing store.EventProcessing,
	documentID string,
	response indexResponse,
) (store.StoredEvent, error) {
	if response.ID != documentID || response.Index == "" {
		return store.StoredEvent{}, fmt.Errorf("elasticsearch event write returned an unexpected identity")
	}
	version, err := encodeVersion(versionPayload{
		Index:       response.Index,
		DocumentID:  response.ID,
		SeqNo:       response.SeqNo,
		PrimaryTerm: response.PrimaryTerm,
	})
	if err != nil {
		return store.StoredEvent{}, err
	}
	return store.StoredEvent{Event: event.Clone(), Processing: processing.Clone(), Version: version}, nil
}

func isResponseStatus(err error, statusCode int) bool {
	var response *responseError
	return errors.As(err, &response) && response.StatusCode == statusCode
}
