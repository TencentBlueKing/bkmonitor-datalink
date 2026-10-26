// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mysqlstore

import (
	"context"
	"encoding/json"
	"fmt"

	"linkd/internal/activeindex"
)

// ScanActiveIndex 用单条一致性 SELECT 流式读取，逐批交付并释放窄投影。
// 不使用 OFFSET 或多条 SELECT，避免翻页期间数据变化造成重复和遗漏。
func (r *Repository) ScanActiveIndex(ctx context.Context, q activeindex.Query, consume func([]activeindex.Row) error) error {
	if err := q.Validate(); err != nil {
		return err
	}
	label := "JSON_EXTRACT(payload, '$.labels.strategy_id')"
	//nolint:gosec // 仅拼接固定列和有界占位符；来源和租户使用绑定参数。
	query := "SELECT bk_tenant_id,event_source_id,fingerprint," + label + ",JSON_EXTRACT(payload, '$.enrich') FROM linkd_alerts WHERE status='active' AND event_source_id IN (" + placeholders(len(q.Sources)) + ")"
	args := make([]any, 0, len(q.Sources)+4)
	for _, source := range q.Sources {
		args = append(args, source)
	}
	if tenant := q.TenantID(); tenant != "" {
		query += " AND bk_tenant_id=?"
		args = append(args, tenant)
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	page := make([]activeindex.Row, 0, q.PageSize)
	bytes := 0
	for rows.Next() {
		var row activeindex.Row
		var labelJSON, enrichJSON []byte
		if err := rows.Scan(&row.BKTenantID, &row.EventSourceID, &row.Fingerprint, &labelJSON, &enrichJSON); err != nil {
			return err
		}
		if len(labelJSON) != 0 && string(labelJSON) != "null" {
			if err := json.Unmarshal(append(append([]byte(`{"strategy_id":`), labelJSON...), '}'), &row.Labels); err != nil {
				return fmt.Errorf("invalid strategy label")
			}
		}
		if len(enrichJSON) > 0 && string(enrichJSON) != "null" {
			if err := json.Unmarshal(enrichJSON, &row.Enrich); err != nil {
				return fmt.Errorf("invalid enrichment payload")
			}
		}
		row, err = row.Projection()
		if err != nil {
			return err
		}
		projection, err := json.Marshal(row)
		if err != nil {
			return err
		}
		if len(projection) > q.MaxBytes {
			return fmt.Errorf("active index row exceeds page byte limit")
		}
		if len(page) > 0 && (len(page) >= q.PageSize || bytes+len(projection) > q.MaxBytes) {
			if err := consume(page); err != nil {
				return err
			}
			page = make([]activeindex.Row, 0, q.PageSize)
			bytes = 0
		}
		page = append(page, row)
		bytes += len(projection)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(page) > 0 {
		return consume(page)
	}
	return nil
}
