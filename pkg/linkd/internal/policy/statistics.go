// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"time"

	"linkd/internal/domain"
)

// StatisticsQuery 只查询当前页显式策略的UTC整点桶，含当前未完整小时；不扫描租户全集。
type StatisticsQuery struct {
	Scope
	IDs   []string `json:"ids"`
	Hours int      `json:"hours"`
}

// Validate 限制查询展开规模，重复身份拒绝以免重复展示计数。
func (q StatisticsQuery) Validate() error {
	if q.Scope.Validate() != nil || len(q.IDs) < 1 || len(q.IDs) > 8 || (q.Hours != 1 && q.Hours != 6 && q.Hours != 24) {
		return ErrInvalid
	}
	seen := map[string]bool{}
	for _, id := range q.IDs {
		if domain.ValidateIdentityPart("policy", id, 80) != nil || seen[id] {
			return ErrInvalid
		}
		seen[id] = true
	}
	return nil
}

// PolicyStatistics 聚合所有发布版本的执行观察；重试、定时重查与依赖候选会重复计数。
// ExecutionSkipped 包含加载、输入、Redis等执行失败，可与同次匹配观察重叠，不能相加作事件总数。
type PolicyStatistics struct {
	ID               string `json:"id"`
	Matched          int64  `json:"matched"`
	NotMatched       int64  `json:"not_matched"`
	Unavailable      int64  `json:"unavailable"`
	ExecutionSkipped int64  `json:"execution_skipped"`
}

// StatisticsPage 的零只表示所查询缓存内没有观察；缓存丢失、采样失败和TTL均不补历史。
type StatisticsPage struct {
	Scope
	From  time.Time          `json:"from"`
	To    time.Time          `json:"to"`
	Hours int                `json:"hours"`
	Mode  string             `json:"mode"`
	Items []PolicyStatistics `json:"items"`
}
