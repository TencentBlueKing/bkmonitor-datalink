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
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"linkd/internal/actiondelivery"
	"linkd/internal/domain"
)

// OldestUnsettled 只读同租户/Alert/目标最早未终结动作，包含失败屏障，不扫描该目标完整历史。
// ES 搜索可有刷新延迟，调用方必须在同目标租约内再次精确读取并校验，不能直接执行搜索载荷。
func (s *Store) OldestUnsettled(ctx context.Context, tenant, alert, target string) (actiondelivery.StoredTask, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || alert == "" || len(alert) > domain.EntityIDMaxBytes || domain.ValidateIdentityPart("target", target, 64) != nil {
		return actiondelivery.StoredTask{}, actiondelivery.ErrInvalid
	}
	if e := ctx.Err(); e != nil {
		return actiondelivery.StoredTask{}, e
	}
	var row actiondelivery.StoredTask
	if s.db != nil {
		var raw []byte
		var id, alertID, targetID string
		var version uint64
		var work, unsettled bool
		var revision int64
		e := s.db.QueryRowContext(ctx, `SELECT id,payload,version,work,unsettled,alert_id,target_id,revision FROM linkd_action_deliveries WHERE namespace=? AND bk_tenant_id=? AND alert_id=? AND target_id=? AND unsettled=1 ORDER BY revision,id LIMIT 1`, s.namespace, tenant, alert, target).Scan(&id, &raw, &version, &work, &unsettled, &alertID, &targetID, &revision)
		if errors.Is(e, sql.ErrNoRows) {
			return row, actiondelivery.ErrNotFound
		}
		if e != nil {
			return row, e
		}
		row, e = decode(raw, tenant, id, strconv.FormatUint(version, 10), work, unsettled, alertID, targetID, revision)
		if e != nil {
			return row, e
		}
	} else {
		body, _ := json.Marshal(map[string]any{"size": 1, "sort": []any{map[string]string{"revision": "asc"}, map[string]string{"id": "asc"}}, "seq_no_primary_term": true, "track_total_hits": false, "query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"bk_tenant_id": tenant}}, map[string]any{"term": map[string]any{"alert_id": alert}}, map[string]any{"term": map[string]any{"target_id": target}}, map[string]any{"term": map[string]any{"unsettled": true}}}}}})
		code, raw, e := s.request(ctx, http.MethodPost, "/"+s.index+"/_search", body)
		if e != nil {
			return row, e
		}
		if code != 200 {
			return row, fmt.Errorf("read action order: HTTP %d", code)
		}
		var response struct {
			TimedOut bool `json:"timed_out"`
			Shards   struct {
				Failed int `json:"failed"`
			} `json:"_shards"`
			Hits struct {
				Hits []esHit `json:"hits"`
			} `json:"hits"`
		}
		if json.Unmarshal(raw, &response) != nil || response.TimedOut || response.Shards.Failed != 0 || response.Hits.Hits == nil || len(response.Hits.Hits) > 1 {
			return row, actiondelivery.ErrInvalid
		}
		if len(response.Hits.Hits) == 0 {
			return row, actiondelivery.ErrNotFound
		}
		hit := response.Hits.Hits[0]
		row, e = s.decodeHit(hit, tenant, hit.Source.ID)
		if e != nil {
			return row, e
		}
	}
	if row.Task.Request.TenantID != tenant || row.Task.Request.AlertID != alert || row.Task.Request.TargetID != target || !row.Task.Unsettled() {
		return actiondelivery.StoredTask{}, actiondelivery.ErrInvalid
	}
	return row, nil
}

// ConfirmVisible 防止实时读取已成功但排序索引仍缺少旧动作时，生产者继续确认较新版本。
// 不主动全索引 refresh，也不无限等待；尚不可见返回 ErrBusy，由原持久意图继续重试。
func (s *Store) ConfirmVisible(ctx context.Context, t actiondelivery.Task) error {
	if t.Validate() != nil {
		return actiondelivery.ErrInvalid
	}
	var row actiondelivery.StoredTask
	if s.db != nil {
		r, e := s.Get(ctx, t.Request.TenantID, t.ID)
		if e != nil {
			return e
		}
		row = r
	} else {
		body, _ := json.Marshal(map[string]any{"size": 1, "seq_no_primary_term": true, "track_total_hits": false, "query": map[string]any{"bool": map[string]any{"filter": []any{map[string]any{"term": map[string]any{"id": t.ID}}, map[string]any{"term": map[string]any{"bk_tenant_id": t.Request.TenantID}}}}}})
		code, raw, e := s.request(ctx, http.MethodPost, "/"+s.index+"/_search", body)
		if e != nil {
			return e
		}
		if code != 200 {
			return fmt.Errorf("confirm action visibility: HTTP %d", code)
		}
		var response struct {
			TimedOut bool `json:"timed_out"`
			Shards   struct {
				Failed int `json:"failed"`
			} `json:"_shards"`
			Hits struct {
				Hits []esHit `json:"hits"`
			} `json:"hits"`
		}
		if json.Unmarshal(raw, &response) != nil || response.TimedOut || response.Shards.Failed != 0 || response.Hits.Hits == nil || len(response.Hits.Hits) > 1 {
			return actiondelivery.ErrInvalid
		}
		if len(response.Hits.Hits) == 0 {
			return actiondelivery.ErrBusy
		}
		row, e = s.decodeHit(response.Hits.Hits[0], t.Request.TenantID, t.ID)
		if e != nil {
			return e
		}
	}
	if row.Task.Request.Hash() != t.Request.Hash() || row.Task.SourceID != t.SourceID || row.Task.SourceVersion != t.SourceVersion || !row.Task.CreatedAt.Equal(t.CreatedAt) {
		return actiondelivery.ErrConflict
	}
	return nil
}
