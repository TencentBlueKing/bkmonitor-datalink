// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package memory

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
)

var _ store.Repository = (*Repository)(nil)

type objectKey struct {
	tenantID string
	objectID string
}

type activeAlertKey struct {
	tenantID      string
	eventSourceID string
	fingerprint   string
}

type eventEntry struct {
	event      domain.Event
	processing store.EventProcessing
	version    store.VersionToken
}

type alertEntry struct {
	alert   domain.Alert
	version store.VersionToken
}

// Repository 是线程安全、无后台任务的内存存储实现。
type Repository struct {
	mu           sync.RWMutex
	nextVersion  uint64
	events       map[objectKey]eventEntry
	alerts       map[objectKey]alertEntry
	alertLogs    map[objectKey]domain.AlertLog
	activeAlerts map[activeAlertKey]objectKey
}

// New 创建空的内存 Repository。
func New() *Repository {
	return &Repository{
		events:       make(map[objectKey]eventEntry),
		alerts:       make(map[objectKey]alertEntry),
		alertLogs:    make(map[objectKey]domain.AlertLog),
		activeAlerts: make(map[activeAlertKey]objectKey),
	}
}

// CreateEvent 创建未处理事件；相同内容的重复创建返回既有快照。
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

	key := objectKey{tenantID: normalized.BKTenantID, objectID: normalized.EventID}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.events[key]; ok {
		if err := domain.ValidateEventReplacement(normalized, existing.event); err != nil {
			return store.CreateEventResult{}, fmt.Errorf(
				"%w: event %q already contains different content: %w",
				store.ErrIdentityConflict,
				normalized.EventID,
				err,
			)
		}
		return store.CreateEventResult{StoredEvent: cloneStoredEvent(existing), Created: false}, nil
	}
	entry := eventEntry{
		event: normalized.Clone(), processing: store.NewUnprocessedEventProcessing(), version: r.newVersionLocked(),
	}
	r.events[key] = entry
	return store.CreateEventResult{StoredEvent: cloneStoredEvent(entry), Created: true}, nil
}

// CreateEvents 按输入顺序返回逐项幂等创建结果。
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

// GetEvent 读取当前租户中的单个事件。
func (r *Repository) GetEvent(ctx context.Context, bkTenantID, eventID string) (store.StoredEvent, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredEvent{}, err
	}
	if err := validateIdentity(bkTenantID, "event_id", eventID); err != nil {
		return store.StoredEvent{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.events[objectKey{tenantID: bkTenantID, objectID: eventID}]
	if !ok {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrNotFound, eventID)
	}
	return cloneStoredEvent(entry), nil
}

