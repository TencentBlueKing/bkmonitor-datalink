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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/prometheus/prompb"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sqlExpr"
)

const (
	selectAll = "*"

	dtEventTimeStamp = "dtEventTimeStamp"
	dtEventTime      = "dtEventTime"
	localTime        = "localTime"
	startTime        = "_startTime_"
	endTime          = "_endTime_"
	theDate          = "thedate"

	timeStamp = "_timestamp_"
	value     = "_value_"

	FieldValue = "_value"
	FieldTime  = "_time"
)

var (
	internalDimension = map[string]struct{}{
		value:            {},
		timeStamp:        {},
		dtEventTimeStamp: {},
		dtEventTime:      {},
		localTime:        {},
		startTime:        {},
		endTime:          {},
		theDate:          {},
	}
)

type QueryFactory struct {
	ctx  context.Context
	lock sync.RWMutex

	query *metadata.Query

	start time.Time
	end   time.Time
	step  time.Duration

	orders metadata.Orders

	timeField string

	expr sqlExpr.SQLExpr
}

func NewQueryFactory(ctx context.Context, query *metadata.Query) *QueryFactory {
	f := &QueryFactory{
		ctx:    ctx,
		query:  query,
		orders: make(metadata.Orders),
	}
	if query.Orders != nil {
		for k, v := range query.Orders {
			f.orders[k] = v
		}
	}

	if query.TimeField.Name != "" {
		f.timeField = query.TimeField.Name
	} else {
		f.timeField = dtEventTimeStamp
	}

	fieldsMap := make(map[string]string)
	f.expr = sqlExpr.GetSQLExpr(f.query.Measurement).WithFieldsMap(fieldsMap).WithInternalFields(f.timeField, query.Field)
	return f
}

func (f *QueryFactory) WithRangeTime(start, end time.Time) *QueryFactory {
	f.start = start
	f.end = end
	return f
}

func (f *QueryFactory) getTheDateIndexFilters() (theDateFilter string, err error) {
	// bkbase 使用 时区东八区 转换为 thedate
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return
	}

	start := f.start.In(loc)
	end := f.end.In(loc)

	dates := function.RangeDateWithUnit("day", start, end, 1)

	if len(dates) == 0 {
		return
	}

	if len(dates) == 1 {
		theDateFilter = fmt.Sprintf("`%s` = '%s'", theDate, dates[0])
		return
	}

	theDateFilter = fmt.Sprintf("`%s` >= '%s' AND `%s` <= '%s'", theDate, dates[0], theDate, dates[len(dates)-1])
	return
}

func (f *QueryFactory) BuildWhere() (string, error) {
	var s []string
	s = append(s, fmt.Sprintf("`%s` >= %d AND `%s` < %d", f.timeField, f.start.UnixMilli(), f.timeField, f.end.UnixMilli()))

	theDateFilter, err := f.getTheDateIndexFilters()
	if err != nil {
		return "", err
	}
	if theDateFilter != "" {
		s = append(s, theDateFilter)
	}

	// QueryString to sql
	if f.query.QueryString != "" {
		qs, err := f.expr.ParserQueryString(f.query.QueryString)
		if err != nil {
			return "", err
		}

		if qs != "" {
			s = append(s, qs)
		}
	}

	// AllConditions to sql
	if len(f.query.AllConditions) > 0 {
		qs, err := f.expr.ParserAllConditions(f.query.AllConditions)
		if err != nil {
			return "", err
		}

		if qs != "" {
			s = append(s, qs)
		}
	}

	return strings.Join(s, " AND "), nil
}

func (f *QueryFactory) SQL() (sql string, err error) {
	selectString, groupString, err := f.expr.ParserAggregates(f.query.Aggregates)
	if err != nil {
		return
	}

	table := fmt.Sprintf("`%s`", f.query.DB)
	if f.query.Measurement != "" {
		table += "." + f.query.Measurement
	}

	sql += fmt.Sprintf("SELECT %s FROM %s", selectString, table)
	whereString, err := f.BuildWhere()
	if err != nil {
		return
	}
	if whereString != "" {
		sql += " WHERE " + whereString
	}
	if groupString != "" {
		sql += " GROUP BY " + groupString
	}

	orders := make([]string, 0)
	for key, asc := range f.orders {
		var orderField string
		switch key {
		case FieldValue:
			orderField = f.query.Field
		case FieldTime:
			orderField = timeStamp
		default:
			orderField = key
		}
		ascName := "ASC"
		if !asc {
			ascName = "DESC"
		}
		orders = append(orders, fmt.Sprintf("`%s` %s", orderField, ascName))
	}
	if len(orders) > 0 {
		sort.Strings(orders)
		sql += " ORDER BY " + strings.Join(orders, ", ")
	}
	if f.query.From > 0 {
		sql += fmt.Sprintf(" OFFSET %d", f.query.From)
	}
	if f.query.Size > 0 {
		sql += fmt.Sprintf(" LIMIT %d", f.query.Size)
	}

	return
}

func (f *QueryFactory) dims(dims []string, field string) []string {
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

func (f *QueryFactory) FormatData(keys []string, list []map[string]interface{}) (*prompb.QueryResult, error) {
	res := &prompb.QueryResult{}

	if len(list) == 0 {
		return res, nil
	}
	// 维度结构体为空则任务异常
	if len(keys) == 0 {
		return res, fmt.Errorf("SelectFieldsOrder is empty")
	}

	// 获取该指标的维度 key
	field := f.query.Field
	dimensions := f.dims(keys, field)

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
			vtLong = 0
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
			return res, fmt.Errorf("%s type is error %T, %v", f.timeField, vtLong, vtLong)
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
