// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/prometheus/promql"
	prom "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// Params
type Params struct {
	MaxSamples           int
	Timeout              time.Duration
	LookbackDelta        time.Duration
	EnableNegativeOffset bool
	EnableAtModifier     bool
}

// 指标对应关系结构
type MetricInfo struct {
	Metric      string
	Database    string
	Measurement string
}

var GlobalEngine *promql.Engine

// NewEngine
func NewEngine(params *Params) {
	// engine的内容里有指标注册操作，所以无法重复注册，所以其参数不能改变
	// 且engine内部成员全为私有，也无法进行修改
	if GlobalEngine != nil {
		return
	}
	GlobalEngine = prom.NewEngine(prom.EngineOpts{
		Reg:                  prometheus.DefaultRegisterer,
		MaxSamples:           params.MaxSamples,
		Timeout:              params.Timeout,
		LookbackDelta:        params.LookbackDelta,
		EnableNegativeOffset: params.EnableNegativeOffset,
		EnableAtModifier:     params.EnableAtModifier,
		NoStepSubqueryIntervalFn: func(rangeMillis int64) int64 {
			return GetDefaultStep().Milliseconds()
		},
	})
}

// 设置promEngine默认步长
var defaultStep = time.Minute

// SetDefaultStep
func SetDefaultStep(t time.Duration) {
	defaultStep = t
}

// GetDefaultStep
func GetDefaultStep() time.Duration {
	if defaultStep == 0 {
		return time.Minute
	}
	return defaultStep
}

// Query
func Query(ctx context.Context, q string, now time.Time) (*Tables, error) {

	querier := &InfluxDBStorage{}
	opt := &promql.QueryOpts{}
	query, err := GlobalEngine.NewInstantQuery(querier, opt, q, now)
	if err != nil {
		return nil, err
	}
	result := query.Exec(ctx)

	vector, err := result.Vector()
	if err != nil {
		return nil, err
	}

	tables := NewTables()
	for index, sample := range vector {
		tables.Add(NewTableWithSample(index, sample, nil))
	}

	return tables, nil
}

// QueryRange
func QueryRange(ctx context.Context, q string, start, end time.Time, interval time.Duration) (*Tables, error) {
	var (
		duration time.Duration
		err      error
	)

	ctx, span := trace.NewSpan(ctx, "promql-query-range")
	defer span.End(&err)

	startQuery := time.Now()

	querier := &InfluxDBStorage{}
	// influxdb会包括最后一个点 [start, end], 而promql是 [start, end)后面是开区间，这里保持对齐，故意-1ns

	endTime := end.Add(-1 * time.Millisecond)

	opt := &promql.QueryOpts{}
	query, err := GlobalEngine.NewRangeQuery(querier, opt, q, start, endTime, interval)
	if err != nil {
		return nil, err
	}
	result := query.Exec(ctx)

	// 计算查询时间
	startAnaylize := time.Now()
	duration = startAnaylize.Sub(startQuery)
	log.Debugf(ctx, "prom range query:%s, query cost:%s", q, duration)

	err = result.Err
	if result.Err != nil {
		log.Errorf(ctx, "query: %s, start: %s, end: %s, interval: %s get error:%s", q, start.String(), end.String(), interval.String(), err)
		return nil, err
	}
	for _, err = range result.Warnings {
		log.Errorf(ctx, "query:%s get warning:%s", q, err)
		return nil, err
	}

	matrix, err := result.Matrix()
	if err != nil {
		return nil, err
	}

	tables := NewTables()
	for index, series := range matrix {
		tables.Add(NewTable(index, series))
	}

	// 计算分析时间
	duration = time.Since(startAnaylize)
	log.Debugf(ctx, "prom range query:%s, anaylize cost:%s", q, time.Since(startAnaylize))

	return tables, nil
}
