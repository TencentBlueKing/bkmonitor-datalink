// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store

import (
	"context"
	"fmt"
	"strings"

	"linkd/internal/domain"
)

// ActionWorkCursor 是内部跨租户扫描的位置，每项执行仍须使用其明确租户和 fingerprint lease。
type ActionWorkCursor struct {
	TenantID string
	AlertID  string
}

// ActionWorkPage 每页最多 16 个保留动作意图的 Alert；满页返回游标，末尾允许空页。
type ActionWorkPage struct {
	Items []StoredAlert
	Next  ActionWorkCursor
}

// ActionWorkStore 发现业务 CAS 已提交但尚未全部入队的动作，不依赖 Redis 或新的输入事件。
// ES 在意图完成前暂缓终态归档，所以扫描 Active 就能覆盖全部未入队动作。
type ActionWorkStore interface {
	ListActionWork(context.Context, ActionWorkCursor, int) (ActionWorkPage, error)
}

// Validate 限制游标作用域和页大小。
func (c ActionWorkCursor) Validate(limit int) error {
	if limit < 1 || limit > 16 {
		return fmt.Errorf("%w: action work budget", ErrInvalidArgument)
	}
	if c == (ActionWorkCursor{}) {
		return nil
	}
	if domain.ValidateIdentityPart("tenant", c.TenantID, 64) != nil || c.AlertID == "" || len(c.AlertID) > domain.EntityIDMaxBytes {
		return fmt.Errorf("%w: action work cursor", ErrInvalidCursor)
	}
	return nil
}

// Compare 按租户和 Alert 身份比较，不把业务版本或时间用作扫描顺序。
func (c ActionWorkCursor) Compare(other ActionWorkCursor) int {
	if n := strings.Compare(c.TenantID, other.TenantID); n != 0 {
		return n
	}
	return strings.Compare(c.AlertID, other.AlertID)
}

// ActionWorkPageFromAlerts 校验完整有序行及待办标记，任一异常整页返回错误，不推进游标。
func ActionWorkPageFromAlerts(rows []StoredAlert, after ActionWorkCursor, limit int) (ActionWorkPage, error) {
	if err := after.Validate(limit); err != nil {
		return ActionWorkPage{}, err
	}
	if len(rows) > limit {
		return ActionWorkPage{}, ErrInvalidArgument
	}
	page := ActionWorkPage{Items: make([]StoredAlert, 0, len(rows))}
	previous := after
	for _, row := range rows {
		key := ActionWorkCursor{TenantID: row.Alert.BKTenantID, AlertID: row.Alert.AlertID}
		if row.Version.IsZero() || row.Alert.Validate() != nil || row.Alert.ActionPending == nil || key.Compare(previous) <= 0 {
			return ActionWorkPage{}, fmt.Errorf("%w: action work row/index mismatch", ErrInvalidArgument)
		}
		previous = key
		page.Items = append(page.Items, StoredAlert{Alert: row.Alert.Clone(), Version: row.Version})
	}
	if len(rows) == limit {
		page.Next = previous
	}
	return page, nil
}
