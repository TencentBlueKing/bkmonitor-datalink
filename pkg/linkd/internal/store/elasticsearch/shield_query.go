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
	"slices"

	"linkd/internal/store"
)

// ListShieldAlerts 从 Active 读当前租户的屏蔽/待输出快照；终态意图完成前归档器保留该副本。
func (r *Repository) ListShieldAlerts(ctx context.Context, tenant, after string, limit int) (store.ShieldAlertPage, error) {
	return r.listShieldAlerts(ctx, tenant, "", after, limit)
}

// ListShieldDependents 查询同租户当前固定到主告警的子告警；未来检查时间不阻止事件提示。
func (r *Repository) ListShieldDependents(ctx context.Context, tenant, main, after string, limit int) (store.ShieldAlertPage, error) {
	if err := store.ValidateShieldDependentsQuery(tenant, main, after, limit); err != nil {
		return store.ShieldAlertPage{}, err
	}
	return r.listShieldAlerts(ctx, tenant, main, after, limit)
}

func (r *Repository) listShieldAlerts(ctx context.Context, tenant, main, after string, limit int) (store.ShieldAlertPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ShieldAlertPage{}, err
	}
	if err := store.ValidateShieldQuery(tenant, after, limit); err != nil {
		return store.ShieldAlertPage{}, err
	}
	targets, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return store.ShieldAlertPage{}, err
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.ShieldAlertPage{}, err
	}
	filters := []any{map[string]any{"term": map[string]any{"bk_tenant_id": tenant}}, map[string]any{"bool": map[string]any{"minimum_should_match": 1, "should": []any{map[string]any{"term": map[string]any{"shield.active": true}}, map[string]any{"exists": map[string]any{"field": "policy_change.operation_id"}}}}}}
	if main != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"shield.active": true}}, map[string]any{"term": map[string]any{"shield_main_alert_ids": main}})
	}
	if after != "" {
		filters = append(filters, map[string]any{"range": map[string]any{"alert_id": map[string]any{"gt": after}}})
	}
	body, err := marshalRequest(map[string]any{"size": limit + 1, "track_total_hits": false, "seq_no_primary_term": true, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []any{map[string]any{"alert_id": "asc"}}})
	if err != nil {
		return store.ShieldAlertPage{}, err
	}
	var response searchResponse
	if err := r.performJSON(ctx, http.MethodPost, "/"+joinTargets(targets)+"/_search", url.Values{"ignore_unavailable": {"true"}}, body, &response); err != nil {
		return store.ShieldAlertPage{}, err
	}
	if len(response.Hits.Hits) > limit+1 {
		return store.ShieldAlertPage{}, fmt.Errorf("shield query exceeds page budget")
	}
	page := store.ShieldAlertPage{Alerts: []store.StoredAlert{}}
	last := after
	for i, hit := range response.Hits.Hits {
		stored, err := decodeAlertHit(hit)
		if err != nil {
			return store.ShieldAlertPage{}, err
		}
		if stored.Alert.BKTenantID != tenant || stored.Alert.AlertID <= last || !store.HasShieldWork(stored.Alert) || (main != "" && !slices.Contains(store.ShieldMainAlertIDs(stored.Alert), main)) {
			return store.ShieldAlertPage{}, fmt.Errorf("shield query scope/order mismatch")
		}
		if i == limit {
			page.Next = last
			break
		}
		last = stored.Alert.AlertID
		page.Alerts = append(page.Alerts, stored)
	}
	return page, nil
}
