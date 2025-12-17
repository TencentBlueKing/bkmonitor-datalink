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
	"github.com/samber/lo"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
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

	dtEventTimeFormat = "2006-01-02 15:04:05"
)

var internalDimensionSet = func() *set.Set[string] {
	s := set.New[string]()
	for _, k := range []string{
		dtEventTimeStamp,
		dtEventTime,
		localTime,
		startTime,
		endTime,
		theDate,
		sql_expr.ShardKey,
	} {
		s.Add(strings.ToLower(k))
	}
	return s
}()

func checkInternalDimension(key string) bool {
	return internalDimensionSet.Existed(strings.ToLower(key))
}

type QueryFactory struct {
	ctx  context.Context
	lock sync.RWMutex

	query *metadata.Query

	start time.Time
	end   time.Time

	maxLimit int

	timeAggregate sql_expr.TimeAggregate
	dimensionSet  *set.Set[string]

	orders metadata.Orders

	timeField string

	expr sql_expr.SQLExpr
}

func NewQueryFactory(ctx context.Context, query *metadata.Query) *QueryFactory {
	f := &QueryFactory{
		ctx:          ctx,
		query:        query,
		dimensionSet: set.New[string](),
	}

	if query.Orders != nil {
		f.orders = query.Orders
	}

	if query.TimeField.Name != "" {
		f.timeField = query.TimeField.Name
	} else {
		f.timeField = dtEventTimeStamp
	}

	f.expr = sql_expr.NewSQLExpr(query.Measurement).
		WithInternalFields(f.timeField, query.Field).
		WithEncode(metadata.GetFieldFormat(ctx).EncodeFunc()).
		WithFieldAlias(query.FieldAlias)

	return f
}

func (f *QueryFactory) WithMaxLimit(maxLimit int) *QueryFactory {
	f.maxLimit = maxLimit
	return f
}

func (f *QueryFactory) WithRangeTime(start, end time.Time) *QueryFactory {
	f.start = start
	f.end = end
	return f
}

func (f *QueryFactory) WithFieldsMap(m metadata.FieldsMap) *QueryFactory {
	f.expr.WithFieldsMap(m)
	return f
}

func (f *QueryFactory) WithKeepColumns(cols []string) *QueryFactory {
	f.expr.WithKeepColumns(cols)
	return f
}

func (f *QueryFactory) FieldMap() metadata.FieldsMap {
	return f.expr.FieldMap()
}

