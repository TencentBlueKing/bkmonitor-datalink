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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

type Instance struct {
	Ctx context.Context

	Timeout      time.Duration
	IntervalTime time.Duration

	Limit     int
	Tolerance int

	Client *Client
}

func (i *Instance) QueryReference(ctx context.Context, query *metadata.Query, start int64, end int64) (*prompb.QueryResult, error) {
	//TODO implement me
	panic("implement me")
}

var _ tsdb.Instance = (*Instance)(nil)

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

	ctx, cancel := context.WithTimeout(ctx, i.Timeout)
	defer cancel()

	// 发起异步查询
	res := i.Client.QuerySync(ctx, sql, span)
	if err = i.checkResult(res); err != nil {
		return data, err
	}

	span.Set("query-timeout", i.Timeout.String())
	span.Set("query-interval-time", i.IntervalTime.String())

	if data, ok = res.Data.(*QuerySyncResultData); !ok {
		return data, fmt.Errorf("queryAsyncResult type is error: %T", res.Data)
	}

	return data, nil
}

func (i *Instance) queryAsync(ctx context.Context, sql string, span *trace.Span) (*QueryAsyncResultData, error) {
	var (
		data       *QueryAsyncData
		stateData  *QueryAsyncStateData
		resultData *QueryAsyncResultData

		startAnaylize time.Time

		ok  bool
		err error
	)

	log.Infof(ctx, "%s: %s", i.GetInstanceType(), sql)
	span.Set("query-sql", sql)

	ctx, cancel := context.WithTimeout(ctx, i.Timeout)
	defer cancel()

	user := metadata.GetUser(ctx)
	startAnaylize = time.Now()

	// 发起异步查询
	res := i.Client.QueryAsync(ctx, sql, span)
	if err = i.checkResult(res); err != nil {
		return resultData, err
	}

	queryCost := time.Since(startAnaylize)
	metric.TsDBRequestSecond(
		ctx, queryCost, user.SpaceUid, i.GetInstanceType(),
	)

	if data, ok = res.Data.(*QueryAsyncData); !ok {
		return resultData, fmt.Errorf("queryAsyncData type is error: %T", res.Data)
	}

	if data == nil || data.QueryId == "" {
		return resultData, fmt.Errorf("queryAsyncData queryID is emtpy: %+v", data)
	}

	span.Set("query-timeout", i.Timeout.String())
	span.Set("query-interval-time", i.IntervalTime.String())
	span.Set("data-query-id", data.QueryId)

	err = func() error {
		for {
			select {
			case <-ctx.Done():
				return fmt.Errorf("queryAsyncState %s timeout %s", data.QueryId, i.Timeout.String())
			default:
				stateRes := i.Client.QueryAsyncState(ctx, data.QueryId, span)
				if err = i.checkResult(res); err != nil {
					return err
				}
				if stateData, ok = stateRes.Data.(*QueryAsyncStateData); !ok {
					return fmt.Errorf("queryAsyncState type is error: %T", res.Data)
				}
				switch stateData.State {
				case RUNNING:
					time.Sleep(i.IntervalTime)
					continue
				case FINISHED:
					return nil
				default:
					return fmt.Errorf("queryAsyncState error %+v", stateData)
				}
			}
		}
	}()

	if err != nil {
		return resultData, err
	}

	resultRes := i.Client.QueryAsyncResult(ctx, data.QueryId, span)
	if err = i.checkResult(res); err != nil {
		return resultData, err
	}

	if resultData, ok = resultRes.Data.(*QueryAsyncResultData); !ok {
		return resultData, fmt.Errorf("queryAsyncResult type is error: %T", res.Data)
	}

	return resultData, nil
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

func (i *Instance) formatData(field string, isCount bool, keys []string, list []map[string]interface{}) (*prompb.QueryResult, error) {
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
			return res, fmt.Errorf("dimension %s is emtpy", timeStamp)
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
		if vvDouble, ok = d[field]; !ok {
			return res, fmt.Errorf("dimension %s is emtpy", field)
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
			return res, fmt.Errorf("%s type is error %T, %v", field, vvDouble, vvDouble)
		}

		var buf strings.Builder
		lbl := make([]prompb.Label, 0, len(dimensions))
		// 获取维度信息
		for _, dimName := range dimensions {
			value, err := getValue(dimName, d)
			if err != nil {
				return res, fmt.Errorf("dimensions %+v %s", dimensions, err.Error())
			}

			buf.WriteString(fmt.Sprintf("%s:%s,", dimName, value))
			lbl = append(lbl, prompb.Label{
				Name:  dimName,
				Value: value,
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

		// 拼装 count 信息
		repNum := 1
		if isCount {
			repNum = int(vv)
		}
		for j := 0; j < repNum; j++ {
			tsMap[key].Samples = append(tsMap[key].Samples, prompb.Sample{
				Value:     vv,
				Timestamp: vt,
			})
		}
	}

	// 转换结构体
	res.Timeseries = make([]*prompb.TimeSeries, 0, len(tsMap))
	for _, ts := range tsMap {
		res.Timeseries = append(res.Timeseries, ts)
	}

	return res, nil
}

// bkSql 构建查询语句
func (i *Instance) bkSql(ctx context.Context, query *metadata.Query, hints *storage.SelectHints, matchers ...*labels.Matcher) (string, bool) {
	var (
		sql string

		aggField    string
		measurement string

		groupList []string
		where     string

		isCount bool
	)

	measurement = query.Measurement
	maxLimit := i.Limit + i.Tolerance
	limit := query.Size
	if limit == 0 || limit > maxLimit {
		limit = maxLimit
	}

	// 判断是否需要提前聚合
	newFuncName, window, dims := query.GetDownSampleFunc(hints)
	if newFuncName != "" {
		isCount = newFuncName == metadata.COUNT
		// 兼容函数
		if newFuncName == metadata.MEAN {
			newFuncName = metadata.AVG
		}

		// 如果符合聚合规则并且聚合周期大于等于1m，则进行提前聚合
		groupList = make([]string, 0, len(dims)+1)
		for _, dim := range dims {
			dim = fmt.Sprintf("`%s`", dim)
			groupList = append(groupList, dim)
		}

		timeField := fmt.Sprintf(`(dtEventTimestamp - (dtEventTimestamp %% %d))`, window.Milliseconds())
		groupList = append(groupList, timeField)

		aggField = fmt.Sprintf("%s(`%s`) AS `%s`, MAX(%s) AS `%s`", strings.ToUpper(newFuncName), query.Field, query.Field, timeField, timeStamp)
		if len(dims) > 0 {
			aggField = fmt.Sprintf("%s, %s", aggField, strings.Join(dims, ", "))
		}
	} else {
		aggField = fmt.Sprintf("*, %s AS `%s`", dtEventTimeStamp, timeStamp)
	}

	where = fmt.Sprintf("%s >= %d AND %s < %d", dtEventTimeStamp, hints.Start, dtEventTimeStamp, hints.End)
	// 拼接过滤条件
	if query.BkSqlCondition != "" {
		where = fmt.Sprintf("%s AND (%s)", where, query.BkSqlCondition)
	}

	sql = fmt.Sprintf(`SELECT %s FROM %s WHERE %s`, aggField, measurement, where)
	if len(groupList) > 0 {
		sql = fmt.Sprintf(`%s GROUP BY %s`, sql, strings.Join(groupList, ", "))
	}
	sql = fmt.Sprintf("%s ORDER BY `%s` ASC", sql, timeStamp)
	if limit > 0 {
		sql = fmt.Sprintf(`%s LIMIT %d`, sql, limit)
	}

	return sql, isCount
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

	if i.Client == nil {
		return nil, fmt.Errorf("es client is nil")
	}

	if i.Limit > 0 {
		maxLimit := i.Limit + i.Tolerance
		// 如果不传 size，则取最大的限制值
		if query.Size == 0 || query.Size > i.Limit {
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

	qr, err := i.formatData(query.Field, fact.IsPromCount(), data.SelectFieldsOrder, data.List)
	return qr, err
}

func (i *Instance) QueryRaw(ctx context.Context, query *metadata.Query, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	var (
		err error
	)
	ctx, span := trace.NewSpan(ctx, "bk-sql-raw")
	defer span.End(&err)

	if hints.Start > hints.End || hints.Start == 0 {
		return storage.ErrSeriesSet(fmt.Errorf("range time is error, start: %d, end: %d ", hints.Start, hints.End))
	}

	if i.Limit > 0 {
		maxLimit := i.Limit + i.Tolerance
		// 如果不传 size，则取最大的限制值
		if query.Size == 0 || query.Size > i.Limit {
			query.Size = maxLimit
		}
	}

	sql, isCount := i.bkSql(ctx, query, hints, matchers...)
	data, err := i.sqlQuery(ctx, sql, span)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	if data == nil {
		return storage.EmptySeriesSet()
	}

	span.Set("data-total-records", data.TotalRecords)
	log.Infof(ctx, "total records: %d", data.TotalRecords)

	if data.TotalRecords > i.Limit {
		return storage.ErrSeriesSet(fmt.Errorf("记录数(%d)超过限制(%d)", data.TotalRecords, i.Limit))
	}

	qr, err := i.formatData(query.Field, isCount, data.SelectFieldsOrder, data.List)
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
