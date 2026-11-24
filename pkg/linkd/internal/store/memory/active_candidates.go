// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package memory

import (
	"context"
	"slices"

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
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := []string{}
	for key, entry := range r.alerts {
		if ctx.Err() != nil {
			return store.ActiveAlertPage{}, ctx.Err()
		}
		if key.tenantID != tenant || key.objectID <= after || entry.alert.Status != domain.AlertStatusActive {
			continue
		}
		ids = append(ids, key.objectID)
		slices.Sort(ids)
		if len(ids) > limit+1 {
			ids = ids[:limit+1]
		}
	}
	page := store.ActiveAlertPage{Alerts: []store.StoredAlert{}}
	if len(ids) > limit {
		page.Next = ids[limit-1]
		ids = ids[:limit]
	}
	for _, id := range ids {
		e := r.alerts[objectKey{tenantID: tenant, objectID: id}]
		page.Alerts = append(page.Alerts, store.StoredAlert{Alert: e.alert.Clone(), Version: e.version})
	}
	return page, nil
}
