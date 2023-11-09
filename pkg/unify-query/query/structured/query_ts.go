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
	"time"

	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"
	oleltrace "go.opentelemetry.io/otel/trace"

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
	SpaceUid string `json:"space_uid"`
	// QueryList 查询实例
	QueryList []*Query `json:"query_list"`
	// MetricMerge 表达式：支持所有PromQL语法
	MetricMerge string `json:"metric_merge" example:"a"`
	// ResultColumns 指定保留返回字段值
	ResultColumns []string `json:"result_columns" swaggerignore:"true"`
	// Start 开始时间：单位为毫秒的时间戳
	Start string `json:"start_time" example:"1657848000"`
	// End 结束时间：单位为毫秒的时间戳
	End string `json:"end_time" example:"1657851600"`
	// Step 步长：最终返回的点数的时间间隔
	Step string `json:"step" example:"1m"`
	// DownSampleRange 降采样：大于Step才能生效，可以为空
	DownSampleRange string `json:"down_sample_range,omitempty" example:"5m"`
	// Timezone 时区
	Timezone string `json:"timezone,omitempty" example:"Asia/Shanghai"`
	// LookBackDelta 偏移量
	LookBackDelta string `json:"look_back_delta"`
	// Instant 瞬时数据
	Instant bool `json:"instant"`
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

