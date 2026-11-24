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

// ListActiveAlerts 只枚举指定租户的活动快照；Next 按 AlertID 前进，查询错误不返回可用的部分页。
func (r *Repository) ListActiveAlerts(ctx context.Context, tenant, after string, limit int) (store.ActiveAlertPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ActiveAlertPage{}, err
	}
	if err := store.ValidateActiveAlertPage(tenant, after, limit); err != nil {
		return store.ActiveAlertPage{}, err
	}
	targets, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return store.ActiveAlertPage{}, err
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.ActiveAlertPage{}, err
	}
	filters := []any{map[string]any{"term": map[string]any{"bk_tenant_id": tenant}}, map[string]any{"term": map[string]any{"status": domain.AlertStatusActive}}}
	if after != "" {
		filters = append(filters, map[string]any{"range": map[string]any{"alert_id": map[string]any{"gt": after}}})
	}
	body, err := marshalRequest(map[string]any{"size": limit + 1, "track_total_hits": false, "seq_no_primary_term": true, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []any{map[string]any{"alert_id": "asc"}}})
	if err != nil {
		return store.ActiveAlertPage{}, err
	}
	var response searchResponse
	if err := r.performJSON(ctx, http.MethodPost, "/"+joinTargets(targets)+"/_search", url.Values{"ignore_unavailable": {"true"}}, body, &response); err != nil {
		return store.ActiveAlertPage{}, err
	}
	page := store.ActiveAlertPage{Alerts: []store.StoredAlert{}}
	last := after
	for _, hit := range response.Hits.Hits {
		item, err := decodeAlertHit(hit)
		if err != nil {
			return page, err
		}
		if item.Alert.BKTenantID != tenant || item.Alert.AlertID <= last || item.Alert.Status != domain.AlertStatusActive {
			return page, fmt.Errorf("active candidate identity/order mismatch")
		}
		if len(page.Alerts) == limit {
			page.Next = last
			break
		}
		last = item.Alert.AlertID
		page.Alerts = append(page.Alerts, item)
	}
	return page, nil
}
