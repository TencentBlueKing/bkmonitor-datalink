// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"
	"fmt"

	"linkd/internal/enrich"
)

// strategyBusinessMatches 判断策略业务能否用于告警来源业务。
// 相同业务直接匹配；跨业务时仅全局业务匹配。跨业务查询缺失或故障表示业务依赖无效。
func strategyBusinessMatches(ctx context.Context, scope *enrich.Scope, strategyBizID, alertBizID int64) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if strategyBizID == alertBizID {
		return true, nil
	}
	isGlobal, found, err := scope.IsGlobalBusiness(ctx, strategyBizID)
	if err != nil {
		return false, fmt.Errorf("query strategy business: %w", err)
	}
	if !found {
		return false, fmt.Errorf("strategy business %d was not found", strategyBizID)
	}
	return isGlobal, nil
}
