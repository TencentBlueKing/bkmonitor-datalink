// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package mysqlstore

import (
	"context"

	"linkd/internal/store"
)

// CompleteAlertChange 保留完整版本条件，只清除意图及维护扫描字段，不传输整个 Alert。
func (r *Repository) CompleteAlertChange(ctx context.Context, current store.StoredAlert, field store.AlertChangeField) (store.StoredAlert, error) {
	if err := contextError(ctx); err != nil {
		return store.StoredAlert{}, err
	}
	_, ok := parseVersion(current.Version)
	if !ok {
		return store.StoredAlert{}, store.ErrVersionConflict
	}
	next, err := store.CompletedAlertChange(current, field)
	if err != nil {
		return store.StoredAlert{}, err
	}
	return r.writeAlert(ctx, alertWrite{current: current, next: next, field: field})
}
