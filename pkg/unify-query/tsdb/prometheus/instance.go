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
	"sync"
	"time"

	ants "github.com/panjf2000/ants/v2"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/errors"
)

// Instance prometheus 查询引擎
type Instance struct {
	tsdb.DefaultInstance

	ctx          context.Context
	instanceType string

	lookBackDelta time.Duration

	queryStorage storage.Queryable

	maxRouting int
	engine     *promql.Engine
}

// NewInstance 初始化引擎
func NewInstance(ctx context.Context, engine *promql.Engine, queryStorage storage.Queryable, lookBackDelta time.Duration, maxRouting int) *Instance {
	return &Instance{
		ctx:           ctx,
		engine:        engine,
		queryStorage:  queryStorage,
		lookBackDelta: lookBackDelta,
		maxRouting:    maxRouting,
	}
}

var _ tsdb.Instance = (*Instance)(nil)

func (i *Instance) Check(ctx context.Context, promql string, start, end time.Time, step time.Duration) string {
	return ""
}

// GetInstanceType 获取引擎类型
func (i *Instance) InstanceType() string {
	if i.instanceType != "" {
		return i.instanceType
	} else {
		return consul.PrometheusStorageType
	}
}

// QuerySeriesSet 给 PromEngine 提供查询接口
func (i *Instance) QuerySeriesSet(
	ctx context.Context,
	query *metadata.Query,
	start, end time.Time,
) storage.SeriesSet {
	return nil
}

// QueryRange 查询范围数据
func (i *Instance) DirectQueryRange(
	ctx context.Context, stmt string,
	start, end time.Time, step time.Duration,
) (promql.Matrix, bool, error) {
	var err error

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
		log.Errorf(ctx, "%s [%s] | 操作: 创建PromQL范围查询 | 错误: %s | 解决: 检查PromQL语法和时间范围", errors.ErrQueryParseInvalidSQL, errors.GetErrorCode(errors.ErrQueryParseInvalidSQL), err.Error())
		return nil, false, err
	}
	result := query.Exec(ctx)
	if result.Err != nil {
		log.Errorf(ctx, "%s [%s] | 操作: Prometheus查询执行 | 错误: %s | 解决: 检查PromQL语法和数据源", errors.ErrBusinessQueryExecution, errors.GetErrorCode(errors.ErrBusinessQueryExecution), result.Err.Error())
		return nil, false, result.Err
	}

	for _, err = range result.Warnings {
		log.Warnf(ctx, "%s [%s] | 操作: Prometheus查询警告 | 警告: %s | 建议: 检查查询优化", "查询警告", "WARN", err.Error())
		return nil, false, err
	}

	matrix, err := result.Matrix()
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, false, err
	}

	return matrix, false, nil
}

// Query instant 查询
func (i *Instance) DirectQuery(
	ctx context.Context, qs string,
	end time.Time,
) (promql.Vector, error) {
	var err error

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

func (i *Instance) DirectLabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	// TODO implement me
	panic("implement me")
}

func (i *Instance) DirectLabelValues(ctx context.Context, name string, start, end time.Time, limit int, matchers ...*labels.Matcher) (list []string, err error) {
	res := set.New[string]()

	ctx, span := trace.NewSpan(ctx, "prometheus-direct-label-values")
	defer span.End(&err)

	span.Set("name", name)
	span.Set("start", start)
	span.Set("end", end)
	span.Set("limit", limit)
	span.Set("matchers", matchers)

	metricName := function.MatcherToMetricName(matchers...)
	if metricName == "" {
		return list, err
	}

	p, _ := ants.NewPool(i.maxRouting)
	defer p.Release()

	var wg sync.WaitGroup
	queryReference := metadata.GetQueryReference(ctx)

	queryReference.Range(metricName, func(qry *metadata.Query) {
		wg.Add(1)
		qry.Size = limit
		_ = p.Submit(func() {
			defer func() {
				wg.Done()
			}()
			instance := GetTsDbInstance(ctx, qry)
			if instance == nil {
				return
			}

			lbl, lvErr := instance.QueryLabelValues(ctx, qry, name, start, end)
			if lvErr == nil {
				for _, l := range lbl {
					res.Add(l)
				}
			}
		})
	})

	wg.Wait()
	list = res.ToArray()
	return list, err
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	return nil, nil
}

func (i *Instance) QueryLabelNames(ctx context.Context, query *metadata.Query, start, end time.Time) ([]string, error) {
	return nil, nil
}

func (i *Instance) QueryLabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time) ([]string, error) {
	return nil, nil
}

func (i *Instance) QuerySeries(ctx context.Context, query *metadata.Query, start, end time.Time) ([]map[string]string, error) {
	return nil, nil
}
