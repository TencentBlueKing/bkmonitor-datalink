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
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
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

func (i *Instance) Check(ctx context.Context, promql string, start, end time.Time, step time.Duration) string {
	return ""
}

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
	start, end time.Time,
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

	log.Infof(ctx, "prometheus-query-range")
	log.Infof(ctx, "promql: %s", stmt)
	log.Infof(ctx, "start: %s", start.String())
	log.Infof(ctx, "end: %s", end.String())
	log.Infof(ctx, "step: %s", step.String())

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

	matrix, err := result.Matrix()
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	return matrix, nil
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

	log.Infof(ctx, "prometheus-query")
	log.Infof(ctx, "promql: %s", qs)
	log.Infof(ctx, "end: %s", end.String())

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
