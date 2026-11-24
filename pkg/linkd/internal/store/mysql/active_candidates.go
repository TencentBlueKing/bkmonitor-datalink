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

	"linkd/internal/domain"
	"linkd/internal/store"
)

// ListActiveAlerts 只枚举指定租户的活动快照；Next 按 AlertID 前进，查询错误不返回可用的部分页。
func (r *Repository) ListActiveAlerts(ctx context.Context, tenant, after string, limit int) (store.ActiveAlertPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ActiveAlertPage{}, err
	}
	if err := store.ValidateActiveAlertPage(tenant, after, limit); err != nil {
		return store.ActiveAlertPage{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT alert_id,payload,version FROM linkd_alerts WHERE bk_tenant_id=? AND status='active' AND alert_id>? ORDER BY alert_id LIMIT ?`, tenant, after, limit+1)
	if err != nil {
		return store.ActiveAlertPage{}, err
	}
	defer func() { _ = rows.Close() }()
	page := store.ActiveAlertPage{Alerts: []store.StoredAlert{}}
	last := after
	for rows.Next() {
		var id string
		var raw []byte
		var version uint64
		if err := rows.Scan(&id, &raw, &version); err != nil {
			return page, err
		}
		if id <= last {
			return page, fmt.Errorf("active alert page order mismatch")
		}
		if len(page.Alerts) == limit {
			page.Next = last
			break
		}
		last = id
		alert, err := decodeAlert(raw)
		if err != nil {
			return page, err
		}
		if alert.BKTenantID != tenant || alert.AlertID != id || alert.Status != domain.AlertStatusActive {
			return page, fmt.Errorf("active candidate identity mismatch")
		}
		page.Alerts = append(page.Alerts, store.StoredAlert{Alert: alert, Version: versionToken(version)})
	}
	return page, rows.Err()
}
