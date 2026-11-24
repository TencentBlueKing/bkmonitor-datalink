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

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ListSourceAlerts 按租户与来源读取原始 Alert；跨 Active/History 归档窗口按 AlertID 折叠副本。
func (r *Repository) ListSourceAlerts(ctx context.Context, q store.SourceAlertQuery) (store.SourceAlertPage, error) {
	if err := contextError(ctx); err != nil {
		return store.SourceAlertPage{}, err
	}
	if err := q.Validate(); err != nil {
		return store.SourceAlertPage{}, err
	}
	targets, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return store.SourceAlertPage{}, err
	}
	if q.Status != domain.AlertStatusActive {
		history, err := r.router.TerminalAlertTargets(ctx)
		if err != nil {
			return store.SourceAlertPage{}, err
		}
		targets = append(targets, history...)
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.SourceAlertPage{}, err
	}
	filters := []any{map[string]any{"term": map[string]any{"bk_tenant_id": q.TenantID}}, map[string]any{"term": map[string]any{"event_source_id": q.EventSourceID}}}
	if q.Status != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"status": q.Status}})
	}
	if q.After != "" {
		filters = append(filters, map[string]any{"range": map[string]any{"alert_id": map[string]any{"gt": q.After}}})
	}
	body, err := marshalRequest(map[string]any{"size": q.Limit + 1, "track_total_hits": false, "seq_no_primary_term": true, "collapse": map[string]any{"field": "alert_id"}, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []any{map[string]any{"alert_id": "asc"}}})
	if err != nil {
		return store.SourceAlertPage{}, err
	}
	var response searchResponse
	if err := r.performJSON(ctx, http.MethodPost, "/"+joinTargets(targets)+"/_search", url.Values{"ignore_unavailable": {"true"}}, body, &response); err != nil {
		return store.SourceAlertPage{}, err
	}
	if err := response.checkComplete(); err != nil {
		return store.SourceAlertPage{}, err
	}
	if len(response.Hits.Hits) > q.Limit+1 {
		return store.SourceAlertPage{}, fmt.Errorf("source alert page exceeds budget")
	}
	page := store.SourceAlertPage{Alerts: []store.StoredAlert{}}
	last := q.After
	for _, hit := range response.Hits.Hits {
		item, err := decodeAlertHit(hit)
		if err != nil {
			return store.SourceAlertPage{}, err
		}
		if !q.Matches(item.Alert) || item.Alert.AlertID <= last {
			return store.SourceAlertPage{}, fmt.Errorf("source alert scope/order mismatch")
		}
		if len(page.Alerts) == q.Limit {
			page.Next = last
			break
		}
		last = item.Alert.AlertID
		page.Alerts = append(page.Alerts, item)
	}
	return page, nil
}