// GetEvents 按首次出现顺序批量读取事件并单列缺失 ID。
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := store.EventBatch{
		Events:   make([]store.StoredEvent, 0, len(ids)),
		NotFound: make([]string, 0),
	}
	for _, eventID := range ids {
		entry, ok := r.events[objectKey{tenantID: bkTenantID, objectID: eventID}]
		if !ok {
			result.NotFound = append(result.NotFound, eventID)
			continue
		}
		result.Events = append(result.Events, cloneStoredEvent(entry))
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
	r.mu.RLock()
	alertEntry, ok := r.alerts[objectKey{tenantID: bkTenantID, objectID: alertID}]
	if !ok {
		r.mu.RUnlock()
		return store.EventPage{}, fmt.Errorf("%w: alert %q", store.ErrNotFound, alertID)
	}
	alert := alertEntry.alert.Clone()
	r.mu.RUnlock()
	from, to, page, err := store.ResolveEventByAlertRange(alert, request, time.Now())
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
	r.mu.RLock()
	candidates := make([]eventEntry, 0)
	for key, entry := range r.events {
		if key.tenantID == bkTenantID && entry.event.RelatedAlertID == alertID &&
			!entry.event.ReceivedAt.Before(from) && !entry.event.ReceivedAt.After(to) {
			candidates = append(candidates, entry)
		}
	}
	r.mu.RUnlock()
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i].event, candidates[j].event
		if left.ReceivedAt.Equal(right.ReceivedAt) {
			return left.EventID < right.EventID
		}
		return left.ReceivedAt.Before(right.ReceivedAt)
	})
	start := 0
	if page.Cursor != "" {
		start = sort.Search(len(candidates), func(index int) bool {
			candidate := candidates[index].event
			return candidate.ReceivedAt.UnixNano() > cursor.TimeNS ||
				(candidate.ReceivedAt.UnixNano() == cursor.TimeNS && candidate.EventID > cursor.ObjectID)
		})
	}
	end := min(start+page.Limit, len(candidates))
	result := store.EventPage{Events: make([]store.StoredEvent, 0, end-start)}
	for _, entry := range candidates[start:end] {
		result.Events = append(result.Events, cloneStoredEvent(entry))
	}
	if end < len(candidates) {
		last := candidates[end-1].event
		result.NextCursor, err = encodeCursor(cursorPayload{
			Kind: cursorKindEventByAlert, TenantID: bkTenantID, ParentID: alertID,
			RangeStartNS: from.UnixNano(), BoundaryNS: to.UnixNano(),
			TimeNS: last.ReceivedAt.UnixNano(), ObjectID: last.EventID,
		})
		if err != nil {
			return store.EventPage{}, err
		}
	}
	return result, nil
}

// ScanUnprocessedEvents 按 received_time、event_id 稳定扫描未处理事件。
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
	if receivedBefore.IsZero() {
		return store.EventPage{}, fmt.Errorf("%w: received_before must not be zero", store.ErrInvalidArgument)
	}
	normalizedPage, err := page.Normalize()
	if err != nil {
		return store.EventPage{}, err
	}
	receivedBefore = receivedBefore.Round(0).UTC()
	expectedCursor := cursorPayload{
		Kind:       cursorKindEvent,
		TenantID:   bkTenantID,
		BoundaryNS: receivedBefore.UnixNano(),
	}
	var cursor cursorPayload
	if normalizedPage.Cursor != "" {
		cursor, err = decodeCursor(normalizedPage.Cursor, expectedCursor)
		if err != nil {
			return store.EventPage{}, err
		}
	}

	r.mu.RLock()
	candidates := make([]eventEntry, 0)
	for key, entry := range r.events {
		if key.tenantID != bkTenantID || entry.processing.State != domain.EventProcessStateUnprocessed ||
			entry.event.ReceivedAt.After(receivedBefore) {
			continue
		}
		candidates = append(candidates, entry)
	}
	r.mu.RUnlock()
	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].event
		right := candidates[j].event
		if left.ReceivedAt.Equal(right.ReceivedAt) {
			return left.EventID < right.EventID
		}
		return left.ReceivedAt.Before(right.ReceivedAt)
	})

	start := 0
	if normalizedPage.Cursor != "" {
		start = sort.Search(len(candidates), func(index int) bool {
			candidate := candidates[index].event
			candidateNS := candidate.ReceivedAt.UnixNano()
			return candidateNS > cursor.TimeNS ||
				(candidateNS == cursor.TimeNS && candidate.EventID > cursor.ObjectID)
		})
	}
	end := min(start+normalizedPage.Limit, len(candidates))
	result := store.EventPage{Events: make([]store.StoredEvent, 0, end-start)}
	for _, entry := range candidates[start:end] {
		result.Events = append(result.Events, cloneStoredEvent(entry))
	}
	if end < len(candidates) {
		last := candidates[end-1].event
		result.NextCursor, err = encodeCursor(cursorPayload{
			Kind:       cursorKindEvent,
			TenantID:   bkTenantID,
			BoundaryNS: receivedBefore.UnixNano(),
			TimeNS:     last.ReceivedAt.UnixNano(),
			ObjectID:   last.EventID,
		})
		if err != nil {
			return store.EventPage{}, err
		}
	}
	return result, nil
}

