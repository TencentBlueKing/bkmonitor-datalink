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
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"linkd/internal/domain"
)

// MergeWorkCursor 同时记录 Alert 和该 Alert 内的窗口位置，防止多策略等待被页大小截断而饿死。
// WindowID 为空表示 merge_change 意图；非空为待检查窗口。仅用于控制面全局扫描，每项仍显式带租户。
type MergeWorkCursor struct {
	TenantID string
	AlertID  string
	WindowID string
}

// MergeWorkItem 是一项持久化待办；WindowID 为空时先完成 Alert 上的 merge_change。
type MergeWorkItem struct {
	Alert    StoredAlert
	WindowID string
}

// MergeWorkPage 的每页预算按窗口/意图计算，不按 Alert 数量计算。
type MergeWorkPage struct {
	Items []MergeWorkItem
	Next  MergeWorkCursor
}

// MergeWorkStore 无时间截断地发现持久化窗口引用和待完成意图，不依赖 Redis 的租户或到期索引。
type MergeWorkStore interface {
	ListMergeWork(context.Context, MergeWorkCursor, int) (MergeWorkPage, error)
}

// Validate 只接受完整 Alert 作用域的游标和最多 16 项的扫描预算。
func (c MergeWorkCursor) Validate(limit int) error {
	if limit < 1 || limit > 16 {
		return fmt.Errorf("%w: merge work budget", ErrInvalidArgument)
	}
	if c == (MergeWorkCursor{}) {
		return nil
	}
	if domain.ValidateIdentityPart("tenant", c.TenantID, 64) != nil || c.AlertID == "" || len(c.AlertID) > domain.EntityIDMaxBytes {
		return fmt.Errorf("%w: merge work scope", ErrInvalidCursor)
	}
	if c.WindowID != "" {
		raw, err := hex.DecodeString(c.WindowID)
		if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != c.WindowID {
			return fmt.Errorf("%w: merge work window", ErrInvalidCursor)
		}
	}
	return nil
}

func (c MergeWorkCursor) Compare(other MergeWorkCursor) int {
	if n := strings.Compare(c.TenantID, other.TenantID); n != 0 {
		return n
	}
	if n := strings.Compare(c.AlertID, other.AlertID); n != 0 {
		return n
	}
	return strings.Compare(c.WindowID, other.WindowID)
}

// HasMergeWork 为写入侧计算索引标记，父恢复后的终态意图也必须继续被扫描。
func HasMergeWork(a domain.Alert) bool {
	return a.MergeChange != nil || (a.Merge != nil && len(a.Merge.Pending) > 0)
}

// MergeWorkPageFromAlerts 将最多 limit+1 个按租户/Alert 排序的完整行展开为窗口页。
// 读取必须包含游标所在 Alert，保留其未遍历窗口；满页始终返回最后一项游标，允许最后一页为空。
// 任一行错误整页失败，不能把错误之后的游标当成已扫描。
func MergeWorkPageFromAlerts(rows []StoredAlert, after MergeWorkCursor, limit int) (MergeWorkPage, error) {
	if err := after.Validate(limit); err != nil {
		return MergeWorkPage{}, err
	}
	if len(rows) > limit+1 {
		return MergeWorkPage{}, fmt.Errorf("%w: merge work row budget", ErrInvalidArgument)
	}
	page := MergeWorkPage{Items: []MergeWorkItem{}}
	var previous MergeWorkCursor
	for _, row := range rows {
		a := row.Alert
		key := MergeWorkCursor{TenantID: a.BKTenantID, AlertID: a.AlertID}
		start := after
		start.WindowID = ""
		if row.Version.IsZero() || a.Validate() != nil || !HasMergeWork(a) || key.Compare(start) < 0 || (previous.TenantID != "" && key.Compare(previous) <= 0) {
			return MergeWorkPage{}, fmt.Errorf("%w: merge work row/index mismatch", ErrInvalidArgument)
		}
		previous = key
		windows := []string{}
		if a.MergeChange != nil {
			windows = append(windows, "")
		}
		if a.Merge != nil {
			for _, w := range a.Merge.Pending {
				windows = append(windows, w.WindowID)
			}
		}
		slices.Sort(windows)
		for _, window := range windows {
			key.WindowID = window
			if key.Compare(after) <= 0 {
				continue
			}
			page.Items = append(page.Items, MergeWorkItem{Alert: StoredAlert{Alert: a.Clone(), Version: row.Version}, WindowID: window})
			if len(page.Items) == limit {
				page.Next = key
				return page, nil
			}
		}
	}
	return page, nil
}
