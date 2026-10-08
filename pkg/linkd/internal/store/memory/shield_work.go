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
	"time"

	"linkd/internal/store"
)

func (r *Repository) ListShieldWork(ctx context.Context, after store.ShieldWorkCursor, at time.Time, limit int) (store.ShieldWorkPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ShieldWorkPage{}, err
	}
	if err := after.Validate(limit, at); err != nil {
		return store.ShieldWorkPage{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := []store.ShieldWorkCursor{}
	for k, e := range r.alerts {
		if err := ctx.Err(); err != nil {
			return store.ShieldWorkPage{}, err
		}
		key := store.ShieldWorkCursor{TenantID: k.tenantID, AlertID: k.objectID}
		if key.Compare(after) <= 0 || !store.HasShieldWork(e.alert) {
			continue
		}
		keys = append(keys, key)
		slices.SortFunc(keys, func(a, b store.ShieldWorkCursor) int { return a.Compare(b) })
		if len(keys) > limit+1 {
			keys = keys[:limit+1]
		}
	}
	page := store.ShieldWorkPage{Alerts: []store.StoredAlert{}}
	if len(keys) > limit {
		page.Next = keys[limit-1]
		keys = keys[:limit]
	}
	for _, k := range keys {
		e := r.alerts[objectKey{tenantID: k.TenantID, objectID: k.AlertID}]
		if store.ShieldWorkDue(e.alert, at) {
			page.Alerts = append(page.Alerts, store.StoredAlert{Alert: e.alert.Clone(), Version: e.version})
		}
	}
	return page, nil
}
