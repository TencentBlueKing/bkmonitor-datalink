// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/prompb"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/storage/remote"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sqlExpr"
)

const (
	KeyHighLight = "__highlight"

	KeyIndex     = "__index"
	KeyTableID   = "__result_table"
	KeyDataLabel = "__data_label"
)

type Instance struct {
	ctx context.Context

	querySyncUrl  string
	queryAsyncUrl string

	headers map[string]string

	timeout      time.Duration
	intervalTime time.Duration

	maxLimit  int
	tolerance int

	client *Client
}

var _ tsdb.Instance = (*Instance)(nil)

type Options struct {
	Address string
	Headers map[string]string

	Timeout   time.Duration
	MaxLimit  int
	Tolerance int

	Curl curl.Curl
}

func NewInstance(ctx context.Context, opt *Options) (*Instance, error) {
	if opt.Address == "" {
		return nil, fmt.Errorf("address is empty")
	}
	instance := &Instance{
		ctx:       ctx,
		timeout:   opt.Timeout,
		maxLimit:  opt.MaxLimit,
		tolerance: opt.Tolerance,
		client:    (&Client{}).WithUrl(opt.Address).WithHeader(opt.Headers).WithCurl(opt.Curl),
	}
	return instance, nil
}

func (i *Instance) Check(ctx context.Context, promql string, start, end time.Time, step time.Duration) string {
	return ""
}

func (i *Instance) checkResult(res *Result) error {
	if !res.Result {
		return fmt.Errorf(
			"%s, %s, %s", res.Message, res.Errors.Error, res.Errors.QueryId,
		)
	}
	if res.Code != StatusOK {
		return fmt.Errorf(
			"%s, %s, %s", res.Message, res.Errors.Error, res.Errors.QueryId,
		)
	}
	if res.Data == nil {
		return fmt.Errorf(
			"%s, %s, %s", res.Message, res.Errors.Error, res.Errors.QueryId,
		)
	}

	return nil
}

func (i *Instance) sqlQuery(ctx context.Context, sql string, span *trace.Span) (*QuerySyncResultData, error) {
	var (
		data *QuerySyncResultData

		ok  bool
		err error
	)

	log.Infof(ctx, "%s: %s", i.InstanceType(), sql)
	span.Set("query-sql", sql)

	ctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	// 发起异步查询
	res := i.client.QuerySync(ctx, sql, span)
	if err = i.checkResult(res); err != nil {
		return data, err
	}

	span.Set("query-timeout", i.timeout.String())
	span.Set("query-internal-time", i.intervalTime.String())

	if data, ok = res.Data.(*QuerySyncResultData); !ok {
		return data, fmt.Errorf("queryAsyncResult type is error: %T", res.Data)
	}

	return data, nil
}

func (i *Instance) dims(dims []string, field string) []string {
	dimensions := make([]string, 0)
	for _, dim := range dims {
		// 判断是否是内置维度，内置维度不是用户上报的维度
		if _, ok := internalDimension[dim]; ok {
			continue
		}
		// 如果是字段值也需要跳过
		if dim == field {
			continue
		}

		dimensions = append(dimensions, dim)
	}
	return dimensions
}

