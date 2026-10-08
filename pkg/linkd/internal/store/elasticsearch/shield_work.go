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
	"fmt"
	"net/http"
	"net/url"
	"time"

	"linkd/internal/store"
)

func (r *Repository) ListShieldWork(ctx context.Context, after store.ShieldWorkCursor, at time.Time, limit int) (store.ShieldWorkPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ShieldWorkPage{}, err
	}
	if err := after.Validate(limit, at); err != nil {
		return store.ShieldWorkPage{}, err
	}
	targets, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return store.ShieldWorkPage{}, err
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.ShieldWorkPage{}, err
	}
	filters := []any{map[string]any{"bool": map[string]any{"minimum_should_match": 1, "should": []any{map[string]any{"term": map[string]any{"shield.active": true}}, map[string]any{"exists": map[string]any{"field": "policy_change.operation_id"}}}}}}
	if after.TenantID != "" {
		filters = append(filters, map[string]any{"bool": map[string]any{"minimum_should_match": 1, "should": []any{map[string]any{"range": map[string]any{"bk_tenant_id": map[string]any{"gt": after.TenantID}}}, map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"bk_tenant_id": after.TenantID}}, map[string]any{"range": map[string]any{"alert_id": map[string]any{"gt": after.AlertID}}}}}}}}})
	}
	body, err := marshalRequest(map[string]any{"size": limit + 1, "track_total_hits": false, "seq_no_primary_term": true, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []any{map[string]any{"bk_tenant_id": "asc"}, map[string]any{"alert_id": "asc"}}})
	if err != nil {
		return store.ShieldWorkPage{}, err
	}
	var response searchResponse
	if err := r.performJSON(ctx, http.MethodPost, "/"+joinTargets(targets)+"/_search", url.Values{"ignore_unavailable": {"true"}}, body, &response); err != nil {
		return store.ShieldWorkPage{}, err
	}
	page := store.ShieldWorkPage{Alerts: []store.StoredAlert{}}
	last := after
	for i, hit := range response.Hits.Hits {
		saved, err := decodeAlertHit(hit)
		if err != nil {
			return page, err
		}
		key := store.ShieldWorkCursor{TenantID: saved.Alert.BKTenantID, AlertID: saved.Alert.AlertID}
		if key.Compare(last) <= 0 || !store.HasShieldWork(saved.Alert) {
			return page, fmt.Errorf("shield work identity/index mismatch")
		}
		if i == limit {
			page.Next = last
			break
		}
		last = key
		if store.ShieldWorkDue(saved.Alert, at) {
			page.Alerts = append(page.Alerts, saved)
		}
	}
	return page, nil
}
