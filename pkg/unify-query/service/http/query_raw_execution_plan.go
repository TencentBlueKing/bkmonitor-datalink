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
	"reflect"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

type rawQueryExecutionPlan struct {
	query metadata.Query
	start time.Time
	end   time.Time
}

type rawQueryExecutionTracker struct {
	queries []rawQueryExecutionPlan
}

// isDuplicate 仅合并同一请求中查询参数与时间范围相同的 ES Raw 请求。
// 历史路由时间用于描述数据位置，不参与 ES Raw 请求体的构造。
func (t *rawQueryExecutionTracker) isDuplicate(query *metadata.Query, start, end time.Time) bool {
	if query.StorageType != metadata.ElasticsearchStorageType || query.Scroll != "" {
		return false
	}
	if option := query.ResultTableOption; option != nil && (option.ScrollID != "" || option.SliceMax > 0 || option.SliceIndex > 0) {
		return false
	}

	comparable := *query
	comparable.RouteStart = time.Time{}
	comparable.RouteEnd = time.Time{}
	comparable.RouteEndInclusive = false
	comparable.RouteQueryStart = time.Time{}
	comparable.RouteQueryEnd = time.Time{}
	for _, previous := range t.queries {
		if previous.start.Equal(start) && previous.end.Equal(end) && reflect.DeepEqual(previous.query, comparable) {
			return true
		}
	}
	t.queries = append(t.queries, rawQueryExecutionPlan{query: comparable, start: start, end: end})
	return false
}

// planRawQueryExecutions 根据历史路由生成普通 Raw 查询计划。
// start 和 end 是本次请求的全局时间范围；各历史分段的生效时间保存在 query 的路由字段中。
func planRawQueryExecutions(
	queryRef metadata.QueryReference,
	queryTs *structured.QueryTs,
	start, end time.Time,
) ([]rawQueryExecutionPlan, int) {
	plans := make([]rawQueryExecutionPlan, 0, queryRef.Count())
	skipped := 0
	queryCount := queryRef.Count()
	var executed rawQueryExecutionTracker
	queryRef.Range("", func(qry *metadata.Query) {
		localQry := *qry
		localQry.ResultTableOption = qry.ResultTableOption.Clone()
		if localQry.ResultTableOption != nil && localQry.ResultTableOption.From != nil && *localQry.ResultTableOption.From > 0 {
			localQry.From = *localQry.ResultTableOption.From
		}
		// SearchAfter 翻页时，已查询完成的路由不再提交，并计入跳过数量。
		if queryTs.IsSearchAfter && len(queryTs.ResultTableOptions) > 0 && !hasRawPaginationCursor(localQry.ResultTableOption) {
			skipped++
			return
		}
		// 多路由合并排序时，每条路由需从起点读取足够数量的数据。
		if queryCount > 1 && !hasRawOffsetCursor(localQry.ResultTableOption) && !queryTs.IsMultiFrom {
			localQry.Size += localQry.From
			localQry.From = 0
		}
		if executed.isDuplicate(&localQry, start, end) {
			return
		}
		plans = append(plans, rawQueryExecutionPlan{query: localQry, start: start, end: end})
	})
	return plans, skipped
}