func (f *QueryFactory) ReloadListData(data map[string]any, ignoreInternalDimension bool) (newData map[string]any) {
	newData = make(map[string]any)
	fieldMap := f.FieldMap()

	for k, d := range data {
		if d == nil {
			continue
		}
		// 忽略内置字段
		if ignoreInternalDimension && checkInternalDimension(k) {
			continue
		}

		fieldOption := fieldMap.Field(k)
		if strings.ToUpper(fieldOption.FieldType) == TableTypeVariant {
			if nd, ok := d.(string); ok {
				objectData, err := json.ParseObject(k, nd)
				if err != nil {
					_ = metadata.NewMessage(
						metadata.MsgTableFormat,
						"构建数据格式异常",
					).Error(f.ctx, err)
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
	return newData
}

func (f *QueryFactory) FormatDataToQueryResult(ctx context.Context, list []map[string]any) (*prompb.QueryResult, error) {
	res := &prompb.QueryResult{}

	if len(list) == 0 {
		return res, nil
	}

	encodeFunc := metadata.GetFieldFormat(ctx).EncodeFunc()
	// 获取 metricLabel
	metricLabel := f.query.MetricLabels(ctx)

	tsMap := map[string]*prompb.TimeSeries{}
	tsTimeMap := make(map[string]map[int64]float64)

	// 判断是否补零
	isAddZero := f.timeAggregate.Window > 0 && f.expr.Type() == sql_expr.Doris

	// 先获取维度的 key 保证顺序一致
	var keys []string
	for _, d := range list {
		// 优先获取时间和值
		var (
			vt int64
			vv float64

			vtLong   any
			vvDouble any

			ok bool
		)

		if d == nil {
			continue
		}

		nd := f.ReloadListData(d, true)
		if len(keys) == 0 {
			for k := range nd {
				// 如果维度使用了该字段，则无需跳过
				if !f.dimensionSet.Existed(f.query.Field) && k == f.query.Field {
					continue
				}
				if !f.dimensionSet.Existed(f.timeField) && k == f.timeField {
					continue
				}

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
					_ = metadata.NewMessage(
						metadata.MsgTableFormat,
						"获取维度信息异常",
					).Error(f.ctx, err)
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

		// 遇到 json.Number 类型，需要先转换成 float64 之后再转换成 int64，不然就会失败
		vt = cast.ToInt64(cast.ToFloat64(vtLong))
		vv = cast.ToFloat64(vvDouble)

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

		startMilli := (f.start.UnixMilli()-f.timeAggregate.OffsetMillis)/ms*ms + f.timeAggregate.OffsetMillis
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

func (f *QueryFactory) getTheDateIndexFilters() (string, error) {
	var conditions []string

	// bkbase 使用 时区东八区 转换为 thedate
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return "", err
	}

	start := f.start.In(loc)
	end := f.end.In(loc)

	conditions = append(conditions, fmt.Sprintf("`%s` >= '%s'", dtEventTime, start.Format(dtEventTimeFormat)))
	// 为了兼容毫秒纳秒等单位，需要+1s
	conditions = append(conditions, fmt.Sprintf("`%s` <= '%s'", dtEventTime, end.Add(time.Second).Format(dtEventTimeFormat)))

	dates := function.RangeDateWithUnit("day", start, end, 1)

	if len(dates) == 1 {
		conditions = append(conditions, fmt.Sprintf("`%s` = '%s'", theDate, dates[0]))
	} else if len(dates) > 1 {
		conditions = append(conditions, fmt.Sprintf("`%s` >= '%s'", theDate, dates[0]))
		conditions = append(conditions, fmt.Sprintf("`%s` <= '%s'", theDate, dates[len(dates)-1]))
	}

	return strings.Join(conditions, " AND "), nil
}

func (f *QueryFactory) BuildWhere() (string, error) {
	var s []string

	s = append(s, f.expr.ParserRangeTime(f.timeField, f.start, f.end))
	// if f.query.StorageType == "bkdata" {
	theDateFilter, err := f.getTheDateIndexFilters()
	if err != nil {
		return "", err
	}
	if theDateFilter != "" {
		s = append(s, theDateFilter)
	}
	//}

	// QueryString to sql
	if f.query.QueryString != "" && f.query.QueryString != "*" {
		qs, err := f.expr.ParserQueryString(f.ctx, f.query.QueryString)
		if err != nil {
			return "", err
		}

		if qs != "" {
			s = append(s, fmt.Sprintf("(%s)", qs))
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

func (f *QueryFactory) Tables() []string {
	dbs := f.query.DBs
	if len(dbs) == 0 {
		dbs = []string{f.query.DB}
	}

	tables := make([]string, 0, len(dbs))
	// 改成倒序遍历
	for idx := len(dbs) - 1; idx >= 0; idx-- {
		db := dbs[idx]
		table := fmt.Sprintf("`%s`", db)
		if f.query.Measurement != "" {
			table += "." + f.query.Measurement
		}
		tables = append(tables, table)
	}

	return tables
}

func (f *QueryFactory) parserSQL() (sql string, err error) {
	var span *trace.Span
	_, span = trace.NewSpan(f.ctx, "make-sql-with-parser")
	defer span.End(&err)

	tables := f.Tables()

	span.Set("tables", tables)

	where, err := f.BuildWhere()
	if err != nil {
		return sql, err
	}
	span.Set("where", where)
	if where != "" {
		where = fmt.Sprintf("(%s)", where)
	}
	from := f.query.From
	if f.query.Scroll != "" && f.query.ResultTableOption.From != nil {
		from = *f.query.ResultTableOption.From
	}

	sql, err = f.expr.ParserSQL(f.ctx, f.query.SQL, tables, where, from, f.query.Size)
	span.Set("query-sql", f.query.SQL)

	span.Set("sql", sql)
	return sql, err
}

func (f *QueryFactory) SQL() (sql string, err error) {
	// sql 解析语法不一样需要重新拼写
	if f.query.SQL != "" {
		return f.parserSQL()
	}

	var (
		span       *trace.Span
		sqlBuilder strings.Builder
	)

	_, span = trace.NewSpan(f.ctx, "make-sql")
	defer span.End(&err)

	selectFields, groupFields, orderFields, dimensionSet, timeAggregate, err := f.expr.ParserAggregatesAndOrders(f.query.SelectDistinct, f.query.Aggregates, f.orders)
	if err != nil {
		return sql, err
	}

	// 用于判定字段是否需要删除
	f.dimensionSet = dimensionSet

	// 用于补零判定
	f.timeAggregate = timeAggregate

	span.Set("select-fields", selectFields)
	span.Set("group-fields", groupFields)
	span.Set("order-fields", orderFields)
	span.Set("timeAggregate", timeAggregate)

	sqlBuilder.WriteString(lo.Ternary(len(f.query.SelectDistinct) > 0, "SELECT DISTINCT ", "SELECT "))
	sqlBuilder.WriteString(strings.Join(selectFields, ", "))

	whereString, err := f.BuildWhere()
	span.Set("where-string", whereString)
	if err != nil {
		return sql, err
	}
	if len(f.Tables()) > 0 {
		var table string
		if len(f.Tables()) == 1 {
			table = f.Tables()[0]
		} else {
			stmts := make([]string, 0, len(f.Tables()))
			for _, t := range f.Tables() {
				s := fmt.Sprintf("SELECT * FROM %s", t)
				if whereString != "" {
					s = fmt.Sprintf("%s WHERE %s", s, whereString)
				}
				stmts = append(stmts, s)
			}

			table = fmt.Sprintf("(%s) AS combined_data", strings.Join(stmts, " UNION ALL "))
			whereString = ""
		}
		sqlBuilder.WriteString(" FROM ")
		sqlBuilder.WriteString(table)
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

	size := f.query.Size
	if f.maxLimit > 0 && (size > f.maxLimit || size == 0) {
		size = f.maxLimit
	}

	if size > 0 {
		sqlBuilder.WriteString(" LIMIT ")
		sqlBuilder.WriteString(fmt.Sprintf("%d", size))
	}
	if f.query.From > 0 {
		sqlBuilder.WriteString(" OFFSET ")
		sqlBuilder.WriteString(fmt.Sprintf("%d", f.query.From))
	}
	sql = sqlBuilder.String()
	span.Set("sql", sql)
	return sql, err
}
