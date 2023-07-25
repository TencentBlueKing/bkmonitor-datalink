// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package victoriaMetrics

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/promql/parser"
	"github.com/prometheus/prometheus/storage"
	oleltrace "go.opentelemetry.io/otel/trace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

const (
	BKDataAuthenticationMethod = "token"
	PreferStorage              = "vm"

	ContentType = "Content-Type"

	APISeries      = "series"
	APILabelNames  = "labels"
	APILabelValues = "label_values"
	APIQueryRange  = "query_range"
	APIQuery       = "query"

	OK = "00"

	VectorType = "vector"
	MatrixType = "matrix"
)

// Instance vm 查询实例
type Instance struct {
	Ctx context.Context

	ContentType string

	Address string
	UriPath string

	ResultTableGroup map[string][]string

	Code   string
	Secret string
	Token  string

	InfluxCompatible bool

	Timeout time.Duration
	Curl    curl.Curl
}

var _ tsdb.Instance = (*Instance)(nil)

func (i *Instance) dataFormat(ctx context.Context, resp *VmResponse, span oleltrace.Span) (promql.Matrix, error) {
	if !resp.Result {
		return nil, fmt.Errorf(
			"%s, %s, %s", resp.Message, resp.Errors.Error, resp.Errors.QueryId,
		)
	}
	if resp.Code != OK {
		return nil, fmt.Errorf(
			"%s, %s, %s", resp.Message, resp.Errors.Error, resp.Errors.QueryId,
		)
	}
	if len(resp.Data.List) > 0 {
		data := resp.Data.List[0].Data
		seriesNum := 0
		pointNum := 0

		matrix := make(promql.Matrix, 0, len(data.Result))
		for _, series := range data.Result {
			metricIndex := 0
			metric := make(labels.Labels, len(series.Metric))
			for name, value := range series.Metric {
				metric[metricIndex] = labels.Label{
					Name:  name,
					Value: value,
				}
				metricIndex++
			}

			points := make([]promql.Point, 0)
			if data.ResultType == VectorType {
				nt, nv, err := series.Value.Point()
				if err != nil {
					log.Errorf(ctx, err.Error())
					continue
				}
				points = append(points, promql.Point{
					T: nt,
					V: nv,
				})
			} else {
				for _, value := range series.Values {
					nt, nv, err := value.Point()
					if err != nil {
						log.Errorf(ctx, err.Error())
						continue
					}
					points = append(points, promql.Point{
						T: nt,
						V: nv,
					})
				}
			}
			matrix = append(matrix, promql.Series{
				Metric: metric,
				Points: points,
			})

			seriesNum++
			pointNum += len(points)
		}

		trace.InsertIntIntoSpan("resp-series-num", seriesNum, span)
		trace.InsertIntIntoSpan("resp-point-num", pointNum, span)
		return matrix, nil
	}
	return nil, nil
}

func (i *Instance) labelFormat(ctx context.Context, resp *VmLableValuesResponse, span oleltrace.Span) ([]string, error) {
	if !resp.Result {
		return nil, fmt.Errorf(
			"%s, %s, %s", resp.Message, resp.Errors.Error, resp.Errors.QueryId,
		)
	}
	if resp.Code != OK {
		log.Errorf(ctx, resp.Errors.Error)
		return nil, fmt.Errorf(
			"%s, %s, %s", resp.Message, resp.Errors.Error, resp.Errors.QueryId,
		)
	}
	lbsMap := make(map[string]struct{}, 0)
	for _, d := range resp.Data.List {
		for _, v := range d.Data {
			lbsMap[v] = struct{}{}
		}
	}
	lbs := make([]string, 0, len(lbsMap))
	for k := range lbsMap {
		lbs = append(lbs, k)
	}

	return lbs, nil
}

func (i *Instance) seriesFormat(ctx context.Context, resp *VmSeriesResponse, span oleltrace.Span) ([]map[string]string, error) {
	if !resp.Result {
		return nil, fmt.Errorf("%s", resp.Message)
	}
	if resp.Code != OK {
		return nil, fmt.Errorf("%s", resp.Message)
	}
	series := make([]map[string]string, 0)
	for _, d := range resp.Data.List {
		series = append(series, d.Data...)
	}

	return series, nil
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
	return nil
}

