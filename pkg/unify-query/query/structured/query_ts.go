// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/offlineDataArchive"
	queryMod "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type QueryTs struct {
	// SpaceUid 空间ID
	SpaceUid string `json:"space_uid,omitempty"`
	// QueryList 查询实例
	QueryList []*Query `json:"query_list,omitempty"`
	// MetricMerge 表达式：支持所有PromQL语法
	MetricMerge string `json:"metric_merge,omitempty" example:"a"`
	// ResultColumns 指定保留返回字段值
	ResultColumns []string `json:"result_columns,omitempty" swaggerignore:"true"`
	// Start 开始时间：单位为毫秒的时间戳
	Start string `json:"start_time,omitempty" example:"1657848000"`
	// End 结束时间：单位为毫秒的时间戳
	End string `json:"end_time,omitempty" example:"1657851600"`
	// Step 步长：最终返回的点数的时间间隔
	Step string `json:"step,omitempty" example:"1m"`
	// DownSampleRange 降采样：大于Step才能生效，可以为空
	DownSampleRange string `json:"down_sample_range,omitempty" example:"5m"`
	// Timezone 时区
	Timezone string `json:"timezone,omitempty" example:"Asia/Shanghai"`
	// LookBackDelta 偏移量
	LookBackDelta string `json:"look_back_delta,omitempty"`
	// Instant 瞬时数据
	Instant bool `json:"instant"`
	// IsNotPromQL 是否使用 PromQL 查询
	IsNotPromQL bool `json:"is_not_promql"`
}

// 根据 timezone 偏移对齐
func timeOffset(t time.Time, timezone string, step time.Duration) (string, time.Time, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		loc = time.UTC
	}
	t0 := t.In(loc)
	_, offset := t0.Zone()
	outTimezone := t0.Location().String()
	offsetDuration := time.Duration(offset) * time.Second
	t1 := t.Add(offsetDuration)
	t2 := time.Unix(int64(math.Floor(float64(t1.Unix())/step.Seconds())*step.Seconds()), 0)
	t3 := t2.Add(offsetDuration * -1).In(loc)
	return outTimezone, t3, nil
}

func ToTime(startStr, endStr, stepStr, timezone string) (time.Time, time.Time, time.Duration, string, error) {
	var (
		start    time.Time
		stop     time.Time
		interval time.Duration
		err      error
	)

	var toTime = func(timestamp string) (time.Time, error) {
		timeNum, err := strconv.Atoi(timestamp)
		if err != nil {
			return time.Time{}, err
		}
		return time.Unix(int64(timeNum), 0), nil
	}

	if startStr != "" {
		start, err = toTime(startStr)
		if err != nil {
			return start, stop, interval, timezone, err
		}
	}

	if endStr == "" {
		stop = time.Now()
	} else {
		stop, err = toTime(endStr)
		if err != nil {
			return start, stop, interval, timezone, err
		}
	}

	if stepStr == "" {
		interval = promql.GetDefaultStep()
	} else {
		dTmp, err := model.ParseDuration(stepStr)
		interval = time.Duration(dTmp)

		if err != nil {
			return start, stop, interval, timezone, err
		}
	}

	// 根据 timezone 来对齐
	timezone, start, err = timeOffset(start, timezone, interval)
	return start, stop, interval, timezone, nil
}

func (q *QueryTs) ToQueryReference(ctx context.Context) (metadata.QueryReference, error) {

	queryReference := make(metadata.QueryReference)
	for _, qry := range q.QueryList {
		qry.Timezone = q.Timezone
		qry.Start = q.Start
		qry.End = q.End
		// 如果 qry.Step 不存在去外部统一的 step
		if qry.Step == "" {
			qry.Step = q.Step
		}

		queryMetric, err := qry.ToQueryMetric(ctx, q.SpaceUid)
		if err != nil {
			return nil, err
		}
		queryReference[qry.ReferenceName] = queryMetric
	}

	return queryReference, nil
}

