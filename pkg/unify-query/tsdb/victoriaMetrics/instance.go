// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// gzl: VictoriaMetrics时序数据库查询实例包
// gzl: 提供VictoriaMetrics的查询接口实现，支持PromQL查询、标签查询、范围查询等功能
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
	// gzl: 默认用户名，用于VictoriaMetrics查询认证
	BkUserName = "admin"
	// gzl: 首选存储类型，标识使用VictoriaMetrics作为时序数据库
	PreferStorage = "vm"

	// gzl: HTTP请求头常量定义
	ContentType   = "Content-Type"
	Authorization = "X-Bkapi-Authorization"

	// gzl: VictoriaMetrics API类型常量
	APISeries      = "series"       // gzl: 时间序列查询API
	APILabelNames  = "labels"       // gzl: 标签名称查询API
	APILabelValues = "label_values" // gzl: 标签值查询API
	APIQueryRange  = "query_range"  // gzl: 范围查询API
	APIQuery       = "query"        // gzl: 即时查询API

	// gzl: 成功响应码，表示VictoriaMetrics查询成功
	OK = "00"

	// gzl: PromQL查询结果类型常量
	VectorType = "vector" // gzl: 向量类型结果，表示单个时间点的数据
	MatrixType = "matrix" // gzl: 矩阵类型结果，表示时间范围内的数据
)

// gzl: VictoriaMetrics实例配置选项结构体
type Options struct {
	Address         string            // gzl: VictoriaMetrics服务地址
	Headers         map[string]string // gzl: HTTP请求头配置
	MaxConditionNum int               // gzl: 最大查询条件数量限制
	Timeout         time.Duration     // gzl: 查询超时时间
	Curl            curl.Curl         // gzl: HTTP客户端实例

	InfluxCompatible bool   // gzl: 是否兼容InfluxDB查询语法
	UseNativeOr      bool   // gzl: 是否使用原生OR操作符
	ForceStorageName string // gzl: 强制使用的存储名称
}

// gzl: VictoriaMetrics查询实例结构体
type Instance struct {
	tsdb.DefaultInstance // gzl: 继承默认时序数据库实例

	ctx context.Context // gzl: 上下文对象，用于传递请求信息

	maxConditionNum int // gzl: 最大查询条件数量

	url     string            // gzl: VictoriaMetrics服务URL
	headers map[string]string // gzl: HTTP请求头

	influxCompatible bool // gzl: InfluxDB语法兼容标志
	useNativeOr      bool // gzl: 原生OR操作符使用标志

	timeout time.Duration // gzl: 查询超时时间
	curl    curl.Curl     // gzl: HTTP客户端

	forceStorageName string // gzl: 强制存储名称，用于指定特定集群
}

// gzl: 获取VictoriaMetrics集群名称
// gzl: 如果配置了强制存储名称，则优先使用强制名称，否则使用传入的集群名称
// gzl: 参数:
// gzl:   clusterName - 原始集群名称
// gzl: 返回值:
// gzl:   string - 最终使用的集群名称
func (i *Instance) getVMClusterName(clusterName string) string {
	// gzl: 如果配置了强制查询的 vm 集群，则取该集群
	if i.forceStorageName != "" {
		return i.forceStorageName
	}

	return clusterName
}

var _ tsdb.Instance = (*Instance)(nil) // gzl: 类型断言，确保Instance实现了tsdb.Instance接口

