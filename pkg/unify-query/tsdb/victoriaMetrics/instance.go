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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

const (
	BkUserName    = "admin"
	PreferStorage = "vm"

	ContentType   = "Content-Type"
	Authorization = "X-Bkapi-Authorization"

	APISeries      = "series"
	APILabelNames  = "labels"
	APILabelValues = "label_values"
	APIQueryRange  = "query_range"
	APIQuery       = "query"

	OK = "00"

	VectorType = "vector"
	MatrixType = "matrix"
)

type Options struct {
	Address         string
	Headers         map[string]string
	MaxConditionNum int
	Timeout         time.Duration
	Curl            curl.Curl

	InfluxCompatible bool
	UseNativeOr      bool
	ForceStorageName string
}

// Instance vm 查询实例
type Instance struct {
	tsdb.DefaultInstance

	ctx context.Context

	maxConditionNum int

	url     string
	headers map[string]string

	influxCompatible bool
	useNativeOr      bool

	timeout time.Duration
	curl    curl.Curl

	forceStorageName string
}

func (i *Instance) getVMClusterName(clusterName string) string {
	// 如果配置了强制查询的 vm 集群，则取该集群
	if i.forceStorageName != "" {
		return i.forceStorageName
	}

	return clusterName
}

var _ tsdb.Instance = (*Instance)(nil)

func NewInstance(ctx context.Context, opt *Options) (*Instance, error) {
	if opt.Address == "" {
		return nil, fmt.Errorf("address is empty")
	}
	instance := &Instance{
		ctx:              ctx,
		maxConditionNum:  opt.MaxConditionNum,
		url:              opt.Address,
		headers:          opt.Headers,
		influxCompatible: opt.InfluxCompatible,
		useNativeOr:      opt.UseNativeOr,
		timeout:          opt.Timeout,
		curl:             opt.Curl,
		forceStorageName: opt.ForceStorageName,
	}
	return instance, nil
}

func (i *Instance) Check(ctx context.Context, q string, start, end time.Time, step time.Duration) string {
	var output strings.Builder

	vmExpand := metadata.GetExpand(ctx)
	if vmExpand == nil || len(vmExpand.ResultTableList) == 0 {
		output.WriteString(fmt.Sprintf("vm expand is empty with: %+v", vmExpand))
		return output.String()
	}

	output.WriteString(fmt.Sprintf("match: %s\n", q))

	output.WriteString(fmt.Sprintf("vm_expand: %+v", vmExpand))
	return output.String()
}

// QuerySeriesSet 给 PromEngine 提供查询接口
func (i *Instance) QuerySeriesSet(ctx context.Context, query *metadata.Query, start, end time.Time) storage.SeriesSet {
	return storage.EmptySeriesSet()
}

func (i *Instance) vectorFormat(ctx context.Context, resp *VmResponse, span *trace.Span) (promql.Vector, error) {
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

	prefix := "response-"
	span.Set(fmt.Sprintf("%s-list-num", prefix), len(resp.Data.List))
	span.Set(fmt.Sprintf("%s-cluster", prefix), resp.Data.Cluster)
	span.Set(fmt.Sprintf("%s-sql", prefix), resp.Data.SQL)
	span.Set(fmt.Sprintf("%s-device", prefix), resp.Data.Device)
	span.Set(fmt.Sprintf("%s-elapsed-time", prefix), resp.Data.BksqlCallElapsedTime)
	span.Set(fmt.Sprintf("%s-total-records", prefix), resp.Data.TotalRecords)
	span.Set(fmt.Sprintf("%s-result-table", prefix), resp.Data.ResultTableIds)
	span.Set(fmt.Sprintf("%s-bk-biz-ids", prefix), resp.Data.BkBizIDs)

	if len(resp.Data.List) > 0 {
		data := resp.Data.List[0].Data
		seriesNum := 0

		vector := make(promql.Vector, 0, len(data.Result))
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

			var point promql.Point
			if data.ResultType != VectorType {
				continue
			}

			nt, nv, err := series.Value.Point()
			if err != nil {
				_ = metadata.Sprintf(
					metadata.MsgQueryVictoriaMetrics,
					"查询异常",
				).Error(ctx, err)
				continue
			}
			point.T = nt
			point.V = nv
			vector = append(vector, promql.Sample{
				Metric: metric,
				Point:  point,
			})

			seriesNum++
		}

		span.Set("resp-series-num", seriesNum)
		return vector, nil
	}

	return nil, nil
}

