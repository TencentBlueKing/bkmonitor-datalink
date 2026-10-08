// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package merge

import (
	"context"
	"encoding/json"
	"strings"

	"linkd/internal/domain"
	"linkd/internal/policy"
)

// DecisionPage 包含租户内的持久化裁决，含已完成记录；Next 不代表仍有匹配过滤条件的数据。
type DecisionPage struct {
	Items []Decision
	Next  string
}

// ListDecisions 按租户和稳定身份分页，不用后台的全局工作扫描代替管理查询。
// 每次只扫描一页，过滤后即使为空也推进游标；不会为填满页面无限扫描历史。
func (j *Journal) ListDecisions(ctx context.Context, tenant, after string, limit int) (DecisionPage, error) {
	if domain.ValidateIdentityPart("tenant", tenant, 64) != nil || limit < 1 || limit > 16 || (after != "" && !validHash(after)) {
		return DecisionPage{}, policy.ErrInvalid
	}
	scope, cursor := prefix(tenant), ""
	if after != "" {
		cursor = scope + after
	}
	rows, err := j.docs.List(ctx, "merge_decisions", scope, cursor, limit)
	if err != nil {
		return DecisionPage{}, err
	}
	if len(rows) > limit {
		return DecisionPage{}, policy.ErrInvalid
	}
	page := DecisionPage{Items: []Decision{}}
	last := after
	for _, raw := range rows {
		var d Decision
		if len(raw) > 8<<20 || json.Unmarshal(raw, &d) != nil || d.Validate() != nil {
			return DecisionPage{}, policy.ErrInvalid
		}
		if d.TenantID != tenant {
			return DecisionPage{}, policy.ErrAccess
		}
		if strings.Compare(d.ID, last) <= 0 {
			return DecisionPage{}, policy.ErrInvalid
		}
		last = d.ID
		page.Items = append(page.Items, d)
	}
	if len(rows) == limit {
		page.Next = last
	}
	return page, nil
}
