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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

const (
	selectAll = "*"

	dtEventTimeStamp = "dtEventTimeStamp"
	dtEventTime      = "dtEventTime"
	localTime        = "localTime"
	startTime        = "_startTime_"
	endTime          = "_endTime_"
	theDate          = "thedate"
)

var (
	internalDimension = map[string]struct{}{
		dtEventTimeStamp: {},
		dtEventTime:      {},
		localTime:        {},
		startTime:        {},
		endTime:          {},
		theDate:          {},

		sql_expr.TimeStamp: {},
		sql_expr.Value:     {},
	}
)

type QueryFactory struct {
	ctx  context.Context
	lock sync.RWMutex

	query *metadata.Query

	start time.Time
	end   time.Time

	timeAggregate sql_expr.TimeAggregate

	orders metadata.Orders

	timeField string

	expr sql_expr.SQLExpr

	highlight *metadata.HighLight
}

func NewQueryFactory(ctx context.Context, query *metadata.Query) *QueryFactory {
	f := &QueryFactory{
		ctx:       ctx,
		query:     query,
		highlight: query.HighLight,
	}

	if query.Orders != nil {
		f.orders = query.Orders
	}

	if query.TimeField.Name != "" {
		f.timeField = query.TimeField.Name
	} else {
		f.timeField = dtEventTimeStamp
	}

	f.expr = sql_expr.NewSQLExpr(f.query.Measurement).
		WithInternalFields(f.timeField, query.Field).
		WithEncode(metadata.GetPromDataFormat(ctx).EncodeFunc())

	if f.highlight != nil && f.highlight.Enable {
		f.expr.IsSetLabels(true)
	}

	return f
}

func (f *QueryFactory) WithRangeTime(start, end time.Time) *QueryFactory {
	f.start = start
	f.end = end
	return f
}

func (f *QueryFactory) WithFieldsMap(m map[string]string) *QueryFactory {
	f.expr.WithFieldsMap(m)
	return f
}

func (f *QueryFactory) WithKeepColumns(cols []string) *QueryFactory {
	f.expr.WithKeepColumns(cols)
	return f
}

func (f *QueryFactory) Table() string {
	table := fmt.Sprintf("`%s`", f.query.DB)
	if f.query.Measurement != "" {
		table += "." + f.query.Measurement
	}
	return table
}

func (f *QueryFactory) DescribeTableSQL() string {
	return f.expr.DescribeTableSQL(f.Table())
}

func (f *QueryFactory) FieldMap() map[string]string {
	return f.expr.FieldMap()
}

func (f *QueryFactory) GetLabelMap() map[string][]string {
	return f.expr.GetLabelMap()
}

func (f *QueryFactory) HighLight(data map[string]any) (newData map[string]any) {
	if f.query.HighLight == nil || !f.query.HighLight.Enable {
		return
	}

	newData = make(map[string]any)
	for k, vs := range f.GetLabelMap() {
		if vs == nil {
			return
		}

		if d, ok := data[k]; ok {
			var (
				mark1 string
				mark2 string
			)

			switch s := d.(type) {
			case string:
				if f.query.HighLight.MaxAnalyzedOffset > 0 && len(s) > f.query.HighLight.MaxAnalyzedOffset {
					mark1 = s[0:f.query.HighLight.MaxAnalyzedOffset]
					mark2 = s[f.query.HighLight.MaxAnalyzedOffset:]
				} else {
					mark1 = s
				}

				for _, v := range vs {
					mark1 = strings.ReplaceAll(mark1, v, fmt.Sprintf("<mark>%s</mark>", v))
				}

				res := fmt.Sprintf("%s%s", mark1, mark2)
				if res != d {
					newData[k] = []string{res}
				}
			}

		}
	}

	return
}

func (f *QueryFactory) ReloadListData(data map[string]any) (newData map[string]any) {
	newData = make(map[string]any)

	fieldMap := f.FieldMap()

	for k, d := range data {
		if v, ok := fieldMap[k]; ok {
			if v == TableTypeVariant {
				objectData, err := json.ParseObject(k, d.(string))
				if err != nil {
					log.Errorf(f.ctx, "json.ParseObject err: %v", err)
					continue
				}
				for nk, nd := range objectData {
					newData[nk] = nd
				}
				continue
			}
		}

		newData[k] = d
	}
	return
}