func (i *Instance) matrixFormat(ctx context.Context, resp *VmResponse, span *trace.Span) (promql.Matrix, bool, error) {
	if !resp.Result || resp.Code != OK {
		return nil, false, metadata.Sprintf(
			metadata.MsgQueryVictoriaMetrics,
			"查询异常 %s",
			resp.Message,
		).Error(ctx, errors.New(resp.Errors.Error))
	}

	prefix := "vm-data"
	span.Set(fmt.Sprintf("%s-list-num", prefix), len(resp.Data.List))
	span.Set(fmt.Sprintf("%s-cluster", prefix), resp.Data.Cluster)
	span.Set(fmt.Sprintf("%s-sql", prefix), resp.Data.SQL)
	span.Set(fmt.Sprintf("%s-device", prefix), resp.Data.Device)
	span.Set(fmt.Sprintf("%s-elapsed-time", prefix), resp.Data.BksqlCallElapsedTime)
	span.Set(fmt.Sprintf("%s-total-records", prefix), resp.Data.TotalRecords)
	span.Set(fmt.Sprintf("%s-result-table", prefix), resp.Data.ResultTableIds)
	span.Set(fmt.Sprintf("%s-bk-biz-ids", prefix), resp.Data.BkBizIDs)

	if len(resp.Data.List) > 0 {
		data := resp.Data.List[0].Data
		seriesNum := 0
		pointNum := 0
		isPartial := resp.Data.List[0].IsPartial

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
					_ = metadata.Sprintf(
						metadata.MsgQueryVictoriaMetrics,
						"值格式解析异常",
					).Error(ctx, err)
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
						_ = metadata.Sprintf(
							metadata.MsgQueryVictoriaMetrics,
							"值格式解析异常",
						).Error(ctx, err)
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

		span.Set("resp-series-num", seriesNum)
		span.Set("resp-point-num", pointNum)
		return matrix, isPartial, nil
	}

	return nil, false, nil
}

func (i *Instance) labelFormat(ctx context.Context, resp *VmLableValuesResponse, span *trace.Span) ([]string, error) {
	if !resp.Result {
		return nil, fmt.Errorf(
			"%s, %s, %s", resp.Message, resp.Errors.Error, resp.Errors.QueryId,
		)
	}
	if resp.Code != OK {
		return nil, metadata.Sprintf(
			metadata.MsgQueryVictoriaMetrics,
			"查询异常 %s, %s, %s",
			resp.Message, resp.Errors.Error, resp.Errors.QueryId,
		).Error(ctx, nil)
	}

	prefix := "vm-data"
	span.Set(fmt.Sprintf("%s-list-num", prefix), len(resp.Data.List))
	span.Set(fmt.Sprintf("%s-cluster", prefix), resp.Data.Cluster)
	span.Set(fmt.Sprintf("%s-sql", prefix), resp.Data.SQL)
	span.Set(fmt.Sprintf("%s-device", prefix), resp.Data.Device)
	span.Set(fmt.Sprintf("%s-elapsed-time", prefix), resp.Data.BksqlCallElapsedTime)
	span.Set(fmt.Sprintf("%s-total-records", prefix), resp.Data.TotalRecords)
	span.Set(fmt.Sprintf("%s-result-table", prefix), resp.Data.ResultTableIds)
	span.Set(fmt.Sprintf("%s-bk-biz-ids", prefix), resp.Data.BkBizIDs)

	lbsMap := set.New[string]()
	for _, d := range resp.Data.List {
		for _, v := range d.Data {
			lbsMap.Add(v)
		}
	}

	lbl := lbsMap.ToArray()
	sort.Strings(lbl)
	return lbl, nil
}

