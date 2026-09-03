// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

const storedEventColumns = `payload, processing, version`

func (r *Repository) CreateEvent(ctx context.Context, event domain.Event) (store.CreateEventResult, error) {
	items, err := r.CreateEvents(ctx, []domain.Event{event})
	if err != nil {
		return store.CreateEventResult{}, err
	}
	return items[0].Result, items[0].Err
}

func (r *Repository) createEvent(ctx context.Context, event domain.Event) (store.CreateEventResult, error) {
	if err := contextError(ctx); err != nil {
		return store.CreateEventResult{}, err
	}
	normalized, err := event.Normalize()
	if err != nil {
		return store.CreateEventResult{}, fmt.Errorf("%w: normalize event: %w", store.ErrInvalidArgument, err)
	}
	if err := domain.ValidateNewEvent(normalized); err != nil {
		return store.CreateEventResult{}, fmt.Errorf("%w: validate new event: %w", store.ErrInvalidArgument, err)
	}
	if err := validateIdentity(normalized.BKTenantID, "event_id", normalized.EventID); err != nil {
		return store.CreateEventResult{}, err
	}
	payload, err := encodeEvent(normalized)
	if err != nil {
		return store.CreateEventResult{}, err
	}
	processing := store.NewUnprocessedEventProcessing()
	processingPayload, err := encodeProcessing(processing)
	if err != nil {
		return store.CreateEventResult{}, err
	}
	_, err = r.db.ExecContext(ctx, `INSERT INTO linkd_events
		(bk_tenant_id, event_id, related_alert_id, version, processing_state, received_at_ns, payload, processing)
		VALUES (?, ?, NULL, 1, ?, ?, ?, ?)`, normalized.BKTenantID, normalized.EventID,
		processing.State, normalized.ReceivedAt.UnixNano(), payload, processingPayload)
	if err == nil {
		return store.CreateEventResult{StoredEvent: store.StoredEvent{Event: normalized.Clone(), Processing: processing, Version: versionToken(1)}, Created: true}, nil
	}
	if !isDuplicateKey(err) {
		return store.CreateEventResult{}, fmt.Errorf("insert event %q: %w", normalized.EventID, err)
	}
	existing, getErr := r.GetEvent(ctx, normalized.BKTenantID, normalized.EventID)
	if getErr != nil {
		return store.CreateEventResult{}, fmt.Errorf("read duplicate event %q: %w", normalized.EventID, getErr)
	}
	if err := domain.ValidateEventReplacement(normalized, existing.Event); err != nil {
		return store.CreateEventResult{}, fmt.Errorf("%w: event %q contains different content: %w", store.ErrIdentityConflict, normalized.EventID, err)
	}
	return store.CreateEventResult{StoredEvent: existing, Created: false}, nil
}

// CreateEvents 首版复用逐项幂等语义；接口允许后续在不改变 Cleaner 的前提下替换为多行写入。
func (r *Repository) CreateEvents(ctx context.Context, events []domain.Event) ([]store.CreateEventItemResult, error) {
	if len(events) == 0 || len(events) > store.MaxBatchSize {
		return nil, fmt.Errorf("%w: event batch size must be between 1 and %d", store.ErrInvalidArgument, store.MaxBatchSize)
	}
	results := make([]store.CreateEventItemResult, len(events))
	for index, event := range events {
		results[index].Result, results[index].Err = r.createEvent(ctx, event)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (r *Repository) GetEvent(ctx context.Context, tenantID, eventID string) (store.StoredEvent, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredEvent{}, err
	}
	if err := validateIdentity(tenantID, "event_id", eventID); err != nil {
		return store.StoredEvent{}, err
	}
	stored, err := scanStoredEvent(r.db.QueryRowContext(ctx, `SELECT `+storedEventColumns+` FROM linkd_events WHERE bk_tenant_id=? AND event_id=?`, tenantID, eventID))
	if errors.Is(err, sql.ErrNoRows) {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrNotFound, eventID)
	}
	if err != nil {
		return store.StoredEvent{}, fmt.Errorf("read event %q: %w", eventID, err)
	}
	return stored, nil
}