// vmQuery
func (i *Instance) vmQuery(
	ctx context.Context, sql string, data interface{}, span oleltrace.Span,
) error {
	var (
		cancel        context.CancelFunc
		startAnaylize time.Time

		err error
	)

	address := fmt.Sprintf("%s/%s", i.Address, i.UriPath)
	user := metadata.GetUser(ctx)
	params := &Params{
		SQL:                        sql,
		BkdataAuthenticationMethod: BKDataAuthenticationMethod,
		BkUsername:                 user.Key,
		BkAppCode:                  i.Code,
		PreferStorage:              PreferStorage,
		BkdataDataToken:            i.Token,
		BkAppSecret:                i.Secret,
	}
	body, err := json.Marshal(params)
	if err != nil {
		return err
	}

	ctx, cancel = context.WithTimeout(ctx, i.Timeout)
	defer cancel()
	startAnaylize = time.Now()

	trace.InsertStringIntoSpan("query-source", user.Source, span)
	trace.InsertStringIntoSpan("query-space-uid", user.SpaceUid, span)
	trace.InsertStringIntoSpan("query-username", user.Name, span)
	trace.InsertStringIntoSpan("query-address", i.Address, span)
	trace.InsertStringIntoSpan("query-uri-path", i.UriPath, span)
	trace.InsertStringIntoSpan("query-sql", sql, span)
	log.Infof(ctx,
		"victoria metrics query: %s, body: %s, sql: %s",
		address, body, sql,
	)

	resp, err := i.Curl.Request(
		ctx, curl.Post,
		curl.Options{
			UrlPath: address,
			Body:    body,
			Headers: map[string]string{
				ContentType: i.ContentType,
			},
		},
	)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf(resp.Status)
	}

	queryCost := time.Since(startAnaylize)
	trace.InsertStringIntoSpan("query-cost", queryCost.String(), span)

	metric.TsDBRequestSecond(
		ctx, queryCost, user.SpaceUid, consul.VictoriaMetricsStorageType,
	)

	err = json.NewDecoder(resp.Body).Decode(data)
	if err != nil {
		return err
	}

	return nil
}

// QueryRange 查询范围数据
func (i *Instance) QueryRange(
	ctx context.Context, promqlStr string,
	start, end time.Time, step time.Duration,
) (promql.Matrix, error) {
	var (
		span      oleltrace.Span
		vmRtGroup map[string][]string

		vmResp = &VmResponse{}
		err    error
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "victoria-metrics-query-range")
	if span != nil {
		defer span.End()
	}

	expand := metadata.GetExpand(ctx)
	if expand != nil {
		if v, ok := expand.(map[string][]string); ok {
			vmRtGroup = v
		}
	}

	trace.InsertStringIntoSpan("query-promql", promqlStr, span)
	trace.InsertStringIntoSpan("query-start", start.String(), span)
	trace.InsertStringIntoSpan("query-end", end.String(), span)
	trace.InsertStringIntoSpan("query-step", step.String(), span)

	if len(vmRtGroup) == 0 {
		return promql.Matrix{}, nil
	}

	metrics := make([]string, 0, len(vmRtGroup))
	for m, rts := range vmRtGroup {
		metrics = append(metrics, m)
		trace.InsertStringSliceIntoSpan(fmt.Sprintf("vm-rt-%s", m), rts, span)
	}
	trace.InsertStringSliceIntoSpan("query-metrics", metrics, span)

	paramsQueryRange := &ParamsQueryRange{
		InfluxCompatible: i.InfluxCompatible,
		APIType:          APIQueryRange,
		APIParams: struct {
			Query string `json:"query"`
			Start int64  `json:"start"`
			End   int64  `json:"end"`
			Step  int64  `json:"step"`
		}{
			Query: promqlStr,
			Start: start.Unix(),
			End:   end.Unix(),
			Step:  int64(step.Seconds()),
		},
		ResultTableGroup: vmRtGroup,
	}

	sql, err := json.Marshal(paramsQueryRange)
	if err != nil {
		return nil, err
	}

	err = i.vmQuery(ctx, string(sql), vmResp, span)
	if err != nil {
		return nil, err
	}

	return i.dataFormat(ctx, vmResp, span)
}