func (q *QueryTs) ToQueryClusterMetric(ctx context.Context) (*metadata.QueryClusterMetric, error) {
	var (
		qry *Query
		err error
	)
	ctx, span := trace.NewSpan(ctx, "to-query-cluster-metric")
	defer span.End(&err)

	if len(q.QueryList) != 1 {
		return nil, errors.Errorf("Only one query supported, now %d ", len(q.QueryList))
	}

	for _, qry = range q.QueryList {
	}

	// 结构定义转换
	allConditions, err := qry.Conditions.AnalysisConditions()
	queryConditions := make([][]metadata.ConditionField, 0, len(allConditions))
	for _, conds := range allConditions {
		queryConds := make([]metadata.ConditionField, 0, len(conds))
		for _, cond := range conds {
			queryConds = append(queryConds, metadata.ConditionField{
				DimensionName: cond.DimensionName,
				Value:         cond.Value,
				Operator:      cond.Operator,
			})
		}
		queryConditions = append(queryConditions, queryConds)
	}
	if err != nil {
		return nil, err
	}
	queryCM := &metadata.QueryClusterMetric{
		MetricName:          qry.FieldName,
		AggregateMethodList: qry.AggregateMethodList.ToQry(),
		Conditions:          queryConditions,
	}
	if qry.TimeAggregation.Function != "" {
		wDuration, err := qry.TimeAggregation.Window.ToTime()
		if err != nil {
			return nil, errors.Errorf("TimeAggregation.Window(%v) format is invalid, %v", qry.TimeAggregation, err)
		}
		queryCM.TimeAggregation = metadata.TimeAggregation{
			Function:       qry.TimeAggregation.Function,
			WindowDuration: wDuration,
		}
	}
	span.Set("query-field", queryCM.MetricName)
	span.Set("query-aggr-methods", fmt.Sprintf("%+v", qry.AggregateMethodList))
	span.Set("query-conditions", fmt.Sprintf("%+v", queryCM.Conditions))
	span.Set("query-time-func", queryCM.TimeAggregation.Function)
	span.Set("query-time-window", strconv.FormatInt(int64(queryCM.TimeAggregation.WindowDuration), 10))
	return queryCM, nil
}

type PromExprOption struct {
	ReferenceNameMetric       map[string]string
	ReferenceNameLabelMatcher map[string][]*labels.Matcher
	FunctionReplace           map[string]string
}

