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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

const (
	selectAll            = "*"
	unionDummyProjection = "1"

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

	tableFieldsMap TableFieldsMap
}

type TableFieldsMap = doris_parser.TableFieldsMap

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

	exprKey := querySQLExprKey(query)

	f.expr = sql_expr.NewSQLExpr(exprKey).
		WithInternalFields(f.timeField, query.Field).
		WithEncode(metadata.GetFieldFormat(ctx).EncodeFunc()).
		WithFieldAlias(query.FieldAlias)

	return f
}

func querySQLExprKey(query *metadata.Query) string {
	if query == nil {
		return ""
	}
	if isTSpiderQuery(query) {
		return sql_expr.TSpider
	}
	return query.Measurement
}

func isTSpiderQuery(query *metadata.Query) bool {
	if query == nil {
		return false
	}
	if query.Measurement == sql_expr.TSpider {
		return true
	}
	return query.StorageType == metadata.BkSqlStorageType && query.Measurement == ""
}

func formatPhysicalTableName(db, measurement string) string {
	table := fmt.Sprintf("`%s`", db)
	if measurement != "" && measurement != sql_expr.TSpider {
		table += "." + measurement
	}
	return table
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

func (f *QueryFactory) WithTableFieldsMap(m TableFieldsMap) *QueryFactory {
	f.tableFieldsMap = m
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
			// SQL 聚合首行常为 NULL。若直接 continue，首行 nd 会缺少 `_value_`/`_timestamp_` 键；
			// FormatDataToQueryResult 只在首行推断 keys，缺 `_value_` 则后续行永远进不了 Value 分支，整列被当成 0。
			if k == sql_expr.Value || k == sql_expr.TimeStamp {
				newData[k] = nil
			}
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
	theDateFilter, err := f.getTheDateIndexFilters()
	if err != nil {
		return "", err
	}
	if theDateFilter != "" {
		s = append(s, theDateFilter)
	}

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
		tables = append(tables, formatPhysicalTableName(db, f.query.Measurement))
	}

	return tables
}

func (f *QueryFactory) parserSQL() (sql string, err error) {
	var span *trace.Span
	_, span = trace.NewSpan(f.ctx, "make-sql-with-parser")
	defer span.End(&err)

	span.Set("bksql.parser_from_user_sql", true)
	span.Set("bksql.user_sql.byte_len", len(f.query.SQL))

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
	if f.query.Scroll != "" && f.query.ResultTableOption != nil && f.query.ResultTableOption.From != nil {
		from = *f.query.ResultTableOption.From
	}

	sql, err = f.expr.ParserSQL(f.ctx, f.query.SQL, tables, where, from, f.query.Size, f.tableFieldsMap)
	span.Set("bksql.sql_expr_type", f.expr.Type())
	span.Set("query-sql", f.query.SQL)

	span.Set("sql", sql)
	return sql, err
}

// collectUnionSelectFields 根据已生成的 SELECT/GROUP/ORDER 表达式提取底层表字段。
//
// 普通聚合路径不经过 Doris SQL visitor，也需要在多 DB 合并时避免 SELECT *。
// 这里从已渲染表达式中收集外层真正依赖的源字段，让内层 UNION 子查询只投影这些列。
// 顶层 wildcard 会在 Doris 多表场景中按表结构转换成公共字段显式投影。
// COUNT(*) 位于函数参数内，不会被当成 wildcard 展开；如果没有任何真实字段依赖，
// UNION 分支只需投影常量，外层 COUNT 仍按行数聚合。
type unionProjection struct {
	selectAll          bool
	qualifiedSelectAll bool
	dummy              bool
	fields             []unionProjectionField
}

type unionProjectionField struct {
	field        string
	validateName string
}

func collectUnionSelectFields(selectFields, groupFields, orderFields []string) string {
	projection := collectUnionProjection(selectFields, groupFields, orderFields)
	switch {
	case projection.selectAll:
		return selectAll
	case projection.dummy:
		return unionDummyProjection
	default:
		return strings.Join(unionProjectionFieldNames(projection.fields), ", ")
	}
}