// ScanAllUnprocessedEvents 按 received_time、租户和 event_id 稳定扫描全部租户的未处理事件。
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
	expectedCursor := cursorPayload{Kind: cursorKindAllEvent, BoundaryNS: receivedBefore.UnixNano()}
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
	type candidate struct {
		key   objectKey
		entry eventEntry
	}
	r.mu.RLock()
	candidates := make([]candidate, 0)
	for key, entry := range r.events {
		if entry.processing.State != domain.EventProcessStateUnprocessed || entry.event.ReceivedAt.After(receivedBefore) {
			continue
		}
		candidates = append(candidates, candidate{key: key, entry: entry})
	}
	r.mu.RUnlock()
	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i]
		right := candidates[j]
		if !left.entry.event.ReceivedAt.Equal(right.entry.event.ReceivedAt) {
			return left.entry.event.ReceivedAt.Before(right.entry.event.ReceivedAt)
		}
		if left.key.tenantID != right.key.tenantID {
			return left.key.tenantID < right.key.tenantID
		}
		return left.key.objectID < right.key.objectID
	})
	start := 0
	if normalizedPage.Cursor != "" {
		start = sort.Search(len(candidates), func(index int) bool {
			item := candidates[index]
			itemNS := item.entry.event.ReceivedAt.UnixNano()
			return itemNS > cursor.TimeNS ||
				(itemNS == cursor.TimeNS && item.key.tenantID > cursor.ItemTenantID) ||
				(itemNS == cursor.TimeNS && item.key.tenantID == cursor.ItemTenantID && item.key.objectID > cursor.ObjectID)
		})
	}
	end := min(start+normalizedPage.Limit, len(candidates))
	result := store.EventPage{Events: make([]store.StoredEvent, 0, end-start)}
	for _, item := range candidates[start:end] {
		result.Events = append(result.Events, cloneStoredEvent(item.entry))
	}
	if end < len(candidates) {
		last := candidates[end-1]
		result.NextCursor, err = encodeCursor(cursorPayload{
			Kind:         cursorKindAllEvent,
			BoundaryNS:   receivedBefore.UnixNano(),
			TimeNS:       last.entry.event.ReceivedAt.UnixNano(),
			ItemTenantID: last.key.tenantID,
			ObjectID:     last.key.objectID,
		})
		if err != nil {
			return store.EventPage{}, err
		}
	}
	return result, nil
}

// CompareAndSetEventResult 在版本匹配时写入 Event 的单向处理结果。
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

	key := objectKey{tenantID: bkTenantID, objectID: eventID}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.events[key]
	if !ok {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrNotFound, eventID)
	}
	if entry.version != expected {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q", store.ErrVersionConflict, eventID)
	}
	normalizedResult, err := result.Normalize()
	if err != nil {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q result: %w", store.ErrInvalidArgument, eventID, err)
	}
	if entry.processing.State != domain.EventProcessStateUnprocessed {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q is already processed", store.ErrInvalidTransition, eventID)
	}
	updated := entry.event.Clone()
	if normalizedResult.RelatedAlertID != "" {
		updated, err = updated.WithRelatedAlertID(normalizedResult.RelatedAlertID)
	}
	if err != nil {
		return store.StoredEvent{}, fmt.Errorf("%w: event %q: %w", store.ErrInvalidTransition, eventID, err)
	}
	processedAt := normalizedResult.ProcessedAt
	processing := store.EventProcessing{
		State: normalizedResult.State, Outcome: normalizedResult.Outcome,
		ReasonCode: normalizedResult.ReasonCode, ProcessedAt: &processedAt,
	}
	entry = eventEntry{event: updated, processing: processing, version: r.newVersionLocked()}
	r.events[key] = entry
	return cloneStoredEvent(entry), nil
}