func (i *Instance) seriesFormat(ctx context.Context, resp *VmSeriesResponse, span *trace.Span) ([]map[string]string, error) {
	if !resp.Result {
		return nil, fmt.Errorf("%s", resp.Message)
	}
	if resp.Code != OK {
		return nil, fmt.Errorf("%s", resp.Message)
	}

	prefix := "vm-data"
	span.Set(fmt.Sprintf("%s-list-num", prefix), len(resp.Data.List))
	span.Set(fmt.Sprintf("%s-cluster", prefix), resp.Data.Cluster)
	span.Set(fmt.Sprintf("%s-sql", prefix), resp.Data.SQL)
	span.Set(fmt.Sprintf("%s-device", prefix), resp.Data.Device)
	span.Set(fmt.Sprintf("%s-elapsed-time", prefix), resp.Data.BksqlCallElapsedTime)
	span.Set(fmt.Sprintf("%s-total-records", prefix), resp.Data.TotalRecords)
	span.Set(fmt.Sprintf("%s-result-table", prefix), resp.Data.ResultTableIds)
	span.Set(fmt.Sprintf("%s-bk-biz-ids", prefix), resp.Data.BkBizIDs)

	series := make([]map[string]string, 0)
	for _, d := range resp.Data.List {
		series = append(series, d.Data...)
	}

	return series, nil
}

// GetInstanceType 获取实例类型
func (i *Instance) InstanceType() string {
	return metadata.VictoriaMetricsStorageType
}

// nocache 判定
// VictoriaMetrics may adjust the returned timestamps if the number of returned data points exceeds 50 - see the corresponding comment in the code for details.
// This behaviour can be disabled by passing -search.disableCache command-line flag to VictoriaMetrics. Another option is to pass nocache=1 query arg to /api/v1/query_range.
// 在一些场景下，如果 step 不能被 start 整除，会导致返回的数据跟我们的开始时间无法对其，所以需要增肌 no-cache=1 参数，避免性能消耗过大，只处理 1m 以上的
func (i *Instance) noCache(ctx context.Context, start, step int64) int {
	if start%step > 0 && step > 60 {
		return 1
	}
	return 0
}

// vmQuery
func (i *Instance) vmQuery(
	ctx context.Context, sql string, data any, span *trace.Span,
) error {
	var (
		cancel        context.CancelFunc
		startAnaylize time.Time

		err error
	)

	user := metadata.GetUser(ctx)

	params := make(map[string]string)
	params["sql"] = sql
	params["prefer_storage"] = PreferStorage

	// body 增加 bkdata auth 信息
	for k, v := range bkapi.GetBkDataAPI().GetDataAuth() {
		params[k] = v
	}

	body, err := json.Marshal(params)
	if err != nil {
		return err
	}

	ctx, cancel = context.WithTimeout(ctx, i.timeout)
	defer cancel()
	startAnaylize = time.Now()

	span.Set("query-source", user.Source)
	span.Set("query-space-uid", user.SpaceUID)
	span.Set("query-username", user.Name)

	span.Set("query-address", i.url)

	headers := metadata.Headers(ctx, i.headers)

	size, err := i.curl.Request(
		ctx, curl.Post,
		curl.Options{
			UrlPath: i.url,
			Body:    body,
			Headers: headers,
		},
		data,
	)
	if err != nil {
		return metadata.Sprintf(
			metadata.MsgQueryVictoriaMetrics,
			"查询异常",
		).Error(ctx, err)
	}

	queryCost := time.Since(startAnaylize)

	span.Set("query-cost", queryCost)
	span.Set("response-size", size)

	metric.TsDBRequestSecond(
		ctx, queryCost, i.InstanceType(), i.url,
	)
	metric.TsDBRequestBytes(ctx, size, i.InstanceType())
	return nil
}

