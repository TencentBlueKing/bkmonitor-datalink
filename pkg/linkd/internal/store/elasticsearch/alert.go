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
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"strconv"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// CreateAlert 使用租户与 AlertID 组成的稳定 _id 和 create-only API 创建活动 Alert。
// 生命周期调度器必须先持有 tenant/source/fingerprint lease；Elasticsearch 本身不提供 fingerprint 唯一约束。
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
	if err := validateAlertIdentity(normalized); err != nil {
		return store.CreateAlertResult{}, err
	}
	if err := validateBucketAlertIdentity(r.router, normalized); err != nil {
		return store.CreateAlertResult{}, fmt.Errorf("%w: %w", store.ErrInvalidArgument, err)
	}
	active, err := r.FindActiveAlert(ctx, store.ActiveAlertKey{
		BKTenantID: normalized.BKTenantID, EventSourceID: normalized.EventSourceID,
		Fingerprint: normalized.Fingerprint,
	})
	if err == nil {
		if active.Alert.AlertID == normalized.AlertID && reflect.DeepEqual(active.Alert, normalized) {
			return store.CreateAlertResult{StoredAlert: active, Created: false}, nil
		}
		return store.CreateAlertResult{}, fmt.Errorf(
			"%w: active alert already exists for source %q and fingerprint %q",
			store.ErrIdentityConflict,
			normalized.EventSourceID,
			normalized.Fingerprint,
		)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.CreateAlertResult{}, err
	}
	route, err := r.router.AlertRoute(ctx, normalized.AlertID)
	if err != nil {
		return store.CreateAlertResult{}, fmt.Errorf("route alert %q: %w", normalized.AlertID, err)
	}
	route, err = normalizeRoute(route, r.config.MaxReadTargets)
	if err != nil {
		return store.CreateAlertResult{}, err
	}
	body, err := encodeAlertDocument(normalized)
	if err != nil {
		return store.CreateAlertResult{}, err
	}
	if len(body) > r.config.MaxDocumentBytes {
		return store.CreateAlertResult{}, fmt.Errorf(
			"%w: alert document exceeds %d bytes", store.ErrInvalidArgument, r.config.MaxDocumentBytes,
		)
	}
	documentID := alertDocumentID(normalized)
	query := url.Values{"refresh": []string{"wait_for"}}
	if route.RequireAlias {
		query.Set("require_alias", "true")
	}
	var response indexResponse
	err = r.performJSON(
		ctx,
		http.MethodPut,
		"/"+route.WriteTarget+"/_create/"+documentID,
		query,
		body,
		&response,
	)
	if err == nil {
		stored, createErr := storedAlertFromIndexResponse(normalized, documentID, response)
		if createErr != nil {
			return store.CreateAlertResult{}, createErr
		}
		return store.CreateAlertResult{StoredAlert: stored, Created: true}, nil
	}
	if !isResponseStatus(err, http.StatusConflict) {
		return store.CreateAlertResult{}, fmt.Errorf("create alert %q: %w", normalized.AlertID, err)
	}
	existing, readErr := r.getAlertFromTarget(
		ctx, route.WriteTarget, normalized.BKTenantID, normalized.AlertID, documentID,
	)
	if readErr != nil {
		return store.CreateAlertResult{}, fmt.Errorf("read duplicate alert %q: %w", normalized.AlertID, readErr)
	}
	if !reflect.DeepEqual(existing.Alert, normalized) {
		return store.CreateAlertResult{}, fmt.Errorf(
			"%w: alert %q already contains different content", store.ErrIdentityConflict, normalized.AlertID,
		)
	}
	return store.CreateAlertResult{StoredAlert: existing, Created: false}, nil
}

