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
	"slices"
	"strings"

	"linkd/internal/domain"
)

// ProjectionWorkCursor 按租户、Alert 和目标分页，无活动/终态或最近日期限制。
type ProjectionWorkCursor struct {
	// TenantID 是上一项的明确租户作用域；初始游标的三个字段均为空。
	TenantID string
	// AlertID 是上一项所属 Alert。
	AlertID string
	// TargetID 是上一项的稳定目标 ID，同一 Alert 的剩余目标仍须继续扫描。
	TargetID string
}

// Validate 只接受完整的三级游标，以及 1..16 项的预算。
func (c ProjectionWorkCursor) Validate(limit int) error {
	if limit < 1 || limit > 16 {
		return fmt.Errorf("%w: projection work budget", ErrInvalidArgument)
	}
	if c == (ProjectionWorkCursor{}) {
		return nil
	}
	if domain.ValidateIdentityPart("tenant", c.TenantID, 64) != nil || c.AlertID == "" || len(c.AlertID) > domain.EntityIDMaxBytes || domain.ValidateIdentityPart("projection target", c.TargetID, 64) != nil {
		return fmt.Errorf("%w: projection work scope", ErrInvalidCursor)
	}
	return nil
}

// Compare 按租户、Alert、目标依次比较，不依赖时间或任务进程。
func (c ProjectionWorkCursor) Compare(other ProjectionWorkCursor) int {
	if n := strings.Compare(c.TenantID, other.TenantID); n != 0 {
		return n
	}
	if n := strings.Compare(c.AlertID, other.AlertID); n != 0 {
		return n
	}
	return strings.Compare(c.TargetID, other.TargetID)
}

// ProjectionWorkItem 是一个尚未确认的目标；执行前仍须实时重读当前 Alert。
type ProjectionWorkItem struct {
	// Alert 保存本次扫描命中的快照与真实仓储版本。
	Alert StoredAlert
	// TargetID 指定仍落后的目标，不代表其他目标已经确认。
	TargetID string
}

// ProjectionWorkPage 的预算按待确认目标计数；满页可返回最后一项作为游标。
type ProjectionWorkPage struct {
	// Items 是有序待确认目标，最多为请求 limit。
	Items []ProjectionWorkItem
	// Next 为空表示本轮扫描结束；满页后的下一页允许为空。
	Next ProjectionWorkCursor
}

// ProjectionWorkStore 供可靠任务生产器补扫，必须覆盖已经归档但尚未完成同步的终态。
type ProjectionWorkStore interface {
	ListProjectionWork(context.Context, ProjectionWorkCursor, int) (ProjectionWorkPage, error)
}

// ProjectionWorkPageFromAlerts 展开最多 limit+1 个按租户/Alert 排序的行；逐目标预算不丢掉同行剩余目标。
func ProjectionWorkPageFromAlerts(rows []StoredAlert, after ProjectionWorkCursor, limit int) (ProjectionWorkPage, error) {
	if err := after.Validate(limit); err != nil {
		return ProjectionWorkPage{}, err
	}
	if len(rows) > limit+1 {
		return ProjectionWorkPage{}, fmt.Errorf("%w: projection row budget", ErrInvalidArgument)
	}
	page := ProjectionWorkPage{Items: []ProjectionWorkItem{}}
	var previous ProjectionWorkCursor
	start := after
	start.TargetID = ""
	for _, row := range rows {
		a := row.Alert
		key := ProjectionWorkCursor{TenantID: a.BKTenantID, AlertID: a.AlertID}
		if row.Version.IsZero() || a.Validate() != nil || !a.Projection.Pending() || key.Compare(start) < 0 || (previous.TenantID != "" && key.Compare(previous) <= 0) {
			return ProjectionWorkPage{}, fmt.Errorf("%w: projection work row/index mismatch", ErrInvalidArgument)
		}
		previous = key
		ids := make([]string, 0, len(a.Projection.Targets))
		for id, target := range a.Projection.Targets {
			if target.RequiredRevision > target.SyncedRevision {
				ids = append(ids, id)
			}
		}
		slices.Sort(ids)
		for _, id := range ids {
			key.TargetID = id
			if key.Compare(after) <= 0 {
				continue
			}
			page.Items = append(page.Items, ProjectionWorkItem{Alert: StoredAlert{Alert: a.Clone(), Version: row.Version}, TargetID: id})
			if len(page.Items) == limit {
				page.Next = key
				return page, nil
			}
		}
	}
	return page, nil
}
