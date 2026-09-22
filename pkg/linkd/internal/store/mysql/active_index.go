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

// ReadActiveIndex 用单条一致性 SELECT 读取窄投影；超限时不返回部分集合。
func (r *Repository) ReadActiveIndex(ctx context.Context, q activeindex.Query) ([]activeindex.Row, error) {
	if err := q.Validate(); err != nil {
		return nil, err
	}
	label := "JSON_EXTRACT(payload, '$.labels.strategy_id')"
	//nolint:gosec // 仅拼接固定列和有界占位符；来源、租户、策略及 LIMIT 均使用绑定参数。
	query := "SELECT bk_tenant_id,event_source_id,fingerprint," + label + " FROM linkd_alerts WHERE status='active' AND event_source_id IN (" + placeholders(len(q.Sources)) + ")"
	args := make([]any, 0, len(q.Sources)+4)
	for _, source := range q.Sources {
		args = append(args, source)
	}
	if q.Scope != nil {
		query += " AND bk_tenant_id=? AND ((JSON_TYPE(" + label + ")='STRING' AND CAST(JSON_UNQUOTE(" + label + ") AS BINARY)=CAST(? AS BINARY))"
		args = append(args, q.Scope.BKTenantID, q.Scope.StrategyID)
		if n, ok := activeindex.NumericStrategy(q.Scope.StrategyID); ok {
			query += " OR (JSON_TYPE(" + label + ") IN ('INTEGER','DOUBLE','DECIMAL') AND CAST(" + label + " AS DOUBLE)=?)"
			args = append(args, n)
		}
		query += ")"
	}
	query += " LIMIT ?"
	args = append(args, q.MaxRows+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]activeindex.Row, 0)
	bytes := 0
	for rows.Next() {
		var row activeindex.Row
		var labelJSON []byte
		if err := rows.Scan(&row.BKTenantID, &row.EventSourceID, &row.Fingerprint, &labelJSON); err != nil {
			return nil, err
		}
		bytes += len(row.BKTenantID) + len(row.EventSourceID) + len(row.Fingerprint) + len(labelJSON)
		if len(result) >= q.MaxRows || bytes > q.MaxBytes {
			return nil, fmt.Errorf("active index snapshot exceeds limits")
		}
		if len(labelJSON) != 0 && string(labelJSON) != "null" {
			if err := json.Unmarshal(append(append([]byte(`{"strategy_id":`), labelJSON...), '}'), &row.Labels); err != nil {
				return nil, fmt.Errorf("invalid strategy label")
			}
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
