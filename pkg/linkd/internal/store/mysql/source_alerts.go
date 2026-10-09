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
	"fmt"

	"linkd/internal/store"
)

// ListSourceAlerts 从 Linkd 自有表读取来源事实，不读取 KAC 文档或执行写入。
func (r *Repository) ListSourceAlerts(ctx context.Context, q store.SourceAlertQuery) (store.SourceAlertPage, error) {
	if err := contextError(ctx); err != nil {
		return store.SourceAlertPage{}, err
	}
	if err := q.Validate(); err != nil {
		return store.SourceAlertPage{}, err
	}
	query := `SELECT alert_id,payload,version FROM linkd_alerts WHERE bk_tenant_id=? AND event_source_id=? AND alert_id>?`
	args := []any{q.TenantID, q.EventSourceID, q.After}
	if q.Status != "" {
		query += ` AND status=?`
		args = append(args, q.Status)
	}
	query += ` ORDER BY alert_id LIMIT ?`
	args = append(args, q.Limit+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return store.SourceAlertPage{}, err
	}
	defer func() { _ = rows.Close() }()
	page := store.SourceAlertPage{Alerts: []store.StoredAlert{}}
	last := q.After
	for rows.Next() {
		var id string
		var raw []byte
		var version uint64
		if err := rows.Scan(&id, &raw, &version); err != nil {
			return store.SourceAlertPage{}, err
		}
		alert, err := decodeAlert(raw)
		if err != nil {
			return store.SourceAlertPage{}, err
		}
		if id <= last || alert.AlertID != id || !q.Matches(alert) {
			return store.SourceAlertPage{}, fmt.Errorf("source alert scope/order mismatch")
		}
		if len(page.Alerts) == q.Limit {
			page.Next = last
			break
		}
		last = id
		page.Alerts = append(page.Alerts, store.StoredAlert{Alert: alert, Version: versionToken(version)})
	}
	if err := rows.Err(); err != nil {
		return store.SourceAlertPage{}, err
	}
	return page, nil
}