func (q *QueryTs) ToPromExpr(ctx context.Context, referenceNameMetric map[string]string, referenceNameLabelMatcher map[string][]*labels.Matcher) (parser.Expr, error) {
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
		var labelsMatcher []*labels.Matcher
		if referenceNameLabelMatcher != nil {
			if v, ok := referenceNameLabelMatcher[query.ReferenceName]; ok {
				labelsMatcher = v
			}
		}

		if expr, err = query.ToPromExpr(ctx, referenceNameMetric, labelsMatcher...); err != nil {
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
	DataSource string `json:"data_source" swaggerignore:"true"`
	// TableID 数据实体ID，容器指标可以为空
	TableID TableID `json:"table_id" example:"system.cpu_summary"`
	// FieldName 查询指标
	FieldName string `json:"field_name" example:"usage"`
	// IsRegexp 指标是否使用正则查询
	IsRegexp bool `json:"is_regexp" example:"false"`
	// FieldList 仅供 exemplar 查询 trace 指标时使用
	FieldList []string `json:"field_list" example:"" swaggerignore:"true"` // 目前是供查询trace指标列时，可以批量查询使用
	// AggregateMethodList 维度聚合函数
	AggregateMethodList []AggregateMethod `json:"function"`
	// TimeAggregation 时间聚合方法
	TimeAggregation TimeAggregation `json:"time_aggregation"`
	// ReferenceName 别名，用于表达式计算
	ReferenceName string `json:"reference_name" example:"a"`
	// Dimensions promQL 使用维度
	Dimensions []string `json:"dimensions" example:"bk_target_ip,bk_target_cloud_id"`
	// Limit 点数限制数量
	Limit int `json:"limit" example:"0"`
	// Timestamp @-modifier 标记
	Timestamp *int64 `json:"timestamp"`
	// StartOrEnd @-modifier 标记，start or end
	StartOrEnd parser.ItemType `json:"start_or_end"`
	// VectorOffset
	VectorOffset time.Duration `json:"vector_offset"`
	// Offset 偏移量
	Offset string `json:"offset" example:""`
	// OffsetForward 偏移方向，默认 false 为向前偏移
	OffsetForward bool `json:"offset_forward" example:"false"`
	// Slimit 维度限制数量
	Slimit int `json:"slimit" example:"0"`
	// Soffset 弃用字段
	Soffset int `json:"soffset" example:"0" swaggerignore:"true"`
	// Conditions 过滤条件
	Conditions Conditions `json:"conditions"`
	// KeepColumns 保留字段
	KeepColumns KeepColumns `json:"keep_columns" swaggerignore:"true"`

	// AlignInfluxdbResult 保留字段，无需配置，是否对齐influxdb的结果,该判断基于promql和influxdb查询原理的差异
	AlignInfluxdbResult bool `json:"-"`

	// Start 保留字段，会被外面的 Start 覆盖
	Start string `json:"-" swaggerignore:"true"`
	// End 保留字段，会被外面的 End 覆盖
	End string `json:"-" swaggerignore:"true"`
	// Step
	Step string `json:"step" swaggerignore:"true"`
	// Timezone 时区，会被外面的 Timezone 覆盖
	Timezone string `json:"-" swaggerignore:"true"`
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
		span          oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "query-ts-to-query-metric")
	if span != nil {
		defer span.End()
	}

	queryMetric := &metadata.QueryMetric{
		ReferenceName: referenceName,
		MetricName:    metricName,
	}

	tsDBs, err := GetTsDBList(ctx, &TsDBOption{
		SpaceUid:  spaceUid,
		TableID:   tableID,
		FieldName: metricName,
		IsRegexp:  q.IsRegexp,
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

	trace.InsertStringIntoSpan("query-space-uid", spaceUid, span)
	trace.InsertStringIntoSpan("query-table-id", string(tableID), span)
	trace.InsertStringIntoSpan("query-metric", metricName, span)
	trace.InsertStringIntoSpan("query-is-regexp", fmt.Sprintf("%v", q.IsRegexp), span)
	trace.InsertIntIntoSpan("tsdb-num", len(tsDBs), span)

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
			LabelsMatcher: make([]*labels.Matcher, 0),
		}
		allCondition AllConditions

		span oleltrace.Span
	)

	ctx, span = trace.IntoContext(ctx, trace.TracerName, "build-metadata-query")
	if span != nil {
		defer span.End()
	}

	metricName := q.FieldName
	expandMetricNames := tsDB.ExpandMetricNames
	// 增加查询条件
	if len(queryConditions) > 0 {
		query.LabelsMatcher = append(query.LabelsMatcher, queryLabelsMatcher...)

		whereList.Append(
			promql.AndOperator,
			promql.NewTextWhere(
				promql.MakeOrExpression(
					ConvertToPromBuffer(queryConditions),
				),
			),
		)
	}

	db := tsDB.DB
	storageID := tsDB.StorageID
	clusterName := tsDB.ClusterName
	tagKeys := tsDB.TagsKey
	vmRt := tsDB.VmRt
	measurement = tsDB.Measurement
	measurements = []string{measurement}

	trace.InsertStringIntoSpan("tsdb-table-id", tsDB.TableID, span)
	trace.InsertStringIntoSpan("tsdb-filters", fmt.Sprintf("%+v", tsDB.Filters), span)
	trace.InsertStringIntoSpan("tsdb-db", db, span)
	trace.InsertStringIntoSpan("tsdb-storge-id", storageID, span)
	trace.InsertStringIntoSpan("tsdb-cluster-name", clusterName, span)
	trace.InsertStringIntoSpan("tsdb-tag-keys", fmt.Sprintf("%+v", tagKeys), span)
	trace.InsertStringIntoSpan("tsdb-vm-rt", vmRt, span)
	trace.InsertStringIntoSpan("tsdb-measurements", fmt.Sprintf("%+v", measurements), span)

	if q.Offset != "" {
		dTmp, err := model.ParseDuration(q.Offset)
		if err != nil {
			return nil, err
		}
		query.OffsetInfo.OffSet = time.Duration(dTmp)
	}

	switch tsDB.MeasurementType {
	case redis.BKTraditionalMeasurement:
		// measurement: cpu_detail, field: usage  =>  cpu_detail_usage
		field, fields = metricName, expandMetricNames
	// 多指标单表，单列多指标，维度: metric_name 为指标名，metric_value 为指标值
	case redis.BkExporter:
		field, fields = promql.StaticMetricValue, []string{promql.StaticMetricValue}
		fieldOp := promql.EqualOperator
		if q.IsRegexp {
			fieldOp = promql.RegexpOperator
		}
		whereList.Append(
			promql.AndOperator,
			promql.NewWhere(
				promql.StaticMetricName, metricName, fieldOp, promql.StringType,
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
		err := fmt.Errorf("%s: %s 类型异常", tsDB.TableID, tsDB.MeasurementType)
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	trace.InsertStringIntoSpan("tsdb-fields", fmt.Sprintf("%+v", fields), span)

	// 拼入空间自带过滤条件
	var filterConditions = make([][]ConditionField, 0, len(tsDB.Filters))
	for _, filter := range tsDB.Filters {
		var (
			cond           = make([]ConditionField, 0, len(filter))
			labelsMatchers = make([]*labels.Matcher, 0, len(filter))
		)
		for k, v := range filter {
			if v != "" {
				cond = append(cond, ConditionField{
					DimensionName: k,
					Value:         []string{v},
					Operator:      Contains,
				})

				matcher, _ := labels.NewMatcher(labels.MatchEqual, k, v)
				labelsMatchers = append(labelsMatchers, matcher)
			}
		}

		if len(cond) > 0 {
			filterConditions = append(filterConditions, cond)
		}

		// labelsMatcher 不支持 or 语法，所以只取第一个
		if len(tsDB.Filters) == 1 {
			query.LabelsMatcher = append(query.LabelsMatcher, labelsMatchers...)
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

	metricsConditions := ConditionField{
		DimensionName: promql.MetricLabelName,
		Operator:      ConditionEqual,
		Value:         []string{fmt.Sprintf("%s_%s", measurement, field)},
	}
	if q.IsRegexp {
		metricsConditions.Operator = ConditionRegEqual
	}

	// 合并查询以及空间过滤条件到 condition 里面
	allCondition = MergeConditionField(queryConditions, filterConditions)

	// 合并指标到 condition 里面
	allCondition = MergeConditionField(allCondition, [][]ConditionField{{metricsConditions}})

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
	query.Field = field
	query.Timezone = timezone
	query.Fields = fields
	query.Measurements = measurements

	query.Condition = whereList.String()
	query.VmCondition, query.VmConditionNum = allCondition.VMString(vmRt)

	return query, nil
}

func (q *Query) ToPromExpr(ctx context.Context, referenceNameMetric map[string]string, matchers ...*labels.Matcher) (parser.Expr, error) {
	var (
		metric string
		err    error

		originalOffset time.Duration
		step           time.Duration
		dTmp           model.Duration

		result parser.Expr
	)

	// 判断是否使用别名作为指标
	metric = q.ReferenceName
	if referenceNameMetric != nil {
		if m, ok := referenceNameMetric[q.ReferenceName]; ok {
			metric = m
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

	timeIdx := 0
	if q.TimeAggregation.Function != "" && q.TimeAggregation.Window != "" {
		// 拼接时间聚合函数，NodeIndex 的数据如下：
		// count_over_time(metric[1m:2m])：vector -> subQuery -> call： nodeIndex 为 2
		// sum by(job, metric_name) (delta(label_replace(metric, "")[1m:]))：vector -> call -> subQuery -> call -> aggr：nodeIndex 为 3
		// count_over_time(a[1m])：vector -> matrix -> call：nodeIndex 为 2
		// 所以最小值为 2
		timeIdx = q.TimeAggregation.NodeIndex - 2
		// 增加小于 0 的场景兼容默认值为空的情况
		if timeIdx <= 0 {
			result, err = q.TimeAggregation.ToProm(result)
			if err != nil {
				return nil, err
			}
		}
	}

	for idx, method := range q.AggregateMethodList {
		if timeIdx > 0 && idx == timeIdx {
			result, err = q.TimeAggregation.ToProm(result)
			if err != nil {
				return nil, err
			}
		}

		if result, err = method.ToProm(result); err != nil {
			log.Errorf(ctx, "failed to translate function for->[%s]", err)
			return nil, err
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
