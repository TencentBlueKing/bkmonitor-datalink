// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
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
	"fmt"
	"net/http"
	"net/url"
	"reflect"

	"linkd/internal/domain"
	"linkd/internal/store"
)

type bulkAlertLogCreateItem struct {
	log          domain.AlertLog
	documentID   string
	writeTarget  string
	requireAlias bool
	body         []byte
}

// AppendAlertLog 委托 batch-of-one，保证单条与 Bulk create 使用完全相同的幂等和冲突语义。
func (r *Repository) AppendAlertLog(
	ctx context.Context,
	log domain.AlertLog,
) (store.AppendAlertLogResult, error) {
	items, err := r.AppendAlertLogs(ctx, []domain.AlertLog{log})
	if err != nil {
		return store.AppendAlertLogResult{}, err
	}
	if len(items) != 1 {
		return store.AppendAlertLogResult{}, fmt.Errorf("append alert log: bulk writer returned %d items", len(items))
	}
	return items[0].Result, items[0].Err
}

// AppendAlertLogs 使用 Elasticsearch Bulk create API 按输入顺序返回逐项结果。
// refresh=false 只取消 search 可见性等待；调用返回前 Elasticsearch 仍须完成每个写请求。
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
	prepared := make([]*bulkAlertLogCreateItem, len(logs))
	groups := map[bool][]int{false: {}, true: {}}
	for index, log := range logs {
		item, err := r.prepareBulkAlertLog(ctx, log)
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
		if err := r.createAlertLogBulkGroup(ctx, prepared, indices, requireAlias, results); err != nil {
			return nil, err
		}
	}
	return results, nil
}

func (r *Repository) prepareBulkAlertLog(ctx context.Context, log domain.AlertLog) (bulkAlertLogCreateItem, error) {
	normalized, err := log.Normalize()
	if err != nil {
		return bulkAlertLogCreateItem{}, fmt.Errorf("%w: normalize alert log: %w", store.ErrInvalidArgument, err)
	}
	if err := validateIdentity(normalized.BKTenantID, "log_id", normalized.LogID); err != nil {
		return bulkAlertLogCreateItem{}, err
	}
	if err := validateIdentity(normalized.BKTenantID, "alert_id", normalized.AlertID); err != nil {
		return bulkAlertLogCreateItem{}, err
	}
	if err := validateBucketAlertReference(r.router, normalized.BKTenantID, normalized.AlertID); err != nil {
		return bulkAlertLogCreateItem{}, fmt.Errorf("%w: %w", store.ErrInvalidArgument, err)
	}
	if err := validateDateNanos("alert log created_time", normalized.CreatedTime); err != nil {
		return bulkAlertLogCreateItem{}, err
	}
	route, err := r.router.AlertLogWriteRoute(ctx, normalized.AlertID, normalized.LogID)
	if err != nil {
		return bulkAlertLogCreateItem{}, fmt.Errorf("route alert log %q: %w", normalized.LogID, err)
	}
	route, err = normalizeRoute(route, r.config.MaxReadTargets)
	if err != nil {
		return bulkAlertLogCreateItem{}, err
	}
	body, err := encodeAlertLogDocument(normalized)
	if err != nil {
		return bulkAlertLogCreateItem{}, err
	}
	if len(body) > r.config.MaxDocumentBytes {
		return bulkAlertLogCreateItem{}, fmt.Errorf(
			"%w: alert log document exceeds %d bytes",
			store.ErrInvalidArgument,
			r.config.MaxDocumentBytes,
		)
	}
	return bulkAlertLogCreateItem{
		log: normalized, documentID: documentID(normalized.BKTenantID, normalized.LogID),
		writeTarget: route.WriteTarget, requireAlias: route.RequireAlias, body: body,
	}, nil
}