// CreateAlert 创建 Alert；相同内容的重复创建返回既有快照。
func (r *Repository) CreateAlert(ctx context.Context, alert domain.Alert) (store.CreateAlertResult, error) {
	if err := contextError(ctx); err != nil {
		return store.CreateAlertResult{}, err
	}
	normalized, err := alert.Normalize()
	if err != nil {
		return store.CreateAlertResult{}, fmt.Errorf("%w: normalize alert: %w", store.ErrInvalidArgument, err)
	}
	if normalized.Status != domain.AlertStatusActive {
		return store.CreateAlertResult{}, fmt.Errorf("%w: new alert must be active", store.ErrInvalidArgument)
	}
	key := objectKey{tenantID: normalized.BKTenantID, objectID: normalized.AlertID}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.alerts[key]; ok {
		if !reflect.DeepEqual(existing.alert, normalized) {
			return store.CreateAlertResult{}, fmt.Errorf(
				"%w: alert %q already contains different content",
				store.ErrIdentityConflict,
				normalized.AlertID,
			)
		}
		return store.CreateAlertResult{StoredAlert: cloneStoredAlert(existing), Created: false}, nil
	}
	if err := r.reserveActiveAlertLocked(normalized, key); err != nil {
		return store.CreateAlertResult{}, err
	}
	entry := alertEntry{alert: normalized.Clone(), version: r.newVersionLocked()}
	r.alerts[key] = entry
	return store.CreateAlertResult{StoredAlert: cloneStoredAlert(entry), Created: true}, nil
}

// GetAlert 读取当前租户中的逻辑 Alert。
func (r *Repository) GetAlert(ctx context.Context, bkTenantID, alertID string) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	if err := validateIdentity(bkTenantID, "alert_id", alertID); err != nil {
		return store.StoredAlert{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.alerts[objectKey{tenantID: bkTenantID, objectID: alertID}]
	if ok {
		return cloneStoredAlert(entry), nil
	}
	return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrNotFound, alertID)
}