func (f *QueryFactory) unionSelectList(selectFields, groupFields, orderFields []string, tables []string) (string, error) {
	projection := collectUnionProjection(selectFields, groupFields, orderFields)
	switch {
	case projection.selectAll:
		if f.expr.Type() == sql_expr.Doris && len(f.tableFieldsMap) > 0 {
			if projection.qualifiedSelectAll {
				return "", fmt.Errorf("doris multi-table union does not support SELECT *; use explicit fields")
			}
			fields, err := doris_parser.ExpandSelectAllUnionFields(tables, f.tableFieldsMap)
			if err != nil {
				return "", err
			}
			if err := doris_parser.ValidateUnionProjectionFieldNames(tables, toDorisUnionProjectionFields(projection.fields), f.tableFieldsMap); err != nil {
				return "", err
			}
			if field := firstMissingUnionProjectionField(fields, unionProjectionFieldNames(projection.fields)); field != "" {
				return "", fmt.Errorf("doris multi-table union SELECT * cannot be combined with field dependency %s; use explicit fields", field)
			}
			if len(fields) > 0 {
				return strings.Join(fields, ", "), nil
			}
		}
		return selectAll, nil
	case projection.dummy:
		return unionDummyProjection, nil
	}
	if err := doris_parser.ValidateUnionProjectionFieldNames(tables, toDorisUnionProjectionFields(projection.fields), f.tableFieldsMap); err != nil {
		return "", err
	}
	return strings.Join(unionProjectionFieldNames(projection.fields), ", "), nil
}

func collectUnionProjection(selectFields, groupFields, orderFields []string) unionProjection {
	selectAll := false
	qualifiedSelectAll := false
	allParts := [][]string{selectFields, groupFields, orderFields}
	for _, parts := range allParts {
		for _, part := range parts {
			if hasTopLevelQualifiedUnionWildcard(part) {
				qualifiedSelectAll = true
				break
			}
			if hasTopLevelUnionWildcard(part) {
				selectAll = true
				break
			}
		}
	}

	aliases := collectSQLAliases(selectFields)
	seen := make(map[string]struct{})
	fields := make([]unionProjectionField, 0)

	for _, field := range collectUnionColumnsFromSQLParts(selectFields, nil) {
		key := unionProjectionFieldKey(field)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		fields = append(fields, field)
	}

	for _, field := range collectUnionColumnsFromSQLParts(groupFields, aliases) {
		key := unionProjectionFieldKey(field)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		fields = append(fields, field)
	}

	for _, field := range collectUnionColumnsFromSQLParts(orderFields, aliases) {
		key := unionProjectionFieldKey(field)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		fields = append(fields, field)
	}

	if selectAll || qualifiedSelectAll {
		return unionProjection{selectAll: true, qualifiedSelectAll: qualifiedSelectAll, fields: fields}
	}
	if len(fields) == 0 {
		return unionProjection{dummy: true}
	}
	return unionProjection{fields: fields}
}

func unionProjectionFieldKey(field unionProjectionField) string {
	return field.field + "\x00" + field.validateName
}

func unionProjectionFieldNames(fields []unionProjectionField) []string {
	names := make([]string, 0, len(fields))
	seen := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if _, ok := seen[field.field]; ok {
			continue
		}
		seen[field.field] = struct{}{}
		names = append(names, field.field)
	}
	return names
}

func firstMissingUnionProjectionField(fields []string, extraFields []string) string {
	seen := make(map[string]struct{}, len(fields)*2)
	for _, field := range fields {
		addUnionProjectionOutputName(seen, field)
	}
	for _, field := range extraFields {
		if unionProjectionOutputNameExists(seen, field) {
			continue
		}
		return field
	}
	return ""
}

func addUnionProjectionOutputName(seen map[string]struct{}, field string) {
	seen[field] = struct{}{}
	if key := normalizedUnionProjectionName(field); key != "" {
		seen[key] = struct{}{}
	}
	upperField := strings.ToUpper(field)
	idx := strings.LastIndex(upperField, " AS `")
	if idx < 0 {
		return
	}
	alias := field[idx+len(" AS "):]
	if strings.HasPrefix(alias, "`") && strings.HasSuffix(alias, "`") {
		seen[alias] = struct{}{}
		if key := normalizedUnionProjectionName(alias); key != "" {
			seen[key] = struct{}{}
		}
	}
}

func unionProjectionOutputNameExists(seen map[string]struct{}, field string) bool {
	if _, ok := seen[field]; ok {
		return true
	}
	if key := normalizedUnionProjectionName(field); key != "" {
		_, ok := seen[key]
		return ok
	}
	return false
}