// Query instant 查询
func (i *Instance) Query(
	ctx context.Context, promqlStr string,
	end time.Time, step time.Duration,
) (promql.Matrix, error) {
	var (
		span      oleltrace.Span
		vmRtGroup map[string][]string

		vmResp = &VmResponse{}
		err    error
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "victoria-metrics-query")
	if span != nil {
		defer span.End()
	}

	expand := metadata.GetExpand(ctx)
	if expand != nil {
		v, ok := expand.(map[string][]string)
		if ok {
			vmRtGroup = v
		}
	}

	trace.InsertStringIntoSpan("query-promql", promqlStr, span)
	trace.InsertStringIntoSpan("query-end", end.String(), span)
	trace.InsertStringIntoSpan("query-step", step.String(), span)

	if len(vmRtGroup) == 0 {
		return promql.Matrix{}, nil
	}

	metrics := make([]string, 0, len(vmRtGroup))
	for m, rts := range vmRtGroup {
		metrics = append(metrics, m)
		trace.InsertStringSliceIntoSpan(fmt.Sprintf("vm-rt-%s", m), rts, span)
	}
	trace.InsertStringSliceIntoSpan("query-metrics", metrics, span)

	paramsQuery := &ParamsQuery{
		InfluxCompatible: i.InfluxCompatible,
		APIType:          APIQuery,
		APIParams: struct {
			Query   string `json:"query"`
			Time    int64  `json:"time"`
			Timeout int64  `json:"timeout"`
		}{
			Query:   promqlStr,
			Time:    end.Unix(),
			Timeout: int64(i.Timeout.Seconds()),
		},
		ResultTableGroup: vmRtGroup,
	}

	sql, err := json.Marshal(paramsQuery)
	if err != nil {
		return nil, err
	}

	err = i.vmQuery(ctx, string(sql), vmResp, span)
	if err != nil {
		return nil, err
	}

	return i.dataFormat(ctx, vmResp, span)
}

func (i *Instance) metric(ctx context.Context, name string) ([]string, error) {
	var (
		span      oleltrace.Span
		vmRtGroup map[string][]string

		resp = &VmLableValuesResponse{}
		err  error
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "victoria-metrics-instance-metric")
	if span != nil {
		defer span.End()
	}
	expand := metadata.GetExpand(ctx)
	if expand != nil {
		v, ok := expand.(map[string][]string)
		if ok {
			vmRtGroup = v
		}
	}

	trace.InsertStringIntoSpan("query-name", name, span)

	if len(vmRtGroup) == 0 {
		return nil, nil
	}

	metrics := make([]string, 0, len(vmRtGroup))
	for m, rts := range vmRtGroup {
		metrics = append(metrics, m)
		trace.InsertStringSliceIntoSpan(fmt.Sprintf("vm-rt-%s", m), rts, span)
	}
	trace.InsertStringSliceIntoSpan("query-metrics", metrics, span)

	paramsQuery := &ParamsLabelValues{
		InfluxCompatible: i.InfluxCompatible,
		APIType:          APILabelValues,
		APIParams: struct {
			Label string `json:"label"`
		}{
			Label: name,
		},
		ResultTableGroup: vmRtGroup,
	}

	sql, err := json.Marshal(paramsQuery)
	if err != nil {
		return nil, err
	}

	err = i.vmQuery(ctx, string(sql), resp, span)
	if err != nil {
		return nil, err
	}

	return i.labelFormat(ctx, resp, span)
}