func (r *Repository) GetEvents(ctx context.Context, tenantID string, eventIDs []string) (store.EventBatch, error) {
	if err := contextError(ctx); err != nil {
		return store.EventBatch{}, err
	}
	ids, err := normalizeBatchIDs(tenantID, "event_id", eventIDs)
	if err != nil {
		return store.EventBatch{}, err
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, tenantID)
	for _, id := range ids {
		args = append(args, id)
	}
	query := `SELECT event_id, ` + storedEventColumns + ` FROM linkd_events WHERE bk_tenant_id=? AND event_id IN (` + placeholders(len(ids)) + `)` //nolint:gosec // placeholders are fixed question marks.
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return store.EventBatch{}, fmt.Errorf("batch read events: %w", err)
	}
	defer func() { _ = rows.Close() }()
	found := make(map[string]store.StoredEvent, len(ids))
	for rows.Next() {
		var id string
		var payload, processing []byte
		var version uint64
		if err := rows.Scan(&id, &payload, &processing, &version); err != nil {
			return store.EventBatch{}, err
		}
		stored, err := storedEventFromColumns(payload, processing, version)
		if err != nil {
			return store.EventBatch{}, err
		}
		found[id] = stored
	}
	if err := rows.Err(); err != nil {
		return store.EventBatch{}, err
	}
	result := store.EventBatch{Events: make([]store.StoredEvent, 0, len(found)), NotFound: []string{}}
	for _, id := range ids {
		if item, ok := found[id]; ok {
			result.Events = append(result.Events, item)
		} else {
			result.NotFound = append(result.NotFound, id)
		}
	}
	return result, nil
}

// ListEventsByAlert 按关联 Alert 和 received_at 稳定分页。
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
	expected := cursorPayload{
		Kind: cursorKindEventByAlert, TenantID: bkTenantID, ParentID: alertID,
		RangeStartNS: from.UnixNano(), BoundaryNS: to.UnixNano(),
	}
	var cursor cursorPayload
	if page.Cursor != "" {
		cursor, err = decodeCursor(page.Cursor, expected)
		if err != nil {
			return store.EventPage{}, err
		}
	}
	query := `SELECT event_id, received_at_ns, ` + storedEventColumns + ` FROM linkd_events
		WHERE bk_tenant_id=? AND related_alert_id=?
		AND received_at_ns>=? AND received_at_ns<=?`
	args := []any{bkTenantID, alertID, from.UnixNano(), to.UnixNano()}
	if page.Cursor != "" {
		query += ` AND (received_at_ns>? OR (received_at_ns=? AND event_id>?))`
		args = append(args, cursor.TimeNS, cursor.TimeNS, cursor.ObjectID)
	}
	query += ` ORDER BY received_at_ns,event_id LIMIT ?`
	args = append(args, page.Limit+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return store.EventPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]store.StoredEvent, 0, page.Limit+1)
	times := make([]int64, 0, page.Limit+1)
	for rows.Next() {
		var id string
		var receivedNS int64
		var payload, processing []byte
		var version uint64
		if err := rows.Scan(&id, &receivedNS, &payload, &processing, &version); err != nil {
			return store.EventPage{}, err
		}
		stored, err := storedEventFromColumns(payload, processing, version)
		if err != nil {
			return store.EventPage{}, err
		}
		items = append(items, stored)
		times = append(times, receivedNS)
	}
	if err := rows.Err(); err != nil {
		return store.EventPage{}, err
	}
	result := store.EventPage{Events: items}
	if len(items) > page.Limit {
		last := items[page.Limit-1]
		result.NextCursor, err = encodeCursor(cursorPayload{
			Kind: cursorKindEventByAlert, TenantID: bkTenantID, ParentID: alertID,
			RangeStartNS: from.UnixNano(), BoundaryNS: to.UnixNano(),
			TimeNS: times[page.Limit-1], ObjectID: last.Event.EventID,
		})
		if err != nil {
			return store.EventPage{}, err
		}
		result.Events = items[:page.Limit]
	}
	return result, nil
}

func storedEventFromColumns(payload, processingPayload []byte, version uint64) (store.StoredEvent, error) {
	event, err := decodeEvent(payload)
	if err != nil {
		return store.StoredEvent{}, err
	}
	processing, err := decodeProcessing(processingPayload)
	if err != nil {
		return store.StoredEvent{}, err
	}
	stored := store.StoredEvent{Event: event, Processing: processing, Version: versionToken(version)}
	if err := stored.Validate(); err != nil {
		return store.StoredEvent{}, err
	}
	return stored, nil
}

