// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"strings"
	"time"

	"github.com/prometheus/prometheus/storage"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

type Query struct {
	instance   tsdb.Instance
	qry        *metadata.Query
	start      time.Time
	end        time.Time
	queryStart time.Time
	queryEnd   time.Time
}

type QueryList []*Query

func (ql QueryList) mergeFuncName(hints *storage.SelectHints) string {
	outerAggName := ql.outerAggName()
	if hints != nil && hints.Func != "" {
		// last_over_time 只是为了扩展回看窗口，真实的存储侧 avg 仍应决定多路由合并方式。
		if strings.EqualFold(hints.Func, "last_over_time") && isAvgBucketFunc(strings.ToLower(outerAggName)) {
			return outerAggName
		}
		return hints.Func
	}

	return outerAggName
}

func (ql QueryList) outerAggName() string {
	for _, query := range ql {
		if query == nil || query.qry == nil {
			continue
		}
		if name := query.qry.Aggregates.OuterAggName(); name != "" {
			return name
		}
	}
	return ""
}

// mergeBucketDuration 返回多路由合并时用于计算 route 覆盖时长的 bucket 宽度。
// 优先使用下推聚合里与当前合并函数匹配的窗口；普通 avg 没有真实时间窗口时返回 0，
// 避免把瞬时点误当成 [t, t+step) 区间；avg_over_time 来自 Prometheus hint 时，
// 如果缺少下推窗口，则优先使用 Prometheus range selector 宽度，再使用查询步长作为兜底 bucket 宽度。
func (ql QueryList) mergeBucketDuration(name string, fallback, rangeSelector time.Duration) time.Duration {
	name = strings.ToLower(name)
	for _, query := range ql {
		if query == nil || query.qry == nil {
			continue
		}
		aggregates := query.qry.Aggregates
		for i := len(aggregates) - 1; i >= 0; i-- {
			agg := aggregates[i]
			if isSameBucketFunc(name, strings.ToLower(agg.Name)) && agg.Window > 0 {
				return agg.Window
			}
		}
	}

	if isRangeBucketFunc(name) {
		// *_over_time 来自 Prometheus hint 且缺少下推聚合窗口时，用 selector range 作为 bucket 宽度。
		if name != function.Avg && name != function.Mean {
			if rangeSelector > 0 {
				return rangeSelector
			}
			return fallback
		}
		return 0
	}
	return fallback
}

func isSameBucketFunc(a, b string) bool {
	if a == b {
		return true
	}
	return isAvgBucketFunc(a) && isAvgBucketFunc(b)
}

func isAvgBucketFunc(name string) bool {
	switch name {
	case function.Avg, function.AvgOT, function.Mean:
		return true
	default:
		return false
	}
}

func isRangeBucketFunc(name string) bool {
	switch name {
	case function.Avg, function.Mean, function.AvgOT, function.SumOT, function.CountOT, function.MinOT, function.MaxOT:
		return true
	default:
		return false
	}
}

type seriesSetWrapKind int

const (
	seriesSetWrapNone seriesSetWrapKind = iota
	seriesSetWrapValidRouteRange
	seriesSetWrapZeroRouteRange
)

type querySelectStrategy struct {
	queryStart  time.Time
	queryEnd    time.Time
	weightStart time.Time
	weightEnd   time.Time
	wrapKind    seriesSetWrapKind
}

func validTimeRange(start, end time.Time) bool {
	return !start.IsZero() && !end.IsZero() && start.Before(end)
}

// calcSelectStrategy 统一计算单条路由在 selectFn 中的查询策略：
// queryStart/queryEnd 是实际下发给 TSDB 的查询范围；weightStart/weightEnd 只用于 avg 类多路由加权；
// wrapKind 决定返回的 SeriesSet 是否携带合法 route 生效范围，或标记为仅用于迁移重叠查询的零权重结果。
func (q *Query) calcSelectStrategy(start, end time.Time) (querySelectStrategy, bool) {
	return q.calcSelectStrategyWithMergeContext(start, end, "")
}

func (q *Query) calcSelectStrategyWithMergeContext(start, end time.Time, mergeFunc string) (querySelectStrategy, bool) {
	strategy := querySelectStrategy{
		queryStart:  start,
		queryEnd:    end,
		weightStart: start,
		weightEnd:   end,
	}
	if q == nil {
		return strategy, false
	}

	hasRouteQueryRange := validTimeRange(q.queryStart, q.queryEnd)
	if hasRouteQueryRange {
		// 分段路由只用 route 查询时间段判断本路是否相关，不裁剪 SelectHints 的 range/lookback 扩展。
		if !start.Before(q.queryEnd) || !q.queryStart.Before(end) {
			return strategy, false
		}
	}

	if validTimeRange(q.start, q.end) {
		// 权重使用 route 真实生效时间段，而不是本次查询扩展范围，避免跨切换点 bucket 权重失真。
		strategy.weightStart = q.start
		strategy.weightEnd = q.end
		if strings.EqualFold(mergeFunc, function.AvgOT) && validTimeRange(q.queryStart, q.queryEnd) &&
			q.queryStart.Before(q.start) {
			// avg_over_time 的 evaluation timestamp 对应向后统计窗口 [t-range, t)。
			// 当 routeStart 被用户查询 start 裁剪时，首个 evaluation 点的有效窗口在 routeStart 前面，
			// 需要使用 route 查询窗口起点参与权重，避免首个 bucket 被当成零权重丢弃。
			strategy.weightStart = q.queryStart
		}
		strategy.wrapKind = seriesSetWrapValidRouteRange
	} else if hasRouteQueryRange {
		// 只有 route 查询扩展范围、没有真实生效范围时，说明这是迁移 overlap-only 路由。
		strategy.wrapKind = seriesSetWrapZeroRouteRange
	}

	return strategy, true
}