func (i *Instance) LabelNames(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	var (
		span      oleltrace.Span
		vmRtGroup map[string][]string

		resp = &VmLableValuesResponse{}
		err  error
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "victoria-metrics-query")
	if span != nil {
		defer span.End()
	}

	expand := metadata.GetExpand(ctx)
	if expand != nil {
		v, ok := expand.(map[string][]string)
		if ok {
			vmRtGroup = v
		}
	}

	trace.InsertStringIntoSpan("query-matchers", fmt.Sprintf("%+v", matchers), span)
	trace.InsertStringIntoSpan("query-start", start.String(), span)
	trace.InsertStringIntoSpan("query-end", end.String(), span)

	if len(vmRtGroup) == 0 {
		return nil, nil
	}

	metrics := make([]string, 0, len(vmRtGroup))
	for m, rts := range vmRtGroup {
		metrics = append(metrics, m)
		trace.InsertStringSliceIntoSpan(fmt.Sprintf("vm-rt-%s", m), rts, span)
	}
	trace.InsertStringSliceIntoSpan("query-metrics", metrics, span)

	metricName := ""
	labelMatchers := make([]*labels.Matcher, 0, len(matchers)-1)
	for _, m := range matchers {
		if m.Name == labels.MetricName {
			metricName = m.Value
		} else {
			labelMatchers = append(labelMatchers, m)
		}
	}

	if metricName == "" {
		return nil, fmt.Errorf("wrong metric name: %+v", matchers)
	}

	vector := &parser.VectorSelector{
		Name:          metricName,
		LabelMatchers: labelMatchers,
	}
	promqlStr := vector.String()
	paramsQuery := &ParamsSeries{
		InfluxCompatible: i.InfluxCompatible,
		APIType:          APILabelNames,
		APIParams: struct {
			Match string `json:"match[]"`
			Start int64  `json:"start"`
			End   int64  `json:"end"`
		}{
			Match: promqlStr,
			Start: start.Unix(),
			End:   end.Unix(),
		},
		ResultTableGroup: vmRtGroup,
	}

	sql, err := json.Marshal(paramsQuery)
	if err != nil {
		return nil, err
	}

	err = i.vmQuery(ctx, string(sql), resp, span)
	if err != nil {
		return nil, err
	}

	return i.labelFormat(ctx, resp, span)
}

func (i *Instance) LabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	var (
		span      oleltrace.Span
		vmRtGroup map[string][]string

		resp = &VmSeriesResponse{}
		err  error
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "victoria-metrics-query")
	if span != nil {
		defer span.End()
	}

	if name == labels.MetricName {
		return i.metric(ctx, name)
	}

	expand := metadata.GetExpand(ctx)
	if expand != nil {
		v, ok := expand.(map[string][]string)
		if ok {
			vmRtGroup = v
		}
	}

	trace.InsertStringIntoSpan("query-name", name, span)
	trace.InsertStringIntoSpan("query-matchers", fmt.Sprintf("%+v", matchers), span)
	trace.InsertStringIntoSpan("query-start", start.String(), span)
	trace.InsertStringIntoSpan("query-end", end.String(), span)

	if len(vmRtGroup) == 0 {
		return nil, nil
	}

	metrics := make([]string, 0, len(vmRtGroup))
	for m, rts := range vmRtGroup {
		metrics = append(metrics, m)
		trace.InsertStringSliceIntoSpan(fmt.Sprintf("vm-rt-%s", m), rts, span)
	}
	trace.InsertStringSliceIntoSpan("query-metrics", metrics, span)

	metricName := ""
	labelMatchers := make([]*labels.Matcher, 0, len(matchers)-1)
	for _, m := range matchers {
		if m.Name == labels.MetricName {
			metricName = m.Value
		} else {
			labelMatchers = append(labelMatchers, m)
		}
	}

	if metricName == "" {
		return nil, fmt.Errorf("wrong metric name: %+v", matchers)
	}

	vector := &parser.VectorSelector{
		Name:          metricName,
		LabelMatchers: labelMatchers,
	}
	promqlStr := vector.String()
	paramsQuery := &ParamsSeries{
		InfluxCompatible: i.InfluxCompatible,
		APIType:          APISeries,
		APIParams: struct {
			Match string `json:"match[]"`
			Start int64  `json:"start"`
			End   int64  `json:"end"`
		}{
			Match: promqlStr,
			Start: start.Unix(),
			End:   end.Unix(),
		},
		ResultTableGroup: vmRtGroup,
	}

	sql, err := json.Marshal(paramsQuery)
	if err != nil {
		return nil, err
	}

	err = i.vmQuery(ctx, string(sql), resp, span)
	if err != nil {
		return nil, err
	}

	series, err := i.seriesFormat(ctx, resp, span)
	if err != nil {
		return nil, err
	}

	lbsMap := make(map[string]struct{}, 0)
	for _, s := range series {
		if v, ok := s[name]; ok {
			lbsMap[v] = struct{}{}
		}
	}

	lbs := make([]string, 0, len(lbsMap))
	for k := range lbsMap {
		lbs = append(lbs, k)
	}

	return lbs, nil
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	panic("implement me")
}

func (i *Instance) Series(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
	return nil
}