func normalizedUnionProjectionName(field string) string {
	field = strings.TrimSpace(field)
	if field == "" {
		return ""
	}
	if strings.HasPrefix(field, "`") && strings.HasSuffix(field, "`") {
		field = strings.TrimSuffix(strings.TrimPrefix(field, "`"), "`")
	}
	if strings.ContainsAny(field, " ()") {
		return ""
	}
	return strings.ToLower(field)
}

func toDorisUnionProjectionFields(fields []unionProjectionField) []doris_parser.UnionProjectionField {
	result := make([]doris_parser.UnionProjectionField, 0, len(fields))
	for _, field := range fields {
		result = append(result, doris_parser.UnionProjectionField{
			Field:        field.field,
			ValidateName: field.validateName,
		})
	}
	return result
}

func collectUnionColumnsFromSQLParts(parts []string, ignoreNames map[string]struct{}) []unionProjectionField {
	fields := make([]unionProjectionField, 0)
	seen := make(map[string]struct{})
	for _, part := range parts {
		for _, field := range collectUnionColumnsFromSQLPart(part, ignoreNames) {
			key := unionProjectionFieldKey(field)
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			fields = append(fields, field)
		}
	}
	return fields
}

// collectUnionColumnsFromSQLPart 不是完整 SQL parser，只处理 bksql 已生成的表达式片段。
// 它会跳过字符串字面量、未反引号 SQL keyword、函数名和 AS 后的 alias，同时保留
// 未反引号 root，例如 CAST(resource['bk.instance.id'] AS STRING) 里的 resource。
// dotted path 只收 root，避免把对象 key 或路径段误投影为原表列。
func collectUnionColumnsFromSQLPart(part string, ignoreNames map[string]struct{}) []unionProjectionField {
	fields := make([]unionProjectionField, 0)
	for idx := 0; idx < len(part); idx++ {
		switch part[idx] {
		case '\'':
			idx = skipSingleQuotedUnionString(part, idx)
			continue
		case '"':
			idx = skipDoubleQuotedUnionString(part, idx)
			continue
		case '`':
			start := idx
			end := strings.IndexByte(part[idx+1:], '`')
			if end < 0 {
				return fields
			}
			end += idx + 1
			name := part[idx+1 : end]
			idx = end
			if isUnionQualifiedWildcardRoot(part, idx+1) {
				continue
			}
			if shouldSkipUnionColumnName(part, start, name, ignoreNames, true) {
				continue
			}
			fields = append(fields, unionProjectionField{
				field:        fmt.Sprintf("`%s`", name),
				validateName: name + collectUnionObjectPathSuffix(part, idx+1),
			})
			continue
		}

		if !isUnionIdentifierStart(part[idx]) {
			continue
		}

		start := idx
		for idx < len(part) && isUnionIdentifierPart(part[idx]) {
			idx++
		}
		name := part[start:idx]
		idx--
		if isUnionIdentifierPartOfNumericLiteral(part, start) {
			continue
		}
		if previousNonSpaceUnionByte(part, start) == '.' {
			continue
		}
		if shouldSkipUnionColumnName(part, start, name, ignoreNames, false) {
			continue
		}
		// 标识符后紧跟 '(' 时是函数名；函数参数会继续被扫描。
		// 这保证 COUNT(*) 不会被误认为需要展开的字段依赖。
		if nextNonSpaceUnionByte(part, idx+1) == '(' {
			continue
		}
		if isUnionQualifiedWildcardRoot(part, idx+1) {
			continue
		}
		fields = append(fields, unionProjectionField{
			field:        fmt.Sprintf("`%s`", name),
			validateName: name + collectUnionObjectPathSuffix(part, idx+1),
		})
	}
	return fields
}

func isUnionQualifiedWildcardRoot(part string, start int) bool {
	for start < len(part) && part[start] == ' ' {
		start++
	}
	if start >= len(part) || part[start] != '.' {
		return false
	}
	start++
	for start < len(part) && part[start] == ' ' {
		start++
	}
	return start < len(part) && part[start] == '*'
}

func shouldSkipUnionColumnName(part string, start int, name string, ignoreNames map[string]struct{}, quoted bool) bool {
	if name == "" {
		return true
	}
	if unionIgnoreNamesContains(ignoreNames, name) {
		return true
	}
	if !quoted && isUnionSQLKeyword(name) {
		return true
	}
	if previousUnionTokenIsAS(part, start) {
		return true
	}
	return false
}

