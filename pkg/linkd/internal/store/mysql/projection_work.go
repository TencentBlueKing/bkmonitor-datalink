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

// ListProjectionWork 使用与 Alert CAS 同步的派生索引，包含终态并保留同行剩余目标。
func (r *Repository) ListProjectionWork(ctx context.Context, after store.ProjectionWorkCursor, limit int) (store.ProjectionWorkPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ProjectionWorkPage{}, err
	}
	if err := after.Validate(limit); err != nil {
		return store.ProjectionWorkPage{}, err
	}
	rows, err := r.db.QueryContext(ctx, `SELECT bk_tenant_id,alert_id,payload,version FROM linkd_alerts WHERE projection_work=1 AND (bk_tenant_id>? OR (bk_tenant_id=? AND alert_id>=?)) ORDER BY bk_tenant_id,alert_id LIMIT ?`, after.TenantID, after.TenantID, after.AlertID, limit+1)
	if err != nil {
		return store.ProjectionWorkPage{}, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]store.StoredAlert, 0, limit+1)
	for rows.Next() {
		var tenant, id string
		var raw []byte
		var version uint64
		if err := rows.Scan(&tenant, &id, &raw, &version); err != nil {
			return store.ProjectionWorkPage{}, err
		}
		a, err := decodeAlert(raw)
		if err != nil {
			return store.ProjectionWorkPage{}, err
		}
		if a.BKTenantID != tenant || a.AlertID != id {
			return store.ProjectionWorkPage{}, fmt.Errorf("projection work identity mismatch")
		}
		result = append(result, store.StoredAlert{Alert: a, Version: versionToken(version)})
	}
	if err := rows.Err(); err != nil {
		return store.ProjectionWorkPage{}, err
	}
	return store.ProjectionWorkPageFromAlerts(result, after, limit)
}
