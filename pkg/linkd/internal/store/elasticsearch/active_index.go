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
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"linkd/internal/store"
)

// ScanActiveIndex 在同一 PIT 中逐页交付窄投影；缺索引和部分结果不能解释为空集合。
func (r *Repository) ScanActiveIndex(ctx context.Context, q store.ActiveIndexQuery, consume func([]store.ActiveIndexRow) error) error {
	if err := q.Validate(); err != nil {
		return err
	}
	targets, err := r.router.ActiveAlertTargets(ctx)
	if err != nil {
		return err
	}
	targets, err = normalizeTargets(targets, r.config.MaxReadTargets)
	if err != nil {
		return err
	}
	var opened pointInTimeResponse
	// ES 7.17 的 PIT 创建不接受 allow_partial_search_results；拒绝缺失目标，
	// 并由 performJSON 检查 PIT 创建和每一页搜索返回的 failed shards/timed_out。
	err = r.performJSON(ctx, http.MethodPost, "/"+joinTargets(targets)+"/_pit", url.Values{"keep_alive": {formatKeepAlive(r.config.PITKeepAlive)}, "ignore_unavailable": {"false"}}, nil, &opened)
	pit := opened.ID
	defer func() {
		if pit != "" {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
			defer cancel()
			_ = r.closePIT(cleanup, pit)
		}
	}()
	if err != nil {
		return err
	}
	if pit == "" {
		return fmt.Errorf("empty active index PIT")
	}
	filters := []any{map[string]any{"term": map[string]any{"status": "active"}}, map[string]any{"terms": map[string]any{"event_source_id": q.Sources}}}

	if q.BKTenantID != "" {
		filters = append(filters, map[string]any{"term": map[string]any{"bk_tenant_id": q.BKTenantID}})
	}
	if len(q.Fingerprints) > 0 {
		filters = append(filters, map[string]any{"terms": map[string]any{"fingerprint": q.Fingerprints}})
	}

	fields := []string{"bk_tenant_id", "event_source_id", "alert_id", "fingerprint"}
	fields = append(fields, q.Fields...)
	body := map[string]any{"size": q.PageSize, "track_total_hits": false, "_source": fields, "query": map[string]any{"bool": map[string]any{"filter": filters}}, "sort": []any{map[string]any{"bk_tenant_id": "asc"}, map[string]any{"alert_id": "asc"}, map[string]any{"_index": "asc"}}}
	for {
		response, next, err := r.searchWithPIT(ctx, targets, pit, body)
		if next != "" {
			pit = next
		}
		if err != nil {
			return err
		}
		page := make([]store.ActiveIndexRow, 0, len(response.Hits.Hits))
		bytes := 0
		for _, hit := range response.Hits.Hits {
			var row store.ActiveIndexRow
			if err := json.Unmarshal(hit.Source, &row); err != nil {
				return fmt.Errorf("invalid active index row")
			}

			projection, err := json.Marshal(row)
			if err != nil {
				return err
			}
			if len(projection) > q.MaxBytes {
				return fmt.Errorf("active index row exceeds page byte limit")
			}
			if len(page) > 0 && bytes+len(projection) > q.MaxBytes {
				if err := consume(page); err != nil {
					return err
				}
				page = make([]store.ActiveIndexRow, 0, len(response.Hits.Hits))
				bytes = 0
			}
			page = append(page, row)
			bytes += len(projection)
		}
		if len(page) > 0 {
			if err := consume(page); err != nil {
				return err
			}
		}
		if len(response.Hits.Hits) < q.PageSize {
			return nil
		}
		last := response.Hits.Hits[len(response.Hits.Hits)-1]
		if len(last.Sort) != 3 {
			return fmt.Errorf("invalid active index cursor")
		}
		body["search_after"] = last.Sort
	}
}
