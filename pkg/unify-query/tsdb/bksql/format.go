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

type TableFieldsMap map[string]metadata.FieldsMap

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

	exprKey := query.Measurement
	// TSpider 表多为单段 table_id，Measurement 为空；用户自定义 SQL 仍需走 Doris 同源解析，且不能改写 Measurement（否则表名会多出 .tspider）
	if exprKey == "" && query.SQL != "" && query.StorageType == metadata.BkSqlStorageType {
		exprKey = sql_expr.TSpider
	}

	f.expr = sql_expr.NewSQLExpr(exprKey).
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

	sql, err = f.expr.ParserSQL(f.ctx, f.query.SQL, tables, where, from, f.query.Size)
	span.Set("bksql.sql_expr_type", f.expr.Type())
	span.Set("query-sql", f.query.SQL)

	span.Set("sql", sql)
	return sql, err
}

// collectUnionSelectFields 根据已生成的 SELECT/GROUP/ORDER 表达式提取底层表字段。
//
// 普通聚合路径不经过 Doris SQL visitor，也需要在多 DB 合并时避免 SELECT *。
// 这里从已渲染表达式中收集外层真正依赖的源字段，让内层 UNION 子查询只投影这些列。
// 顶层 wildcard 仍保留为 *，让调用方拒绝多表 schema 漂移场景的 raw 明细查询。
// COUNT(*) 位于函数参数内，不会被当成 wildcard 展开；如果没有任何真实字段依赖，
// UNION 分支只需投影常量，外层 COUNT 仍按行数聚合。
type unionProjection struct {
	selectAll bool
	dummy     bool
	fields    []string
}

func collectUnionSelectFields(selectFields, groupFields, orderFields []string) string {
	projection := collectUnionProjection(selectFields, groupFields, orderFields)
	switch {
	case projection.selectAll:
		return selectAll
	case projection.dummy:
		return unionDummyProjection
	default:
		return strings.Join(projection.fields, ", ")
	}
}

func (f *QueryFactory) unionSelectList(selectFields, groupFields, orderFields []string, tables []string) (string, error) {
	projection := collectUnionProjection(selectFields, groupFields, orderFields)
	switch {
	case projection.selectAll:
		if len(f.tableFieldsMap) > 0 {
			return "", fmt.Errorf("doris multi-table union does not support SELECT *; use explicit fields or aggregate dependencies")
		}
		return selectAll, nil
	case projection.dummy:
		return unionDummyProjection, nil
	}
	if err := validateUnionProjectionFields(tables, projection.fields, f.tableFieldsMap); err != nil {
		return "", err
	}
	return strings.Join(projection.fields, ", "), nil
}

func validateUnionProjectionFields(tables []string, fields []string, tableFieldsMap TableFieldsMap) error {
	if len(tableFieldsMap) == 0 {
		return nil
	}
	for _, field := range fields {
		name := unquoteUnionField(field)
		var base metadata.FieldOption
		var baseTable string
		for _, table := range tables {
			fieldsMap, ok := tableFieldsMap[table]
			if !ok {
				return fmt.Errorf("doris multi-table union missing schema for table %s", table)
			}
			fieldOption := fieldsMap.Field(name)
			if !fieldOption.Existed() {
				return fmt.Errorf("doris multi-table union field %s is missing from table %s", field, table)
			}
			if isUnsupportedUnionFieldType(fieldOption.FieldType) {
				return fmt.Errorf("doris multi-table union field %s in table %s has unsupported type %s", field, table, fieldOption.FieldType)
			}
			if base.Existed() && !compatibleUnionFieldTypes(base.FieldType, fieldOption.FieldType) {
				return fmt.Errorf(
					"doris multi-table union field %s type mismatch: table %s has %s, table %s has %s",
					field, baseTable, base.FieldType, table, fieldOption.FieldType,
				)
			}
			if !base.Existed() {
				base = fieldOption
				baseTable = table
			}
		}
	}
	return nil
}

func unquoteUnionField(field string) string {
	return strings.TrimSuffix(strings.TrimPrefix(field, "`"), "`")
}

func isUnsupportedUnionFieldType(fieldType string) bool {
	switch normalizeUnionFieldType(fieldType) {
	case "json", "jsonb":
		return true
	default:
		return false
	}
}

func compatibleUnionFieldTypes(left, right string) bool {
	return normalizeUnionFieldType(left) == normalizeUnionFieldType(right)
}