// GetAlerts 按首次出现顺序批量读取 Alert 并单列缺失 ID。
func (r *Repository) GetAlerts(
	ctx context.Context,
	bkTenantID string,
	alertIDs []string,
) (store.AlertBatch, error) {
	if err := contextError(ctx); err != nil {
		return store.AlertBatch{}, err
	}
	ids, err := normalizeBatchIDs(bkTenantID, "alert_id", alertIDs)
	if err != nil {
		return store.AlertBatch{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := store.AlertBatch{
		Alerts:   make([]store.StoredAlert, 0, len(ids)),
		NotFound: make([]string, 0),
	}
	for _, alertID := range ids {
		if entry, ok := r.alerts[objectKey{tenantID: bkTenantID, objectID: alertID}]; ok {
			result.Alerts = append(result.Alerts, cloneStoredAlert(entry))
			continue
		}
		result.NotFound = append(result.NotFound, alertID)
	}
	return result, nil
}

// FindActiveAlert 使用稳定关联键查找唯一活动 Alert。
func (r *Repository) FindActiveAlert(
	ctx context.Context,
	key store.ActiveAlertKey,
) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	if err := validateActiveAlertKey(key); err != nil {
		return store.StoredAlert{}, err
	}
	lookup := activeAlertKey{
		tenantID:      key.BKTenantID,
		eventSourceID: key.EventSourceID,
		fingerprint:   key.Fingerprint,
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	object, ok := r.activeAlerts[lookup]
	if !ok {
		return store.StoredAlert{}, fmt.Errorf("%w: active alert", store.ErrNotFound)
	}
	entry, ok := r.alerts[object]
	if !ok {
		return store.StoredAlert{}, fmt.Errorf("%w: active alert index is inconsistent", store.ErrIdentityConflict)
	}
	return cloneStoredAlert(entry), nil
}

// FindAlertEndedByEvent 查找由指定 Event 终止的唯一 Alert，用于恢复跨对象部分成功。
func (r *Repository) FindAlertEndedByEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	if err := validateIdentity(bkTenantID, "event_id", eventID); err != nil {
		return store.StoredAlert{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	var found *alertEntry
	for key, entry := range r.alerts {
		endedByEvent := entry.alert.EndType == domain.AlertEndTypeSource ||
			entry.alert.EndType == domain.AlertEndTypeSeverityUpgrade
		if key.tenantID != bkTenantID || !entry.alert.Status.Terminal() || !endedByEvent ||
			entry.alert.LatestEventID != eventID {
			continue
		}
		if found != nil {
			return store.StoredAlert{}, fmt.Errorf(
				"%w: multiple terminal alerts for event %q",
				store.ErrIdentityConflict,
				eventID,
			)
		}
		cloned := entry
		found = &cloned
	}
	if found == nil {
		return store.StoredAlert{}, fmt.Errorf("%w: terminal alert for event %q", store.ErrNotFound, eventID)
	}
	return cloneStoredAlert(*found), nil
}

// CompareAndSetAlert 在版本匹配且领域身份不变时替换 Alert 当前态。
func (r *Repository) CompareAndSetAlert(
	ctx context.Context,
	bkTenantID, alertID string,
	expected store.VersionToken,
	replacement domain.Alert,
) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	if err := validateIdentity(bkTenantID, "alert_id", alertID); err != nil {
		return store.StoredAlert{}, err
	}
	if expected.IsZero() {
		return store.StoredAlert{}, fmt.Errorf("%w: expected alert version must not be empty", store.ErrInvalidArgument)
	}
	normalized, err := replacement.Normalize()
	if err != nil {
		return store.StoredAlert{}, fmt.Errorf("%w: normalize replacement alert: %w", store.ErrInvalidArgument, err)
	}
	key := objectKey{tenantID: bkTenantID, objectID: alertID}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.alerts[key]
	if !ok {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrNotFound, alertID)
	}
	if entry.version != expected {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrVersionConflict, alertID)
	}
	if err := domain.ValidateAlertReplacement(entry.alert, normalized); err != nil {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q: %w", store.ErrInvalidTransition, alertID, err)
	}
	if entry.alert.Status == domain.AlertStatusActive && normalized.Status.Terminal() {
		delete(r.activeAlerts, activeKeyForAlert(entry.alert))
	}
	if normalized.Status == domain.AlertStatusActive {
		if err := r.reserveActiveAlertLocked(normalized, key); err != nil {
			return store.StoredAlert{}, err
		}
	}
	entry = alertEntry{
		alert: normalized.Clone(), version: r.newVersionLocked(),
	}
	r.alerts[key] = entry
	return cloneStoredAlert(entry), nil
}

// AppendAlertLog 委托 batch-of-one，保证单条与批量追加使用相同语义。
func (r *Repository) AppendAlertLog(
	ctx context.Context,
	log domain.AlertLog,
) (store.AppendAlertLogResult, error) {
	items, err := r.AppendAlertLogs(ctx, []domain.AlertLog{log})
	if err != nil {
		return store.AppendAlertLogResult{}, err
	}
	if len(items) != 1 {
		return store.AppendAlertLogResult{}, fmt.Errorf("append alert log: batch writer returned %d items", len(items))
	}
	return items[0].Result, items[0].Err
}

// AppendAlertLogs 按输入顺序逐项追加不可变流水，不承诺整批事务。
func (r *Repository) AppendAlertLogs(
	ctx context.Context,
	logs []domain.AlertLog,
) ([]store.AppendAlertLogItemResult, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if len(logs) == 0 || len(logs) > store.MaxBatchSize {
		return nil, fmt.Errorf(
			"%w: alert log batch size must be between 1 and %d",
			store.ErrInvalidArgument,
			store.MaxBatchSize,
		)
	}
	results := make([]store.AppendAlertLogItemResult, len(logs))
	for index, log := range logs {
		results[index].Result, results[index].Err = r.appendAlertLog(ctx, log)
	}
	return results, nil
}