// GetAlert 读取当前租户中的逻辑 Alert。
func (r *Repository) GetAlert(ctx context.Context, bkTenantID, alertID string) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	if err := validateIdentity(bkTenantID, "alert_id", alertID); err != nil {
		return store.StoredAlert{}, err
	}
	results, err := r.loadAlerts(ctx, bkTenantID, []string{alertID})
	if err != nil {
		return store.StoredAlert{}, err
	}
	stored, exists := results[alertID]
	if !exists {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrNotFound, alertID)
	}
	return stored, nil
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
	results, err := r.loadAlerts(ctx, bkTenantID, ids)
	if err != nil {
		return store.AlertBatch{}, err
	}
	batch := store.AlertBatch{Alerts: make([]store.StoredAlert, 0, len(results)), NotFound: make([]string, 0)}
	for _, alertID := range ids {
		if stored, ok := results[alertID]; ok {
			batch.Alerts = append(batch.Alerts, stored)
		} else {
			batch.NotFound = append(batch.NotFound, alertID)
		}
	}
	return batch, nil
}

// FindActiveAlert 按租户、来源和 fingerprint 查询唯一活动 Alert。
func (r *Repository) FindActiveAlert(
	ctx context.Context,
	key store.ActiveAlertKey,
) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	for name, value := range map[string]string{
		"bk_tenant_id": key.BKTenantID, "event_source_id": key.EventSourceID, "fingerprint": key.Fingerprint,
	} {
		if value == "" || len(value) > maxIdentityBytes {
			return store.StoredAlert{}, fmt.Errorf("%w: %s is invalid", store.ErrInvalidArgument, name)
		}
	}
	targets, err := r.router.TerminalAlertTargets(ctx)
	if err != nil {
		return store.StoredAlert{}, fmt.Errorf("route active alert search: %w", err)
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.StoredAlert{}, err
	}
	body, err := marshalRequest(map[string]any{
		"size":                2,
		"track_total_hits":    false,
		"seq_no_primary_term": true,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"bk_tenant_id": key.BKTenantID}},
			map[string]any{"term": map[string]any{"event_source_id": key.EventSourceID}},
			map[string]any{"term": map[string]any{"fingerprint": key.Fingerprint}},
			map[string]any{"term": map[string]any{"status": domain.AlertStatusActive}},
		}}},
	})
	if err != nil {
		return store.StoredAlert{}, err
	}
	var response searchResponse
	query := url.Values{"ignore_unavailable": []string{"true"}}
	if err := r.performJSON(
		ctx, http.MethodPost, "/"+joinTargets(targets)+"/_search", query, body, &response,
	); err != nil {
		return store.StoredAlert{}, fmt.Errorf("find active alert: %w", err)
	}
	switch len(response.Hits.Hits) {
	case 0:
		return store.StoredAlert{}, fmt.Errorf("%w: active alert", store.ErrNotFound)
	case 1:
		stored, decodeErr := decodeAlertHit(response.Hits.Hits[0])
		if decodeErr != nil {
			return store.StoredAlert{}, decodeErr
		}
		return stored, nil
	default:
		return store.StoredAlert{}, fmt.Errorf(
			"%w: multiple active alerts for source %q and fingerprint %q",
			store.ErrIdentityConflict,
			key.EventSourceID,
			key.Fingerprint,
		)
	}
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
	targets, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return store.StoredAlert{}, fmt.Errorf("route terminal alert search: %w", err)
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.StoredAlert{}, err
	}
	body, err := marshalRequest(map[string]any{
		"size":                2,
		"track_total_hits":    false,
		"seq_no_primary_term": true,
		"query": map[string]any{"bool": map[string]any{"filter": []any{
			map[string]any{"term": map[string]any{"bk_tenant_id": bkTenantID}},
			map[string]any{"term": map[string]any{"latest_event_id": eventID}},
			map[string]any{"terms": map[string]any{"end_type": []domain.AlertEndType{
				domain.AlertEndTypeSource, domain.AlertEndTypeSeverityUpgrade,
			}}},
		}}},
	})
	if err != nil {
		return store.StoredAlert{}, err
	}
	var response searchResponse
	query := url.Values{"ignore_unavailable": []string{"true"}}
	if err := r.performJSON(
		ctx, http.MethodPost, "/"+joinTargets(targets)+"/_search", query, body, &response,
	); err != nil {
		return store.StoredAlert{}, fmt.Errorf("find terminal alert for event %q: %w", eventID, err)
	}
	switch len(response.Hits.Hits) {
	case 0:
		return store.StoredAlert{}, fmt.Errorf("%w: terminal alert for event %q", store.ErrNotFound, eventID)
	case 1:
		return decodeAlertHit(response.Hits.Hits[0])
	default:
		first, decodeErr := decodeAlertHit(response.Hits.Hits[0])
		if decodeErr != nil {
			return store.StoredAlert{}, decodeErr
		}
		for _, hit := range response.Hits.Hits[1:] {
			candidate, candidateErr := decodeAlertHit(hit)
			if candidateErr != nil {
				return store.StoredAlert{}, candidateErr
			}
			if candidate.Alert.AlertID != first.Alert.AlertID {
				return store.StoredAlert{}, fmt.Errorf(
					"%w: multiple terminal alerts for event %q", store.ErrIdentityConflict, eventID,
				)
			}
		}
		return collapseAlertHits(response.Hits.Hits, bkTenantID, first.Alert.AlertID)
	}
}

