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
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql"
	"github.com/prometheus/prometheus/storage"
	"github.com/prometheus/prometheus/storage/remote"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/precision"
)

const (
	TableFieldName       = "Field"
	TableFieldNameColumn = "Column"
	TableFieldType       = "Type"
	TableFieldAnalyzed   = "Analyzed"

	TableTypeVariant = "VARIANT"
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

func (i *Instance) sqlQuery(ctx context.Context, req QuerySyncRequest) (*QuerySyncResultData, error) {
	var (
		data *QuerySyncResultData

		ok   bool
		err  error
		span *trace.Span
	)

	ctx, span = trace.NewSpan(ctx, "sql-query")
	defer span.End(&err)

	if req.SQL == "" {
		return data, nil
	}

	span.Set("query-sql", req.SQL)
	if req.ClusterName != "" {
		span.Set("query-cluster-name", req.ClusterName)
	}

	user := metadata.GetUser(ctx)

	span.Set("query-source", user.Key)
	span.Set("query-username", user.Name)

	ctx, cancel := context.WithTimeout(ctx, i.timeout)
	defer cancel()

	// 发起异步查询
	res := i.client.QuerySync(ctx, req, span)
	if res == nil {
		return nil, nil
	}

	if !res.Result || res.Code != StatusOK || res.Data == nil {
		return data, metadata.NewMessage(
			metadata.MsgQueryBKSQL,
			"查询异常 %s",
			res.Message,
		).Error(ctx, errors.New(res.Errors.Error))
	}

	span.Set("query-timeout", i.timeout.String())
	span.Set("query-internal-time", i.intervalTime.String())

	if data, ok = res.Data.(*QuerySyncResultData); !ok {
		return data, fmt.Errorf("queryAsyncResult type is error: %T", res.Data)
	}

	span.Set("result-size", len(data.List))
	span.Set("result-sql", data.Sql)
	// BK Data query_sync 返回 data.device（如 tspider / doris），便于链路区分实际执行引擎
	if data.Device != "" {
		span.Set("device", data.Device)
	}

	return data, nil
}

func queryClusterName(query *metadata.Query) string {
	if query == nil {
		return ""
	}
	// 分段路由会把命中的 Doris 集群放在 ClusterName，StorageName 仅作为旧字段兼容。
	if query.ClusterName != "" {
		return query.ClusterName
	}
	return query.StorageName
}

func newQuerySyncRequest(sql string, query *metadata.Query) QuerySyncRequest {
	return QuerySyncRequest{
		SQL:         sql,
		ClusterName: queryClusterName(query),
	}
}

func (i *Instance) getFieldsMap(ctx context.Context, req QuerySyncRequest) (metadata.FieldsMap, error) {
	fieldsMap := make(metadata.FieldsMap)

	if req.SQL == "" {
		return nil, nil
	}

	data, err := i.sqlQuery(ctx, req)
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
			// HDFS 使用 Column 名称标识
			k, ok = list[TableFieldNameColumn].(string)
			if !ok {
				continue
			}
		}

		fieldType, ok = list[TableFieldType].(string)
		if !ok || fieldType == "" {
			continue
		}

		opt := metadata.FieldOption{
			FieldType: fieldType,
		}

		if fieldAnalyzed, ok = list[TableFieldAnalyzed].(string); ok {
			opt.IsAnalyzed = fieldAnalyzed == "true"
		}

		fieldsMap[k] = opt
	}

	return fieldsMap, nil
}

// needFieldMap 判断是否需要执行 QueryFieldMap 并注入 FieldsMap。
func needFieldMap(query *metadata.Query) bool {
	if query == nil {
		return false
	}
	if isTSpiderQuery(query) {
		return true
	}
	switch query.Measurement {
	case sql_expr.Doris, sql_expr.HDFS:
		return true
	case "":
		return query.StorageType == metadata.BkSqlStorageType && query.SQL != ""
	default:
		return false
	}
}

func queryPhysicalTables(query *metadata.Query) []string {
	if query == nil {
		return nil
	}

	dbs := query.DBs
	if len(dbs) == 0 && query.DB != "" {
		dbs = []string{query.DB}
	}

	tables := make([]string, 0, len(dbs))
	for _, db := range dbs {
		if db == "" {
			continue
		}
		tables = append(tables, formatPhysicalTableName(db, query.Measurement))
	}
	return tables
}

