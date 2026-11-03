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
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	ants "github.com/panjf2000/ants/v2"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/storage"
	promRemote "github.com/prometheus/prometheus/storage/remote"
	"github.com/prometheus/prometheus/tsdb/chunkenc"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// 区间聚合不具有样本时间。仅迁移路由上的无时间分桶查询进入独立合并路径。
func (ql QueryList) hasRoutedRangeAggregate() bool {
	routed := false
	for _, q := range ql {
		if len(q.qry.Aggregates) == 0 {
			return false
		}
		for _, agg := range q.qry.Aggregates {
			if agg.HasTimeBucket(q.qry.StorageType) {
				return false
			}
		}
		routed = routed || validTimeRange(q.queryStart, q.queryEnd)
	}
	return routed
}

type rangeAggregateQuery struct {
	query        *Query
	start, end   time.Time
	endExclusive bool
}

// RouteQueryStart/End 是候选查询范围与原始半开路由区间的交集，未越过切换边界。
// 不能使用 RouteStart/End：查询恰好结束于切换点时，新路由的权重区间为空。
func (q *Query) rangeAggregateQuery(start, end time.Time) (rangeAggregateQuery, bool) {
	plan := rangeAggregateQuery{query: q, start: start, end: end}
	if validTimeRange(q.queryStart, q.queryEnd) {
		if q.queryStart.After(plan.start) {
			plan.start = q.queryStart
		}
		if !q.queryEnd.After(plan.end) {
			plan.end = q.queryEnd
			plan.endExclusive = true
		}
	}
	return plan, plan.start.Before(plan.end) || (plan.start.Equal(plan.end) && !plan.endExclusive)
}

type rangeAggregateValue struct {
	labels labels.Labels
	value  float64
	count  float64
}

type rangeAggregateResult struct {
	values map[string]rangeAggregateValue
	err    error
}

func (q *Querier) selectRangeAggregate(ctx context.Context, queries QueryList) storage.SeriesSet {
	qp := metadata.GetQueryParams(ctx)
	var plans []rangeAggregateQuery
	for _, query := range queries {
		if plan, ok := query.rangeAggregateQuery(qp.Start, qp.End); ok {
			plans = append(plans, plan)
		}
	}
	if len(plans) == 0 {
		return storage.EmptySeriesSet()
	}
	method := strings.ToLower(plans[0].query.qry.Aggregates.OuterAggName())
	for _, plan := range plans {
		query := plan.query.qry
		// 未接入精确路由边界的后端不能绕过原过滤后悄悄重复统计。
		if query.StorageType != metadata.ElasticsearchStorageType &&
			!(query.StorageType == metadata.BkSqlStorageType && query.Measurement == metadata.DorisStorageType) {
			return storage.ErrSeriesSet(fmt.Errorf("range aggregation across storage routes is unsupported for %s", query.StorageType))
		}
		if len(plans) > 1 {
			if len(query.Aggregates) != 1 || strings.ToLower(query.Aggregates[0].Name) != method {
				return storage.ErrSeriesSet(fmt.Errorf("range aggregation across storage routes requires one matching aggregate"))
			}
			switch method {
			case function.Sum, function.Count, function.Min, function.Max, function.Avg:
			default:
				return storage.ErrSeriesSet(fmt.Errorf("range aggregate %s cannot be merged across storage routes without intermediate state", method))
			}
		}
	}

	pool, err := ants.NewPool(q.maxRouting)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}
	defer pool.Release()
	results := make([]rangeAggregateResult, len(plans))
	var wg sync.WaitGroup
	for i, plan := range plans {
		wg.Add(1)
		err := pool.Submit(func() {
			defer wg.Done()
			// 一个路由的 sum/count 在同一个并发槽内执行；任一失败时整路丢弃。
			results[i].values, results[i].err = plan.execute(ctx, method == function.Avg && len(plans) > 1)
		})
		if err != nil {
			results[i].err = err
			wg.Done()
		}
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return storage.ErrSeriesSet(err)
	}

	merged := make(map[string]rangeAggregateValue)
	var failures []error
	successes := 0
	for _, result := range results {
		if result.err != nil {
			failures = append(failures, result.err)
			continue
		}
		successes++
		for key, value := range result.values {
			previous, exists := merged[key]
			if !exists {
				merged[key] = value
				continue
			}
			switch method {
			case function.Min:
				previous.value = math.Min(previous.value, value.value)
			case function.Max:
				previous.value = math.Max(previous.value, value.value)
			default:
				previous.value += value.value
			}
			previous.count += value.count
			merged[key] = previous
		}
	}
	if len(failures) > 0 {
		var messages []string
		for _, err := range failures {
			messages = append(messages, err.Error())
		}
		detail := strings.Join(messages, "; ")
		if successes == 0 {
			return storage.ErrSeriesSet(metadata.ErrorWithCauses(detail, failures...))
		}
		message := "查询时序数据部分失败: " + detail
		if status := metadata.GetStatus(ctx); status != nil && status.Message != "" {
			message = status.Message + "; " + message
		}
		metadata.SetStatus(ctx, metadata.QueryTsPartial, message)
	}
	result := &prompb.QueryResult{}
	for _, value := range merged {
		if method == function.Avg && len(plans) > 1 {
			if value.count == 0 {
				continue
			}
			value.value /= value.count
		}
		series := &prompb.TimeSeries{Samples: []prompb.Sample{{Timestamp: qp.Start.UnixMilli(), Value: value.value}}}
		for _, label := range value.labels {
			series.Labels = append(series.Labels, prompb.Label{Name: label.Name, Value: label.Value})
		}
		result.Timeseries = append(result.Timeseries, series)
	}
	return promRemote.FromQueryResult(true, result)
}

