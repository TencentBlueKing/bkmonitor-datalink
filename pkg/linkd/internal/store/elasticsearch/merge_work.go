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
	"net/http"
	"net/url"

	"linkd/internal/store"
)

func (r *Repository) ListMergeWork(ctx context.Context, after store.MergeWorkCursor, limit int) (store.MergeWorkPage, error) {
	if err := contextError(ctx); err != nil {
		return store.MergeWorkPage{}, err
	}
	if err := after.Validate(limit); err != nil {
		return store.MergeWorkPage{}, err
	}
	targets, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return store.MergeWorkPage{}, err
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return store.MergeWorkPage{}, err
	}
	filters := []any{map[string]any{"term": map[string]any{"merge_work": true}}}
	if after.TenantID != "" {
		filters = append(filters, map[string]any{"bool": map[string]any{"minimum_should_match": 1, "should": []any{
			map[string]any{"range": map[string]any{"bk_tenant_id": map[string]any{"gt": after.TenantID}}},
			map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"bk_tenant_id": after.TenantID}}, map[string]any{"range": map[string]any{"alert_id": map[string]any{"gte": after.AlertID}}}}}},
		}}})
	}
	body, err := marshalRequest(map[string]any{"size": limit + 1, "track_total_hits": false, "seq_no_primary_term": true, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []any{map[string]any{"bk_tenant_id": "asc"}, map[string]any{"alert_id": "asc"}}})
	if err != nil {
		return store.MergeWorkPage{}, err
	}
	var response searchResponse
	if err := r.performJSON(ctx, http.MethodPost, "/"+joinTargets(targets)+"/_search", url.Values{"ignore_unavailable": {"true"}}, body, &response); err != nil {
		return store.MergeWorkPage{}, err
	}
	rows := make([]store.StoredAlert, 0, len(response.Hits.Hits))
	for _, hit := range response.Hits.Hits {
		row, err := decodeAlertHit(hit)
		if err != nil {
			return store.MergeWorkPage{}, err
		}
		rows = append(rows, row)
	}
	return store.MergeWorkPageFromAlerts(rows, after, limit)
}