func shouldDisableShardKeyTimeBucket(query *metadata.Query, fieldsMap metadata.FieldsMap, tableFieldsMap TableFieldsMap, timeField string) bool {
	if query == nil || query.Measurement != sql_expr.Doris {
		return false
	}

	tables := queryPhysicalTables(query)
	if len(tables) > 0 {
		for _, table := range tables {
			tableFields, ok := tableFieldsMap[table]
			if !ok {
				// 无法证明该物理表包含 __shard_key__ 时，不启用 shard key 时间桶优化。
				return true
			}
			if !tableFields.Field(sql_expr.ShardKey).Existed() {
				return true
			}
		}
		return false
	}

	// 没有逐表信息时退回合并字段表判断；只有字段表能证明 timeField 存在时，
	// 才用它判断 __shard_key__ 缺失，避免字段表为空或不完整时误判。
	if !fieldsMap.Field(timeField).Existed() {
		return false
	}
	return !fieldsMap.Field(sql_expr.ShardKey).Existed()
}

func (i *Instance) InitQueryFactory(ctx context.Context, query *metadata.Query, start, end time.Time) (*QueryFactory, error) {
	f := NewQueryFactory(ctx, query).
		WithRangeTime(start, end)

	// Doris / HDFS 均需获取字段表结构；TSpider 与 Doris 共用 SQL 表达式，也需要 FieldsMap，
	// 否则 dimTransform 会将未知列变为 NULL。
	if needFieldMap(query) {
		fieldsMap, tableFieldsMap, err := i.queryFieldMaps(ctx, query, start, end)
		if err != nil {
			return nil, err
		}

		// 只能使用在表结构的字段才能使用
		var keepColumns []string
		for _, k := range query.Source {
			if _, ok := fieldsMap[k]; ok {
				keepColumns = append(keepColumns, k)
			}
		}
		f.WithFieldsMap(fieldsMap).WithTableFieldsMap(tableFieldsMap).WithKeepColumns(keepColumns)
		if shouldDisableShardKeyTimeBucket(query, fieldsMap, tableFieldsMap, f.timeField) {
			f.WithShardKeyTimeBucket(false)
		}
	}

	return f, nil
}

func (i *Instance) Table(query *metadata.Query) string {
	return formatPhysicalTableName(query.DB, query.Measurement)
}

// QueryFieldMap 查询字段映射
func (i *Instance) QueryFieldMap(ctx context.Context, query *metadata.Query, start, end time.Time) (metadata.FieldsMap, error) {
	fieldsMap, _, err := i.queryFieldMaps(ctx, query, start, end)
	return fieldsMap, err
}

func (i *Instance) queryFieldMaps(ctx context.Context, query *metadata.Query, start, end time.Time) (metadata.FieldsMap, TableFieldsMap, error) {
	var err error

	if query == nil {
		return nil, nil, nil
	}

	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("es query error: %s", r)
		}
	}()

	ctx, span := trace.NewSpan(ctx, "bk-sql-query-field-map")
	defer span.End(&err)

	f := NewQueryFactory(ctx, query).WithRangeTime(start, end)

	dbs := query.DBs
	if len(dbs) == 0 {
		dbs = []string{query.DB}
	}

	if len(dbs) == 0 {
		err = fmt.Errorf("%s 配置的查询别名为空", query.TableID)
		return nil, nil, err
	}

	fieldsMap := make(metadata.FieldsMap)
	tableFieldsMap := make(TableFieldsMap)
	needTSpiderFieldMap := isTSpiderQuery(query)
	var (
		fieldMapTables  []string
		lastFieldMapErr error
	)

	// 多表的字段进行合并查询，进行倒序遍历
	for idx := len(dbs) - 1; idx >= 0; idx-- {
		db := dbs[idx]
		table := formatPhysicalTableName(db, f.query.Measurement)
		fieldMapTables = append(fieldMapTables, table)

		sql := f.expr.DescribeTableSQL(table)
		res, err := i.getFieldsMap(ctx, newQuerySyncRequest(sql, query))
		if err != nil {
			lastFieldMapErr = err
			continue
		}
		normalized := normalizeFieldsMap(res, query.FieldAlias)
		tableFieldsMap[table] = normalized

		for k, v := range normalized {
			if k == "" || v.FieldType == "" {
				continue
			}
			// 如果字段相同则忽略
			if _, ok := fieldsMap[k]; ok {
				continue
			}

			fieldsMap[k] = v
		}
	}

	if needTSpiderFieldMap && len(fieldsMap) == 0 {
		tableNames := strings.Join(fieldMapTables, ", ")
		if lastFieldMapErr != nil {
			err = fmt.Errorf("query tspider field map failed for %s: %w", tableNames, lastFieldMapErr)
			return nil, nil, err
		}
		err = fmt.Errorf("query tspider field map empty for %s", tableNames)
		return nil, nil, err
	}

	return fieldsMap, tableFieldsMap, nil
}