func (p rangeAggregateQuery) execute(ctx context.Context, average bool) (map[string]rangeAggregateValue, error) {
	query := *p.query.qry
	query.Aggregates = query.Aggregates.Copy()
	query.IsRangeAggregation = true
	query.RangeEndExclusive = p.endExclusive
	if average {
		query.Aggregates[0].Name = function.Sum
	}
	values, err := readRangeAggregate(p.query.instance.QuerySeriesSet(ctx, &query, p.start, p.end))
	if err != nil || !average {
		return values, err
	}
	query.Aggregates[0].Name = function.Count
	counts, err := readRangeAggregate(p.query.instance.QuerySeriesSet(ctx, &query, p.start, p.end))
	if err != nil {
		return nil, err
	}
	for key, value := range values {
		count, ok := counts[key]
		if !ok {
			return nil, fmt.Errorf("range aggregate sum/count groups differ")
		}
		value.count = count.value
		values[key] = value
	}
	if len(values) != len(counts) {
		return nil, fmt.Errorf("range aggregate sum/count groups differ")
	}
	return values, nil
}

// 每个维度只允许一个部分值，避免将意外的时间序列按区间聚合吞掉。
func readRangeAggregate(set storage.SeriesSet) (map[string]rangeAggregateValue, error) {
	if set == nil {
		return nil, fmt.Errorf("query series set is nil")
	}
	values := make(map[string]rangeAggregateValue)
	for set.Next() {
		series := set.At()
		iterator := series.Iterator(nil)
		sampleType := iterator.Next()
		if sampleType == chunkenc.ValNone {
			if err := iterator.Err(); err != nil {
				return nil, err
			}
			continue
		}
		if sampleType != chunkenc.ValFloat {
			return nil, fmt.Errorf("range aggregate requires a scalar value")
		}
		timestamp, value := iterator.At()
		for sampleType = iterator.Next(); sampleType != chunkenc.ValNone; sampleType = iterator.Next() {
			if sampleType != chunkenc.ValFloat {
				return nil, fmt.Errorf("range aggregate requires a scalar value")
			}
			// 兼容解析结果中完全相同的重复 bucket；不同时间或值仍不是区间聚合。
			nextTime, nextValue := iterator.At()
			if nextTime != timestamp || nextValue != value {
				return nil, fmt.Errorf("range aggregate returned multiple samples")
			}
		}
		if err := iterator.Err(); err != nil {
			return nil, err
		}
		key := series.Labels().String()
		if _, exists := values[key]; exists {
			return nil, fmt.Errorf("range aggregate returned duplicate groups")
		}
		values[key] = rangeAggregateValue{labels: series.Labels().Copy(), value: value}
	}
	if err := set.Err(); err != nil {
		return nil, err
	}
	if warnings := set.Warnings(); len(warnings) > 0 {
		return nil, fmt.Errorf("range aggregate warnings: %v", warnings)
	}
	return values, nil
}