// DirectQueryRange 查询范围数据
func (i *Instance) DirectQueryRange(
	ctx context.Context, promqlStr string,
	start, end time.Time, step time.Duration,
) (promql.Matrix, bool, error) {
	var (
		vmExpand *metadata.VmExpand

		vmResp = &VmResponse{}
		err    error
	)

	ctx, span := trace.NewSpan(ctx, "victoria-metrics-query-range")
	defer span.End(&err)

	vmExpand = metadata.GetExpand(ctx)

	noCache := i.noCache(ctx, start.Unix(), int64(step.Seconds()))

	span.Set("query-start", start)
	span.Set("query-start-unix", start.Unix())
	span.Set("query-end", end)
	span.Set("query-end-unix", end.Unix())
	span.Set("query-step", step)
	span.Set("query-step-unix", step.Seconds())
	span.Set("query-no-cache", noCache)
	span.Set("query-match", promqlStr)

	if vmExpand == nil || len(vmExpand.ResultTableList) == 0 {
		return promql.Matrix{}, false, nil
	}

	span.Set("vm-expand-cluster-name", vmExpand.ClusterName)

	rangeLeftTime := end.Sub(start)
	metric.TsDBRequestRangeMinute(ctx, rangeLeftTime, i.InstanceType())

	paramsQueryRange := &ParamsQueryRange{
		InfluxCompatible: i.influxCompatible,
		APIType:          APIQueryRange,
		APIParams: struct {
			Query   string `json:"query"`
			Start   int64  `json:"start"`
			End     int64  `json:"end"`
			Step    int64  `json:"step"`
			NoCache int    `json:"nocache"`
		}{
			Query:   promqlStr,
			Start:   start.Unix(),
			End:     end.Unix(),
			Step:    int64(step.Seconds()),
			NoCache: noCache,
		},
		UseNativeOr:           i.useNativeOr,
		MetricFilterCondition: vmExpand.MetricFilterCondition,
		ResultTableList:       vmExpand.ResultTableList,
		ClusterName:           i.getVMClusterName(vmExpand.ClusterName),
	}

	span.Set("query-cluster-name", paramsQueryRange.ClusterName)

	sql, err := json.Marshal(paramsQueryRange)
	if err != nil {
		return nil, false, err
	}

	err = i.vmQuery(ctx, string(sql), vmResp, span)
	if err != nil {
		return nil, false, err
	}

	return i.matrixFormat(ctx, vmResp, span)
}

// Query instant 查询
func (i *Instance) DirectQuery(
	ctx context.Context, promqlStr string,
	end time.Time,
) (promql.Vector, error) {
	var (
		vmExpand *metadata.VmExpand

		vmResp = &VmResponse{}
		err    error
	)

	ctx, span := trace.NewSpan(ctx, "victoria-metrics-query")
	defer span.End(&err)

	vmExpand = metadata.GetExpand(ctx)

	span.Set("query-end", end)
	span.Set("query-end-unix", end.Unix())
	span.Set("query-match", promqlStr)

	if vmExpand == nil || len(vmExpand.ResultTableList) == 0 {
		return promql.Vector{}, nil
	}

	span.Set("vm-expand-cluster-name", vmExpand.ClusterName)

	paramsQuery := &ParamsQuery{
		InfluxCompatible: i.influxCompatible,
		APIType:          APIQuery,
		APIParams: struct {
			Query   string `json:"query"`
			Time    int64  `json:"time"`
			Timeout int64  `json:"timeout"`
		}{
			Query:   promqlStr,
			Time:    end.Unix(),
			Timeout: int64(i.timeout.Seconds()),
		},
		UseNativeOr:           i.useNativeOr,
		MetricFilterCondition: vmExpand.MetricFilterCondition,
		ResultTableList:       vmExpand.ResultTableList,
		ClusterName:           i.getVMClusterName(vmExpand.ClusterName),
	}

	span.Set("query-cluster-name", paramsQuery.ClusterName)

	sql, err := json.Marshal(paramsQuery)
	if err != nil {
		return nil, err
	}

	err = i.vmQuery(ctx, string(sql), vmResp, span)
	if err != nil {
		return nil, err
	}

	return i.vectorFormat(ctx, vmResp, span)
}