func normalizeFieldsMap(fieldsMap metadata.FieldsMap, fieldAlias metadata.FieldAlias) metadata.FieldsMap {
	normalized := make(metadata.FieldsMap, len(fieldsMap))
	for k, v := range fieldsMap {
		if k == "" || v.FieldType == "" {
			continue
		}
		v.AliasName = fieldAlias.AliasName(k)
		v.FieldName = k
		ks := strings.Split(k, ".")
		v.OriginField = ks[0]
		v.TokenizeOnChars = make([]string, 0)
		normalized[k] = v
	}
	return normalized
}

// QueryRawData 直接查询原始返回
func (i *Instance) QueryRawData(ctx context.Context, query *metadata.Query, start, end time.Time, dataCh chan<- map[string]any) (size int64, total int64, option *metadata.ResultTableOption, err error) {
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
		return size, total, option, err
	}

	rangeLeftTime := end.Sub(start)
	metric.TsDBRequestRangeMinute(ctx, rangeLeftTime, i.InstanceType())

	if option.From != nil {
		query.From = *option.From
	}

	queryFactory, err := i.InitQueryFactory(ctx, query, start, end)
	if err != nil {
		return size, total, option, err
	}
	queryFactory.WithMaxLimit(i.maxLimit + i.tolerance)
	sql, err := queryFactory.SQL()
	if err != nil {
		return size, total, option, err
	}

	// 如果是 dry run 则直接返回 sql 查询语句
	if query.DryRun {
		option.SQL = sql
		return size, total, option, err
	}

	data, err := i.sqlQuery(ctx, newQuerySyncRequest(sql, query))
	if err != nil {
		err = fmt.Errorf("sql [%s] query err: %s", sql, err.Error())
		return size, total, option, err
	}

	if data == nil {
		return size, total, option, err
	}

	if data.ResultSchema != nil {
		option.ResultSchema = data.ResultSchema
	}

	span.Set("data-total-records", data.TotalRecords)
	span.Set("data-list-size", len(data.List))

	for _, list := range data.List {
		newData := queryFactory.ReloadListData(list, false)
		query.FieldAlias.AddAliasKeysWhenOriginalFieldPresent(newData)
		newData[metadata.KeyIndex] = query.DB
		// 注入原始数据需要的字段
		query.DataReload(newData)

		dataCh <- newData
	}

	size = int64(len(data.List))
	total = int64(data.TotalRecords)

	return size, total, option, err
}

func (i *Instance) QuerySeriesSet(ctx context.Context, query *metadata.Query, start, end time.Time) storage.SeriesSet {
	var err error
	ctx, span := trace.NewSpan(ctx, "bk-sql-query-series-set")
	defer span.End(&err)

	span.Set("query-series-set-start", start)
	span.Set("query-series-set-end", end)

	if start.UnixMilli() > end.UnixMilli() || start.UnixMilli() == 0 {
		return storage.ErrSeriesSet(fmt.Errorf("range time is error, start: %s, end: %s ", start, end))
	}

	rangeLeftTime := end.Sub(start)
	metric.TsDBRequestRangeMinute(ctx, rangeLeftTime, i.InstanceType())

	// series 计算需要按照时间排序。这里不能原地修改入参 query，调用方可能在多路查询中复用同一个 *metadata.Query。
	seriesQuery := *query
	seriesQuery.Orders = append(metadata.Orders{
		{
			Name: sql_expr.FieldTime,
			Ast:  true,
		},
	}, append(metadata.Orders(nil), query.Orders...)...)

	queryFactory, err := i.InitQueryFactory(ctx, &seriesQuery, start, end)
	if err != nil {
		return storage.ErrSeriesSet(err)
	}
	queryFactory.WithMaxLimit(i.maxLimit + i.tolerance)
	sql, err := queryFactory.SQL()
	if err != nil {
		return storage.ErrSeriesSet(err)
	}

	data, err := i.sqlQuery(ctx, newQuerySyncRequest(sql, query))
	if err != nil {
		err = metadata.NewMessage(
			metadata.MsgQueryBKSQL,
			"%s 查询失败",
			sql,
		).Error(ctx, err)
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
		err = metadata.NewMessage(
			metadata.MsgQueryBKSQL,
			"数据解析失败",
		).Error(ctx, err)
		return storage.ErrSeriesSet(err)
	}

	return remote.FromQueryResult(true, qr)
}

func (i *Instance) DirectQueryRange(ctx context.Context, promql string, start, end time.Time, step time.Duration) (promql.Matrix, bool, error) {
	return nil, false, nil
}

