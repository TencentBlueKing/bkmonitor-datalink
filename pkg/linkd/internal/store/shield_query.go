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

	"linkd/internal/domain"
)

// ShieldAlertPage 是租户内当前屏蔽或仍有状态输出意图的 Alert 页，包含未来才到期的绑定。
type ShieldAlertPage struct {
	Alerts []StoredAlert
	Next   string
}

// ShieldAlertReader 供管理查询使用，不复用跨租户后台工作游标；已解除历史通过 AlertLog 查询。
type ShieldAlertReader interface {
	ListShieldAlerts(context.Context, string, string, int) (ShieldAlertPage, error)
}

// ValidateShieldQuery 限制明确租户与每页预算，不允许无作用域管理扫描。
func ValidateShieldQuery(tenant, after string, limit int) error {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || len(after) > domain.EntityIDMaxBytes || limit < 1 || limit > 16 {
		return fmt.Errorf("%w: shield query scope/budget", ErrInvalidArgument)
	}
	return nil
}

// ShieldDependencyReader 按明确租户与固定主 Alert 枚举当前依赖关系，不包含已解除历史。
// 用于事件提示加速；不按 NextCheckAt 截断，返回后执行方仍须在指纹租约内重读。
type ShieldDependencyReader interface {
	ListShieldDependents(context.Context, string, string, string, int) (ShieldAlertPage, error)
}

// ValidateShieldDependentsQuery 限制查询租户、主身份、子游标和最多十六项的页面。
func ValidateShieldDependentsQuery(tenant, main, after string, limit int) error {
	if err := ValidateShieldQuery(tenant, after, limit); err != nil {
		return err
	}
	if main == "" || len(main) > domain.EntityIDMaxBytes {
		return fmt.Errorf("%w: shield main identity", ErrInvalidArgument)
	}
	return nil
}

// ShieldMainAlertIDs 提取当前活动依赖关系的去重主身份，用于随 Alert 原子更新的查询索引。
// 时间屏蔽、已解除绑定及待输出历史不建立新依赖；后者由独立工作扫描补齐。
func ShieldMainAlertIDs(a domain.Alert) []string {
	if !a.Shield.Active {
		return nil
	}
	var ids []string
	for _, b := range a.Shield.Bindings {
		if b.Type == "rely_shield" && b.MainAlertID != "" {
			ids = append(ids, b.MainAlertID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}
