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
	"errors"

	"linkd/internal/domain"
	"linkd/internal/store"
)

// QueryAlertByEvent 返回 Event 及其已经关联的可选 Alert。
// 该组合查询不承诺跨两次读取的事务快照。
func (r *Repository) QueryAlertByEvent(
	ctx context.Context,
	bkTenantID, eventID string,
) (store.AlertByEventResult, error) {
	event, err := r.GetEvent(ctx, bkTenantID, eventID)
	if err != nil {
		return store.AlertByEventResult{}, err
	}
	result := store.AlertByEventResult{Event: event}
	if event.Processing.State != domain.EventProcessStateAccepted &&
		event.Processing.State != domain.EventProcessStateSuppressed {
		return result, nil
	}
	alert, err := r.GetAlert(ctx, bkTenantID, event.Event.RelatedAlertID)
	if errors.Is(err, store.ErrNotFound) {
		return result, nil
	}
	if err != nil {
		return store.AlertByEventResult{}, err
	}
	result.Alert = &alert
	return result, nil
}