// gzl: 创建新的VictoriaMetrics查询实例
// gzl: 参数:
// gzl:   ctx - 上下文对象，用于传递请求信息
// gzl:   opt - VictoriaMetrics配置选项
// gzl: 返回值:
// gzl:   *Instance - VictoriaMetrics实例指针
// gzl:   error - 错误信息，如果地址为空则返回错误
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
	if !resp.Result || resp.Code != OK {
		return nil, metadata.NewMessage(
			metadata.MsgQueryVictoriaMetrics,
			"查询异常 %s",
			resp.Message,
		).Error(ctx, errors.New(resp.Errors.Error))
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
				_ = metadata.NewMessage(
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
		return nil, false, metadata.NewMessage(
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
					_ = metadata.NewMessage(
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
						_ = metadata.NewMessage(
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
		return nil, metadata.NewMessage(
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

// gzl: 获取实例类型，返回VictoriaMetrics存储类型标识
// gzl: 返回值:
// gzl:   string - 存储类型常量，标识为VictoriaMetrics
func (i *Instance) InstanceType() string {
	return metadata.VictoriaMetricsStorageType
}

// gzl: nocache判定方法
// gzl: VictoriaMetrics在返回数据点超过50个时可能会调整时间戳，可以通过禁用缓存来避免此行为
// gzl: 在一些场景下，如果step不能被start整除，会导致返回的数据跟开始时间无法对齐
// gzl: 因此需要增加no-cache=1参数，避免性能消耗过大，只处理1分钟以上的步长
// gzl: 参数:
// gzl:   ctx - 上下文对象
// gzl:   start - 查询开始时间戳
// gzl:   step - 查询步长（秒）
// gzl: 返回值:
// gzl:   int - 是否禁用缓存（1表示禁用，0表示使用缓存）
func (i *Instance) noCache(ctx context.Context, start, step int64) int {
	if start%step > 0 && step > 60 {
		return 1
	}
	return 0
}

// gzl: VictoriaMetrics查询核心方法，执行实际的HTTP请求
// gzl: 参数:
// gzl:   ctx - 上下文对象，包含用户信息和追踪信息
// gzl:   sql - 查询SQL语句，包含查询参数和条件
// gzl:   data - 响应数据接收对象，用于解析VictoriaMetrics返回结果
// gzl:   span - 追踪span，用于记录查询性能和调试信息
// gzl: 返回值:
// gzl:   error - 查询过程中发生的错误，如超时、网络错误等
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

	// gzl: body 增加 bkdata auth 信息，用于认证和权限控制
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
		return metadata.NewMessage(
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

// gzl: DirectQuery - 即时查询方法，查询指定时间点的数据
// gzl: 用于获取单个时间点的监控指标数据，返回PromQL向量格式结果
// gzl: 参数:
// gzl:   ctx - 上下文对象，包含用户信息和查询参数
// gzl:   promqlStr - PromQL查询语句
// gzl:   end - 查询结束时间点
// gzl: 返回值:
// gzl:   promql.Vector - 查询结果向量，包含时间点和对应值
// gzl:   error - 查询过程中发生的错误
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

	// gzl: 如果VM扩展信息为空或没有结果表列表，返回空向量
	if vmExpand == nil || len(vmExpand.ResultTableList) == 0 {
		return promql.Vector{}, nil
	}

	span.Set("vm-expand-cluster-name", vmExpand.ClusterName)

	paramsQuery := &ParamsQuery{
		BkBizID:          metadata.GetBkBizID(ctx),
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

// gzl: DirectQueryRange - 范围查询方法，查询指定时间范围内的数据
// gzl: 用于获取时间序列数据，返回PromQL矩阵格式结果，支持部分数据返回标识
// gzl: 参数:
// gzl:   ctx - 上下文对象，包含用户信息和查询参数
// gzl:   promqlStr - PromQL查询语句
// gzl:   start - 查询开始时间
// gzl:   end - 查询结束时间
// gzl:   step - 查询步长
// gzl: 返回值:
// gzl:   promql.Matrix - 查询结果矩阵，包含时间序列数据
// gzl:   bool - 是否为部分数据（用于大数据量分页查询）
// gzl:   error - 查询过程中发生的错误
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

	// gzl: 判断是否需要禁用缓存，避免时间戳对齐问题
	noCache := i.noCache(ctx, start.Unix(), int64(step.Seconds()))

	span.Set("query-start", start)
	span.Set("query-start-unix", start.Unix())
	span.Set("query-end", end)
	span.Set("query-end-unix", end.Unix())
	span.Set("query-step", step)
	span.Set("query-step-unix", step.Seconds())
	span.Set("query-no-cache", noCache)
	span.Set("query-match", promqlStr)

	// gzl: 如果VM扩展信息为空或没有结果表列表，返回空矩阵
	if vmExpand == nil || len(vmExpand.ResultTableList) == 0 {
		return promql.Matrix{}, false, nil
	}

	span.Set("vm-expand-cluster-name", vmExpand.ClusterName)

	rangeLeftTime := end.Sub(start)
	metric.TsDBRequestRangeMinute(ctx, rangeLeftTime, i.InstanceType())

	paramsQueryRange := &ParamsQueryRange{
		BkBizID:          metadata.GetBkBizID(ctx),
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
		BkBizID:          metadata.GetBkBizID(ctx),
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

// gzl: QueryLabelNames - 标签名称查询方法，获取指定时间范围内的所有标签名称
// gzl: 用于元数据发现，帮助用户了解可用的监控指标标签
// gzl: 参数:
// gzl:   ctx - 上下文对象，包含用户信息和查询参数
// gzl:   query - 查询对象，包含查询条件和配置
// gzl:   start - 查询开始时间
// gzl:   end - 查询结束时间
// gzl: 返回值:
// gzl:   []string - 标签名称列表，按字母顺序排序
// gzl:   error - 查询过程中发生的错误
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

	// gzl: 如果VM结果表为空，直接返回空结果
	if query.VmRt == "" {
		return nil, nil
	}

	paramsQuery := &ParamsSeries{
		BkBizID:          metadata.GetBkBizID(ctx),
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

// gzl: QueryLabelValues - 标签值查询方法，获取指定标签名称的所有可能值
// gzl: 支持两种查询策略：24小时内使用范围查询，超过24小时使用直接标签查询
// gzl: 参数:
// gzl:   ctx - 上下文对象，包含用户信息和查询参数
// gzl:   query - 查询对象，包含查询条件和配置
// gzl:   name - 标签名称
// gzl:   start - 查询开始时间
// gzl:   end - 查询结束时间
// gzl: 返回值:
// gzl:   []string - 标签值列表，去重后按字母顺序排序
// gzl:   error - 查询过程中发生的错误
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

	// gzl: 如果使用 end - start 作为 step，查询的时候会多查一个step的数据量，所以这里需要减少点数
	left := end.Sub(start)

	span.Set("query-left", left.String())

	// gzl: 如果查询时间范围小于24小时，使用范围查询策略
	if left.Hours() < 24 {
		step := int64(left.Seconds()) / 10
		if step < 60 {
			step = 60
		}
		span.Set("query-step", step)

		// gzl: 构建查询语句，使用count聚合和topk限制结果数量
		queryString := query.VmCondition.ToMatch()
		queryString = fmt.Sprintf(`count(%s) by (%s)`, queryString, name)
		if query.Size > 0 {
			queryString = fmt.Sprintf(`topk(%d, %s)`, query.Size, queryString)
		}

		span.Set("query-storage-name", query.StorageName)

		paramsQueryRange := &ParamsQueryRange{
			BkBizID:          metadata.GetBkBizID(ctx),
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
				// gzl: 从查询结果中提取标签值并去重
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

	// gzl: 如果标签值查询超过24小时或者报错，则跳转到DirectLabelValues查询
	matcher, _ := labels.NewMatcher(labels.MatchEqual, labels.MetricName, metadata.DefaultReferenceName)

	// gzl: 构建新的上下文进行缓存写入，避免影响原查询，因为会有多个查询并发
	ctx = metadata.InitHashID(ctx)
	metadata.SetExpand(ctx, query.VMExpand())

	return i.DirectLabelValues(ctx, name, start, end, query.Size, matcher)
}

func (i *Instance) DirectLabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	// TODO implement me
	panic("implement me")
}

// gzl: DirectLabelValues - 直接标签值查询方法，用于处理大数据量或长时间范围的标签查询
// gzl: 通过VictoriaMetrics的label_values API直接获取标签值，避免范围查询的性能问题
// gzl: 参数:
// gzl:   ctx - 上下文对象，包含用户信息和VM扩展配置
// gzl:   name - 标签名称
// gzl:   start - 查询开始时间
// gzl:   end - 查询结束时间
// gzl:   limit - 结果数量限制
// gzl:   matchers - 标签匹配器，用于过滤指标
// gzl: 返回值:
// gzl:   []string - 标签值列表，去重后按字母顺序排序
// gzl:   error - 查询过程中发生的错误
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

	// gzl: 从匹配器中提取指标名称，用于构建查询条件
	metricName := function.MatcherToMetricName(matchers...)
	if metricName == "" {
		return list, err
	}

	// gzl: 构建匹配条件字符串，使用VM扩展中的指标过滤条件
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
		BkBizID:          metadata.GetBkBizID(ctx),
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

	// gzl: 设置查询时间范围，如果时间戳大于0则使用
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
