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
	"time"

	"linkd/internal/domain"
)

// ShieldWorkCursor 标识控制面全局工作枚举的位置，不是无租户的用户查询入口。
// 每一项仍携带完整租户身份，执行前必须用该租户实时重读并取得同 fingerprint lease。
type ShieldWorkCursor struct {
	TenantID string
	AlertID  string
}

// ShieldWorkPage 返回到期或有待补齐输出的 Alert；Next 包含最后扫描位置，未来到期的行不阻塞分页。
type ShieldWorkPage struct {
	Alerts []StoredAlert
	Next   ShieldWorkCursor
}

// ShieldWorkStore 只供控制面扫描持久化工作，不依赖 Redis 提示或最近几天的 Event。
type ShieldWorkStore interface {
	ListShieldWork(context.Context, ShieldWorkCursor, time.Time, int) (ShieldWorkPage, error)
}

func (c ShieldWorkCursor) Validate(limit int, at time.Time) error {
	if limit < 1 || limit > 32 || at.IsZero() {
		return fmt.Errorf("%w: shield work budget/time", ErrInvalidArgument)
	}
	if c.TenantID == "" && c.AlertID == "" {
		return nil
	}
	if err := domain.ValidateIdentityPart("tenant", c.TenantID, 64); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidCursor, err)
	}
	if c.AlertID == "" || len(c.AlertID) > domain.EntityIDMaxBytes {
		return fmt.Errorf("%w: shield cursor id", ErrInvalidCursor)
	}
	return nil
}

func (c ShieldWorkCursor) Compare(other ShieldWorkCursor) int {
	if v := strings.Compare(c.TenantID, other.TenantID); v != 0 {
		return v
	}
	return strings.Compare(c.AlertID, other.AlertID)
}

// HasShieldWork 为写入侧计算索引标记，已结束但尚有输出意图的记录也要可发现。
func HasShieldWork(a domain.Alert) bool { return a.Shield.Active || a.PolicyChange != nil }

// ShieldWorkDue 仅把到期绑定与待完成输出交给执行端，过滤不改变扫描游标。
func ShieldWorkDue(a domain.Alert, at time.Time) bool {
	return a.PolicyChange != nil || (a.Shield.Active && a.Shield.NextCheckAt != nil && !a.Shield.NextCheckAt.After(at))
}
