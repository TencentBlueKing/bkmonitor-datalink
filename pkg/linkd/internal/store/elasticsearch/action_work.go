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

	"linkd/internal/store"
)

// ListActionWork 按租户和 Alert 身份有界扫描动作意图；执行方须在身份租约内重新读取。
func (r *Repository) ListActionWork(ctx context.Context, after store.ActionWorkCursor, limit int) (store.ActionWorkPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ActionWorkPage{}, err
	}
	if err := after.Validate(limit); err != nil {
		return store.ActionWorkPage{}, err
	}
	targets, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return store.ActionWorkPage{}, err
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.ActionWorkPage{}, err
	}
	filters := []any{map[string]any{"term": map[string]any{"action_work": true}}}
	if after.TenantID != "" {
		filters = append(filters, map[string]any{"bool": map[string]any{"minimum_should_match": 1, "should": []any{
			map[string]any{"range": map[string]any{"bk_tenant_id": map[string]any{"gt": after.TenantID}}},
			map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"bk_tenant_id": after.TenantID}}, map[string]any{"range": map[string]any{"alert_id": map[string]any{"gt": after.AlertID}}}}}},
		}}})
	}
	body, err := marshalRequest(map[string]any{"size": limit, "track_total_hits": false, "seq_no_primary_term": true, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []any{map[string]any{"bk_tenant_id": "asc"}, map[string]any{"alert_id": "asc"}}})
	if err != nil {
		return store.ActionWorkPage{}, err
	}
	var response searchResponse
	if err := r.performJSON(ctx, http.MethodPost, "/"+joinTargets(targets)+"/_search", url.Values{"ignore_unavailable": {"true"}}, body, &response); err != nil {
		return store.ActionWorkPage{}, err
	}
	if response.Hits.Hits == nil {
		return store.ActionWorkPage{}, fmt.Errorf("action work search omitted hits")
	}
	rows := make([]store.StoredAlert, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		row, err := decodeAlertHit(hit)
		if err != nil {
			return store.ActionWorkPage{}, err
		}
		rows = append(rows, row)
	}
	return store.ActionWorkPageFromAlerts(rows, after, limit)
}
