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
	"time"

	"linkd/internal/store"
)

// JSON 中的时间已由领域模型归一成 UTC；把可选小数补齐到纳秒后按文本比较，
// 避免 DATETIME(6) 截断精度或无小数的 RFC3339 Z 排序错误。现有 policy_work 索引
// 仍限定候选，未增加所有 Alert 写路径都必须维护的到期列。
const shieldDueTimeSQL = `CONCAT(LEFT(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.shield.next_check_at')),19),'.',RPAD(TRIM(TRAILING 'Z' FROM SUBSTRING(JSON_UNQUOTE(JSON_EXTRACT(payload,'$.shield.next_check_at')),21,9)),9,'0'))`

const shieldWorkSQL = `SELECT bk_tenant_id,alert_id,payload,version FROM linkd_alerts WHERE policy_work=1 AND (JSON_EXTRACT(payload,'$.policy_change.operation_id') IS NOT NULL OR (JSON_EXTRACT(payload,'$.shield.active')=true AND ` + shieldDueTimeSQL + `<=?)) AND (bk_tenant_id>? OR (bk_tenant_id=? AND alert_id>?)) ORDER BY bk_tenant_id,alert_id LIMIT ?`

func (r *Repository) ListShieldWork(ctx context.Context, after store.ShieldWorkCursor, at time.Time, limit int) (store.ShieldWorkPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ShieldWorkPage{}, err
	}
	if err := after.Validate(limit, at); err != nil {
		return store.ShieldWorkPage{}, err
	}
	rows, err := r.db.QueryContext(ctx, shieldWorkSQL, at.UTC().Format("2006-01-02T15:04:05.000000000"), after.TenantID, after.TenantID, after.AlertID, limit+1)
	if err != nil {
		return store.ShieldWorkPage{}, err
	}
	defer func() { _ = rows.Close() }()
	page := store.ShieldWorkPage{Alerts: []store.StoredAlert{}}
	seen := 0
	last := after
	for rows.Next() {
		var key store.ShieldWorkCursor
		var raw []byte
		var version uint64
		if err := rows.Scan(&key.TenantID, &key.AlertID, &raw, &version); err != nil {
			return page, err
		}
		if key.Compare(last) <= 0 {
			return page, fmt.Errorf("shield work ordering mismatch")
		}
		if seen == limit {
			page.Next = last
			break
		}
		seen++
		last = key
		alert, err := decodeAlert(raw)
		if err != nil {
			return page, err
		}
		if alert.BKTenantID != key.TenantID || alert.AlertID != key.AlertID || !store.HasShieldWork(alert) {
			return page, fmt.Errorf("shield work identity/index mismatch")
		}
		if store.ShieldWorkDue(alert, at) {
			page.Alerts = append(page.Alerts, store.StoredAlert{Alert: alert, Version: versionToken(version)})
		}
	}
	return page, rows.Err()
}
