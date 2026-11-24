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

	"linkd/internal/domain"
)

// ActiveAlertPage 供策略按明确租户枚举活动候选，顺序只按 AlertID；业务选主排序在完整有界集合上执行。
type ActiveAlertPage struct {
	Alerts []StoredAlert
	Next   string
}

// ActiveAlertReader 不承诺搜索结果是跨对象事务快照；选中主告警后仍须实时重读。
type ActiveAlertReader interface {
	ListActiveAlerts(context.Context, string, string, int) (ActiveAlertPage, error)
}

// ValidateActiveAlertPage 拒绝无租户或超过有界页大小的扫描请求。
func ValidateActiveAlertPage(tenant, after string, limit int) error {
	if err := domain.ValidateIdentityPart("tenant", tenant, 64); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if len(after) > domain.EntityIDMaxBytes || limit < 1 || limit > 32 {
		return fmt.Errorf("%w: active alert page", ErrInvalidArgument)
	}
	return nil
}