func (i *Instance) QuerySeries(ctx context.Context, query *metadata.Query, start, end time.Time) (series []map[string]string, err error) {
	resp := &VmSeriesResponse{}

	ctx, span := trace.NewSpan(ctx, "victoria-metrics-instance-query-series")
	defer span.End(&err)

	span.Set("query-info", query)
	span.Set("query-start", start)
	span.Set("query-end", end)

	span.Set("query-storage-name", query.StorageName)

	if query.VmRt == "" {
		return series, err
	}

	paramsQuery := &ParamsSeries{
		InfluxCompatible: i.influxCompatible,
		APIType:          APISeries,
		APIParams: struct {
			Match string `json:"match[]"`
			Start int64  `json:"start"`
			End   int64  `json:"end"`
			Limit int    `json:"limit"`
		}{
			Match: query.VmCondition.ToMatch(),
			Start: start.Unix(),
			End:   end.Unix(),
			Limit: query.Size,
		},
		UseNativeOr:     i.useNativeOr,
		ResultTableList: []string{query.VmRt},
		ClusterName:     i.getVMClusterName(query.StorageName),
	}

	span.Set("params-cluster-name", paramsQuery.ClusterName)

	sql, err := json.Marshal(paramsQuery)
	if err != nil {
		return series, err
	}

	err = i.vmQuery(ctx, string(sql), resp, span)
	if err != nil {
		return series, err
	}

	series, err = i.seriesFormat(ctx, resp, span)
	return series, err
}

func (i *Instance) QueryLabelNames(ctx context.Context, query *metadata.Query, start, end time.Time) ([]string, error) {
	var (
		resp = &VmLableValuesResponse{}
		err  error
	)

	ctx, span := trace.NewSpan(ctx, "victoria-metrics-query")
	defer span.End(&err)

	span.Set("query-info", query)
	span.Set("query-start", start)
	span.Set("query-end", end)

	span.Set("query-storage-name", query.StorageName)

	if query.VmRt == "" {
		return nil, nil
	}

	paramsQuery := &ParamsSeries{
		InfluxCompatible: i.influxCompatible,
		APIType:          APILabelNames,
		APIParams: struct {
			Match string `json:"match[]"`
			Start int64  `json:"start"`
			End   int64  `json:"end"`
			Limit int    `json:"limit"`
		}{
			Match: query.VmCondition.ToMatch(),
			Start: start.Unix(),
			End:   end.Unix(),
			Limit: query.Size,
		},
		ResultTableList: []string{query.VmRt},
		ClusterName:     i.getVMClusterName(query.StorageName),
	}

	span.Set("params-cluster-name", paramsQuery.ClusterName)

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

func (i *Instance) QueryLabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time) (res []string, err error) {
	resp := &VmResponse{}

	ctx, span := trace.NewSpan(ctx, "victoria-metrics-instance-label-values")
	defer span.End(&err)

	span.Set("query-info", query)
	span.Set("query-name", name)
	span.Set("query-start", start)
	span.Set("query-end", end)

	if query.VmRt == "" {
		return nil, nil
	}

	// 如果使用 end - start 作为 step，查询的时候会多查一个step的数据量，所以这里需要减少点数
	left := end.Sub(start)

	span.Set("query-left", left.String())

	if left.Hours() < 24 {
		step := int64(left.Seconds()) / 10
		if step < 60 {
			step = 60
		}
		span.Set("query-step", step)

		queryString := query.VmCondition.ToMatch()
		queryString = fmt.Sprintf(`count(%s) by (%s)`, queryString, name)
		if query.Size > 0 {
			queryString = fmt.Sprintf(`topk(%d, %s)`, query.Size, queryString)
		}

		span.Set("query-storage-name", query.StorageName)

		paramsQueryRange := &ParamsQueryRange{
			InfluxCompatible: i.influxCompatible,
			APIType:          APIQueryRange,
			APIParams: struct {
				Query   string `json:"query"`
				Start   int64  `json:"start"`
				End     int64  `json:"end"`
				Step    int64  `json:"step"`
				NoCache int    `json:"nocache"`
			}{
				Query: queryString,
				Start: start.Unix(),
				End:   end.Unix(),
				Step:  step,
			},
			ResultTableList: []string{query.VmRt},
			ClusterName:     i.getVMClusterName(query.StorageName),
		}

		span.Set("params-cluster-name", paramsQueryRange.ClusterName)

		sql, err := json.Marshal(paramsQueryRange)
		if err != nil {
			return nil, err
		}

		err = i.vmQuery(ctx, string(sql), resp, span)
		if err == nil {
			series, _, err := i.matrixFormat(ctx, resp, span)
			if err == nil {
				lbsMap := set.New[string]()
				for _, s := range series {
					for _, l := range s.Metric {
						if l.Name == name {
							lbsMap.Add(l.Value)
						}
					}
				}

				return lbsMap.ToArray(), nil
			}
		}
	}

	// 如果 tag values 超过 24h 或者报错的话，则跳转到 DirectLabelValues 查询
	matcher, _ := labels.NewMatcher(labels.MatchEqual, labels.MetricName, metadata.DefaultReferenceName)
	metadata.SetExpand(ctx, query.VMExpand())

	return i.DirectLabelValues(ctx, name, start, end, query.Size, matcher)
}