func (i *Instance) DirectQuery(ctx context.Context, qs string, end time.Time) (promql.Vector, error) {
	return nil, nil
}

func (i *Instance) QueryExemplar(ctx context.Context, fields []string, query *metadata.Query, start, end time.Time, matchers ...*labels.Matcher) (*decoder.Response, error) {
	return nil, nil
}

func (i *Instance) QueryLabelNames(ctx context.Context, query *metadata.Query, start, end time.Time) ([]string, error) {
	var err error

	ctx, span := trace.NewSpan(ctx, "bk-sql-label-name")
	defer span.End(&err)

	// 取字段名不需要返回数据，但是 size 不能使用 0，所以还是用 1。这里不能原地修改入参 query，调用方可能复用同一个 *metadata.Query。
	labelNamesQuery := *query
	labelNamesQuery.Size = 1

	queryFactory, err := i.InitQueryFactory(ctx, &labelNamesQuery, start, end)
	if err != nil {
		return nil, err
	}

	sql, err := queryFactory.SQL()
	if err != nil {
		return nil, err
	}

	data, err := i.sqlQuery(ctx, newQuerySyncRequest(sql, query))
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

	labelValuesQuery := *query
	labelValuesQuery.SelectDistinct = []string{name}

	queryFactory, err := i.InitQueryFactory(ctx, &labelValuesQuery, start, end)
	if err != nil {
		return nil, err
	}

	sql, err := queryFactory.SQL()
	if err != nil {
		return nil, err
	}

	data, err := i.sqlQuery(ctx, newQuerySyncRequest(sql, query))
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
	var err error

	ctx, span := trace.NewSpan(ctx, "bk-sql-query-series")
	defer span.End(&err)

	if len(query.Source) == 0 {
		err = fmt.Errorf("no source specified")
		return nil, err
	}

	fieldMap, err := i.QueryFieldMap(ctx, query, start, end)
	if err != nil {
		return nil, err
	}

	var labelNames []string
	for _, k := range query.Source {
		if checkInternalDimension(k) {
			continue
		}
		if k == sql_expr.TimeStamp || k == sql_expr.Value {
			continue
		}
		labelNames = append(labelNames, k)
	}

	span.Set("field-map", fieldMap)
	span.Set("label-names", labelNames)

	if len(labelNames) == 0 {
		return nil, nil
	}

	// 设置 SelectDistinct 以获取唯一标签组合。这里不能原地修改入参 query，调用方可能复用同一个 *metadata.Query。
	seriesQuery := *query
	seriesQuery.SelectDistinct = append([]string(nil), labelNames...)

	queryFactory, err := i.InitQueryFactory(ctx, &seriesQuery, start, end)
	if err != nil {
		return nil, err
	}
	distinctSQL, err := queryFactory.SQL()
	if err != nil {
		return nil, err
	}

	distinctData, err := i.sqlQuery(ctx, newQuerySyncRequest(distinctSQL, query))
	if err != nil {
		return nil, err
	}

	encodeFunc := metadata.GetFieldFormat(ctx).EncodeFunc()

	series := make([]map[string]string, 0, len(distinctData.List))
	for _, d := range distinctData.List {
		seriesMap := make(map[string]string)
		for _, name := range labelNames {
			encodedName := name
			if encodeFunc != nil {
				encodedName = encodeFunc(name)
			}

			value, valErr := getValue(encodedName, d)
			if valErr != nil {
				// 字段不存在时视为空值（NULL），不返回错误
				continue
			}

			if value != "" {
				seriesMap[name] = value
			}
		}

		if len(seriesMap) > 0 {
			series = append(series, seriesMap)
		}
	}

	span.Set("series-count", len(series))
	return series, nil
}

func (i *Instance) InstanceType() string {
	return metadata.BkSqlStorageType
}

func getValue(k string, d map[string]any) (string, error) {
	var value string
	if v, ok := d[k]; ok {
		// 增加 nil 判断，避免回传的数值为空
		if v == nil {
			return value, nil
		}

		switch t := v.(type) {
		case string:
			value = fmt.Sprintf("%s", v)
		case float64, float32:
			value = fmt.Sprintf("%.f", v)
		case int64, int32, int:
			value = fmt.Sprintf("%d", v)
		case json.Number:
			processed := precision.ProcessNumber(t)
			value = fmt.Sprintf("%v", processed)
		default:
			return value, fmt.Errorf("get_value_error: type %T, %v in %s with %+v", v, v, k, d)
		}
	}
	return value, nil
}
