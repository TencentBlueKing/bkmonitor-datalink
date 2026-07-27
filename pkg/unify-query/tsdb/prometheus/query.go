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
	instance     tsdb.Instance
	qry          *metadata.Query
	start        time.Time
	end          time.Time
	endInclusive bool
	queryStart   time.Time
	queryEnd     time.Time
}

type QueryList []*Query

// allowRouteStartBoundaryBucket 判断当前 query 的 routeStart 前是否存在同一逻辑数据源的相邻时间路由。
// 同一个 reference 可能展开成多个独立 RT，不能用 QueryList 总长度判断是否为单路 route。
func (ql QueryList) allowRouteStartBoundaryBucket(target *Query) bool {
	if target == nil {
		return false
	}

	for _, other := range ql {
		if other == nil || other == target || !isSameRouteSource(target, other) {
			continue
		}
		if validTimeRange(other.start, other.end) && other.end.Equal(target.start) {
			return false
		}
		if validTimeRange(other.queryStart, other.queryEnd) && other.queryEnd.Equal(target.start) {
			return false
		}
	}
	return true
}

func isSameRouteSource(a, b *Query) bool {
	if a == nil || b == nil || a.qry == nil || b.qry == nil {
		return true
	}
	if a.qry.TableID == "" || b.qry.TableID == "" {
		// 缺少逻辑 RT 身份时保持保守语义，避免把真实相邻 route 误判为独立 source。
		return true
	}
	return a.qry.DataSource == b.qry.DataSource &&
		a.qry.TableID == b.qry.TableID &&
		a.qry.Field == b.qry.Field
}

func (ql QueryList) mergeFuncName(hints *storage.SelectHints) string {
	outerAggName := ql.outerAggName()
	if hints != nil && hints.Func != "" {
		hintFunc := strings.ToLower(hints.Func)
		// last_over_time 只是为了扩展回看窗口，真实的存储侧窗口聚合仍应决定多路由合并方式。
		if hintFunc == "last_over_time" {
			if name := ql.windowedStorageAggName(); name != "" {
				return name
			}
		}
		if hintFunc == "last_over_time" && function.IsAvgFunc(outerAggName) {
			return outerAggName
		}
		// PromQL *_over_time 下推到存储后，返回的是以窗口起点为 timestamp 的普通聚合 bucket。
		// 合并和 route 过滤必须使用匹配的存储聚合函数，不能继续按 evaluation instant 的后向窗口处理。
		if isRangeBucketFunc(hintFunc) {
			if name := ql.matchedWindowedStorageAggName(hintFunc); name != "" {
				return name
			}
		}
		return hints.Func
	}

	return outerAggName
}

func (ql QueryList) matchedWindowedStorageAggName(hintFunc string) string {
	for _, query := range ql {
		if query == nil || query.qry == nil {
			continue
		}
		aggregates := query.qry.Aggregates
		for i := len(aggregates) - 1; i >= 0; i-- {
			agg := aggregates[i]
			if agg.Window <= 0 || !isSameBucketFunc(hintFunc, strings.ToLower(agg.Name)) {
				continue
			}
			if name := storageBucketFuncName(strings.ToLower(agg.Name)); name != "" {
				return name
			}
		}
	}
	return ""
}

func (ql QueryList) windowedStorageAggName() string {
	for _, query := range ql {
		if query == nil || query.qry == nil {
			continue
		}
		aggregates := query.qry.Aggregates
		for i := len(aggregates) - 1; i >= 0; i-- {
			agg := aggregates[i]
			if agg.Window <= 0 {
				continue
			}
			if name := storageBucketFuncName(strings.ToLower(agg.Name)); name != "" {
				return name
			}
		}
	}
	return ""
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
// 优先使用下推聚合里与当前合并函数匹配的窗口。Prometheus 原生 *_over_time
// 没有对应下推聚合时，存储返回的是原始样本，必须返回 0 按 timestamp 过滤，
// 不能把 SelectHints.Range 误当成存储 bucket 宽度。
func (ql QueryList) mergeBucketDuration(name string, fallback time.Duration) time.Duration {
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

	if isPlainBucketFunc(name) {
		return 0
	}
	if name == function.Avg || name == function.Mean {
		return 0
	}
	if isRangeBucketFunc(name) {
		return 0
	}
	return fallback
}

func storageBucketFuncName(name string) string {
	switch name {
	case function.Sum, function.Count, function.Min, function.Max, function.Avg, function.Mean:
		return name
	case function.SumOT:
		return function.Sum
	case function.CountOT:
		return function.Count
	case function.MinOT:
		return function.Min
	case function.MaxOT:
		return function.Max
	case function.AvgOT:
		return function.Avg
	default:
		return ""
	}
}

func isSameBucketFunc(a, b string) bool {
	if a == b {
		return true
	}
	if storageBucketFuncName(a) == storageBucketFuncName(b) && storageBucketFuncName(a) != "" {
		return true
	}
	return function.IsAvgFunc(a) && function.IsAvgFunc(b)
}

func isPlainBucketFunc(name string) bool {
	switch name {
	case function.Sum, function.Count, function.Min, function.Max:
		return true
	default:
		return false
	}
}

func isRangeBucketFunc(name string) bool {
	switch name {
	case function.AvgOT, function.SumOT, function.CountOT, function.MinOT, function.MaxOT:
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
	if q == nil {
		return querySelectStrategy{
			queryStart:  start,
			queryEnd:    end,
			weightStart: start,
			weightEnd:   end,
		}, false
	}

	strategy := querySelectStrategy{
		queryStart:  start,
		queryEnd:    end,
		weightStart: start,
		weightEnd:   end,
	}

	hasRouteQueryRange := validTimeRange(q.queryStart, q.queryEnd)
	hasEffectiveRouteRange := validTimeRange(q.start, q.end)
	if hasRouteQueryRange && hasEffectiveRouteRange {
		// 分段路由只用 route 查询时间段判断本路是否相关，不裁剪 SelectHints 的 range/lookback 扩展。
		if !start.Before(q.queryEnd) || !q.queryStart.Before(end) {
			return strategy, false
		}
	}

	if hasEffectiveRouteRange {
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
