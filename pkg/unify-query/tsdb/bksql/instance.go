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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
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

var _ tsdb.Instance = (*Instance)(nil)

func (i Instance) checkResult(res *Result) error {
	if !res.Result {
		return fmt.Errorf(
			"%s, %s, %s", res.Message, res.Errors.Error, res.Errors.QueryId,
		)
	}
	if res.Code != OK {
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

func (i Instance) query(ctx context.Context, sql string) (*QueryAsyncResultData, error) {
	var (
		data       *QueryAsyncData
		stateData  *QueryAsyncStateData
		resultData *QueryAsyncResultData

		ok  bool
		err error
	)

	log.Infof(ctx, "%s: %s", i.GetInstanceType(), sql)

	// 发起异步查询
	res := i.Client.QueryAsync(ctx, sql)
	if err = i.checkResult(res); err != nil {
		return resultData, err
	}

	ctx, cancel := context.WithTimeout(ctx, i.Timeout)
	defer cancel()

	if data, ok = res.Data.(*QueryAsyncData); !ok {
		return resultData, fmt.Errorf("queryAsyncData type is error: %T", res.Data)
	}

	if data == nil || data.QueryId == "" {
		return resultData, fmt.Errorf("queryAsyncData queryID is emtpy: %+v", data)
	}

	receiveCH := make(chan struct{}, 1)
	go func() {
		defer func() {
			receiveCH <- struct{}{}
		}()

		for {
			select {
			case <-ctx.Done():
				err = fmt.Errorf("queryAsyncState timeout %s", i.Timeout.String())
				return
			default:
				stateRes := i.Client.QueryAsyncState(ctx, data.QueryId)
				if err = i.checkResult(res); err != nil {
					return
				}
				if stateData, ok = stateRes.Data.(*QueryAsyncStateData); !ok {
					err = fmt.Errorf("queryAsyncState type is error: %T", res.Data)
					return
				}
				switch stateData.State {
				case RUNNING:
					time.Sleep(i.IntervalTime)
					continue
				case FINISHED:
					return
				default:
					err = fmt.Errorf("queryAsyncState error %+v", stateData)
					return
				}
			}
		}
	}()

	<-receiveCH
	if err != nil {
		return resultData, err
	}

	resultRes := i.Client.QueryAsyncResult(ctx, data.QueryId)
	if err = i.checkResult(res); err != nil {
		return resultData, err
	}

	if resultData, ok = resultRes.Data.(*QueryAsyncResultData); !ok {
		return resultData, fmt.Errorf("queryAsyncResult type is error: %T", res.Data)
	}

	return resultData, nil
}

func (i Instance) formatData(field string, data *QueryAsyncResultData) (*prompb.QueryResult, error) {
	res := &prompb.QueryResult{}

	if data == nil {
		return res, fmt.Errorf("data is nil")
	}
	if len(data.List) == 0 {
		return res, nil
	}
	// 维度结构体为空则任务异常
	if len(data.ResultSchema) == 0 {
		return res, fmt.Errorf("schema is empty")
	}

	// 获取该指标的维度 key
	dimensions := make([]string, 0)
	for _, dim := range data.ResultSchema {
		// 判断是否是内置维度，内置维度不是用户上报的维度
		if _, ok := internalDimension[dim.FieldAlias]; ok {
			continue
		}
		// 如果是字段值也需要跳过
		if dim.FieldAlias == field {
			continue
		}

		dimensions = append(dimensions, dim.FieldAlias)
	}

	if len(dimensions) == 0 {
		return res, fmt.Errorf("dimensions is empty")
	}

	tsMap := make(map[string]*prompb.TimeSeries, 0)
	for _, d := range data.List {
		// 优先获取时间和值
		var (
			vt int64
			vv float64

			vtLong   interface{}
			vvDouble interface{}

			ok bool
		)

		// 获取时间戳，单位是毫秒
		if vtLong, ok = d[dtEventTimeStamp]; !ok {
			return res, fmt.Errorf("dimension %s is emtpy", dtEventTimeStamp)
		} else {
			switch vtLong.(type) {
			case int64:
				vt = vtLong.(int64)
			case float64:
				vt = int64(vtLong.(float64))
			default:
				return res, fmt.Errorf("%s type is error %T, %v", dtEventTimeStamp, vtLong, vtLong)
			}
		}

		// 获取值
		if vvDouble, ok = d[field]; !ok {
			return res, fmt.Errorf("dimension %s is emtpy", field)
		} else {
			switch vvDouble.(type) {
			case int64:
				vv = float64(vvDouble.(int64))
			case float64:
				vv = vvDouble.(float64)
			default:
				return res, fmt.Errorf("%s type is error %T, %v", field, vvDouble, vvDouble)
			}
		}

		var buf strings.Builder
		lbl := make([]prompb.Label, 0, len(dimensions))
		// 获取维度信息
		for _, dimName := range dimensions {
			var (
				value string
				v     interface{}
			)
			if v, ok = d[dimName]; ok {
				switch v.(type) {
				case string:
					value = v.(string)
				default:
					return res, fmt.Errorf("dimensions error type %T, %v in %s with %+v", v, v, dimName, d)
				}
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

// bkSql 构建查询语句
func (i Instance) bkSql(ctx context.Context, query *metadata.Query, hints *storage.SelectHints, matchers ...*labels.Matcher) string {
	var (
		sql string

		aggField    string
		measurement string

		groupList []string
		where     string

		limit int
	)

	measurement = query.Measurement

	if i.Limit <= 0 {
		// 确保一定有值
		i.Limit = 2e5
	}
	limit = i.Limit + i.Tolerance

	// 判断是否需要提前聚合
	newFuncName, window, dims := query.GetDownSampleFunc(hints)
	if newFuncName != "" && window.Seconds() >= time.Minute.Seconds() {
		// 如果符合聚合规则并且聚合周期大于等于1m，则进行提前聚合
		groupList = make([]string, 0, len(dims)+1)
		for _, dim := range dims {
			groupList = append(groupList, dim)
		}

		timeGrouping := fmt.Sprintf("minute%d", int(window.Minutes()))
		groupList = append(groupList, timeGrouping)

		aggField = fmt.Sprintf("%s(`%s`) AS `%s`, %s, MAX(%s) AS %s", strings.ToUpper(newFuncName), query.Field, query.Field, strings.Join(groupList, ", "), dtEventTimeStamp, dtEventTimeStamp)
	} else {
		aggField = "*"
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
	sql = fmt.Sprintf(`%s ORDER BY %s ASC`, sql, dtEventTimeStamp)
	if limit > 0 {
		sql = fmt.Sprintf(`%s LIMIT %d`, sql, limit)
	}

	return sql
}

func (i Instance) QueryRaw(ctx context.Context, query *metadata.Query, hints *storage.SelectHints, matchers ...*labels.Matcher) storage.SeriesSet {
	if hints.Start > hints.End || hints.Start == 0 {
		return storage.ErrSeriesSet(fmt.Errorf("range time is error, start: %d, end: %d ", hints.Start, hints.End))
	}

	sql := i.bkSql(ctx, query, hints, matchers...)
	data, err := i.query(ctx, sql)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}
	if data.TotalRecords > i.Limit {
		return storage.ErrSeriesSet(fmt.Errorf("记录数(%d)超过限制(%d)", data.TotalRecords, i.Limit))
	}

	qr, err := i.formatData(query.Field, data)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	return remote.FromQueryResult(true, qr)
}

func (i Instance) QueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, error) {
	//TODO implement me
	panic("implement me")
}

func (i Instance) Query(ctx context.Context, qs string, end time.Time) (promql.Vector, error) {
	//TODO implement me
	panic("implement me")
}

func (i Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	//TODO implement me
	panic("implement me")
}

func (i Instance) LabelNames(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	//TODO implement me
	panic("implement me")
}

func (i Instance) LabelValues(ctx context.Context, query *metadata.Query, name string, start, end time.Time, matchers ...*labels.Matcher) ([]string, error) {
	//TODO implement me
	panic("implement me")
}

func (i Instance) Series(ctx context.Context, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) storage.SeriesSet {
	//TODO implement me
	panic("implement me")
}

func (i Instance) GetInstanceType() string {
	return i.Client.PreferStorage
}
