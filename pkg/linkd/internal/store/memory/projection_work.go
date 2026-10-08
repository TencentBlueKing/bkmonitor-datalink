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

// ListProjectionWork 在一致的内存读锁下选取有界行，再按目标展开分页。
func (r *Repository) ListProjectionWork(ctx context.Context, after store.ProjectionWorkCursor, limit int) (store.ProjectionWorkPage, error) {
	if err := contextError(ctx); err != nil {
		return store.ProjectionWorkPage{}, err
	}
	if err := after.Validate(limit); err != nil {
		return store.ProjectionWorkPage{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	start := after
	start.TargetID = ""
	keys := []store.ProjectionWorkCursor{}
	for k, v := range r.alerts {
		if err := ctx.Err(); err != nil {
			return store.ProjectionWorkPage{}, err
		}
		key := store.ProjectionWorkCursor{TenantID: k.tenantID, AlertID: k.objectID}
		if key.Compare(start) < 0 || !v.alert.Projection.Pending() {
			continue
		}
		keys = append(keys, key)
		slices.SortFunc(keys, func(a, b store.ProjectionWorkCursor) int { return a.Compare(b) })
		if len(keys) > limit+1 {
			keys = keys[:limit+1]
		}
	}
	rows := make([]store.StoredAlert, 0, len(keys))
	for _, key := range keys {
		v := r.alerts[objectKey{tenantID: key.TenantID, objectID: key.AlertID}]
		rows = append(rows, store.StoredAlert{Alert: v.alert, Version: v.version})
	}
	return store.ProjectionWorkPageFromAlerts(rows, after, limit)
}
