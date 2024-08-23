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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
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

func (i *Instance) QueryReference(ctx context.Context, query *metadata.Query, start int64, end int64) (*prompb.QueryResult, error) {
	//TODO implement me
	panic("implement me")
}

var _ tsdb.Instance = (*Instance)(nil)

type Options struct {
	Address string
	Headers map[string]string

	Timeout      time.Duration
	IntervalTime time.Duration
	MaxLimit     int
	Tolerance    int

	Curl curl.Curl
}

func NewInstance(ctx context.Context, opt Options) (*Instance, error) {
	if opt.Address == "" {
		return nil, fmt.Errorf("address is empty")
	}
	instance := &Instance{
		ctx:          ctx,
		timeout:      opt.Timeout,
		intervalTime: opt.IntervalTime,
		maxLimit:     opt.MaxLimit,
		tolerance:    opt.Tolerance,
		client:       (&Client{}).WithUrl(opt.Address).WithHeader(opt.Headers).WithCurl(opt.Curl),
	}
	return instance, nil
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

	log.Infof(ctx, "%s: %s", i.GetInstanceType(), sql)
	span.Set("query-sql", sql)

	ctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	// 发起异步查询
	res := i.client.QuerySync(ctx, sql, span)
	if err = i.checkResult(res); err != nil {
		return data, err
	}

	span.Set("query-timeout", i.timeout.String())
	span.Set("query-interval-time", i.intervalTime.String())

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

func (i *Instance) formatData(start time.Time, field string, keys []string, list []map[string]interface{}) (*prompb.QueryResult, error) {
	res := &prompb.QueryResult{}

	if len(list) == 0 {
		return res, nil
	}
	// 维度结构体为空则任务异常
	if len(keys) == 0 {
		return res, fmt.Errorf("SelectFieldsOrder is empty")
	}

	// 获取该指标的维度 key
	dimensions := i.dims(keys, field)

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
		if vtLong, ok = d[timeStamp]; !ok {
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
		if vvDouble, ok = d[value]; !ok {
			return res, fmt.Errorf("dimension %s is emtpy", value)
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
			return res, fmt.Errorf("%s type is error %T, %v", value, vvDouble, vvDouble)
		}

		var buf strings.Builder
		lbl := make([]prompb.Label, 0, len(dimensions))
		// 获取维度信息
		for _, dimName := range dimensions {
			val, err := getValue(dimName, d)
			if err != nil {
				return res, fmt.Errorf("dimensions %+v %s", dimensions, err.Error())
			}

			buf.WriteString(fmt.Sprintf("%s:%s,", dimName, val))
			lbl = append(lbl, prompb.Label{
				Name:  dimName,
				Value: val,
			})
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
		table += "." + query.Measurement
	}
	return table
}

// bkSql 构建查询语句
func (i *Instance) bkSql(ctx context.Context, query *metadata.Query, start, end time.Time) (string, error) {
	var (
		selectList = make([]string, 0)
		groupList  = make([]string, 0)
		orderList  = make([]string, 0)

		sqlBuilder strings.Builder
		err        error
	)

	ctx, span := trace.NewSpan(ctx, "bksql-make-sqlBuilder")
	defer span.End(&err)

	maxLimit := i.maxLimit + i.tolerance
	limit := query.Size
	if limit == 0 || limit > maxLimit {
		limit = maxLimit
	}

	if len(query.Aggregates) > 1 {
		return "", fmt.Errorf("bksql 不支持多函数聚合查询, %+v", query.Aggregates)
	}

	if len(query.Aggregates) == 1 {
		agg := query.Aggregates[0]
		if len(agg.Dimensions) > 0 {
			for _, dim := range agg.Dimensions {
				newDim := dim
				if newDim != "*" {
					newDim = fmt.Sprintf("`%s`", newDim)
				}
				groupList = append(groupList, newDim)
				selectList = append(selectList, newDim)
			}
		}

		selectList = append(selectList, fmt.Sprintf("%s(`%s`) AS `%s`", strings.ToUpper(agg.Name), query.Field, value))
		if agg.Window > 0 && !agg.Without {
			timeField := fmt.Sprintf("(`%s` - (`%s` %% %d))", dtEventTimeStamp, dtEventTimeStamp, agg.Window.Milliseconds())
			groupList = append(groupList, timeField)
			selectList = append(selectList, fmt.Sprintf("MAX(%s) AS `%s`", timeField, timeStamp))
			orderList = append(orderList, fmt.Sprintf("`%s` ASC", timeStamp))
		}
	} else {
		selectList = append(selectList, "*")
		selectList = append(selectList, fmt.Sprintf("`%s` AS `%s`", query.Field, value))
		selectList = append(selectList, fmt.Sprintf("`%s` AS `%s`", dtEventTimeStamp, timeStamp))
	}

	sqlBuilder.WriteString("SELECT ")
	sqlBuilder.WriteString(strings.Join(selectList, ", ") + " ")
	sqlBuilder.WriteString("FROM " + i.table(query) + " ")
	sqlBuilder.WriteString("WHERE " + fmt.Sprintf("`%s` >= %d AND `%s` < %d", dtEventTimeStamp, start.UnixMilli(), dtEventTimeStamp, end.UnixMilli()))
	if query.BkSqlCondition != "" {
		sqlBuilder.WriteString(" AND (" + query.BkSqlCondition + ")")
	}
	if len(groupList) > 0 {
		sqlBuilder.WriteString(" GROUP BY " + strings.Join(groupList, ", "))
	}
	if len(orderList) > 0 {
		sqlBuilder.WriteString(" ORDER BY " + strings.Join(orderList, ", "))
	}
	if limit > 0 {
		sqlBuilder.WriteString(fmt.Sprintf(" LIMIT %d", limit))
	}

	return sqlBuilder.String(), nil
}

func (i *Instance) query(
	ctx context.Context,
	query *metadata.Query,
	start time.Time,
	end time.Time,
	step time.Duration,
) (*prompb.QueryResult, error) {
	var (
		err error
	)

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("es query error: %s", r)
		}
	}()

	ctx, span := trace.NewSpan(ctx, "bk-sql-query")
	defer span.End(&err)

	if i.client == nil {
		return nil, fmt.Errorf("es client is nil")
	}

	if i.maxLimit > 0 {
		maxLimit := i.maxLimit + i.tolerance
		// 如果不传 size，则取最大的限制值
		if query.Size == 0 || query.Size > i.maxLimit {
			query.Size = maxLimit
		}
	}

	fact := NewQueryFactory(ctx, query).WithRangeTime(start, end, step)
	err = fact.ParserQuery()
	if err != nil {
		return nil, fmt.Errorf("sql parser error: %v", err)
	}

	data, err := i.sqlQuery(ctx, fact.SQL(), span)
	if err != nil {
		return nil, err
	}

	qr, err := i.formatData(start, query.Field, data.SelectFieldsOrder, data.List)
	return qr, err
}