func (r *Repository) appendAlertLog(
	ctx context.Context,
	log domain.AlertLog,
) (store.AppendAlertLogResult, error) {
	if err := contextError(ctx); err != nil {
		return store.AppendAlertLogResult{}, err
	}
	normalized, err := log.Normalize()
	if err != nil {
		return store.AppendAlertLogResult{}, fmt.Errorf("%w: normalize alert log: %w", store.ErrInvalidArgument, err)
	}
	key := objectKey{tenantID: normalized.BKTenantID, objectID: normalized.LogID}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.alertLogs[key]; ok {
		if !reflect.DeepEqual(existing, normalized) {
			return store.AppendAlertLogResult{}, fmt.Errorf(
				"%w: alert log %q already contains different content",
				store.ErrIdentityConflict,
				normalized.LogID,
			)
		}
		return store.AppendAlertLogResult{Log: existing.Clone(), Created: false}, nil
	}
	r.alertLogs[key] = normalized.Clone()
	return store.AppendAlertLogResult{Log: normalized.Clone(), Created: true}, nil
}

// ListAlertLogs 按 created_time、log_id 稳定读取指定 Alert 的流水。
func (r *Repository) ListAlertLogs(
	ctx context.Context,
	bkTenantID, alertID string,
	page store.PageRequest,
) (store.AlertLogPage, error) {
	if err := contextError(ctx); err != nil {
		return store.AlertLogPage{}, err
	}
	if err := validateIdentity(bkTenantID, "alert_id", alertID); err != nil {
		return store.AlertLogPage{}, err
	}
	normalizedPage, err := page.Normalize()
	if err != nil {
		return store.AlertLogPage{}, err
	}
	expectedCursor := cursorPayload{
		Kind:     cursorKindAlertLog,
		TenantID: bkTenantID,
		ParentID: alertID,
	}
	var cursor cursorPayload
	if normalizedPage.Cursor != "" {
		cursor, err = decodeCursor(normalizedPage.Cursor, expectedCursor)
		if err != nil {
			return store.AlertLogPage{}, err
		}
	}

	r.mu.RLock()
	candidates := make([]domain.AlertLog, 0)
	for key, log := range r.alertLogs {
		if key.tenantID == bkTenantID && log.AlertID == alertID {
			candidates = append(candidates, log.Clone())
		}
	}
	r.mu.RUnlock()
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].CreatedTime.Equal(candidates[j].CreatedTime) {
			return candidates[i].LogID < candidates[j].LogID
		}
		return candidates[i].CreatedTime.Before(candidates[j].CreatedTime)
	})

	start := 0
	if normalizedPage.Cursor != "" {
		start = sort.Search(len(candidates), func(index int) bool {
			candidateNS := candidates[index].CreatedTime.UnixNano()
			return candidateNS > cursor.TimeNS ||
				(candidateNS == cursor.TimeNS && candidates[index].LogID > cursor.ObjectID)
		})
	}
	end := min(start+normalizedPage.Limit, len(candidates))
	result := store.AlertLogPage{Logs: make([]domain.AlertLog, 0, end-start)}
	for _, log := range candidates[start:end] {
		result.Logs = append(result.Logs, log.Clone())
	}
	if end < len(candidates) {
		last := candidates[end-1]
		result.NextCursor, err = encodeCursor(cursorPayload{
			Kind:     cursorKindAlertLog,
			TenantID: bkTenantID,
			ParentID: alertID,
			TimeNS:   last.CreatedTime.UnixNano(),
			ObjectID: last.LogID,
		})
		if err != nil {
			return store.AlertLogPage{}, err
		}
	}
	return result, nil
}