func (i *Instance) formatData(ctx context.Context, start time.Time, query *metadata.Query, keys []string, list []map[string]interface{}) (*prompb.QueryResult, error) {
	res := &prompb.QueryResult{}

	if len(list) == 0 {
		return res, nil
	}
	// 维度结构体为空则任务异常
	if len(keys) == 0 {
		return res, fmt.Errorf("SelectFieldsOrder is empty")
	}

	// 获取该指标的维度 key
	dimensions := i.dims(keys, query.Field)

	// 获取 metricLabel
	metricLabel := query.MetricLabels(ctx)

	tsMap := make(map[string]*prompb.TimeSeries, 0)
	for _, d := range list {
		// 优先获取时间和值
		var (
			vt int64
			vv float64

			vtLong   interface{}
			vvDouble interface{}

			ok bool
		)

		if d == nil {
			continue
		}

		// 获取时间戳，单位是毫秒
		if vtLong, ok = d[sqlExpr.TimeStamp]; !ok {
			vtLong = start.UnixMilli()
		}

		if vtLong == nil {
			continue
		}
		switch vtLong.(type) {
		case int64:
			vt = vtLong.(int64)
		case float64:
			vt = int64(vtLong.(float64))
		default:
			return res, fmt.Errorf("%s type is error %T, %v", dtEventTimeStamp, vtLong, vtLong)
		}

		// 获取值
		if vvDouble, ok = d[sqlExpr.Value]; !ok {
			return res, fmt.Errorf("dimension %s is emtpy", sqlExpr.Value)
		}

		if vvDouble == nil {
			continue
		}
		switch vvDouble.(type) {
		case int64:
			vv = float64(vvDouble.(int64))
		case float64:
			vv = vvDouble.(float64)
		default:
			return res, fmt.Errorf("%s type is error %T, %v", sqlExpr.Value, vvDouble, vvDouble)
		}

		lbl := make([]prompb.Label, 0)
		// 获取维度信息
		for _, dimName := range dimensions {
			val, err := getValue(dimName, d)
			if err != nil {
				return res, fmt.Errorf("dimensions %+v %s", dimensions, err.Error())
			}

			lbl = append(lbl, prompb.Label{
				Name:  dimName,
				Value: val,
			})
		}

		// 如果是非时间聚合计算，则无需进行指标名的拼接作用
		if metricLabel != nil {
			lbl = append(lbl, *metricLabel)
		}

		var buf strings.Builder
		for _, l := range lbl {
			buf.WriteString(l.String())
		}

		// 同一个 series 进行合并分组
		key := buf.String()
		if _, ok := tsMap[key]; !ok {
			tsMap[key] = &prompb.TimeSeries{
				Labels:  lbl,
				Samples: make([]prompb.Sample, 0),
			}
		}

		tsMap[key].Samples = append(tsMap[key].Samples, prompb.Sample{
			Value:     vv,
			Timestamp: vt,
		})
	}

	// 转换结构体
	res.Timeseries = make([]*prompb.TimeSeries, 0, len(tsMap))
	for _, ts := range tsMap {
		res.Timeseries = append(res.Timeseries, ts)
	}

	return res, nil
}

func (i *Instance) table(query *metadata.Query) string {
	table := fmt.Sprintf("`%s`", query.DB)
	if query.Measurement != "" {
	}
	return table
}

// QueryRawData 直接查询原始返回
func (i *Instance) QueryRawData(ctx context.Context, query *metadata.Query, start, end time.Time, dataCh chan<- map[string]any) (total int64, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("doris query error: %s", r)
		}
	}()

	ctx, span := trace.NewSpan(ctx, "bk-sql-query-raw")
	defer span.End(&err)

	span.Set("query-raw-start", start)
	span.Set("query-raw-end", end)

	if start.UnixMilli() > end.UnixMilli() || start.UnixMilli() == 0 {
		return
	}

	rangeLeftTime := end.Sub(start)
	metric.TsDBRequestRangeMinute(ctx, rangeLeftTime, i.InstanceType())

	if i.maxLimit > 0 {
		maxLimit := i.maxLimit + i.tolerance
		// 如果不传 size，则取最大的限制值
		if query.Size == 0 || query.Size > i.maxLimit {
			query.Size = maxLimit
		}
	}

	queryFactory := NewQueryFactory(ctx, query).WithRangeTime(start, end)

	sql, err := queryFactory.SQL()
	if err != nil {
		return
	}

	data, err := i.sqlQuery(ctx, sql, span)
	if err != nil {
		return
	}

	if data == nil {
		return
	}

	span.Set("data-total-records", data.TotalRecords)
	log.Infof(ctx, "total records: %d", data.TotalRecords)

	if i.maxLimit > 0 && data.TotalRecords > i.maxLimit {
		return
	}

	for _, list := range data.List {
		list[KeyIndex] = query.DB
		list[KeyTableID] = query.TableID
		list[KeyDataLabel] = query.DataLabel

		if query.HighLight.Enable {
			list[KeyHighLight] = ""
		}

		dataCh <- list
	}

	total = int64(data.TotalRecords)
	return
}

