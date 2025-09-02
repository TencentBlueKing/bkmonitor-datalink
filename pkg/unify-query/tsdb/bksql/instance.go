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
	"encoding/json"
	"fmt"
	"time"

	"github.com/prometheus/prometheus/model/labels"
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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

const (
	TableFieldName     = "Field"
	TableFieldType     = "Type"
	TableFieldAnalyzed = "Analyzed"

	TableTypeVariant = "variant"
)

type Instance struct {
	tsdb.DefaultInstance

	ctx context.Context

	querySyncUrl  string
	queryAsyncUrl string

	headers map[string]string

	timeout      time.Duration
	intervalTime time.Duration

	maxLimit   int
	tolerance  int
	sliceLimit int

	client *Client
}

var _ tsdb.Instance = (*Instance)(nil)

type Options struct {
	Address string
	Headers map[string]string

	Timeout    time.Duration
	MaxLimit   int
	SliceLimit int
	Tolerance  int

	Curl curl.Curl
}

func NewInstance(ctx context.Context, opt *Options) (*Instance, error) {
	if opt.Address == "" {
		return nil, fmt.Errorf("address is empty")
	}
	instance := &Instance{
		ctx:        ctx,
		timeout:    opt.Timeout,
		maxLimit:   opt.MaxLimit,
		tolerance:  opt.Tolerance,
		sliceLimit: opt.SliceLimit,
		client:     (&Client{}).WithUrl(opt.Address).WithHeader(opt.Headers).WithCurl(opt.Curl),
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

func (i *Instance) sqlQuery(ctx context.Context, sql string) (*QuerySyncResultData, error) {
	var (
		data *QuerySyncResultData

		ok   bool
		err  error
		span *trace.Span
	)

	ctx, span = trace.NewSpan(ctx, "sql-query")
	defer span.End(&err)

	if sql == "" {
		return data, nil
	}

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

	span.Set("result-size", len(data.List))
	span.Set("result-sql", data.Sql)

	return data, nil
}

func (i *Instance) getFieldsMap(ctx context.Context, sql string) (map[string]sql_expr.FieldOption, error) {
	fieldsMap := make(map[string]sql_expr.FieldOption)

	if sql == "" {
		return nil, nil
	}

	data, err := i.sqlQuery(ctx, sql)
	if err != nil {
		return nil, err
	}

	for _, list := range data.List {
		var (
			k             string
			fieldType     string
			fieldAnalyzed string

			ok bool
		)
		k, ok = list[TableFieldName].(string)
		if !ok {
			continue
		}

		fieldType, ok = list[TableFieldType].(string)
		if !ok {
			continue
		}

		opt := sql_expr.FieldOption{
			Type: fieldType,
		}

		if fieldAnalyzed, ok = list[TableFieldAnalyzed].(string); ok {
			if fieldAnalyzed == "true" {
				opt.Analyzed = true
			}
		}

		fieldsMap[k] = opt
	}

	return fieldsMap, nil
}

func (i *Instance) InitQueryFactory(ctx context.Context, query *metadata.Query, start, end time.Time) (*QueryFactory, error) {
	var err error

	ctx, span := trace.NewSpan(ctx, "instance-init-query-factory")
	defer span.End(&err)

	f := NewQueryFactory(ctx, query).WithRangeTime(start, end)

	// 只有 Doris 才需要获取字段表结构
	if query.Measurement == sql_expr.Doris {
		fieldsMap, err := i.getFieldsMap(ctx, f.DescribeTableSQL())
		if err != nil {
			return f, err
		}

		// 只能使用在表结构的字段才能使用
		var keepColumns []string
		for _, k := range query.Source {
			if _, ok := fieldsMap[k]; ok {
				keepColumns = append(keepColumns, k)
			}
		}
		out, _ := json.Marshal(fieldsMap)
		span.Set("table_fields_map", string(out))

		span.Set("keep-columns", keepColumns)
		f.WithFieldsMap(fieldsMap).WithKeepColumns(keepColumns)
	}

	return f, nil
}

func (i *Instance) Table(query *metadata.Query) string {
	table := fmt.Sprintf("`%s`", query.DB)
	if query.Measurement != "" {
		table += "." + query.Measurement
	}
	return table
}

// QueryRawData 直接查询原始返回
func (i *Instance) QueryRawData(ctx context.Context, query *metadata.Query, start, end time.Time, dataCh chan<- map[string]any) (total int64, option *metadata.ResultTableOption, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("doris query panic: %s", r)
		}
	}()

	option = query.ResultTableOption
	if option == nil {
		option = &metadata.ResultTableOption{}
	}

	ctx, span := trace.NewSpan(ctx, "bk-sql-query-raw")
	defer span.End(&err)

	span.Set("query-raw-start", start)
	span.Set("query-raw-end", end)

	if start.UnixMilli() > end.UnixMilli() || start.UnixMilli() == 0 {
		err = fmt.Errorf("start time must less than end time")
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

	if option.From != nil {
		query.From = *option.From
	}

	queryFactory, err := i.InitQueryFactory(ctx, query, start, end)
	if err != nil {
		return
	}
	sql, err := queryFactory.SQL()
	if err != nil {
		return
	}

	// 如果是 dry run 则直接返回 sql 查询语句
	if query.DryRun {
		option.SQL = sql
		return
	}

	data, err := i.sqlQuery(ctx, sql)
	if err != nil {
		err = fmt.Errorf("sql [%s] query err: %s", sql, err.Error())
		return
	}

	if data == nil {
		return
	}

	if data.ResultSchema != nil {
		option.ResultSchema = data.ResultSchema
	}

	span.Set("data-total-records", data.TotalRecords)
	span.Set("data-list-size", len(data.List))

	for _, list := range data.List {
		newData := queryFactory.ReloadListData(list, false)
		newData[metadata.KeyIndex] = query.DB
		// 注入原始数据需要的字段
		query.DataReload(newData)

		dataCh <- newData
	}

	total = int64(data.TotalRecordSize)
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

	// series 计算需要按照时间排序
	query.Orders = append(metadata.Orders{
		{
			Name: sql_expr.FieldTime,
			Ast:  true,
		},
	}, query.Orders...)

	queryFactory, err := i.InitQueryFactory(ctx, query, start, end)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}
	sql, err := queryFactory.SQL()
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	data, err := i.sqlQuery(ctx, sql)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	if data == nil {
		return storage.EmptySeriesSet()
	}

	span.Set("data-total-records", data.TotalRecords)

	if i.maxLimit > 0 && data.TotalRecords > i.maxLimit {
		return storage.ErrSeriesSet(fmt.Errorf("记录数(%d)超过限制(%d)", data.TotalRecords, i.maxLimit))
	}

	qr, err := queryFactory.FormatDataToQueryResult(ctx, data.List)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	return remote.FromQueryResult(true, qr)
}

func (i *Instance) DirectQueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, bool, error) {
	log.Warnf(ctx, "%s not support direct query range", i.InstanceType())
	return nil, false, nil
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

	queryFactory, err := i.InitQueryFactory(ctx, query, start, end)
	if err != nil {
		return nil, err
	}

	sql, err := queryFactory.SQL()
	if err != nil {
		return nil, err
	}

	data, err := i.sqlQuery(ctx, sql)
	if err != nil {
		return nil, err
	}

	var lbs []string
	for _, k := range data.SelectFieldsOrder {
		// 忽略内置字段
		if checkInternalDimension(k) {
			continue
		}

		// 忽略内置值和时间字段
		if k == sql_expr.TimeStamp || k == sql_expr.Value {
			continue
		}

		lbs = append(lbs, k)
	}

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

	queryFactory, err := i.InitQueryFactory(ctx, query, start, end)
	if err != nil {
		return nil, err
	}
	sql, err := queryFactory.SQL()
	if err != nil {
		return nil, err
	}

	data, err := i.sqlQuery(ctx, sql)
	if err != nil {
		return nil, err
	}

	encodeFunc := metadata.GetFieldFormat(ctx).EncodeFunc()
	if encodeFunc != nil {
		name = encodeFunc(name)
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
