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

// ListSourceAlerts 返回原始来源快照；屏蔽和合并关系不改变活动状态过滤。
func (r *Repository) ListSourceAlerts(ctx context.Context, q store.SourceAlertQuery) (store.SourceAlertPage, error) {
	if err := contextError(ctx); err != nil {
		return store.SourceAlertPage{}, err
	}
	if err := q.Validate(); err != nil {
		return store.SourceAlertPage{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := []string{}
	for key, entry := range r.alerts {
		if err := ctx.Err(); err != nil {
			return store.SourceAlertPage{}, err
		}
		if !q.Matches(entry.alert) {
			continue
		}
		ids = append(ids, key.objectID)
		slices.Sort(ids)
		if len(ids) > q.Limit+1 {
			ids = ids[:q.Limit+1]
		}
	}
	page := store.SourceAlertPage{Alerts: []store.StoredAlert{}}
	if len(ids) > q.Limit {
		page.Next = ids[q.Limit-1]
		ids = ids[:q.Limit]
	}
	for _, id := range ids {
		entry := r.alerts[objectKey{tenantID: q.TenantID, objectID: id}]
		page.Alerts = append(page.Alerts, store.StoredAlert{Alert: entry.alert.Clone(), Version: entry.version})
	}
	return page, nil
}