func (q *QueryTs) ToPromExpr(
	ctx context.Context,
	promExprOpt *PromExprOption,
) (parser.Expr, error) {
	var (
		err     error
		result  parser.Expr
		expr    parser.Expr
		exprMap = make(map[string]*PromExpr, len(q.QueryList))
	)

	if q.MetricMerge == "" {
		err = fmt.Errorf("metric merge is empty")
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	// 先解析表达式
	if result, err = parser.ParseExpr(q.MetricMerge); err != nil {
		log.Errorf(ctx, "failed to parser metric_merge->[%s] for err->[%s]", string(q.MetricMerge), err)
		return nil, err
	}

	// 获取指标查询的表达式
	for _, query := range q.QueryList {
		if expr, err = query.ToPromExpr(ctx, promExprOpt); err != nil {
			return nil, err
		}
		exprMap[query.ReferenceName] = &PromExpr{
			Expr:       expr,
			Dimensions: nil,
			ctx:        ctx,
		}
	}

	result, err = HandleExpr(exprMap, result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

type Query struct {
	// DataSource 暂不使用
	DataSource string `json:"data_source,omitempty" swaggerignore:"true"`
	// TableID 数据实体ID，容器指标可以为空
	TableID TableID `json:"table_id,omitempty" example:"system.cpu_summary"`
	// FieldName 查询指标
	FieldName string `json:"field_name,omitempty" example:"usage"`
	// IsRegexp 指标是否使用正则查询
	IsRegexp bool `json:"is_regexp" example:"false"`
	// FieldList 仅供 exemplar 查询 trace 指标时使用
	FieldList []string `json:"field_list,omitempty" example:"" swaggerignore:"true"` // 目前是供查询trace指标列时，可以批量查询使用
	// AggregateMethodList 维度聚合函数
	AggregateMethodList AggregateMethodList `json:"function"`
	// TimeAggregation 时间聚合方法
	TimeAggregation TimeAggregation `json:"time_aggregation"`
	// ReferenceName 别名，用于表达式计算
	ReferenceName string `json:"reference_name,omitempty" example:"a"`
	// Dimensions promQL 使用维度
	Dimensions []string `json:"dimensions,omitempty" example:"bk_target_ip,bk_target_cloud_id"`
	// Limit 点数限制数量
	Limit int `json:"limit,omitempty" example:"0"`
	// Timestamp @-modifier 标记
	Timestamp *int64 `json:"timestamp,omitempty"`
	// StartOrEnd @-modifier 标记，start or end
	StartOrEnd parser.ItemType `json:"start_or_end,omitempty"`
	// VectorOffset
	VectorOffset time.Duration `json:"vector_offset,omitempty"`
	// Offset 偏移量
	Offset string `json:"offset,omitempty" example:""`
	// OffsetForward 偏移方向，默认 false 为向前偏移
	OffsetForward bool `json:"offset_forward,omitempty" example:"false"`
	// Slimit 维度限制数量
	Slimit int `json:"slimit,omitempty" example:"0"`
	// Soffset 弃用字段
	Soffset int `json:"soffset,omitempty" example:"0" swaggerignore:"true"`
	// Conditions 过滤条件
	Conditions Conditions `json:"conditions,omitempty"`
	// KeepColumns 保留字段
	KeepColumns KeepColumns `json:"keep_columns,omitempty" swaggerignore:"true"`

	// AlignInfluxdbResult 是否对齐开始时间
	AlignInfluxdbResult bool `json:"align_result,omitempty"`

	// Start 保留字段，会被外面的 Start 覆盖
	Start string `json:"-" swaggerignore:"true"`
	// End 保留字段，会被外面的 End 覆盖
	End string `json:"-" swaggerignore:"true"`
	// Step
	Step string `json:"step,omitempty" swaggerignore:"true"`
	// Timezone 时区，会被外面的 Timezone 覆盖
	Timezone string `json:"-" swaggerignore:"true"`

	// QueryString es 专用关键字查询
	QueryString string `json:"query_string"`

	// IsNotPromQL 是否使用 PromQL 查询
	IsNotPromQL bool `json:"-" swaggerignore:"true"`
}

func (q *Query) ToRouter() (*Route, error) {
	router := &Route{
		dataSource: q.DataSource,
		metricName: q.FieldName,
	}
	router.db, router.measurement = q.TableID.Split()
	return router, nil
}

// ToQueryMetric 通过 spaceUid 转换成可查询结构体
func (q *Query) ToQueryMetric(ctx context.Context, spaceUid string) (*metadata.QueryMetric, error) {
	var (
		referenceName = q.ReferenceName
		metricName    = q.FieldName
		tableID       = q.TableID
		err           error
	)

	ctx, span := trace.NewSpan(ctx, "query-ts-to-query-metric")
	defer span.End(&err)

	queryMetric := &metadata.QueryMetric{
		ReferenceName: referenceName,
		MetricName:    metricName,
	}

	// 判断是否查询非路由 tsdb
	if q.DataSource != "" {
		// 如果是 BkSql 查询无需获取 tsdb 路由关系
		if q.DataSource == BkData {
			allConditions, err := q.Conditions.AnalysisConditions()
			if err != nil {
				return nil, err
			}

			qry := &metadata.Query{
				StorageID:           consul.BkSqlStorageType,
				Measurement:         string(q.TableID),
				Field:               q.FieldName,
				AggregateMethodList: q.AggregateMethodList.ToQry(),
				BkSqlCondition:      allConditions.BkSql(),
			}

			span.Set("query-storage-id", qry.StorageID)
			span.Set("query-measurement", qry.Measurement)
			span.Set("query-field", qry.Field)
			span.Set("query-aggr-method-list", fmt.Sprintf("%+v", qry.AggregateMethodList))
			span.Set("query-bk-sql-condition", qry.BkSqlCondition)

			queryMetric.QueryList = []*metadata.Query{qry}
			return queryMetric, nil
		}
	}

	isSkipField := false
	if metricName == "" || q.DataSource == BkLog || q.DataSource == BkApm {
		isSkipField = true
	}

	tsDBs, err := GetTsDBList(ctx, &TsDBOption{
		SpaceUid:    spaceUid,
		TableID:     tableID,
		FieldName:   metricName,
		IsRegexp:    q.IsRegexp,
		Conditions:  q.Conditions,
		IsSkipSpace: metadata.GetUser(ctx).IsSkipSpace(),
		IsSkipField: isSkipField,
	})
	if err != nil {
		return nil, err
	}

	queryConditions, err := q.Conditions.AnalysisConditions()
	if err != nil {
		return nil, err
	}

	queryMetric.QueryList = make([]*metadata.Query, 0, len(tsDBs))

	queryLabelsMatcher, _, _ := q.Conditions.ToProm()

	span.Set("query-space-uid", spaceUid)
	span.Set("query-table-id", string(tableID))
	span.Set("query-metric", metricName)
	span.Set("query-is-regexp", fmt.Sprintf("%v", q.IsRegexp))
	span.Set("tsdb-num", len(tsDBs))

	for _, tsDB := range tsDBs {
		query, err := q.BuildMetadataQuery(ctx, tsDB, queryConditions, queryLabelsMatcher)
		if err != nil {
			return nil, err
		}
		queryMetric.QueryList = append(queryMetric.QueryList, query)
	}

	return queryMetric, nil
}

func (q *Query) BuildMetadataQuery(
	ctx context.Context,
	tsDB *queryMod.TsDBV2,
	queryConditions [][]ConditionField,
	queryLabelsMatcher []*labels.Matcher,
) (*metadata.Query, error) {
	var (
		field        string
		fields       []string
		measurement  string
		measurements []string

		whereList = promql.NewWhereList()

		query = &metadata.Query{
			SegmentedEnable: tsDB.SegmentedEnable,
			OffsetInfo: metadata.OffSetInfo{
				Limit:   q.Limit,
				SOffSet: q.Soffset,
				SLimit:  q.Slimit,
			},
		}
		allCondition AllConditions

		err error
	)

	ctx, span := trace.NewSpan(ctx, "build-metadata-query")
	defer span.End(&err)

	metricName := q.FieldName
	expandMetricNames := tsDB.ExpandMetricNames

	db := tsDB.DB
	storageID := tsDB.StorageID
	storageName := tsDB.StorageName
	clusterName := tsDB.ClusterName
	tagKeys := tsDB.TagsKey
	vmRt := tsDB.VmRt
	measurement = tsDB.Measurement
	measurements = []string{measurement}

	span.Set("tsdb-table-id", tsDB.TableID)
	span.Set("tsdb-field-list", tsDB.Field)
	span.Set("tsdb-measurement-type", tsDB.MeasurementType)
	span.Set("tsdb-filters", fmt.Sprintf("%+v", tsDB.Filters))
	span.Set("tsdb-data-label", tsDB.DataLabel)
	span.Set("tsdb-storage-id", storageID)
	span.Set("tsdb-storage-name", storageName)
	span.Set("tsdb-cluster-name", clusterName)
	span.Set("tsdb-tag-keys", fmt.Sprintf("%+v", tagKeys))
	span.Set("tsdb-vm-rt", vmRt)
	span.Set("tsdb-db", db)
	span.Set("tsdb-measurements", fmt.Sprintf("%+v", measurements))

	if q.Offset != "" {
		dTmp, err := model.ParseDuration(q.Offset)
		if err != nil {
			return nil, err
		}
		query.OffsetInfo.OffSet = time.Duration(dTmp)
	}

	if len(queryConditions) > 0 {

		// influxdb 查询特殊处理逻辑
		influxdbConditions := ConvertToPromBuffer(queryConditions)
		if len(influxdbConditions) > 0 {
			whereList.Append(
				promql.AndOperator,
				promql.NewTextWhere(
					promql.MakeOrExpression(
						influxdbConditions,
					),
				),
			)
		}
	}

	switch tsDB.MeasurementType {
	case redis.BKTraditionalMeasurement:
		// measurement: cpu_detail, field: usage  =>  cpu_detail_usage
		field, fields = metricName, expandMetricNames
	// 多指标单表，单列多指标，维度: metric_name 为指标名，metric_value 为指标值
	case redis.BkExporter:
		field, fields = promql.StaticMetricValue, []string{promql.StaticMetricValue}
		fieldOp := promql.EqualOperator
		valueType := promql.StringType
		if q.IsRegexp {
			fieldOp = promql.RegexpOperator
			valueType = promql.RegexpType
		}
		whereList.Append(
			promql.AndOperator,
			promql.NewWhere(
				promql.StaticMetricName, metricName, fieldOp, valueType,
			),
		)
	// 多指标单表，字段名为指标名
	case redis.BkStandardV2TimeSeries:
		field, fields = metricName, expandMetricNames
	// 单指标单表，指标名为表名，值为指定字段 value
	case redis.BkSplitMeasurement:
		// measurement: usage, field: value  => usage_value
		measurement, measurements = metricName, expandMetricNames
		field, fields = promql.StaticField, []string{promql.StaticField}
	default:
		field, fields = metricName, expandMetricNames
	}

	span.Set("tsdb-fields", fmt.Sprintf("%+v", fields))

	filterConditions := make([][]ConditionField, 0)
	satisfy, tKeys := judgeFilter(tsDB.Filters)
	// 满足压缩条件
	if satisfy {
		filterConditions = compressFilterCondition(tKeys, tsDB.Filters)
	} else {
		for _, filter := range tsDB.Filters {
			cond := make([]ConditionField, 0, len(filter))
			for k, v := range filter {
				if v != "" {
					cond = append(cond, ConditionField{
						DimensionName: k,
						Value:         []string{v},
						Operator:      Contains,
					})
				}
			}
			if len(cond) > 0 {
				filterConditions = append(filterConditions, cond)
			}
		}
	}
	if len(filterConditions) > 0 {
		whereList.Append(
			promql.AndOperator,
			promql.NewTextWhere(
				promql.MakeOrExpression(
					ConvertToPromBuffer(filterConditions),
				),
			),
		)
	}

	// 用于 vm 的查询逻辑特殊处理
	var vmMetric string
	if metricName != "" {
		vmMetric = fmt.Sprintf("%s_%s", metricName, promql.StaticField)
	}

	// 因为 vm 查询指标会转换格式，所以在查询的时候需要把用到指标的函数都进行替换，例如 label_replace
	for _, a := range q.AggregateMethodList {
		switch a.Method {
		// label_replace(v instant-vector, dst_label string, replacement string, src_label string, regex string)
		case "label_replace":
			if len(a.VArgsList) == 4 && a.VArgsList[2] == promql.MetricLabelName {
				if strings.LastIndex(fmt.Sprintf("%s", a.VArgsList[3]), field) < 0 {
					a.VArgsList[3] = fmt.Sprintf("%s_%s", a.VArgsList[3], field)
				}
			}
		}
	}

	// 因为 vm 查询指标会转换格式，所以在查询的时候需要把用到指标的条件都进行替换，也就是条件中使用 __name__ 的
	for _, qc := range queryConditions {
		for _, c := range qc {
			if c.DimensionName == promql.MetricLabelName {
				for ci, cv := range c.Value {
					if strings.LastIndex(cv, field) < 0 {
						c.Value[ci] = fmt.Sprintf("%s_%s", cv, field)
					}
				}
			}
		}
	}

	// 合并查询以及空间过滤条件到 condition 里面
	allCondition = MergeConditionField(queryConditions, filterConditions)

	if len(queryConditions) > 1 || len(filterConditions) > 1 {
		query.IsHasOr = true
	}

	query.AggregateMethodList = make([]metadata.AggrMethod, 0, len(q.AggregateMethodList))
	for _, aggr := range q.AggregateMethodList {
		query.AggregateMethodList = append(query.AggregateMethodList, metadata.AggrMethod{
			Name:       aggr.Method,
			Dimensions: aggr.Dimensions,
			Without:    aggr.Without,
		})
	}

	query.IsSingleMetric = tsDB.IsSplit()

	// 通过过期时间判断是否读取归档模块
	start, end, _, timezone, err := ToTime(q.Start, q.End, q.Step, q.Timezone)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}
	// tag 路由转换
	tagRouter, err := influxdb.GetTagRouter(ctx, tsDB.TagsKey, whereList.String())
	if err != nil {
		return nil, err
	}
	// 获取可以查询的 ShardID
	offlineDataArchiveQuery, _ := offlineDataArchive.GetMetaData().GetReadShardsByTimeRange(
		ctx, clusterName, tagRouter, db, query.RetentionPolicy, start.UnixNano(), end.UnixNano(),
	)

	if len(offlineDataArchiveQuery) > 0 {
		query.StorageID = consul.OfflineDataArchive
	} else {
		query.StorageID = storageID
	}

	query.TableID = tsDB.TableID
	query.ClusterName = clusterName
	query.TagsKey = tagKeys
	query.DB = db
	query.Measurement = measurement
	query.VmRt = vmRt
	query.StorageName = storageName
	query.Field = field
	query.Timezone = timezone
	query.Fields = fields
	query.Measurements = measurements
	query.IsNotPromQL = q.IsNotPromQL

	query.Condition = whereList.String()
	query.VmCondition, query.VmConditionNum = allCondition.VMString(vmRt, vmMetric, q.IsRegexp)

	// 写入 ES 所需内容
	query.DataSource = q.DataSource
	query.AllConditions = make(metadata.AllConditions, len(allCondition))
	for i, conditions := range allCondition {
		conds := make([]metadata.ConditionField, len(conditions))
		for j, c := range conditions {
			conds[j] = metadata.ConditionField{
				DimensionName: c.DimensionName,
				Value:         c.Value,
				Operator:      c.Operator,
			}
		}
		query.AllConditions[i] = conds
	}
	query.QueryString = q.QueryString
	if q.TimeAggregation.Window != "" {
		windowDuration, err := q.TimeAggregation.Window.ToTime()
		if err != nil {
			return nil, err
		}
		query.TimeAggregation = &metadata.TimeAggregation{
			Function:       q.TimeAggregation.Function,
			WindowDuration: windowDuration,
		}
	}

	span.Set("query-source-type", query.SourceType)
	span.Set("query-table-id", query.TableID)
	span.Set("query-db", query.DB)
	span.Set("query-measurement", query.Measurement)
	span.Set("query-measurements", query.Measurements)
	span.Set("query-field", query.Field)
	span.Set("query-fields", query.Fields)
	span.Set("query-offset-info", fmt.Sprintf("%+v", query.OffsetInfo))
	span.Set("query-timezone", query.Timezone)
	span.Set("query-condition", query.Condition)
	span.Set("query-vm-condition", query.VmCondition)
	span.Set("query-vm-condition-num", query.VmConditionNum)
	span.Set("query-is-regexp", fmt.Sprintf("%v", q.IsRegexp))

	span.Set("query-storage-type", query.StorageType)
	span.Set("query-storage-name", query.StorageName)

	span.Set("query-cluster-name", query.ClusterName)
	span.Set("query-tag-keys", query.TagsKey)
	span.Set("query-vm-rt", query.VmRt)

	return query, nil
}

func (q *Query) ToPromExpr(ctx context.Context, promExprOpt *PromExprOption) (parser.Expr, error) {
	var (
		metric string
		err    error

		originalOffset time.Duration
		step           time.Duration
		dTmp           model.Duration

		result   parser.Expr
		matchers []*labels.Matcher
	)

	// 判断是否使用别名作为指标
	metric = q.ReferenceName
	if promExprOpt != nil {
		// 替换指标名
		if m, ok := promExprOpt.ReferenceNameMetric[q.ReferenceName]; ok {
			metric = m
		}

		// 增加 Matchers
		for _, m := range promExprOpt.ReferenceNameLabelMatcher[q.ReferenceName] {
			matchers = append(matchers, m)
		}

		// 替换函数名
		if nf, ok := promExprOpt.FunctionReplace[q.TimeAggregation.Function]; ok {
			q.TimeAggregation.Function = nf
		}

		// 替换函数名
		for aggIdx, aggrVal := range q.AggregateMethodList {
			if nf, ok := promExprOpt.FunctionReplace[aggrVal.Method]; ok {
				q.AggregateMethodList[aggIdx].Method = nf
			}
		}
	}

	if q.AlignInfluxdbResult && q.TimeAggregation.Window != "" {
		step = promql.GetDefaultStep()
		if q.Step != "" {
			dTmp, err = model.ParseDuration(q.Step)
			if err != nil {
				log.Errorf(ctx, "parse step err->[%s]", err)
				return nil, err
			}
			step = time.Duration(dTmp)
		}
		// 控制偏移，promQL 只支持毫秒级别数据
		originalOffset = -step + time.Millisecond
	}

	if q.Offset != "" {
		dTmp, err = model.ParseDuration(q.Offset)
		if err != nil {
			return nil, err
		}
		offset := time.Duration(dTmp)
		if q.OffsetForward {
			// 时间戳向后平移，查询后面的数据
			originalOffset -= offset
		} else {
			// 时间戳向前平移，查询前面的数据
			originalOffset += offset
		}
	}

	if q.IsRegexp {
		metricMatcher, err := labels.NewMatcher(labels.MatchRegexp, labels.MetricName, metric)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, metricMatcher)
		metric = ""
	}

	result = &parser.VectorSelector{
		Name:          metric,
		LabelMatchers: matchers,

		Offset:         q.VectorOffset,
		Timestamp:      q.Timestamp,
		StartOrEnd:     q.StartOrEnd,
		OriginalOffset: originalOffset,
	}

	timeIdx := -1
	funcNums := len(q.AggregateMethodList)

	if q.TimeAggregation.Function != "" && q.TimeAggregation.Window != "" {
		funcNums += 1

		// 拼接时间聚合函数，NodeIndex 的数据如下：
		// count_over_time(metric[1m:2m])：vector -> subQuery -> call： nodeIndex 为 2
		// sum by(job, metric_name) (delta(label_replace(metric, "")[1m:]))：vector -> call -> subQuery -> call -> aggr：nodeIndex 为 3
		// count_over_time(a[1m])：vector -> matrix -> call：nodeIndex 为 2
		// 所以最小值为 2
		timeIdx = q.TimeAggregation.NodeIndex - 2
		// 增加小于 0 的场景兼容默认值为空的情况
		if timeIdx <= 0 {
			timeIdx = 0
		}
	}

	for idx := 0; idx < funcNums; idx++ {
		if idx == timeIdx {
			result, err = q.TimeAggregation.ToProm(result)
			if err != nil {
				return nil, err
			}
		} else {
			methodIdx := idx
			if timeIdx > -1 && methodIdx >= timeIdx {
				methodIdx -= 1
			}
			method := q.AggregateMethodList[methodIdx]
			if result, err = method.ToProm(result); err != nil {
				log.Errorf(ctx, "failed to translate function for->[%s]", err)
				return nil, err
			}
		}
	}

	return result, nil
}

func (c *Conditions) Append(field ConditionField, condition string) {
	if len(c.FieldList) > len(c.ConditionList) {
		c.ConditionList = append(c.ConditionList, condition)
	}
	c.FieldList = append(c.FieldList, field)
}
