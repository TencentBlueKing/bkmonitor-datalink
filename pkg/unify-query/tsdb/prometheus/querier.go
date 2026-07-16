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
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ants "github.com/panjf2000/ants/v2"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/storage"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	ReferenceName = "a"
)

type QueryRangeStorage struct {
	QueryMaxRouting int
	Timeout         time.Duration
}

func (s *QueryRangeStorage) Querier(ctx context.Context, min, max int64) (storage.Querier, error) {
	return NewQuerier(ctx, time.Unix(min, 0), time.Unix(max, 0), s.QueryMaxRouting, s.Timeout), nil
}

func NewQuerier(ctx context.Context, min, max time.Time, maxRouting int, timeout time.Duration) *Querier {
	return &Querier{
		ctx:        ctx,
		min:        min,
		max:        max,
		maxRouting: maxRouting,
		timeout:    timeout,
	}
}

type Querier struct {
	ctx        context.Context
	min        time.Time
	max        time.Time
	maxRouting int
	timeout    time.Duration
}

// checkCtxDone
func (q *Querier) checkCtxDone() bool {
	select {
	case <-q.ctx.Done():
		return true
	default:
		return false
	}
}

func (q *Querier) getQueryList(matchers []*labels.Matcher) (string, QueryList) {
	var (
		ctx           = q.ctx
		referenceName string
		queryList     QueryList
		err           error
	)

	ctx, span := trace.NewSpan(ctx, "querier-get-query-list")
	defer span.End(&err)

	queryReference := metadata.GetQueryReference(ctx)
	for _, m := range matchers {
		if m.Name == labels.MetricName {
			referenceName = m.Value
			break
		}
	}

	queryList = make(QueryList, 0)
	queryReference.Range(referenceName, func(qry *metadata.Query) {
		instance := GetTsDbInstance(ctx, qry)
		if instance == nil {
			metadata.NewMessage(
				metadata.MsgQueryTs,
				"查询实例为空",
			).Warn(ctx)
			return
		}

		queryList = append(queryList, &Query{
			instance:   instance,
			qry:        qry,
			start:      qry.RouteStart,
			end:        qry.RouteEnd,
			queryStart: qry.RouteQueryStart,
			queryEnd:   qry.RouteQueryEnd,
		})
	})

	return referenceName, queryList
}

// selectFn 获取原始数据
func (q *Querier) selectFn(hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	var (
		ctx context.Context

		referenceName string

		set storage.SeriesSet

		successedPaths atomic.Uint32

		setCh    = make(chan storage.SeriesSet, 1)
		recvDone = make(chan struct{})

		errorMessage strings.Builder
		lock         sync.Mutex

		wg  sync.WaitGroup
		err error
	)

	ctx, span := trace.NewSpan(q.ctx, "prometheus-querier-select-fn")
	defer span.End(&err)

	qp := metadata.GetQueryParams(ctx)

	span.Set("max-routing", q.maxRouting)

	referenceName, queryList := q.getQueryList(matchers)
	span.Set("reference_name", referenceName)
	mergeFunc := queryList.mergeFuncName(hints)
	var rangeSelector time.Duration
	if hints != nil && hints.Range > 0 {
		rangeSelector = time.Duration(hints.Range) * time.Millisecond
	}
	bucketDuration := queryList.mergeBucketDuration(mergeFunc, qp.Step, rangeSelector)
	span.Set("merge_func", mergeFunc)
	span.Set("merge_bucket_duration", bucketDuration)

	go func() {
		defer func() {
			recvDone <- struct{}{}
		}()
		var sets []storage.SeriesSet
		for s := range setCh {
			if s != nil {
				sets = append(sets, s)
			}
		}

		// avg 类函数在带 route 时间段时会使用聚合 bucket 宽度计算覆盖时长；其它函数不受 bucket 宽度影响。
		if len(sets) == 1 {
			sets[0] = function.NewRouteRangeFilterSeriesSet(sets[0], mergeFunc, bucketDuration)
		}
		set = storage.NewMergeSeriesSet(sets, function.NewMergeSeriesSetWithFuncAndSortByStep(mergeFunc, bucketDuration))
	}()

	p, _ := ants.NewPool(q.maxRouting)
	defer p.Release()

	// 统一收敛子路由错误，最终用于 partial status 或全失败报错。
	recordQueryError := func(queryErr error) {
		if queryErr == nil {
			return
		}
		lock.Lock()
		errorMessage.WriteString(fmt.Sprintf("query error: %s ", queryErr.Error()))
		lock.Unlock()
	}

	for i, query := range queryList {
		wg.Add(1)
		err = p.Submit(func() {
			defer func() {
				wg.Done()
			}()

			span.Set(fmt.Sprintf("query_%d_instance_type", i), query.instance.InstanceType())
			span.Set(fmt.Sprintf("query_%d_qry_source", i), query.qry.SourceType)
			span.Set(fmt.Sprintf("query_%d_qry_db", i), query.qry.DB)
			span.Set(fmt.Sprintf("query_%d_qry_vmrt", i), query.qry.VmRt)

			var (
				startTime time.Time
				endTime   time.Time
			)
			if qp.IsReference {
				startTime = qp.Start
				endTime = qp.End
			} else {
				// Prometheus SelectHints.Start/End 是毫秒时间戳；UQ 的 qp.Start/End 是 time.Time，保留了纳秒精度。
				// 这里用 hints 决定 PromQL 实际需要的取数范围，再从 qp.Start/End 补回毫秒以下的纳秒尾数。
				startTime = function.MsIntMergeNs(hints.Start, qp.Start)
				endTime = function.MsIntMergeNs(hints.End, qp.End)
			}
			strategy, ok := query.calcSelectStrategyWithMergeContext(startTime, endTime, mergeFunc)
			if !ok {
				return
			}

			// 逐路查询：失败只记录，不立即中断其他路由；成功路由进入 merge 阶段。
			currentSet := query.instance.QuerySeriesSet(ctx, query.qry, strategy.queryStart, strategy.queryEnd)
			if currentSet == nil {
				recordQueryError(fmt.Errorf("query series set is nil"))
				return
			}
			if setErr := currentSet.Err(); setErr != nil {
				recordQueryError(setErr)
				return
			}

			successedPaths.Add(1)
			switch strategy.wrapKind {
			case seriesSetWrapValidRouteRange:
				metric.RouteSeriesWrapInc(ctx, metric.RouteSeriesWrapValid, mergeFunc)
				setCh <- function.NewTimeRangeSeriesSet(currentSet, strategy.weightStart, strategy.weightEnd)
			case seriesSetWrapZeroRouteRange:
				metric.RouteSeriesWrapInc(ctx, metric.RouteSeriesWrapZero, mergeFunc)
				setCh <- function.NewZeroTimeRangeSeriesSet(currentSet)
			default:
				metric.RouteSeriesWrapInc(ctx, metric.RouteSeriesWrapNone, mergeFunc)
				setCh <- currentSet
			}
		})
		if err != nil {
			recordQueryError(err)
			wg.Done()
		}
	}
	wg.Wait()

	close(setCh)
	<-recvDone

	// 多路并发后的兜底语义：
	// 1) 至少一路成功：返回成功数据，并通过 status 标记部分失败；
	// 2) 全部失败：保持历史行为，整体返回错误。
	if errorMessage.Len() > 0 {
		partialDetail := strings.TrimSpace(errorMessage.String())
		if successedPaths.Load() > 0 {
			span.Set("partial_errors", partialDetail)
			const warnPrefix = "查询时序数据部分失败: "
			fullMsg := warnPrefix + partialDetail
			if existing := metadata.GetStatus(ctx); existing != nil && existing.Message != "" {
				fullMsg = existing.Message + "; " + fullMsg
			}
			metadata.SetStatus(ctx, metadata.QueryTsPartial, fullMsg)
		} else {
			return storage.ErrSeriesSet(metadata.NewMessage(
				metadata.MsgQueryTs,
				"查询异常",
			).Error(ctx, errors.New(partialDetail)))
		}
	}

	return set
}