func (r *Repository) ScanUnprocessedEvents(ctx context.Context, tenantID string, before time.Time, page store.PageRequest) (store.EventPage, error) {
	if err := contextError(ctx); err != nil {
		return store.EventPage{}, err
	}
	if tenantID == "" || before.IsZero() {
		return store.EventPage{}, fmt.Errorf("%w: tenant and received_before are required", store.ErrInvalidArgument)
	}
	page, err := page.Normalize()
	if err != nil {
		return store.EventPage{}, err
	}
	before = before.Round(0).UTC()
	expected := cursorPayload{Kind: cursorKindEvent, TenantID: tenantID, BoundaryNS: before.UnixNano()}
	var cursor cursorPayload
	if page.Cursor != "" {
		cursor, err = decodeCursor(page.Cursor, expected)
		if err != nil {
			return store.EventPage{}, err
		}
	}
	query := `SELECT event_id, received_at_ns, ` + storedEventColumns + ` FROM linkd_events
        WHERE bk_tenant_id=? AND processing_state=? AND received_at_ns<=?`
	args := []any{tenantID, domain.EventProcessStateUnprocessed, before.UnixNano()}
	if page.Cursor != "" {
		query += ` AND (received_at_ns>? OR (received_at_ns=? AND event_id>?))`
		args = append(args, cursor.TimeNS, cursor.TimeNS, cursor.ObjectID)
	}
	query += ` ORDER BY received_at_ns,event_id LIMIT ?`
	args = append(args, page.Limit+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return store.EventPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]store.StoredEvent, 0, page.Limit+1)
	times := make([]int64, 0, page.Limit+1)
	for rows.Next() {
		var id string
		var ns int64
		var payload, proc []byte
		var version uint64
		if err := rows.Scan(&id, &ns, &payload, &proc, &version); err != nil {
			return store.EventPage{}, err
		}
		stored, err := storedEventFromColumns(payload, proc, version)
		if err != nil {
			return store.EventPage{}, err
		}
		items = append(items, stored)
		times = append(times, ns)
	}
	if err := rows.Err(); err != nil {
		return store.EventPage{}, err
	}
	result := store.EventPage{}
	if len(items) > page.Limit {
		last := items[page.Limit-1]
		result.NextCursor, err = encodeCursor(cursorPayload{Kind: cursorKindEvent, TenantID: tenantID, BoundaryNS: before.UnixNano(), TimeNS: times[page.Limit-1], ObjectID: last.Event.EventID})
		if err != nil {
			return store.EventPage{}, err
		}
		items = items[:page.Limit]
	}
	result.Events = items
	return result, nil
}

func (r *Repository) ScanAllUnprocessedEvents(ctx context.Context, before time.Time, page store.PageRequest) (store.EventPage, error) {
	if err := contextError(ctx); err != nil {
		return store.EventPage{}, err
	}
	if before.IsZero() {
		return store.EventPage{}, fmt.Errorf("%w: received_before is required", store.ErrInvalidArgument)
	}
	page, err := page.Normalize()
	if err != nil {
		return store.EventPage{}, err
	}
	before = before.Round(0).UTC()
	expected := cursorPayload{Kind: cursorKindAllEvent, BoundaryNS: before.UnixNano()}
	var cursor cursorPayload
	if page.Cursor != "" {
		cursor, err = decodeCursor(page.Cursor, expected)
		if err != nil {
			return store.EventPage{}, err
		}
	}
	query := `SELECT bk_tenant_id,event_id,received_at_ns, ` + storedEventColumns + ` FROM linkd_events WHERE processing_state=? AND received_at_ns<=?`
	args := []any{domain.EventProcessStateUnprocessed, before.UnixNano()}
	if page.Cursor != "" {
		query += ` AND (received_at_ns>? OR (received_at_ns=? AND bk_tenant_id>?) OR (received_at_ns=? AND bk_tenant_id=? AND event_id>?))`
		args = append(args, cursor.TimeNS, cursor.TimeNS, cursor.ItemTenantID, cursor.TimeNS, cursor.ItemTenantID, cursor.ObjectID)
	}
	query += ` ORDER BY received_at_ns,bk_tenant_id,event_id LIMIT ?`
	args = append(args, page.Limit+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return store.EventPage{}, err
	}
	defer func() { _ = rows.Close() }()
	items := make([]store.StoredEvent, 0, page.Limit+1)
	tenants := make([]string, 0, page.Limit+1)
	times := make([]int64, 0, page.Limit+1)
	for rows.Next() {
		var tenant, id string
		var ns int64
		var payload, proc []byte
		var version uint64
		if err := rows.Scan(&tenant, &id, &ns, &payload, &proc, &version); err != nil {
			return store.EventPage{}, err
		}
		stored, err := storedEventFromColumns(payload, proc, version)
		if err != nil {
			return store.EventPage{}, err
		}
		items = append(items, stored)
		tenants = append(tenants, tenant)
		times = append(times, ns)
	}
	if err := rows.Err(); err != nil {
		return store.EventPage{}, err
	}
	result := store.EventPage{}
	if len(items) > page.Limit {
		index := page.Limit - 1
		result.NextCursor, err = encodeCursor(cursorPayload{Kind: cursorKindAllEvent, BoundaryNS: before.UnixNano(), TimeNS: times[index], ItemTenantID: tenants[index], ObjectID: items[index].Event.EventID})
		if err != nil {
			return store.EventPage{}, err
		}
		items = items[:page.Limit]
	}
	result.Events = items
	return result, nil
}

