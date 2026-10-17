// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package lifecycle

import (
	"context"
	"errors"

	"linkd/internal/store"
)

func (p *Processor) completeAlertChange(ctx context.Context, current store.StoredAlert, field store.AlertChangeField) (store.StoredAlert, error) {
	if repository, ok := p.repository.(store.AlertChangeCompletionStore); ok {
		defer p.measureStep(phaseApply, "alert_cas")()
		updated, err := repository.CompleteAlertChange(ctx, current, field)
		if errors.Is(err, store.ErrVersionConflict) {
			err = errors.Join(err, p.repairRecentAlert(ctx, current.Alert.BKTenantID, current.Alert.AlertID))
		}
		// 完成确认没有改变依赖资格，不重复刷新共享主索引。
		return updated, err
	}
	next, err := store.CompletedAlertChange(current, field)
	if err != nil {
		return store.StoredAlert{}, err
	}
	return p.compareAndSetAlert(ctx, current, next)
}
