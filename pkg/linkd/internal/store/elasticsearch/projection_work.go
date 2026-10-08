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

// ListProjectionWork 同时扫描 Active/History；归档搬迁中最多两个物理副本按同一逻辑 Alert 合并。
// 每页最多 16 个目标；最多读取 2*(limit+1) 个命中，异常副本或部分查询不能当成完整成功。
func (r *Repository) ListProjectionWork(ctx context.Context, after store.ProjectionWorkCursor, limit int) (store.ProjectionWorkPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ProjectionWorkPage{}, err
	}
	if err := after.Validate(limit); err != nil {
		return store.ProjectionWorkPage{}, err
	}
	active, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return store.ProjectionWorkPage{}, err
	}
	history, err := r.router.TerminalAlertTargets(ctx)
	if err != nil {
		return store.ProjectionWorkPage{}, err
	}
	targets, err := normalizeTargets(append(active, history...), r.config.MaxReadTargets)
	if err != nil {
		return store.ProjectionWorkPage{}, err
	}
	filters := []any{map[string]any{"term": map[string]any{"projection_work": true}}}
	if after.TenantID != "" {
		filters = append(filters, map[string]any{"bool": map[string]any{"minimum_should_match": 1, "should": []any{
			map[string]any{"range": map[string]any{"bk_tenant_id": map[string]any{"gt": after.TenantID}}},
			map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"bk_tenant_id": after.TenantID}}, map[string]any{"range": map[string]any{"alert_id": map[string]any{"gte": after.AlertID}}}}}},
		}}})
	}
	body, err := marshalRequest(map[string]any{"size": 2 * (limit + 1), "track_total_hits": false, "seq_no_primary_term": true, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []any{map[string]any{"bk_tenant_id": "asc"}, map[string]any{"alert_id": "asc"}}})
	if err != nil {
		return store.ProjectionWorkPage{}, err
	}
	var response searchResponse
	if err := r.performJSON(ctx, http.MethodPost, "/"+joinTargets(targets)+"/_search", url.Values{"ignore_unavailable": {"true"}}, body, &response); err != nil {
		return store.ProjectionWorkPage{}, err
	}
	if len(response.Hits.Hits) > 2*(limit+1) {
		return store.ProjectionWorkPage{}, fmt.Errorf("projection search exceeded hit budget")
	}
	rows := make([]store.StoredAlert, 0, limit+1)
	for start := 0; start < len(response.Hits.Hits) && len(rows) < limit+1; {
		first, err := decodeAlertHit(response.Hits.Hits[start])
		if err != nil {
			return store.ProjectionWorkPage{}, err
		}
		end := start + 1
		for end < len(response.Hits.Hits) {
			next, err := decodeAlertHit(response.Hits.Hits[end])
			if err != nil {
				return store.ProjectionWorkPage{}, err
			}
			if next.Alert.BKTenantID != first.Alert.BKTenantID || next.Alert.AlertID != first.Alert.AlertID {
				break
			}
			end++
		}
		if end-start > 2 {
			return store.ProjectionWorkPage{}, fmt.Errorf("%w: too many physical alert copies", store.ErrIdentityConflict)
		}
		row, err := collapseAlertHits(response.Hits.Hits[start:end], first.Alert.BKTenantID, first.Alert.AlertID)
		if err != nil {
			return store.ProjectionWorkPage{}, err
		}
		rows = append(rows, row)
		start = end
	}
	return store.ProjectionWorkPageFromAlerts(rows, after, limit)
}