// CompareAndSetAlert 使用 _seq_no/_primary_term 原地替换 Alert。
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
	version, ok := decodeVersion(expected)
	if !ok {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrVersionConflict, alertID)
	}
	current, err := r.GetAlert(ctx, bkTenantID, alertID)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if current.Version != expected {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrVersionConflict, alertID)
	}
	normalized, err := replacement.Normalize()
	if err != nil {
		return store.StoredAlert{}, fmt.Errorf("%w: normalize replacement alert: %w", store.ErrInvalidArgument, err)
	}
	if err := domain.ValidateAlertReplacement(current.Alert, normalized); err != nil {
		return store.StoredAlert{}, fmt.Errorf("%w: alert %q: %w", store.ErrInvalidTransition, alertID, err)
	}
	if err := validateAlertIdentity(normalized); err != nil {
		return store.StoredAlert{}, err
	}
	body, err := encodeAlertDocument(normalized)
	if err != nil {
		return store.StoredAlert{}, err
	}
	if len(body) > r.config.MaxDocumentBytes {
		return store.StoredAlert{}, fmt.Errorf(
			"%w: alert document exceeds %d bytes", store.ErrInvalidArgument, r.config.MaxDocumentBytes,
		)
	}
	query := url.Values{
		"if_seq_no":       []string{strconv.FormatInt(version.SeqNo, 10)},
		"if_primary_term": []string{strconv.FormatInt(version.PrimaryTerm, 10)},
		"refresh":         []string{"wait_for"},
	}
	var response indexResponse
	err = r.performJSON(
		ctx, http.MethodPut, "/"+version.Index+"/_doc/"+version.DocumentID, query, body, &response,
	)
	if err != nil {
		if isResponseStatus(err, http.StatusConflict) {
			return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrVersionConflict, alertID)
		}
		if isResponseStatus(err, http.StatusNotFound) {
			return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrNotFound, alertID)
		}
		return store.StoredAlert{}, fmt.Errorf("update alert %q: %w", alertID, err)
	}
	return storedAlertFromIndexResponse(normalized, version.DocumentID, response)
}

