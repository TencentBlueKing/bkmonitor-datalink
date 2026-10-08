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
	"slices"

	"linkd/internal/store"
)

// ListShieldAlerts 使用持久化工作索引枚举当前租户，不按检查时间过滤，也不扫描其他租户。
func (r *Repository) ListShieldAlerts(ctx context.Context, tenant, after string, limit int) (store.ShieldAlertPage, error) {
	return r.listShieldAlerts(ctx, tenant, "", after, limit)
}

// ListShieldDependents 查询同租户当前固定到主告警的子告警；未来检查时间不阻止事件提示。
func (r *Repository) ListShieldDependents(ctx context.Context, tenant, main, after string, limit int) (store.ShieldAlertPage, error) {
	if err := store.ValidateShieldDependentsQuery(tenant, main, after, limit); err != nil {
		return store.ShieldAlertPage{}, err
	}
	return r.listShieldAlerts(ctx, tenant, main, after, limit)
}

func (r *Repository) listShieldAlerts(ctx context.Context, tenant, main, after string, limit int) (store.ShieldAlertPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ShieldAlertPage{}, err
	}
	if err := store.ValidateShieldQuery(tenant, after, limit); err != nil {
		return store.ShieldAlertPage{}, err
	}
	query := `SELECT alert_id,payload,version FROM linkd_alerts WHERE bk_tenant_id=? AND policy_work=1 AND alert_id>?`
	args := []any{tenant, after}
	if main != "" {
		// 利用租户/工作集合缩小候选，再直接过滤同次 CAS 保存的 JSON，不引入异步关系镜像。
		query += ` AND JSON_EXTRACT(payload,'$.shield.active')=true AND JSON_CONTAINS(JSON_EXTRACT(payload,'$.shield.bindings[*].main_alert_id'),JSON_QUOTE(?))`
		args = append(args, main)
	}
	query += ` ORDER BY alert_id LIMIT ?`
	args = append(args, limit+1)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return store.ShieldAlertPage{}, err
	}
	defer func() { _ = rows.Close() }()
	page := store.ShieldAlertPage{Alerts: []store.StoredAlert{}}
	last := after
	for rows.Next() {
		var id string
		var raw []byte
		var version uint64
		if err := rows.Scan(&id, &raw, &version); err != nil {
			return store.ShieldAlertPage{}, err
		}
		if id <= last {
			return store.ShieldAlertPage{}, fmt.Errorf("shield query ordering mismatch")
		}
		if len(page.Alerts) == limit {
			page.Next = last
			break
		}
		alert, err := decodeAlert(raw)
		if err != nil {
			return store.ShieldAlertPage{}, err
		}
		if alert.BKTenantID != tenant || alert.AlertID != id || !store.HasShieldWork(alert) || (main != "" && !slices.Contains(store.ShieldMainAlertIDs(alert), main)) {
			return store.ShieldAlertPage{}, fmt.Errorf("shield query identity/index mismatch")
		}
		page.Alerts = append(page.Alerts, store.StoredAlert{Alert: alert, Version: versionToken(version)})
		last = id
	}
	if err := rows.Err(); err != nil {
		return store.ShieldAlertPage{}, err
	}
	return page, nil
}
