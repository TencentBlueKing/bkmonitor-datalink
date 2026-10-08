// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"linkd/internal/domain"
	"linkd/internal/projection"
)

// CountWork 在调用方持有租户准入租约时有界统计未完成任务。写入和待办标记同一 CAS，
// ES Put 等待搜索可见后才确认成功；搜索超时/分片失败必须拒绝准入，不能视作空队列。
func (s *Store) CountWork(ctx context.Context, tenant string, limit int) (int, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || limit < 1 || limit > projection.MaxPendingPerTenant {
		return 0, projection.ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if s.db != nil {
		var n int
		err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT id FROM linkd_projection_tasks WHERE namespace=? AND bk_tenant_id=? AND work=1 LIMIT ?) AS pending`, s.namespace, tenant, limit).Scan(&n)
		return n, err
	}
	body, _ := json.Marshal(map[string]any{"size": 0, "track_total_hits": limit, "query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"bk_tenant_id": tenant}}, map[string]any{"term": map[string]any{"work": true}}}}}})
	code, raw, err := s.request(ctx, http.MethodPost, "/"+s.index+"/_search", body)
	if err != nil {
		return 0, err
	}
	if code != 200 {
		return 0, fmt.Errorf("count projection work: HTTP %d", code)
	}
	var result struct {
		TimedOut bool `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Total *struct {
				Value    int    `json:"value"`
				Relation string `json:"relation"`
			} `json:"total"`
		} `json:"hits"`
	}
	if json.Unmarshal(raw, &result) != nil || result.TimedOut || result.Shards.Failed != 0 || result.Hits.Total == nil || result.Hits.Total.Value < 0 || (result.Hits.Total.Relation != "eq" && result.Hits.Total.Relation != "gte") {
		return 0, projection.ErrInvalid
	}
	if result.Hits.Total.Relation == "gte" {
		return limit, nil
	}
	return min(limit, result.Hits.Total.Value), nil
}
