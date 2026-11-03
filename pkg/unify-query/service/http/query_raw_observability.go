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
	"context"
	"sync/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type rawQueryTraceStats struct {
	routes                   int
	plannedQueries           int
	missingIndexQueries      int
	paginationSkippedQueries int
	readyQueries             int
}

// newRawQueryTraceStats 保存执行筛选前的规划数量，并统计缺少索引前缀的查询。
func newRawQueryTraceStats(plan structured.QueryPlan) rawQueryTraceStats {
	stats := rawQueryTraceStats{routes: plan.Routes.Count(), plannedQueries: plan.Queries.Count()}
	plan.Queries.Range("", func(query *metadata.Query) {
		if query.IsElasticsearchIndexPrefixMissing() {
			stats.missingIndexQueries++
		}
	})
	return stats
}

// record 在请求结束时上报各阶段的查询数量和过滤原因。
func (s rawQueryTraceStats) record(ctx context.Context, span *trace.Span) {
	span.Set("raw_route_count", s.routes)
	span.Set("raw_planned_query_count", s.plannedQueries)
	span.Set("raw_missing_index_query_count", s.missingIndexQueries)
	span.Set("raw_pagination_skipped_query_count", s.paginationSkippedQueries)
	span.Set("raw_ready_query_count", s.readyQueries)
	metric.RawQueryDecisionAdd(ctx, metric.RawQueryDecisionMissingIndex, s.missingIndexQueries)
	metric.RawQueryDecisionAdd(ctx, metric.RawQueryDecisionPaginationFinished, s.paginationSkippedQueries)
}

type rawScrollTraceStats struct {
	candidates        int
	completed         atomic.Uint32
	attempted         atomic.Uint32
	succeeded         atomic.Uint32
	failed            atomic.Uint32
	preparationFailed atomic.Uint32
	submissionFailed  atomic.Uint32
}

// record 在切片任务结束后上报结果，失败数包含未达到重试阈值的查询失败。
func (s *rawScrollTraceStats) record(span *trace.Span) {
	span.Set("raw_slice_candidate_count", s.candidates)
	span.Set("raw_slice_completed_skip_count", int(s.completed.Load()))
	span.Set("raw_slice_attempted_count", int(s.attempted.Load()))
	span.Set("raw_slice_succeeded_count", int(s.succeeded.Load()))
	span.Set("raw_slice_failed_count", int(s.failed.Load()))
	span.Set("raw_slice_preparation_failed_count", int(s.preparationFailed.Load()))
	span.Set("raw_slice_submission_failed_count", int(s.submissionFailed.Load()))
}