func (r *Repository) createAlertLogBulkGroup(
	ctx context.Context,
	prepared []*bulkAlertLogCreateItem,
	indices []int,
	requireAlias bool,
	results []store.AppendAlertLogItemResult,
) error {
	var body bytes.Buffer
	for _, index := range indices {
		item := prepared[index]
		metadata, err := json.Marshal(map[string]any{"create": map[string]string{
			"_index": item.writeTarget,
			"_id":    item.documentID,
		}})
		if err != nil {
			return fmt.Errorf("encode elasticsearch alert log bulk metadata: %w", err)
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
		return fmt.Errorf("bulk append alert logs: %w", err)
	}
	if len(response.Items) != len(indices) {
		return fmt.Errorf("elasticsearch alert log bulk returned %d items for %d logs", len(response.Items), len(indices))
	}
	for responseIndex, resultIndex := range indices {
		item := prepared[resultIndex]
		created := response.Items[responseIndex].Create
		switch created.Status {
		case http.StatusCreated:
			if created.ID != item.documentID || created.Index == "" {
				results[resultIndex].Err = fmt.Errorf("elasticsearch alert log bulk returned an unexpected identity")
				continue
			}
			results[resultIndex].Result = store.AppendAlertLogResult{Log: item.log.Clone(), Created: true}
		case http.StatusConflict:
			existing, err := r.getAlertLogFromTarget(
				ctx,
				item.writeTarget,
				item.log.BKTenantID,
				item.log.LogID,
			)
			if err == nil && !reflect.DeepEqual(existing, item.log) {
				err = fmt.Errorf(
					"%w: alert log %q already contains different content",
					store.ErrIdentityConflict,
					item.log.LogID,
				)
			}
			results[resultIndex] = store.AppendAlertLogItemResult{
				Result: store.AppendAlertLogResult{Log: existing.Clone(), Created: false},
				Err:    err,
			}
		default:
			err := fmt.Errorf("elasticsearch bulk append alert log %q returned status %d", item.log.LogID, created.Status)
			if created.Error != nil {
				err = fmt.Errorf(
					"elasticsearch bulk append alert log %q returned status %d: %s: %s",
					item.log.LogID,
					created.Status,
					created.Error.Type,
					created.Error.Reason,
				)
			}
			if created.Status >= http.StatusBadRequest && created.Status < http.StatusInternalServerError &&
				created.Status != http.StatusTooManyRequests {
				err = fmt.Errorf("%w: %w", store.ErrInvalidArgument, err)
			}
			results[resultIndex].Err = err
		}
	}
	return nil
}

// ListAlertLogs 在 alert_id 路由得到的有界目标内按 created_time、log_id 稳定分页。
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
	route, err := r.router.AlertLogReadRoute(ctx, alertID)
	if err != nil {
		return store.AlertLogPage{}, fmt.Errorf("route alert log timeline %q: %w", alertID, err)
	}
	route, err = normalizeRoute(route, r.config.MaxReadTargets)
	if err != nil {
		return store.AlertLogPage{}, err
	}
	expectedCursor := cursorPayload{
		Kind:          cursorKindAlertLog,
		TenantID:      bkTenantID,
		ParentID:      alertID,
		TargetsDigest: targetsDigest(route.ReadTargets),
	}
	var cursor cursorPayload
	if normalizedPage.Cursor != "" {
		cursor, err = decodeCursor(normalizedPage.Cursor, expectedCursor)
		if err != nil {
			return store.AlertLogPage{}, err
		}
	}
	body := map[string]any{
		"size":             normalizedPage.Limit + 1,
		"track_total_hits": false,
		"query": map[string]any{
			"bool": map[string]any{
				"filter": []any{
					map[string]any{"term": map[string]any{"bk_tenant_id": bkTenantID}},
					map[string]any{"term": map[string]any{"alert_id": alertID}},
				},
			},
		},
		"sort": []any{
			map[string]any{"created_time": map[string]any{
				"order":  "asc",
				"format": "strict_date_optional_time_nanos",
			}},
			map[string]any{"log_id": map[string]any{"order": "asc"}},
			map[string]any{"_shard_doc": map[string]any{"order": "asc"}},
		},
	}
	if normalizedPage.Cursor != "" {
		body["search_after"] = cursor.SearchAfter
	}
	response, pitID, err := r.searchWithPIT(ctx, route.ReadTargets, cursor.PITID, body)
	if err != nil {
		return store.AlertLogPage{}, fmt.Errorf("list alert logs for %q: %w", alertID, err)
	}
	count := min(len(response.Hits.Hits), normalizedPage.Limit)
	result := store.AlertLogPage{Logs: make([]domain.AlertLog, 0, count)}
	seen := make(map[string]struct{}, count)
	if normalizedPage.Cursor != "" {
		seen[cursor.ObjectID] = struct{}{}
	}
	for _, hit := range response.Hits.Hits[:count] {
		if len(hit.Sort) != 3 {
			return store.AlertLogPage{}, fmt.Errorf("elasticsearch alert log search returned invalid sort values")
		}
		log, err := decodeAlertLogHit(hit)
		if err != nil {
			return store.AlertLogPage{}, err
		}
		if log.BKTenantID != bkTenantID || log.AlertID != alertID {
			return store.AlertLogPage{}, fmt.Errorf("elasticsearch alert log search returned an unexpected identity")
		}
		if _, exists := seen[log.LogID]; exists {
			return store.AlertLogPage{}, fmt.Errorf(
				"%w: alert log %q exists in multiple elasticsearch indices",
				store.ErrIdentityConflict,
				log.LogID,
			)
		}
		seen[log.LogID] = struct{}{}
		result.Logs = append(result.Logs, log)
	}
	if len(response.Hits.Hits) > normalizedPage.Limit {
		lastHit := response.Hits.Hits[normalizedPage.Limit-1]
		last := result.Logs[len(result.Logs)-1]
		result.NextCursor, err = encodeCursor(cursorPayload{
			Kind:          cursorKindAlertLog,
			TenantID:      bkTenantID,
			ParentID:      alertID,
			TargetsDigest: targetsDigest(route.ReadTargets),
			TimeNS:        last.CreatedTime.UnixNano(),
			ObjectID:      last.LogID,
			PITID:         pitID,
			SearchAfter:   append([]json.RawMessage(nil), lastHit.Sort...),
		})
		if err != nil {
			return store.AlertLogPage{}, err
		}
	} else if err := r.closePIT(ctx, pitID); err != nil {
		return store.AlertLogPage{}, err
	}
	return result, nil
}

func (r *Repository) getAlertLogFromTarget(
	ctx context.Context,
	target, bkTenantID, logID string,
) (domain.AlertLog, error) {
	documentID := documentID(bkTenantID, logID)
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
		if isResponseStatus(err, http.StatusNotFound) {
			return domain.AlertLog{}, fmt.Errorf("%w: alert log %q", store.ErrNotFound, logID)
		}
		return domain.AlertLog{}, err
	}
	log, err := decodeAlertLogHit(response.hit())
	if err != nil {
		return domain.AlertLog{}, err
	}
	if log.BKTenantID != bkTenantID || log.LogID != logID {
		return domain.AlertLog{}, fmt.Errorf("elasticsearch alert log GET returned an unexpected identity")
	}
	return log, nil
}