func (i *Instance) QuerySeriesSet(ctx context.Context, query *metadata.Query, start, end time.Time) storage.SeriesSet {
	var (
		err error
	)
	ctx, span := trace.NewSpan(ctx, "bk-sql-query-series-set")
	defer span.End(&err)

	span.Set("query-series-set-start", start)
	span.Set("query-series-set-end", end)

	if start.UnixMilli() > end.UnixMilli() || start.UnixMilli() == 0 {
		return storage.ErrSeriesSet(fmt.Errorf("range time is error, start: %s, end: %s ", start, end))
	}

	rangeLeftTime := end.Sub(start)
	metric.TsDBRequestRangeMinute(ctx, rangeLeftTime, i.InstanceType())

	if i.maxLimit > 0 {
		maxLimit := i.maxLimit + i.tolerance
		// 如果不传 size，则取最大的限制值
		if query.Size == 0 || query.Size > i.maxLimit {
			query.Size = maxLimit
		}
	}

	queryFactory := NewQueryFactory(ctx, query).WithRangeTime(start, end)

	sql, err := queryFactory.SQL()
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	data, err := i.sqlQuery(ctx, sql, span)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	if data == nil {
		return storage.EmptySeriesSet()
	}

	span.Set("data-total-records", data.TotalRecords)
	log.Infof(ctx, "total records: %d", data.TotalRecords)

	if i.maxLimit > 0 && data.TotalRecords > i.maxLimit {
		return storage.ErrSeriesSet(fmt.Errorf("记录数(%d)超过限制(%d)", data.TotalRecords, i.maxLimit))
	}

	qr, err := i.formatData(ctx, start, query, data.SelectFieldsOrder, data.List)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	return remote.FromQueryResult(true, qr)
}

func (i *Instance) DirectQueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, error) {
	log.Warnf(ctx, "%s not support direct query range", i.InstanceType())
	return nil, nil
}

func (i *Instance) DirectQuery(ctx context.Context, qs string, end time.Time) (promql.Vector, error) {
	log.Warnf(ctx, "%s not support direct query", i.InstanceType())
	return nil, nil
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	log.Warnf(ctx, "%s not support query exemplar", i.InstanceType())
	return nil, nil
}

func (i *Instance) QueryLabelNames(ctx context.Context, query *metadata.Query, start, end time.Time) ([]string, error) {
	var (
		err error
	)

	ctx, span := trace.NewSpan(ctx, "bk-sql-label-name")
	defer span.End(&err)

	// 取字段名不需要返回数据，但是 size 不能使用 0，所以还是用 1
	query.Size = 1

	queryFactory := NewQueryFactory(ctx, query).WithRangeTime(start, end)
	sql, err := queryFactory.SQL()
	if err != nil {
		return nil, err
	}

	data, err := i.sqlQuery(ctx, sql, span)
	if err != nil {
		return nil, err
	}

	lbs := i.dims(data.SelectFieldsOrder, query.Field)
	return lbs, err
}

func (i *Instance) QueryLabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time) ([]string, error) {
	var (
		err error

		lbMap = make(map[string]struct{})
	)

	ctx, span := trace.NewSpan(ctx, "bk-sql-label-values")
	defer span.End(&err)

	if name == labels.MetricName {
		return nil, fmt.Errorf("not support metric query with %s", name)
	}

	// 使用聚合的方式统计维度组合
	query.Aggregates = metadata.Aggregates{
		{
			Dimensions: []string{name},
			Name:       "count",
		},
	}

	queryFactory := NewQueryFactory(ctx, query).WithRangeTime(start, end)
	sql, err := queryFactory.SQL()
	if err != nil {
		return nil, err
	}

	data, err := i.sqlQuery(ctx, sql, span)
	if err != nil {
		return nil, err
	}

	for _, d := range data.List {
		value, err := getValue(name, d)
		if err != nil {
			return nil, err
		}

		if value != "" {
			lbMap[value] = struct{}{}
		}
	}

	lbs := make([]string, 0, len(lbMap))
	for k := range lbMap {
		lbs = append(lbs, k)
	}

	return lbs, err
}

func (i *Instance) QuerySeries(ctx context.Context, query *metadata.Query, start, end time.Time) ([]map[string]string, error) {
	return nil, nil
}

func (i *Instance) DirectLabelNames(ctx context.Context, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	return nil, nil
}

func (i *Instance) DirectLabelValues(ctx context.Context, name string, start, end time.Time, limit int, matchers ...*labels.Matcher) ([]string, error) {
	return nil, nil
}

func (i *Instance) InstanceType() string {
	return consul.BkSqlStorageType
}

func getValue(k string, d map[string]interface{}) (string, error) {
	var value string
	if v, ok := d[k]; ok {
		// 增加 nil 判断，避免回传的数值为空
		if v == nil {
			return value, nil
		}

		switch v.(type) {
		case string:
			value = fmt.Sprintf("%s", v)
		case float64, float32:
			value = fmt.Sprintf("%.f", v)
		case int64, int32, int:
			value = fmt.Sprintf("%d", v)
		default:
			return value, fmt.Errorf("get_value_error: type %T, %v in %s with %+v", v, v, k, d)
		}
	}
	return value, nil
}