func (r *Repository) loadAlerts(
	ctx context.Context,
	bkTenantID string,
	alertIDs []string,
) (map[string]store.StoredAlert, error) {
	headers := make([]map[string]any, len(alertIDs))
	bodies := make([]map[string]any, len(alertIDs))
	for index, alertID := range alertIDs {
		route, err := r.router.AlertRoute(ctx, alertID)
		if err != nil {
			return nil, fmt.Errorf("route alert %q: %w", alertID, err)
		}
		route, err = normalizeRoute(route, r.config.MaxReadTargets)
		if err != nil {
			return nil, err
		}
		headers[index] = map[string]any{"index": route.ReadTargets, "ignore_unavailable": true}
		bodies[index] = map[string]any{
			"size": 3, "track_total_hits": false, "seq_no_primary_term": true,
			"query": map[string]any{"bool": map[string]any{"filter": []any{
				map[string]any{"term": map[string]any{"bk_tenant_id": bkTenantID}},
				map[string]any{"term": map[string]any{"alert_id": alertID}},
			}}},
		}
	}
	responses, err := r.multiSearch(ctx, headers, bodies)
	if err != nil {
		return nil, fmt.Errorf("batch search alerts: %w", err)
	}
	results := make(map[string]store.StoredAlert, len(alertIDs))
	for index, hits := range responses {
		alertID := alertIDs[index] //nolint:gosec // response count is validated by multiSearch.
		switch len(hits) {
		case 0:
			continue
		default:
			stored, decodeErr := collapseAlertHits(hits, bkTenantID, alertID)
			if decodeErr != nil {
				return nil, decodeErr
			}
			results[alertID] = stored
		}
	}
	return results, nil
}

func (r *Repository) getAlertFromTarget(
	ctx context.Context,
	target, bkTenantID, alertID, elasticsearchID string,
) (store.StoredAlert, error) {
	var response getResponse
	err := r.performJSON(
		ctx,
		http.MethodGet,
		"/"+target+"/_doc/"+elasticsearchID,
		url.Values{"realtime": []string{"true"}},
		nil,
		&response,
	)
	if err != nil {
		if isResponseStatus(err, http.StatusNotFound) {
			return store.StoredAlert{}, fmt.Errorf("%w: alert %q", store.ErrNotFound, alertID)
		}
		return store.StoredAlert{}, err
	}
	stored, err := decodeAlertHit(response.hit())
	if err != nil {
		return store.StoredAlert{}, err
	}
	if stored.Alert.BKTenantID != bkTenantID || stored.Alert.AlertID != alertID {
		return store.StoredAlert{}, fmt.Errorf("elasticsearch alert GET returned an unexpected identity")
	}
	return stored, nil
}

func collapseAlertHits(hits []searchHit, bkTenantID, alertID string) (store.StoredAlert, error) {
	var selected store.StoredAlert
	for _, hit := range hits {
		stored, err := decodeAlertHit(hit)
		if err != nil {
			return store.StoredAlert{}, err
		}
		if stored.Alert.BKTenantID != bkTenantID || stored.Alert.AlertID != alertID {
			return store.StoredAlert{}, fmt.Errorf("elasticsearch alert search returned an unexpected identity")
		}
		if selected.Alert.AlertID == "" {
			selected = stored
			continue
		}
		if !reflect.DeepEqual(selected.Alert, stored.Alert) {
			return store.StoredAlert{}, fmt.Errorf(
				"%w: alert %q exists with different content in multiple elasticsearch indices",
				store.ErrIdentityConflict, alertID,
			)
		}
	}
	return selected, nil
}

func storedAlertFromIndexResponse(
	alert domain.Alert,
	documentID string,
	response indexResponse,
) (store.StoredAlert, error) {
	if response.ID != documentID || response.Index == "" {
		return store.StoredAlert{}, fmt.Errorf("elasticsearch alert write returned an unexpected identity")
	}
	version, err := encodeVersion(versionPayload{
		Index: response.Index, DocumentID: response.ID, SeqNo: response.SeqNo, PrimaryTerm: response.PrimaryTerm,
	})
	if err != nil {
		return store.StoredAlert{}, err
	}
	return store.StoredAlert{Alert: alert.Clone(), Version: version}, nil
}

func validateAlertIdentity(alert domain.Alert) error {
	for name, value := range map[string]string{
		"bk_tenant_id": alert.BKTenantID, "alert_id": alert.AlertID, "event_source_id": alert.EventSourceID,
		"fingerprint": alert.Fingerprint, "severity": alert.Severity,
	} {
		if value == "" || len(value) > maxIdentityBytes {
			return fmt.Errorf("%w: %s is invalid", store.ErrInvalidArgument, name)
		}
	}
	return nil
}

var _ store.AlertStore = (*Repository)(nil)