func (i *Instance) QueryRaw(ctx context.Context, query *metadata.Query, start, end time.Time) storage.SeriesSet {
	var (
		err error
	)
	ctx, span := trace.NewSpan(ctx, "bk-sql-raw")
	defer span.End(&err)

	if start.UnixMilli() > end.UnixMilli() || start.UnixMilli() == 0 {
		return storage.ErrSeriesSet(fmt.Errorf("range time is error, start: %s, end: %s ", start, end))
	}

	if i.maxLimit > 0 {
		maxLimit := i.maxLimit + i.tolerance
		// 如果不传 size，则取最大的限制值
		if query.Size == 0 || query.Size > i.maxLimit {
			query.Size = maxLimit
		}
	}

	sql, err := i.bkSql(ctx, query, start, end)
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

	qr, err := i.formatData(start, query.Field, data.SelectFieldsOrder, data.List)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	return remote.FromQueryResult(true, qr)
}

func (i *Instance) QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, error) {
	//TODO implement me
	panic("implement me")
}

func (i *Instance) Query(ctx context.Context, qs string, end time.Time) (promql.Vector, error) {
	//TODO implement me
	panic("implement me")
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	//TODO implement me
	panic("implement me")
}

func (i *Instance) LabelNames(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	var (
		err error
	)

	ctx, span := trace.NewSpan(ctx, "bk-sql-label-name")
	defer span.End(&err)

	where := fmt.Sprintf("%s >= %d AND %s < %d", dtEventTimeStamp, start.UnixMilli(), dtEventTimeStamp, end.UnixMilli())
	// 拼接过滤条件
	if query.BkSqlCondition != "" {
		where = fmt.Sprintf("%s AND (%s)", where, query.BkSqlCondition)
	}
	sql := fmt.Sprintf("SELECT * FROM %s WHERE %s LIMIT 1", query.Measurement, where)
	data, err := i.sqlQuery(ctx, sql, span)
	if err != nil {
		return nil, err
	}

	lbs := i.dims(data.SelectFieldsOrder, query.Field)
	return lbs, err
}

func (i *Instance) LabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	var (
		err error

		lbMap = make(map[string]struct{})
	)

	ctx, span := trace.NewSpan(ctx, "bk-sql-label-values")
	defer span.End(&err)

	if name == labels.MetricName {
		return nil, fmt.Errorf("not support metric query with %s", name)
	}

	where := fmt.Sprintf("%s >= %d AND %s < %d", dtEventTimeStamp, start.UnixMilli(), dtEventTimeStamp, end.UnixMilli())
	// 拼接过滤条件
	if query.BkSqlCondition != "" {
		where = fmt.Sprintf("%s AND (%s)", where, query.BkSqlCondition)
	}
	sql := fmt.Sprintf("SELECT COUNT(`%s`) AS `%s`, %s FROM %s WHERE %s GROUP BY %s", query.Field, query.Field, name, query.Measurement, where, name)
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

func (i *Instance) Series(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
	//TODO implement me
	panic("implement me")
}

func (i *Instance) GetInstanceType() string {
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