// QueryAlertByEvent 返回事件及其已经接受时关联的 Alert。
func (r *Repository) QueryAlertByEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (store.AlertByEventResult, error) {
	if err := contextError(ctx); err != nil {
		return store.AlertByEventResult{}, err
	}
	if err := validateIdentity(bkTenantID, "event_id", eventID); err != nil {
		return store.AlertByEventResult{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	eventEntry, ok := r.events[objectKey{tenantID: bkTenantID, objectID: eventID}]
	if !ok {
		return store.AlertByEventResult{}, fmt.Errorf("%w: event %q", store.ErrNotFound, eventID)
	}
	result := store.AlertByEventResult{Event: cloneStoredEvent(eventEntry)}
	if eventEntry.processing.State != domain.EventProcessStateAccepted &&
		eventEntry.processing.State != domain.EventProcessStateSuppressed {
		return result, nil
	}
	alertKey := objectKey{tenantID: bkTenantID, objectID: eventEntry.event.RelatedAlertID}
	if alertEntry, ok := r.alerts[alertKey]; ok {
		alert := cloneStoredAlert(alertEntry)
		result.Alert = &alert
	}
	return result, nil
}

func (r *Repository) reserveActiveAlertLocked(alert domain.Alert, object objectKey) error {
	if alert.Status != domain.AlertStatusActive {
		return nil
	}
	lookup := activeKeyForAlert(alert)
	if existing, ok := r.activeAlerts[lookup]; ok && existing != object {
		return fmt.Errorf(
			"%w: active alert already exists for source %q and fingerprint %q",
			store.ErrIdentityConflict,
			alert.EventSourceID,
			alert.Fingerprint,
		)
	}
	r.activeAlerts[lookup] = object
	return nil
}

func (r *Repository) newVersionLocked() store.VersionToken {
	r.nextVersion++
	return store.NewVersionToken("memory:" + strconv.FormatUint(r.nextVersion, 10))
}

func activeKeyForAlert(alert domain.Alert) activeAlertKey {
	return activeAlertKey{
		tenantID:      alert.BKTenantID,
		eventSourceID: alert.EventSourceID,
		fingerprint:   alert.Fingerprint,
	}
}

func cloneStoredEvent(entry eventEntry) store.StoredEvent {
	return store.StoredEvent{
		Event: entry.event.Clone(), Processing: entry.processing.Clone(), Version: entry.version,
	}
}

func cloneStoredAlert(entry alertEntry) store.StoredAlert {
	return store.StoredAlert{Alert: entry.alert.Clone(), Version: entry.version}
}

func validateIdentity(bkTenantID, idName, id string) error {
	if bkTenantID == "" {
		return fmt.Errorf("%w: bk_tenant_id must not be empty", store.ErrInvalidArgument)
	}
	if id == "" {
		return fmt.Errorf("%w: %s must not be empty", store.ErrInvalidArgument, idName)
	}
	return nil
}

func normalizeBatchIDs(bkTenantID, idName string, ids []string) ([]string, error) {
	if bkTenantID == "" {
		return nil, fmt.Errorf("%w: bk_tenant_id must not be empty", store.ErrInvalidArgument)
	}
	if len(ids) == 0 || len(ids) > store.MaxBatchSize {
		return nil, fmt.Errorf(
			"%w: %s batch size must be between 1 and %d",
			store.ErrInvalidArgument,
			idName,
			store.MaxBatchSize,
		)
	}
	seen := make(map[string]struct{}, len(ids))
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			return nil, fmt.Errorf("%w: %s must not be empty", store.ErrInvalidArgument, idName)
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result, nil
}

func validateActiveAlertKey(key store.ActiveAlertKey) error {
	for name, value := range map[string]string{
		"bk_tenant_id":    key.BKTenantID,
		"event_source_id": key.EventSourceID,
		"fingerprint":     key.Fingerprint,
	} {
		if value == "" {
			return fmt.Errorf("%w: active alert %s must not be empty", store.ErrInvalidArgument, name)
		}
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: context must not be nil", store.ErrInvalidArgument)
	}
	return ctx.Err()
}