func normalizeUnionFieldType(fieldType string) string {
	t := strings.ToLower(strings.TrimSpace(fieldType))
	if strings.HasPrefix(t, "array<") && strings.HasSuffix(t, ">") {
		return "array:" + normalizeUnionFieldType(t[len("array<"):len(t)-1])
	}
	if strings.HasSuffix(t, " array") {
		return "array:" + normalizeUnionFieldType(strings.TrimSuffix(t, " array"))
	}
	if idx := strings.IndexByte(t, '('); idx >= 0 {
		t = t[:idx]
	}
	t = strings.TrimSpace(t)
	switch t {
	case "char", "varchar", "string", "text":
		return "string"
	case "tinyint", "smallint", "int", "integer", "bigint", "largeint":
		return "integer"
	case "float", "double", "decimal", "decimalv3":
		return "number"
	case "bool", "boolean":
		return "boolean"
	case "date", "datetime", "timestamp":
		return "time"
	default:
		return t
	}
}

func collectUnionProjection(selectFields, groupFields, orderFields []string) unionProjection {
	allParts := [][]string{selectFields, groupFields, orderFields}
	for _, parts := range allParts {
		for _, part := range parts {
			if hasTopLevelUnionWildcard(part) {
				return unionProjection{selectAll: true}
			}
		}
	}

	aliases := collectSQLAliases(selectFields)
	seen := make(map[string]struct{})
	fields := make([]string, 0)

	for _, field := range collectUnionColumnsFromSQLParts(selectFields, nil) {
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		fields = append(fields, field)
	}

	for _, field := range collectUnionColumnsFromSQLParts(groupFields, aliases) {
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		fields = append(fields, field)
	}

	for _, field := range collectUnionColumnsFromSQLParts(orderFields, aliases) {
		if _, ok := seen[field]; ok {
			continue
		}
		seen[field] = struct{}{}
		fields = append(fields, field)
	}

	if len(fields) == 0 {
		return unionProjection{dummy: true}
	}
	return unionProjection{fields: fields}
}

func collectUnionColumnsFromSQLParts(parts []string, ignoreNames map[string]struct{}) []string {
	fields := make([]string, 0)
	seen := make(map[string]struct{})
	for _, part := range parts {
		for _, field := range collectUnionColumnsFromSQLPart(part, ignoreNames) {
			if _, ok := seen[field]; ok {
				continue
			}
			seen[field] = struct{}{}
			fields = append(fields, field)
		}
	}
	return fields
}

// collectUnionColumnsFromSQLPart 不是完整 SQL parser，只处理 bksql 已生成的表达式片段。
// 它会跳过字符串字面量、未反引号 SQL keyword、函数名和 AS 后的 alias，同时保留
// 未反引号 root，例如 CAST(resource['bk.instance.id'] AS STRING) 里的 resource。
// dotted path 只收 root，避免把对象 key 或路径段误投影为原表列。
func collectUnionColumnsFromSQLPart(part string, ignoreNames map[string]struct{}) []string {
	fields := make([]string, 0)
	for idx := 0; idx < len(part); idx++ {
		switch part[idx] {
		case '\'':
			idx = skipSingleQuotedUnionString(part, idx)
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
			if shouldSkipUnionColumnName(part, start, name, ignoreNames, true) {
				continue
			}
			fields = append(fields, fmt.Sprintf("`%s`", name))
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
		fields = append(fields, fmt.Sprintf("`%s`", name))
	}
	return fields
}

func shouldSkipUnionColumnName(part string, start int, name string, ignoreNames map[string]struct{}, quoted bool) bool {
	if name == "" {
		return true
	}
	if _, ok := ignoreNames[name]; ok {
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

func isUnionSQLKeyword(name string) bool {
	switch strings.ToUpper(name) {
	case "AND", "ARRAY", "AS", "ASC", "BETWEEN", "BIGINT", "BOOL", "BOOLEAN", "BY", "CASE", "CAST",
		"DATE", "DATETIME", "DECIMAL", "DESC", "DISTINCT", "DOUBLE", "ELSE", "END", "FALSE", "FLOAT",
		"FROM", "GROUP", "IN", "INT", "INTEGER", "IS", "LIKE", "LIMIT", "MATCH_ALL", "MATCH_PHRASE",
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
	for idx := start + 1; idx < len(s); idx++ {
		switch s[idx] {
		case '\\':
			idx++
		case '\'':
			if idx+1 < len(s) && s[idx+1] == '\'' {
				idx++
				continue
			}
			return idx
		}
	}
	return len(s) - 1
}

func hasTopLevelUnionWildcard(s string) bool {
	depth := 0
	for idx := 0; idx < len(s); idx++ {
		switch s[idx] {
		case '\'':
			idx = skipSingleQuotedUnionString(s, idx)
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
			if depth == 0 && isUnionWildcardToken(s, idx) {
				return true
			}
		}
	}
	return false
}

func isUnionWildcardToken(s string, idx int) bool {
	prev := previousNonSpaceUnionByte(s, idx)
	next := nextNonSpaceUnionByte(s, idx+1)
	return (prev == 0 || prev == ',' || prev == '.') && (next == 0 || next == ',')
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