func (i *Instance) DirectLabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	// TODO implement me
	panic("implement me")
}

func (i *Instance) DirectLabelValues(ctx context.Context, name string, start, end time.Time, limit int, matchers ...*labels.Matcher) (list []string, err error) {
	var (
		vmExpand *metadata.VmExpand
		resp     = &VmLableValuesResponse{}
	)

	ctx, span := trace.NewSpan(ctx, "victoria-metrics-instance-direct-label-values")
	defer span.End(&err)

	vmExpand = metadata.GetExpand(ctx)
	if vmExpand == nil {
		return list, err
	}

	metricName := function.MatcherToMetricName(matchers...)
	if metricName == "" {
		return list, err
	}

	var match strings.Builder
	if filter, ok := vmExpand.MetricFilterCondition[metricName]; ok {
		match.WriteString("{")
		match.WriteString(filter)
		match.WriteString("}")
	}

	if match.Len() == 0 {
		return list, err
	}

	paramsQuery := &ParamsLabelValues{
		InfluxCompatible: i.influxCompatible,
		APIType:          APILabelValues,
		APIParams: struct {
			Label string `json:"label"`
			Match string `json:"match[]"`
			Start int64  `json:"start"`
			End   int64  `json:"end"`
			Limit int    `json:"limit"`
		}{
			Label: name,
			Match: match.String(),
			Limit: limit,
		},
		ResultTableList: vmExpand.ResultTableList,
		ClusterName:     i.getVMClusterName(vmExpand.ClusterName),
	}

	span.Set("query-label", name)
	span.Set("query-match", match.String())
	span.Set("query-limit", limit)
	span.Set("query-rt-list", vmExpand.ResultTableList)
	span.Set("query-start", start)
	span.Set("query-end", end)
	span.Set("query-cluster-name", paramsQuery.ClusterName)

	if start.Unix() > 0 {
		paramsQuery.APIParams.Start = start.Unix()
	}
	if end.Unix() > 0 {
		paramsQuery.APIParams.End = end.Unix()
	}

	sql, err := json.Marshal(paramsQuery)
	if err != nil {
		return list, err
	}

	err = i.vmQuery(ctx, string(sql), resp, span)
	if err != nil {
		return list, err
	}

	return i.labelFormat(ctx, resp, span)
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	panic("implement me")
}
