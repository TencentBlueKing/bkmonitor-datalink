// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// prepareRawQueryExecutions 为已解析的执行目标应用分页，供普通查询和 Batch 共用。
// 每次执行复制查询及游标，避免执行过程修改原查询计划和调用方的分页状态。
func prepareRawQueryExecutions(queryRef metadata.QueryReference, queryTs *structured.QueryTs) (metadata.QueryReference, int) {
	prepared := make(metadata.QueryReference, len(queryRef))
	queryCount := queryRef.Count()
	skipped := 0
	for name, references := range queryRef {
		for _, reference := range references {
			if reference == nil {
				continue
			}
			queries := make(metadata.QueryList, 0, len(reference.QueryList))
			for _, query := range reference.QueryList {
				if query == nil {
					continue
				}
				localQuery := *query
				localQuery.ResultTableOption = query.ResultTableOption.Clone()
				localQuery.IsSearchAfter = queryTs.IsSearchAfter
				if hasRawOffsetCursor(localQuery.ResultTableOption) {
					localQuery.From = *localQuery.ResultTableOption.From
				}
				// 后续页只查询仍有游标的目标；Doris 兼容仅返回 From 的分页方式。
				if queryTs.IsSearchAfter && len(queryTs.ResultTableOptions) > 0 && !hasRawPaginationCursor(localQuery.ResultTableOption) {
					skipped++
					continue
				}
				// 多目标合并排序时，从起点读取足够数量的数据，再统一裁剪。
				if queryCount > 1 && !hasRawOffsetCursor(localQuery.ResultTableOption) && !queryTs.IsMultiFrom {
					localQuery.Size += localQuery.From
					localQuery.From = 0
				}
				queries = append(queries, &localQuery)
			}
			if len(queries) > 0 {
				localReference := *reference
				localReference.QueryList = queries
				prepared[name] = append(prepared[name], &localReference)
			}
		}
	}
	return prepared, skipped
}