func (f *QueryFactory) FormatDataToQueryResult(ctx context.Context, list []map[string]interface{}) (*prompb.QueryResult, error) {
	res := &prompb.QueryResult{}

	if len(list) == 0 {
		return res, nil
	}

	encodeFunc := metadata.GetPromDataFormat(ctx).EncodeFunc()
	// 获取 metricLabel
	metricLabel := f.query.MetricLabels(ctx)

	tsMap := map[string]*prompb.TimeSeries{}
	tsTimeMap := make(map[string]map[int64]float64)
	isAddZero := f.timeAggregate.Window > 0 && f.expr.Type() == sql_expr.Doris

	// 先获取维度的 key 保证顺序一致
	keys := make([]string, 0)
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

		nd := f.ReloadListData(d)
		if len(keys) == 0 {
			for k := range nd {
				keys = append(keys, k)
			}
			sort.Strings(keys)
		}

		lbl := make([]prompb.Label, 0)
		for _, k := range keys {
			switch k {
			case sql_expr.TimeStamp:
				if _, ok = nd[k]; ok {
					vtLong = nd[k]
				}
			case sql_expr.Value:
				if _, ok = nd[k]; ok {
					vvDouble = nd[k]
				}
			default:
				// 获取维度信息
				val, err := getValue(k, nd)
				if err != nil {
					log.Errorf(ctx, "get dimension (%s) value error in %+v %s", k, d, err.Error())
					continue
				}

				if encodeFunc != nil {
					k = encodeFunc(k)
				}

				lbl = append(lbl, prompb.Label{
					Name:  k,
					Value: val,
				})

			}
		}

		if vtLong == nil {
			vtLong = f.start.UnixMilli()
		}

		switch vtLong.(type) {
		case int64:
			vt = vtLong.(int64)
		case float64:
			vt = int64(vtLong.(float64))
		default:
			return res, fmt.Errorf("%s type is error %T, %v", dtEventTimeStamp, vtLong, vtLong)
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
			return res, fmt.Errorf("%s type is error %T, %v", sql_expr.Value, vvDouble, vvDouble)
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

		// 如果是时间聚合需要进行补零，否则直接返回
		if isAddZero {
			if _, ok := tsTimeMap[key]; !ok {
				tsTimeMap[key] = make(map[int64]float64)
			}

			tsTimeMap[key][vt] = vv
		} else {
			tsMap[key].Samples = append(tsMap[key].Samples, prompb.Sample{
				Value:     vv,
				Timestamp: vt,
			})
		}
	}

	// 转换结构体
	res.Timeseries = make([]*prompb.TimeSeries, 0, len(tsMap))

	// 如果是时间聚合需要进行补零，否则直接返回
	if isAddZero {
		var (
			start time.Time
			end   time.Time
		)

		ms := f.timeAggregate.Window.Milliseconds()

		startMilli := (f.start.UnixMilli()+f.timeAggregate.OffsetMillis)/ms*ms - f.timeAggregate.OffsetMillis
		start = time.UnixMilli(startMilli)
		end = f.end

		for key, ts := range tsMap {
			for i := start; end.Sub(i) > 0; i = i.Add(f.timeAggregate.Window) {
				sample := prompb.Sample{
					Timestamp: i.UnixMilli(),
					Value:     0,
				}
				if v, ok := tsTimeMap[key][i.UnixMilli()]; ok {
					sample.Value = v
				}
				ts.Samples = append(ts.Samples, sample)
			}
			res.Timeseries = append(res.Timeseries, ts)
		}
	} else {
		for _, ts := range tsMap {
			res.Timeseries = append(res.Timeseries, ts)
		}
	}

	return res, nil
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
	s = append(s, fmt.Sprintf("`%s` >= %d AND `%s` <= %d", f.timeField, f.start.UnixMilli(), f.timeField, f.end.UnixMilli()))

	theDateFilter, err := f.getTheDateIndexFilters()
	if err != nil {
		return "", err
	}
	if theDateFilter != "" {
		s = append(s, theDateFilter)
	}

	// QueryString to sql
	if f.query.QueryString != "" && f.query.QueryString != "*" {
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
	var (
		span       *trace.Span
		sqlBuilder strings.Builder
	)

	_, span = trace.NewSpan(f.ctx, "make-sql")
	defer span.End(&err)

	selectFields, groupFields, orderFields, timeAggregate, err := f.expr.ParserAggregatesAndOrders(f.query.Aggregates, f.orders)
	if err != nil {
		return
	}

	f.timeAggregate = timeAggregate

	span.Set("select-fields", selectFields)
	span.Set("group-fields", groupFields)
	span.Set("order-fields", orderFields)
	span.Set("timeAggregate", timeAggregate)

	sqlBuilder.WriteString("SELECT ")
	sqlBuilder.WriteString(strings.Join(selectFields, ", "))
	sqlBuilder.WriteString(" FROM ")
	sqlBuilder.WriteString(f.Table())

	whereString, err := f.BuildWhere()
	span.Set("where-string", whereString)

	if err != nil {
		return
	}
	if whereString != "" {
		sqlBuilder.WriteString(" WHERE ")
		sqlBuilder.WriteString(whereString)
	}
	if len(groupFields) > 0 {
		sqlBuilder.WriteString(" GROUP BY ")
		sqlBuilder.WriteString(strings.Join(groupFields, ", "))
	}

	if len(orderFields) > 0 {
		sort.Strings(orderFields)
		sqlBuilder.WriteString(" ORDER BY ")
		sqlBuilder.WriteString(strings.Join(orderFields, ", "))
	}
	if f.query.Size > 0 {
		sqlBuilder.WriteString(" LIMIT ")
		sqlBuilder.WriteString(fmt.Sprintf("%d", f.query.Size))
	}
	if f.query.From > 0 {
		sqlBuilder.WriteString(" OFFSET ")
		sqlBuilder.WriteString(fmt.Sprintf("%d", f.query.From))
	}
	sql = sqlBuilder.String()
	span.Set("sql", sql)
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
