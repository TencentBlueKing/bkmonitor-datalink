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

// ListActionWork 按租户和 Alert 身份有界扫描动作意图；执行方须在身份租约内重新读取。
func (r *Repository) ListActionWork(ctx context.Context, after store.ActionWorkCursor, limit int) (store.ActionWorkPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ActionWorkPage{}, err
	}
	if err := after.Validate(limit); err != nil {
		return store.ActionWorkPage{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT bk_tenant_id,alert_id,payload,version FROM linkd_alerts WHERE action_work=1 AND (bk_tenant_id>? OR (bk_tenant_id=? AND alert_id>?)) ORDER BY bk_tenant_id,alert_id LIMIT ?`, after.TenantID, after.TenantID, after.AlertID, limit)
	if err != nil {
		return store.ActionWorkPage{}, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]store.StoredAlert, 0, limit)
	for rows.Next() {
		var tenant, id string
		var raw []byte
		var version uint64
		if err := rows.Scan(&tenant, &id, &raw, &version); err != nil {
			return store.ActionWorkPage{}, err
		}
		a, err := decodeAlert(raw)
		if err != nil {
			return store.ActionWorkPage{}, err
		}
		if a.BKTenantID != tenant || a.AlertID != id {
			return store.ActionWorkPage{}, fmt.Errorf("action work identity mismatch")
		}
		result = append(result, store.StoredAlert{Alert: a, Version: versionToken(version)})
	}
	if err := rows.Err(); err != nil {
		return store.ActionWorkPage{}, err
	}
	return store.ActionWorkPageFromAlerts(result, after, limit)
}
