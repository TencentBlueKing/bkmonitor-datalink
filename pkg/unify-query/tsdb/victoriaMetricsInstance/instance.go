// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package victoriaMetricsInstance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

const (
	MetricLableName = "__name__"
)

// Instance vm 查询实例
type Instance struct {
	ctx context.Context

	address string

	timeout time.Duration
	curl    curl.Curl
}

// NewInstance 初始化查询引擎
func NewInstance(ctx context.Context, address string, timeout time.Duration, curl curl.Curl) *Instance {
	return &Instance{
		ctx:     ctx,
		address: address,
		timeout: timeout,
		curl:    curl,
	}
}

var _ tsdb.Instance = (*Instance)(nil)

func (i *Instance) urlPath(name, params string) string {
	urlPath := fmt.Sprintf("%s/%s", i.address, name)
	if params != "" {
		urlPath = fmt.Sprintf("%s?%s", urlPath, params)
	}
	return urlPath
}

// GetInstanceType 获取实例类型
func (i *Instance) GetInstanceType() string {
	return consul.VictoriaMetricsStorageType
}

// QueryRaw 查询原始数据
func (i *Instance) QueryRaw(
	ctx context.Context,
	query *metadata.Query,
	hints *storage.SelectHints,
	matchers ...*labels.Matcher,
) storage.SeriesSet {
	// 数据量过大，暂时不支持此查询
	return nil
}

func (i *Instance) matrixFormat(data *Data, span oleltrace.Span) promql.Matrix {
	seriesNum := 0
	pointNum := 0

	matrix := make(promql.Matrix, len(data.Data.Result))
	for index, series := range data.Data.Result {
		metricIndex := 0
		metric := make(labels.Labels, len(series.Metric))
		for name, value := range series.Metric {
			metric[metricIndex] = labels.Label{
				Name:  name,
				Value: value,
			}
			metricIndex++
		}

		var values [][]interface{}
		if data.Data.ResultType == "vector" {
			values = append(values, series.Value)
		} else {
			values = series.Values
		}

		points := make([]promql.Point, len(values))
		for idx := 0; idx < len(values); idx++ {
			if len(values[idx]) != 2 {
				continue
			}
			var (
				nt  int64
				nv  float64
				err error
			)

			// 时间从 float64 转换为 int64
			switch pt := values[idx][0].(type) {
			case float64:
				// 从秒转换为毫秒
				nt = int64(pt) * 1e3
			default:
				continue
			}

			// 值从 string 转换为 float64
			switch pv := values[idx][1].(type) {
			case string:
				nv, err = strconv.ParseFloat(pv, 64)
				if err != nil {
					continue
				}
			default:
				continue
			}
			points[idx] = promql.Point{
				T: nt,
				V: nv,
			}
		}
		matrix[index] = promql.Series{
			Metric: metric,
			Points: points,
		}

		seriesNum++
		pointNum += len(points)
	}

	trace.InsertIntIntoSpan("resp-series-num", seriesNum, span)
	trace.InsertIntIntoSpan("resp-point-num", pointNum, span)

	return matrix
}

// QueryRange 查询范围数据
func (i *Instance) QueryRange(
	ctx context.Context, promqlStr string,
	start, end time.Time, step time.Duration,
) (promql.Matrix, error) {
	var (
		cancel        context.CancelFunc
		span          oleltrace.Span
		startAnaylize time.Time

		err error
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "victoria-metrics-query-range")
	if span != nil {
		defer span.End()
	}
	values := &url.Values{}
	values.Set("query", promqlStr)
	values.Set("step", fmt.Sprintf("%.f", step.Seconds()))
	values.Set("start", fmt.Sprintf("%d", start.Unix()))
	values.Set("end", fmt.Sprintf("%d", end.Unix()))
	urlPath := i.urlPath("query_range", values.Encode())

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()
	startAnaylize = time.Now()

	trace.InsertStringIntoSpan("query-url-path", urlPath, span)
	trace.InsertStringIntoSpan("query-promql", promqlStr, span)
	log.Infof(ctx,
		"victoria metrics query: %s, promql: %s",
		urlPath, promqlStr,
	)

	resp, err := i.curl.Request(
		ctx, curl.Get,
		curl.Options{
			UrlPath: urlPath,
		},
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	trace.InsertStringIntoSpan("query-cost", time.Since(startAnaylize).String(), span)

	data := &Data{}
	err = json.NewDecoder(resp.Body).Decode(data)
	if err != nil {
		return nil, err
	}
	if data.Status != "success" {
		return nil, errors.New(data.Error)
	}

	return i.matrixFormat(data, span), err
}

// Query instant 查询
func (i *Instance) Query(
	ctx context.Context, promqlStr string,
	end time.Time, step time.Duration,
) (promql.Matrix, error) {
	var (
		cancel        context.CancelFunc
		span          oleltrace.Span
		startAnaylize time.Time

		err error
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "victoria-metrics-query")
	if span != nil {
		defer span.End()
	}
	values := &url.Values{}
	values.Set("query", promqlStr)
	values.Set("step", fmt.Sprintf("%.f", step.Seconds()))
	values.Set("time", fmt.Sprintf("%d", end.Unix()))
	urlPath := i.urlPath("query", values.Encode())

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()
	startAnaylize = time.Now()

	trace.InsertStringIntoSpan("query-url-path", urlPath, span)
	trace.InsertStringIntoSpan("query-promql", promqlStr, span)
	log.Infof(ctx,
		"victoria metrics query: %s, promql: %s",
		urlPath, promqlStr,
	)

	resp, err := i.curl.Request(
		ctx, curl.Get,
		curl.Options{
			UrlPath: urlPath,
		},
	)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	trace.InsertStringIntoSpan("query-cost", time.Since(startAnaylize).String(), span)

	data := &Data{}
	err = json.NewDecoder(resp.Body).Decode(data)
	if err != nil {
		return nil, err
	}
	if data.Status != "success" {
		return nil, errors.New(data.Error)
	}
	return i.matrixFormat(data, span), err
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	panic("implement me")
}

func (i *Instance) LabelNames(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	panic("implement me")
}

func (i *Instance) LabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	panic("implement me")
}

func (i *Instance) Series(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
	panic("implement me")
}
