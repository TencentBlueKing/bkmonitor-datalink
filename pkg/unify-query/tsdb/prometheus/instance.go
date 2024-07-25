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
	"sync"
	"time"

	"github.com/panjf2000/ants/v2"
	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	servicePromql "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/actor"
)

// Instance prometheus 查询引擎
type Instance struct {
	ctx          context.Context
	instanceType string

	lookBackDelta time.Duration

	queryStorage storage.Queryable

	engine *promql.Engine
}

// NewInstance 初始化引擎
func NewInstance(ctx context.Context, engine *promql.Engine, queryStorage storage.Queryable, lookBackDelta time.Duration) *Instance {
	return &Instance{
		ctx:           ctx,
		engine:        engine,
		queryStorage:  queryStorage,
		lookBackDelta: lookBackDelta,
	}
}

var _ tsdb.Instance = (*Instance)(nil)

// GetInstanceType 获取引擎类型
func (i *Instance) GetInstanceType() string {
	if i.instanceType != "" {
		return i.instanceType
	} else {
		return consul.PrometheusStorageType
	}
}

// QueryRaw 查询原始数据
func (i *Instance) QueryRaw(
	ctx context.Context,
	query *metadata.Query,
	hints *storage.SelectHints,
	matchers ...*labels.Matcher,
) storage.SeriesSet {
	return nil
}

// QueryRange 查询范围数据
func (i *Instance) QueryRange(
	ctx context.Context, stmt string,
	start, end time.Time, step time.Duration,
) (promql.Matrix, error) {

	var (
		err error
	)

	ctx, span := trace.NewSpan(ctx, "prometheus-query-range")
	defer span.End(&err)

	span.Set("query-promql", stmt)
	span.Set("query-start", start.String())
	span.Set("query-end", end.String())
	span.Set("query-step", step.String())
	span.Set("query-opts-look-back-delta", i.lookBackDelta.String())
	opt := &promql.QueryOpts{
		LookbackDelta: i.lookBackDelta,
	}
	query, err := i.engine.NewRangeQuery(i.queryStorage, opt, stmt, start, end, step)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}
	result := query.Exec(ctx)

	if result.Err != nil {
		log.Errorf(ctx, result.Err.Error())
		return nil, result.Err
	}

	for _, err = range result.Warnings {
		log.Errorf(ctx, err.Error())
		return nil, err
	}
	matrix, err := i.DistributedQuery(ctx, stmt, start, end, step)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	return matrix, nil
}

func (i *Instance) DistributedQuery(ctx context.Context, stmt string,
	start, end time.Time, step time.Duration) (promql.Matrix, error) {
	if qrStorage, ok := i.queryStorage.(*QueryRangeStorage); ok {
		if customQuerier, ok := qrStorage.InnerQuerier.(*Querier); ok {
			QueryMaxRouting, ok := ctx.Value("QueryMaxRouting").(int)
			if !ok {
				log.Errorf(ctx, ErrQueryMaxRoutingNotFound.Error())
				return nil, ErrQueryMaxRoutingNotFound
			}

			SingleflightTimeout, ok := ctx.Value("SingleflightTimeout").(time.Duration)
			if !ok {
				log.Errorf(ctx, ErrQuerySingleflightTimeoutNotFound.Error())
				return nil, ErrQuerySingleflightTimeoutNotFound
			}

			LookBackDelta, ok := ctx.Value("LookBackDelta").(time.Duration)
			if !ok {
				log.Errorf(ctx, ErrQueryLookBackDeltaNotFound.Error())
				return nil, ErrQueryLookBackDeltaNotFound
			}
			duration := end.Sub(start)
			if step.Seconds() == 0 {
				log.Errorf(ctx, ZeroStep.Error())
				return nil, ZeroStep
			}
			results := make([]promql.Vector, int(math.Ceil(duration.Seconds()/step.Seconds())))
			hints := customQuerier.hints
			windowSize := time.Duration(hints.Range) * time.Millisecond
			if windowSize == 0 {
				windowSize = time.Duration(hints.Step) * time.Millisecond
			}
			// 从父节点提取对应的窗口信息

			pool, _ := ants.NewPool(10)
			defer pool.Release()
			var globalError error
			var wg sync.WaitGroup
			for _, result := range customQuerier.QueryResult {
				resultCopy := result // 创建 result 的副本 这里其实应该是根据 map[指标名]进行处理的
				wg.Add(1)
				pool.Submit(func() {
					defer wg.Done()
					var innerWg sync.WaitGroup
					for t, idx := start, 0; t.Before(end); t, idx = t.Add(step), idx+1 {
						idxCopy, tCopy := idx, t
						innerWg.Add(1)
						windowEnd := t.Add(step)
						windowStart := windowEnd.Add(-windowSize)
						pool.Submit(func() {
							defer innerWg.Done()
							filterQR := filterTimeSeriesByWindow(*resultCopy, windowStart, windowEnd)
							if filterQR != nil {
								engine := servicePromql.EnginePool.Get().(*promql.Engine)
								defer servicePromql.EnginePool.Put(engine)
								instance := NewInstance(ctx, engine, &actor.ActorQueryRangeStorage{
									QueryMaxRouting: QueryMaxRouting,
									Timeout:         SingleflightTimeout,
									Data:            filterQR,
								}, LookBackDelta)
								res, err := instance.Query(ctx, stmt, tCopy)
								if err != nil {
									log.Errorf(ctx, err.Error())
									globalError = err
								}
								results[idxCopy] = res
							}
						})
					}
					defer innerWg.Wait()
				})
			}

			wg.Wait()
			if globalError != nil {
				return nil, globalError
			}

			matrix := mergeVectorsToMatrix(results)
			return matrix, nil

		} else {
			fmt.Println("InnerQuerier is not of type *Querier")
			return nil, errors.New("InnerQuerier is not of type *Querier")
		}
	} else {
		fmt.Println("queryStorage is not of type *QueryRangeStorage")
		return nil, errors.New("queryStorage is not of type *QueryRangeStorage")
	}
}

// Query instant 查询
func (i *Instance) Query(
	ctx context.Context, qs string,
	end time.Time,
) (promql.Vector, error) {
	var (
		err error
	)

	ctx, span := trace.NewSpan(ctx, "prometheus-query-range")
	defer span.End(&err)

	span.Set("query-promql", qs)
	span.Set("query-end", end.String())
	opt := &promql.QueryOpts{
		LookbackDelta: i.lookBackDelta,
	}
	span.Set("query-opts-look-back-delta", i.lookBackDelta.String())
	query, err := i.engine.NewInstantQuery(i.queryStorage, opt, qs, end)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}
	result := query.Exec(ctx)
	if result.Err != nil {
		log.Errorf(ctx, result.Err.Error())
		return nil, result.Err
	}
	for _, err = range result.Warnings {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	vector, err := result.Vector()
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	return vector, nil
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	return nil, nil
}

func (i *Instance) LabelNames(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	return nil, nil
}

func (i *Instance) LabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	return nil, nil
}

func (i *Instance) Series(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
	return nil
}

func (i *Instance) QueryRawWithPromResult(
	ctx context.Context,
	query *metadata.Query,
	hints *storage.SelectHints,
	matchers ...*labels.Matcher,
) (storage.SeriesSet, *prompb.QueryResult) {
	return nil, nil
}