func (r *Repository) CompareAndSetEventResult(ctx context.Context, tenantID, eventID string, expected store.VersionToken, result store.EventResult) (store.StoredEvent, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredEvent{}, err
	}
	if err := validateIdentity(tenantID, "event_id", eventID); err != nil {
		return store.StoredEvent{}, err
	}
	expectedVersion, ok := parseVersion(expected)
	if !ok {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrVersionConflict, eventID)
	}
	normalizedResult, err := result.Normalize()
	if err != nil {
		return store.StoredEvent{}, fmt.Errorf("%w: %w", store.ErrInvalidArgument, err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return store.StoredEvent{}, err
	}
	defer rollback(tx)
	current, err := scanStoredEvent(tx.QueryRowContext(ctx, `SELECT `+storedEventColumns+` FROM linkd_events WHERE bk_tenant_id=? AND event_id=? FOR UPDATE`, tenantID, eventID))
	if errors.Is(err, sql.ErrNoRows) {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrNotFound, eventID)
	}
	if err != nil {
		return store.StoredEvent{}, err
	}
	if current.Version != expected {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrVersionConflict, eventID)
	}
	if current.Processing.State != domain.EventProcessStateUnprocessed {
		return store.StoredEvent{}, fmt.Errorf("%w: event already processed", store.ErrInvalidTransition)
	}
	updated := current.Event.Clone()
	if normalizedResult.RelatedAlertID != "" {
		updated, err = updated.WithRelatedAlertID(normalizedResult.RelatedAlertID)
		if err != nil {
			return store.StoredEvent{}, fmt.Errorf("%w: %w", store.ErrInvalidTransition, err)
		}
	}
	if err := domain.ValidateEventReplacement(current.Event, updated); err != nil {
		return store.StoredEvent{}, fmt.Errorf("%w: %w", store.ErrInvalidTransition, err)
	}
	processedAt := normalizedResult.ProcessedAt
	processing := store.EventProcessing{State: normalizedResult.State, Outcome: normalizedResult.Outcome, ReasonCode: normalizedResult.ReasonCode, ProcessedAt: &processedAt}
	payload, err := encodeEvent(updated)
	if err != nil {
		return store.StoredEvent{}, err
	}
	processingPayload, err := encodeProcessing(processing)
	if err != nil {
		return store.StoredEvent{}, err
	}
	newVersion := expectedVersion + 1
	var relatedAlertID any
	if updated.RelatedAlertID != "" {
		relatedAlertID = updated.RelatedAlertID
	}
	execResult, err := tx.ExecContext(ctx, `UPDATE linkd_events SET payload=?,processing=?,processing_state=?,related_alert_id=?,version=? WHERE bk_tenant_id=? AND event_id=? AND version=?`, payload, processingPayload, processing.State, relatedAlertID, newVersion, tenantID, eventID, expectedVersion)
	if err != nil {
		return store.StoredEvent{}, err
	}
	affected, err := execResult.RowsAffected()
	if err != nil || affected != 1 {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrVersionConflict, eventID)
	}
	if err := tx.Commit(); err != nil {
		return store.StoredEvent{}, err
	}
	return store.StoredEvent{Event: updated, Processing: processing, Version: versionToken(newVersion)}, nil
}
