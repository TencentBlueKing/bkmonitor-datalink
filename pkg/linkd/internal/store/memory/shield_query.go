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

	"linkd/internal/store"
)

// ListShieldAlerts 只枚举明确租户的当前屏蔽/待输出记录，返回独立快照。
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := []string{}
	for key, entry := range r.alerts {
		if err := ctx.Err(); err != nil {
			return store.ShieldAlertPage{}, err
		}
		if key.tenantID != tenant || key.objectID <= after || !store.HasShieldWork(entry.alert) || (main != "" && !slices.Contains(store.ShieldMainAlertIDs(entry.alert), main)) {
			continue
		}
		ids = append(ids, key.objectID)
		slices.Sort(ids)
		if len(ids) > limit+1 {
			ids = ids[:limit+1]
		}
	}
	page := store.ShieldAlertPage{Alerts: []store.StoredAlert{}}
	if len(ids) > limit {
		page.Next = ids[limit-1]
		ids = ids[:limit]
	}
	for _, id := range ids {
		entry := r.alerts[objectKey{tenantID: tenant, objectID: id}]
		page.Alerts = append(page.Alerts, store.StoredAlert{Alert: entry.alert.Clone(), Version: entry.version})
	}
	return page, nil
}
