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

	"linkd/internal/policy"
)

// CountShieldCheckRequests 仅用于持有租户准入锁的容量校验，最多观察 limit 条，不扫描历史已完成请求。
// 新 pending 写入等待 ES 搜索可见；完成标记可延迟可见，只会暂时保守拒绝新请求。
func (s *Store) CountShieldCheckRequests(ctx context.Context, prefix string, limit int) (int, error) {
	return s.countControlRequests(ctx, "shield_requests", prefix, limit)
}

// CountSuppressionCheckRequests 在租户准入锁内有界读取未完成抑制复核数。
func (s *Store) CountSuppressionCheckRequests(ctx context.Context, prefix string, limit int) (int, error) {
	return s.countControlRequests(ctx, "suppression_requests", prefix, limit)
}

func (s *Store) countControlRequests(ctx context.Context, kind, prefix string, limit int) (int, error) {
	if !hasRequestWork(kind) || prefix == "" || len(prefix) > 256 || limit < 1 || limit > 1024 {
		return 0, policy.ErrInvalid
	}
	if s.db != nil {
		var n int
		// kind 限制为固定请求集合白名单，业务输入使用参数。
		err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM (SELECT id FROM "+s.table(kind)+" WHERE namespace=? AND work=1 AND id>=? AND id<? LIMIT ?) AS pending", s.namespace, prefix, prefix+string([]byte{255}), limit).Scan(&n)
		return n, err
	}
	query := map[string]any{"size": 0, "track_total_hits": limit, "query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"work": true}}, map[string]any{"prefix": map[string]any{"id": prefix}}}}}}
	raw, _ := json.Marshal(query)
	code, raw, err := s.request(ctx, http.MethodPost, "/"+s.table(kind)+"/_search", raw)
	if err != nil {
		return 0, err
	}
	if code != 200 {
		return 0, fmt.Errorf("count control requests: HTTP %d", code)
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
	if json.Unmarshal(raw, &result) != nil || result.TimedOut || result.Shards.Failed > 0 || result.Hits.Total == nil || result.Hits.Total.Value < 0 || (result.Hits.Total.Relation != "eq" && result.Hits.Total.Relation != "gte") {
		return 0, fmt.Errorf("incomplete control request count")
	}
	if result.Hits.Total.Relation == "gte" {
		return limit, nil
	}
	return min(limit, result.Hits.Total.Value), nil
}

// ListShieldCheckRequests 只供控制面枚举 pending；精确执行前仍须重新读取请求状态和 Alert 版本。
func (s *Store) ListShieldCheckRequests(ctx context.Context, prefix, after string, limit int) ([]json.RawMessage, error) {
	return s.listControlRequests(ctx, "shield_requests", prefix, after, limit)
}

// ListSuppressionCheckRequests 只枚举 pending 抑制复核，精确执行前重新读取。
func (s *Store) ListSuppressionCheckRequests(ctx context.Context, prefix, after string, limit int) ([]json.RawMessage, error) {
	return s.listControlRequests(ctx, "suppression_requests", prefix, after, limit)
}

func (s *Store) listControlRequests(ctx context.Context, kind, prefix, after string, limit int) ([]json.RawMessage, error) {
	if !hasRequestWork(kind) || len(prefix) > 256 || len(after) > 256 || limit < 1 || limit > 16 {
		return nil, policy.ErrInvalid
	}
	if s.db != nil {
		//nolint:gosec // G202: kind 限制为固定请求集合白名单，业务输入使用参数。
		rows, err := s.db.QueryContext(ctx, "SELECT payload FROM "+s.table(kind)+" WHERE namespace=? AND work=1 AND id>? AND id>=? AND id<? ORDER BY id LIMIT ?", s.namespace, after, prefix, prefix+string([]byte{255}), limit)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		out := []json.RawMessage{}
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return nil, err
			}
			if len(raw) > controlRecordLimit(kind) {
				return nil, fmt.Errorf("oversized control request")
			}
			out = append(out, raw)
		}
		return out, rows.Err()
	}
	query := map[string]any{"size": limit, "sort": []string{"id"}, "query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"work": true}}, map[string]any{"prefix": map[string]any{"id": prefix}}, map[string]any{"range": map[string]any{"id": map[string]any{"gt": after}}}}}}}
	raw, _ := json.Marshal(query)
	code, raw, err := s.request(ctx, http.MethodPost, "/"+s.table(kind)+"/_search", raw)
	if err != nil {
		return nil, err
	}
	if code != 200 {
		return nil, fmt.Errorf("list control requests: HTTP %d", code)
	}
	var result struct {
		TimedOut bool `json:"timed_out"`
		Shards   struct {
			Failed int `json:"failed"`
		} `json:"_shards"`
		Hits struct {
			Hits []struct {
				Source struct {
					Payload json.RawMessage `json:"payload"`
				} `json:"_source"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if json.Unmarshal(raw, &result) != nil || result.TimedOut || result.Shards.Failed > 0 || result.Hits.Hits == nil || len(result.Hits.Hits) > limit {
		return nil, fmt.Errorf("incomplete control request page")
	}
	out := []json.RawMessage{}
	for _, hit := range result.Hits.Hits {
		if len(hit.Source.Payload) > controlRecordLimit(kind) {
			return nil, fmt.Errorf("oversized control request")
		}
		out = append(out, hit.Source.Payload)
	}
	return out, nil
}

// CountMergeRequests 对租户前缀计数，达到限制即停止，不扫描完整历史。
func (s *Store) CountMergeRequests(ctx context.Context, prefix string, limit int) (int, error) {
	return s.countControlRequests(ctx, "merge_requests", prefix, limit)
}

// ListMergeRequests 只供控制面有界扫描 pending；正式执行前仍需精确读取。
func (s *Store) ListMergeRequests(ctx context.Context, prefix, after string, limit int) ([]json.RawMessage, error) {
	return s.listControlRequests(ctx, "merge_requests", prefix, after, limit)
}