func (q *Querier) Select(_ bool, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	promise := make(chan storage.SeriesSet, 1)
	go func() {
		defer close(promise)
		if ok := q.checkCtxDone(); ok {
			promise <- storage.ErrSeriesSet(ErrContextDone)
			return
		}

		promise <- q.selectFn(hints, matchers...)
	}()

	return &lazySeriesSet{
		create: func() (s storage.SeriesSet, ok bool) {
			set, ok := <-promise
			if set.Err() != nil {
				err := metadata.NewMessage(
					metadata.MsgQueryTs,
					"查询异常",
				).Error(q.ctx, set.Err())
				return storage.ErrSeriesSet(err), false
			}
			if !ok {
				return storage.ErrSeriesSet(ErrChannelReceived), false
			}
			return set, set.Next()
		},
		set: nil,
	}
}

// LabelValues 返回可能的标签(维度)值。
// 在查询器的生命周期以外使用这些字符串是不安全的
func (q *Querier) LabelValues(name string, matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	var (
		ctx context.Context
		err error

		labelMap = make(map[string]struct{}, 0)
	)

	ctx, span := trace.NewSpan(q.ctx, "prometheus-querier-label-values")
	defer span.End(&err)

	_, queryList := q.getQueryList(matchers)
	for _, query := range queryList {
		lbl, err := query.instance.QueryLabelValues(ctx, query.qry, name, q.min, q.max)
		if err != nil {
			_ = metadata.NewMessage(
				metadata.MsgQueryTs,
				"查询异常",
			).Error(q.ctx, err)
			continue
		}
		for _, l := range lbl {
			labelMap[l] = struct{}{}
		}
	}

	lbn := make([]string, 0, len(labelMap))
	for k := range labelMap {
		lbn = append(lbn, k)
	}

	sort.Strings(lbn)
	return lbn, nil, nil
}

// LabelNames 以块中的排序顺序返回所有的唯一的标签
func (q *Querier) LabelNames(matchers ...*labels.Matcher) ([]string, storage.Warnings, error) {
	var (
		ctx context.Context
		err error

		labelMap = make(map[string]struct{}, 0)
	)

	ctx, span := trace.NewSpan(q.ctx, "prometheus-querier-label-names")
	defer span.End(&err)

	_, queryList := q.getQueryList(matchers)
	for _, query := range queryList {
		lbl, err := query.instance.QueryLabelNames(ctx, query.qry, q.min, q.max)
		if err != nil {
			return nil, nil, err
		}
		for _, lb := range lbl {
			labelMap[lb] = struct{}{}
		}
	}

	lbn := make([]string, 0, len(labelMap))
	for k := range labelMap {
		lbn = append(lbn, k)
	}

	sort.Strings(lbn)
	return lbn, nil, nil
}

// Close 释放查询器的所有资源
func (q *Querier) Close() error {
	return nil
}