func unionIgnoreNamesContains(ignoreNames map[string]struct{}, name string) bool {
	if len(ignoreNames) == 0 {
		return false
	}
	if _, ok := ignoreNames[name]; ok {
		return true
	}
	_, ok := ignoreNames[strings.ToLower(name)]
	return ok
}

func collectUnionObjectPathSuffix(part string, start int) string {
	var parts []string
	for idx := start; idx < len(part); {
		for idx < len(part) && part[idx] == ' ' {
			idx++
		}
		if idx >= len(part) {
			break
		}
		switch part[idx] {
		case '.':
			idx++
			for idx < len(part) && part[idx] == ' ' {
				idx++
			}
			partStart := idx
			if idx >= len(part) || !isUnionIdentifierStart(part[idx]) {
				return strings.Join(parts, "")
			}
			for idx < len(part) && isUnionIdentifierPart(part[idx]) {
				idx++
			}
			parts = append(parts, "."+part[partStart:idx])
		case '[':
			pathPart, next, ok := scanUnionBracketObjectPathPart(part, idx)
			if !ok {
				return strings.Join(parts, "")
			}
			if pathPart != "" {
				parts = append(parts, "."+pathPart)
			}
			idx = next
		default:
			return strings.Join(parts, "")
		}
	}
	return strings.Join(parts, "")
}

func scanUnionBracketObjectPathPart(part string, start int) (string, int, bool) {
	idx := start + 1
	for idx < len(part) && part[idx] == ' ' {
		idx++
	}
	if idx >= len(part) {
		return "", idx, false
	}

	var pathPart string
	switch part[idx] {
	case '\'', '"', '`':
		quote := part[idx]
		end := skipQuotedUnionString(part, idx, quote)
		if end <= idx || end >= len(part) {
			return "", end, false
		}
		pathPart = part[idx+1 : end]
		idx = end + 1
	default:
		partStart := idx
		for idx < len(part) && isUnionIdentifierPart(part[idx]) {
			idx++
		}
		pathPart = part[partStart:idx]
	}

	for idx < len(part) && part[idx] == ' ' {
		idx++
	}
	if idx >= len(part) || part[idx] != ']' {
		return "", idx, false
	}
	return pathPart, idx + 1, true
}

func previousUnionTokenIsAS(part string, start int) bool {
	idx := start - 1
	for idx >= 0 && part[idx] == ' ' {
		idx--
	}
	end := idx + 1
	for idx >= 0 && isUnionIdentifierPart(part[idx]) {
		idx--
	}
	return strings.EqualFold(part[idx+1:end], "AS")
}

func nextNonSpaceUnionByte(part string, start int) byte {
	for idx := start; idx < len(part); idx++ {
		if part[idx] != ' ' {
			return part[idx]
		}
	}
	return 0
}

func previousNonSpaceUnionByte(part string, start int) byte {
	for idx := start - 1; idx >= 0; idx-- {
		if part[idx] != ' ' {
			return part[idx]
		}
	}
	return 0
}

func isUnionIdentifierStart(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

func isUnionIdentifierPart(b byte) bool {
	return isUnionIdentifierStart(b) || b >= '0' && b <= '9'
}

func isUnionDigit(b byte) bool {
	return b >= '0' && b <= '9'
}

func isUnionIdentifierPartOfNumericLiteral(part string, start int) bool {
	return start > 0 && isUnionDigit(part[start-1])
}

func isUnionSQLKeyword(name string) bool {
	switch strings.ToUpper(name) {
	case "AND", "ARRAY", "AS", "ASC", "BETWEEN", "BIGINT", "BOOL", "BOOLEAN", "BY", "CASE", "CAST",
		"DATE", "DATETIME", "DECIMAL", "DESC", "DISTINCT", "DOUBLE", "ELSE", "END", "FALSE", "FLOAT",
		"FROM", "GROUP", "IN", "INT", "INTEGER", "IS", "LIKE", "LIMIT", "MATCH_ALL", "MATCH_ANY",
		"MATCH_PHRASE", "MATCH_PHRASE_EDGE", "MATCH_PHRASE_PREFIX", "MATCH_REGEXP",
		"NOT", "NULL", "OR", "ORDER", "REGEXP", "SELECT", "STRING", "TEXT", "THEN", "TIME", "TIMESTAMP",
		"TRUE", "VARCHAR", "WHEN", "WHERE":
		return true
	default:
		return false
	}
}

func collectSQLAliases(parts []string) map[string]struct{} {
	aliases := make(map[string]struct{})
	for _, part := range parts {
		depth := 0
		for idx := 0; idx < len(part); idx++ {
			switch part[idx] {
			case '\'':
				idx = skipSingleQuotedUnionString(part, idx)
				continue
			case '"':
				idx = skipDoubleQuotedUnionString(part, idx)
				continue
			case '`':
				end := strings.IndexByte(part[idx+1:], '`')
				if end < 0 {
					return aliases
				}
				idx += end + 1
				continue
			case '(':
				depth++
				continue
			case ')':
				if depth > 0 {
					depth--
				}
				continue
			}
			if depth > 0 || !isUnionASClauseAt(part, idx) {
				continue
			}

			idx += len(" AS ")
			for idx < len(part) && part[idx] == ' ' {
				idx++
			}
			if idx >= len(part) {
				break
			}
			if part[idx] == '`' {
				end := strings.IndexByte(part[idx+1:], '`')
				if end < 0 {
					break
				}
				name := part[idx+1 : idx+1+end]
				if name != "" {
					aliases[name] = struct{}{}
				}
				idx += end + 1
				continue
			}
			start := idx
			for idx < len(part) && (part[idx] == '_' || part[idx] == '.' || part[idx] >= '0' && part[idx] <= '9' ||
				part[idx] >= 'a' && part[idx] <= 'z' || part[idx] >= 'A' && part[idx] <= 'Z') {
				idx++
			}
			if idx > start {
				aliases[part[start:idx]] = struct{}{}
			}
		}
	}
	return aliases
}

func isUnionASClauseAt(s string, idx int) bool {
	return idx+len(" AS ") <= len(s) && strings.EqualFold(s[idx:idx+len(" AS ")], " AS ")
}

func skipSingleQuotedUnionString(s string, start int) int {
	return skipQuotedUnionString(s, start, '\'')
}

func skipDoubleQuotedUnionString(s string, start int) int {
	return skipQuotedUnionString(s, start, '"')
}

func skipQuotedUnionString(s string, start int, quote byte) int {
	for idx := start + 1; idx < len(s); idx++ {
		switch s[idx] {
		case '\\':
			idx++
		case quote:
			if idx+1 < len(s) && s[idx+1] == quote {
				idx++
				continue
			}
			return idx
		}
	}
	return len(s) - 1
}

func hasTopLevelUnionWildcard(s string) bool {
	if isUnionDistinctStarExpression(s) {
		return true
	}

	return scanTopLevelUnionWildcard(s, isUnionWildcardToken)
}

func hasTopLevelQualifiedUnionWildcard(s string) bool {
	return scanTopLevelUnionWildcard(s, isUnionQualifiedWildcardToken)
}

func scanTopLevelUnionWildcard(s string, match func(string, int) bool) bool {
	depth := 0
	for idx := 0; idx < len(s); idx++ {
		switch s[idx] {
		case '\'':
			idx = skipSingleQuotedUnionString(s, idx)
		case '"':
			idx = skipDoubleQuotedUnionString(s, idx)
		case '`':
			end := strings.IndexByte(s[idx+1:], '`')
			if end < 0 {
				return false
			}
			idx += end + 1
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '*':
			if depth == 0 && match(s, idx) {
				return true
			}
		}
	}
	return false
}

func isUnionDistinctStarExpression(s string) bool {
	normalized := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		default:
			return r
		}
	}, s)
	return strings.EqualFold(normalized, "DISTINCT(*)") || strings.EqualFold(normalized, "DISTINCT*")
}

func isUnionWildcardToken(s string, idx int) bool {
	prev := previousNonSpaceUnionByte(s, idx)
	next := nextNonSpaceUnionByte(s, idx+1)
	return (prev == 0 || prev == ',') && (next == 0 || next == ',')
}

func isUnionQualifiedWildcardToken(s string, idx int) bool {
	prev := previousNonSpaceUnionByte(s, idx)
	next := nextNonSpaceUnionByte(s, idx+1)
	return prev == '.' && (next == 0 || next == ',')
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
		tables := f.Tables()
		if len(tables) == 1 {
			table = tables[0]
		} else {
			stmts := make([]string, 0, len(tables))
			selectList, err := f.unionSelectList(selectFields, groupFields, orderFields, tables)
			if err != nil {
				return "", err
			}
			for _, t := range tables {
				// 显式投影可以让 current/his Doris 表字段不完全一致时仍能完成 UNION ALL。
				s := fmt.Sprintf("SELECT %s FROM %s", selectList, t)
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
