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
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/querystring"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	queryMod "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

const (
	// Error messages
	ErrUnknownOperatorMsg = "unknown operator: %s"
)

type QueryTs struct {
	// SpaceUid 空间ID
	SpaceUid string `json:"space_uid,omitempty"`
	// QueryList 查询实例
	QueryList []*Query `json:"query_list,omitempty"`
	// MetricMerge 表达式：支持所有PromQL语法
	MetricMerge string `json:"metric_merge,omitempty" example:"a"`
	// OrderBy 排序字段列表，按顺序排序，负数代表倒序, ["_time", "-_time"]
	OrderBy OrderBy `json:"order_by,omitempty"`
	// ResultColumns 指定保留返回字段值
	ResultColumns []string `json:"result_columns,omitempty" swaggerignore:"true"`
	// Start 开始时间：单位为任意长度的时间戳
	Start string `json:"start_time,omitempty" example:"1657848000"`
	// End 结束时间：单位为任意长度的时间戳
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

	// 增加公共限制
	// Limit 点数限制数量
	Limit int `json:"limit,omitempty" example:"0"`
	// From 翻页开启数字
	From int `json:"from,omitempty" example:"0"`

	// Scroll 是否启用 Scroll 查询
	Scroll string `json:"scroll,omitempty"`
	// IsMultiFrom 是否启用 MultiFrom 查询
	IsMultiFrom bool `json:"is_multi_from,omitempty"`

	ResultTableOptions metadata.ResultTableOptions `json:"result_table_options,omitempty"`

	// HighLight 是否开启高亮
	HighLight *metadata.HighLight `json:"highlight,omitempty"`
}

func (q *QueryTs) LabelMap() (map[string][]string, error) {
	labelMap := make(map[string][]string)
	labelCheck := make(map[string]struct{})

	for _, query := range q.QueryList {
		m, err := query.LabelMap()
		if err != nil {
			return nil, err
		}
		for key, values := range m {
			for _, value := range values {
				checkKey := key + ":" + value
				if _, ok := labelCheck[checkKey]; !ok {
					labelCheck[checkKey] = struct{}{}
					labelMap[key] = append(labelMap[key], value)
				}
			}
		}
	}

	return labelMap, nil
}

// StepParse 解析step
func StepParse(step string) time.Duration {
	if step == "" {
		return promql.GetDefaultStep()
	} else {
		dTmp, err := model.ParseDuration(step)
		stepDuration := time.Duration(dTmp)

		if err != nil {
			return promql.GetDefaultStep()
		}

		return stepDuration
	}
}

// AlignTime 开始时间根据时区对齐
func AlignTime(start, end time.Time, stepStr, timezone string) (time.Time, time.Time, time.Duration, string, error) {
	step := StepParse(stepStr)

	// 根据 timezone 来对齐开始时间
	newTimezone, newStart := function.TimeOffset(start, timezone, step)
	return newStart, end, step, newTimezone, nil
}

// GetMaxWindow 获取最大聚合时间
func (q *QueryTs) GetMaxWindow() (time.Duration, error) {
	var window time.Duration = 0
	for _, query := range q.QueryList {
		for _, agg := range query.AggregateMethodList {
			if agg.Window != "" {
				aw, err := agg.Window.Duration()
				if err != nil {
					return 0, err
				}

				if aw > window {
					window = aw
				}
			}
		}
	}
	return window, nil
}

func (q *QueryTs) ToQueryReference(ctx context.Context) (metadata.QueryReference, error) {
	queryReference := make(metadata.QueryReference)
	for _, query := range q.QueryList {
		// 时间复用
		query.Timezone = q.Timezone
		query.Start = q.Start
		query.End = q.End

		// 排序复用
		query.OrderBy = q.OrderBy

		// 如果 query.Step 不存在去外部统一的 step
		if query.Step == "" {
			query.Step = q.Step
		}
		if q.SpaceUid == "" {
			q.SpaceUid = metadata.GetUser(ctx).SpaceUID
		}

		if q.ResultTableOptions != nil {
			query.ResultTableOptions = q.ResultTableOptions
		}

		if q.Scroll != "" {
			query.Scroll = q.Scroll
		}

		// 复用字段配置，没有特殊配置的情况下使用公共配置
		if len(query.KeepColumns) == 0 && len(q.ResultColumns) != 0 {
			query.KeepColumns = q.ResultColumns
		}

		queryMetric, err := query.ToQueryMetric(ctx, q.SpaceUid)
		if err != nil {
			return nil, err
		}
		if _, ok := queryReference[query.ReferenceName]; !ok {
			queryReference[query.ReferenceName] = make([]*metadata.QueryMetric, 0)
		}

		queryReference[query.ReferenceName] = append(queryReference[query.ReferenceName], queryMetric)
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

	qry = q.QueryList[0]

	// 结构定义转换
	allConditions, err := qry.Conditions.AnalysisConditions()
	queryConditions := allConditions.MetaDataAllConditions()

	if err != nil {
		return nil, err
	}

	agg, err := qry.AggregateMethodList.ToQry(qry.Timezone)
	if err != nil {
		return nil, err
	}

	queryCM := &metadata.QueryClusterMetric{
		MetricName: qry.FieldName,
		Aggregates: agg,
		Conditions: queryConditions,
	}
	if qry.TimeAggregation.Function != "" {
		wDuration, err := qry.TimeAggregation.Window.Duration()
		if err != nil {
			return nil, errors.Errorf("TimeAggregation.Window(%v) format is invalid, %v", qry.TimeAggregation, err)
		}
		queryCM.TimeAggregation = metadata.TimeAggregation{
			Function:       qry.TimeAggregation.Function,
			WindowDuration: wDuration,
		}
	}
	span.Set("query-field", queryCM.MetricName)
	span.Set("query-aggr-methods", qry.AggregateMethodList)
	span.Set("query-conditions", queryCM.Conditions)
	span.Set("query-time-func", queryCM.TimeAggregation.Function)
	span.Set("query-time-window", queryCM.TimeAggregation.WindowDuration)
	return queryCM, nil
}

type PromExprOption struct {
	ReferenceNameMetric         map[string]string
	ReferenceNameLabelMatcher   map[string][]*labels.Matcher
	FunctionReplace             map[string]string
	IgnoreTimeAggregationEnable bool
}

func (q *QueryTs) ToPromQL(ctx context.Context) (promQLString string, checkErr error) {
	var (
		promExprOpt = &PromExprOption{
			ReferenceNameMetric:       make(map[string]string),
			ReferenceNameLabelMatcher: make(map[string][]*labels.Matcher),
		}
	)
	for _, ql := range q.QueryList {
		// 保留查询条件
		matcher, _, err := ql.Conditions.ToProm()
		if err != nil {
			checkErr = err
			return
		}
		promExprOpt.ReferenceNameLabelMatcher[ql.ReferenceName] = matcher

		router, err := ql.ToRouter()
		if err != nil {
			checkErr = err
			return
		}
		promExprOpt.ReferenceNameMetric[ql.ReferenceName] = router.RealMetricName()
	}

	promExpr, err := q.ToPromExpr(ctx, promExprOpt)
	if err != nil {
		checkErr = err
		return
	}

	return promExpr.String(), nil
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
		if query.Step == "" {
			query.Step = q.Step
		}
		if expr, err = query.ToPromExpr(ctx, promExprOpt); err != nil {
			return nil, err
		}

		// 表达式转换只支持一个 reference_name
		if _, ok := exprMap[query.ReferenceName]; !ok {
			exprMap[query.ReferenceName] = &PromExpr{
				Expr:       expr,
				Dimensions: nil,
				ctx:        ctx,
			}
		}
	}

	result, err = HandleExpr(exprMap, result)
	if err != nil {
		return nil, err
	}

	return result, nil
}

type TimeField struct {
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
	Unit string `json:"unit,omitempty"`
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
	// IsDomSampled 是否命中降采样算法
	IsDomSampled bool `json:"is_dom_sampled"`
	// ReferenceName 别名，用于表达式计算
	ReferenceName string `json:"reference_name,omitempty" example:"a"`
	// Dimensions promQL 使用维度
	Dimensions []string `json:"dimensions,omitempty" example:"bk_target_ip,bk_target_cloud_id"`
	// Limit 点数限制数量
	Limit int `json:"limit,omitempty" example:"0"`
	// From 翻页开启数字
	From int `json:"from,omitempty" example:"0"`
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

	// AlignInfluxdbResult 保留字段，无需配置，是否对齐influxdb的结果,该判断基于promql和influxdb查询原理的差异
	AlignInfluxdbResult bool `json:"-"`

	// OrderBy 排序字段列表，按顺序排序，负数代表倒序, ["_time", "-_time"]
	OrderBy OrderBy `json:"-,omitempty"`
	// Start 保留字段，会被外面的 Start 覆盖
	Start string `json:"-" swaggerignore:"true"`
	// End 保留字段，会被外面的 End 覆盖
	End string `json:"-" swaggerignore:"true"`
	// Step
	Step string `json:"step,omitempty" swaggerignore:"true"`
	// Timezone 时区，会被外面的 Timezone 覆盖
	Timezone string `json:"-" swaggerignore:"true"`

	// QueryString 关键字查询
	QueryString string `json:"query_string"`
	// IsPrefix 是否启用前缀匹配
	IsPrefix bool `json:"is_prefix"`

	// IsReference 是否使用非时间聚合查询
	IsReference bool `json:"-" swaggerignore:"true"`

	// ResultTableOptions
	ResultTableOptions metadata.ResultTableOptions `json:"-"`
	// Scroll
	Scroll string `json:"-"`
	// Collapse
	Collapse *metadata.Collapse `json:"collapse,omitempty"`
}

func (q *Query) LabelMap() (map[string][]string, error) {
	labelMap := make(map[string][]string)
	labelCheck := make(map[string]struct{})

	addLabel := func(key, value string) {
		if value == "" {
			return
		}

		checkKey := key + ":" + value
		if _, ok := labelCheck[checkKey]; !ok {
			labelCheck[checkKey] = struct{}{}
			labelMap[key] = append(labelMap[key], value)
		}
	}

	for _, condition := range q.Conditions.FieldList {
		for _, value := range condition.Value {
			if value != "" {
				isPositive, err := IsPositiveOperator(condition.Operator)
				if err != nil {
					return nil, err
				}
				if isPositive {
					addLabel(condition.DimensionName, value)
				}
			}
		}
	}

	if q.QueryString != "" {
		qLabelMap, err := querystring.LabelMap(q.QueryString)
		if err != nil {
			return nil, err
		}
		for key, values := range qLabelMap {
			for _, value := range values {
				addLabel(key, value)
			}
		}
	}

	return labelMap, nil
}

func (q *Query) ToRouter() (*Route, error) {
	router := &Route{
		dataSource: q.DataSource,
		metricName: q.FieldName,
	}
	router.db, router.measurement = q.TableID.Split()
	return router, nil
}

func (q *Query) Aggregates() (aggs metadata.Aggregates, err error) {
	if len(q.AggregateMethodList) == 0 {
		return
	}

	// 非时间聚合函数使用透传的方式
	if q.IsReference {
		aggs, err = q.AggregateMethodList.ToQry(q.Timezone)
		return
	}

	// PromQL 聚合方式需要找到 TimeAggregation 共同判断
	if q.TimeAggregation.Function == "" {
		return
	}

	// 只支持第一层级的将采样，所以时间聚合函数一定要在指标之后
	if q.TimeAggregation.NodeIndex > 2 {
		return
	}

	if len(q.AggregateMethodList) < 1 {
		return
	}

	am := q.AggregateMethodList[0]
	// 将采样不支持 without
	if am.Without {
		return
	}

	window, err := model.ParseDuration(string(q.TimeAggregation.Window))
	if err != nil {
		return
	}

	step, err := model.ParseDuration(q.Step)
	if err != nil {
		return
	}

	// 如果 step < window 则不进行降采样聚合处理,因为计算出来的数据不准确
	if step < window {
		return
	}

	if name, ok := domSampledFunc[am.Method+q.TimeAggregation.Function]; ok {
		agg := metadata.Aggregate{
			Name:       name,
			Field:      am.Field,
			Dimensions: append([]string{}, am.Dimensions...),
			Without:    am.Without,
			Window:     time.Duration(window),
			TimeZone:   q.Timezone,
			Args:       am.VArgsList,
		}
		aggs = append(aggs, agg)

		// 是否命中降采样计算
		q.IsDomSampled = true
	}

	return
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

	var aggregates metadata.Aggregates
	aggregates, err = q.Aggregates()
	if err != nil {
		return nil, err
	}

	allConditions, err := q.Conditions.AnalysisConditions()
	if err != nil {
		return nil, err
	}

	// 如果 DataSource 为空，则自动补充
	if q.DataSource == "" {
		q.DataSource = BkMonitor
	}

	// 如果是 BkSql 查询无需获取 tsdb 路由关系
	if q.DataSource == BkData {
		// 判断空间跟业务是否匹配
		isMatchBizID := func() bool {
			space := strings.Split(spaceUid, "__")
			if len(space) != 2 {
				return false
			}
			// 只允许业务下查询
			if space[0] != "bkcc" {
				return false
			}

			if !strings.HasPrefix(string(tableID), space[1]+"_") {
				return false
			}
			return true
		}()

		ff := metadata.GetBkDataTableIDCheck(ctx, string(tableID))
		metric.BkDataRequestInc(ctx, spaceUid, string(tableID), fmt.Sprintf("%v", isMatchBizID), fmt.Sprintf("%v", ff))

		// 特性开关是否，打开 bkdata tableid 校验
		if ff {
			// 增加 bkdata tableid 校验，只有业务开头的才有权限，防止越权
			if !isMatchBizID {
				return queryMetric, nil
			}
		}

		route, bkDataErr := MakeRouteFromTableID(q.TableID)
		if bkDataErr != nil {
			err = bkDataErr
			return nil, bkDataErr
		}

		query := &metadata.Query{
			StorageType:   consul.BkSqlStorageType,
			TableID:       string(tableID),
			DataSource:    q.DataSource,
			DB:            route.DB(),
			Measurement:   route.Measurement(),
			Field:         q.FieldName,
			MetricName:    metricName,
			Aggregates:    aggregates,
			AllConditions: allConditions.MetaDataAllConditions(),
			Size:          q.Limit,
			From:          q.From,
			Collapse:      q.Collapse,
		}

		if len(q.OrderBy) > 0 {
			query.Orders = make(metadata.Orders, 0, len(q.OrderBy))
			for _, o := range q.OrderBy {
				if len(o) == 0 {
					continue
				}

				asc := true
				name := o

				if strings.HasPrefix(name, "-") {
					asc = false
					name = name[1:]
				}

				query.Orders = append(query.Orders, metadata.Order{
					Name: name,
					Ast:  asc,
				})
			}
		}

		metadata.GetQueryParams(ctx).SetStorageType(query.StorageType)

		// 配置别名
		query.ConfigureAlias(ctx)

		queryMetric.QueryList = []*metadata.Query{query}
		return queryMetric, nil
	}

	isSkipField := false
	if metricName == "" || q.DataSource == BkLog || q.DataSource == BkApm {
		isSkipField = true
	}

	tsDBs, err := GetTsDBList(ctx, &TsDBOption{
		SpaceUid:      spaceUid,
		TableID:       tableID,
		FieldName:     metricName,
		IsRegexp:      q.IsRegexp,
		AllConditions: allConditions,
		IsSkipSpace:   metadata.GetUser(ctx).IsSkipSpace(),
		IsSkipK8s:     metadata.GetQueryParams(ctx).IsSkipK8s,
		IsSkipField:   isSkipField,
	})
	if err != nil {
		return nil, err
	}

	queryMetric.QueryList = make([]*metadata.Query, 0, len(tsDBs))

	span.Set("tsdb-num", len(tsDBs))

	for _, tsDB := range tsDBs {
		query, buildErr := q.BuildMetadataQuery(ctx, tsDB, allConditions)
		if buildErr != nil {
			return nil, buildErr
		}

		query.Aggregates = aggregates

		// 针对 vmRt 不为空的情况，进行 vm 判定
		if query.VmRt != "" {
			isVmQuery := func() bool {
				dims := set.New[string]()
				for _, a := range q.AggregateMethodList {
					dims.Add(a.Dimensions...)
				}

				if query.CheckDruidQuery(ctx, dims) {
					return true
				}
				if metadata.GetMustVmQueryFeatureFlag(ctx, tsDB.TableID) {
					return true
				}

				return false
			}()

			if isVmQuery {
				query.StorageType = consul.VictoriaMetricsStorageType
			}
		}

		metadata.GetQueryParams(ctx).SetStorageType(query.StorageType)

		// 配置别名
		query.ConfigureAlias(ctx)

		queryMetric.QueryList = append(queryMetric.QueryList, query)
	}

	return queryMetric, nil
}

func (q *Query) BuildMetadataQuery(
	ctx context.Context,
	tsDB *queryMod.TsDBV2,
	queryConditions [][]ConditionField,
) (*metadata.Query, error) {
	var (
		field        string
		fields       []string
		measurement  string
		measurements []string

		whereList = promql.NewWhereList()
		query     = &metadata.Query{
			SegmentedEnable: tsDB.SegmentedEnable,
			OffsetInfo: metadata.OffSetInfo{
				Limit:   q.Limit,
				SOffSet: q.Soffset,
				SLimit:  q.Slimit,
			},
		}
		allCondition AllConditions
		err          error
	)

	ctx, span := trace.NewSpan(ctx, "build-metadata-query")
	defer span.End(&err)

	metricName := q.FieldName
	expandMetricNames := tsDB.ExpandMetricNames
	measurement = tsDB.Measurement
	if measurement != "" {
		measurements = []string{measurement}
	}

	span.Set("tsdb-table-id", tsDB.TableID)
	span.Set("tsdb-field-alias", tsDB.FieldAlias)
	span.Set("tsdb-measurement-type", tsDB.MeasurementType)
	span.Set("tsdb-filters", tsDB.Filters)
	span.Set("tsdb-data-label", tsDB.DataLabel)
	span.Set("tsdb-storage-type", tsDB.StorageType)
	span.Set("tsdb-storage-id", tsDB.StorageID)
	span.Set("tsdb-storage-cluster-records", tsDB.StorageClusterRecords)
	span.Set("tsdb-storage-name", tsDB.StorageName)
	span.Set("tsdb-cluster-name", tsDB.ClusterName)
	span.Set("tsdb-tag-keys", tsDB.TagsKey)
	span.Set("tsdb-vm-rt", tsDB.VmRt)
	span.Set("tsdb-db", tsDB.DB)
	span.Set("tsdb-measurements", measurements)
	span.Set("tsdb-time-field", tsDB.TimeField)
	span.Set("tsdb-need-add-time", tsDB.NeedAddTime)
	span.Set("tsdb-source-type", tsDB.SourceType)

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
						Operator:      ConditionEqual,
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

	query.StorageID = tsDB.StorageID
	query.StorageType = tsDB.StorageType

	_, startTime, endTime, err := function.QueryTimestamp(q.Start, q.End)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	// 通过过期时间判断查询的 storage
	start, end, _, timezone, err := AlignTime(startTime, endTime, q.Step, q.Timezone)
	if err != nil {
		log.Errorf(ctx, err.Error())
		return nil, err
	}

	query.StorageIDs, err = func() (storageIDs []string, err error) {
		if len(tsDB.StorageClusterRecords) == 0 {
			return
		}

		storageIDSet := set.New[string]()
		// 遍历 storageClusterRecords 记录，按照开启时间倒序
		for _, record := range tsDB.StorageClusterRecords {
			// 开始时间和结束时间分别扩 1h 预留查询量
			checkStart := start.Add(time.Hour * -1).Unix()
			checkEnd := end.Add(time.Hour * 1).Unix()

			// 开启时间小于结束时间则加入查询队列
			if record.EnableTime < checkEnd {
				storageIDSet.Add(record.StorageID)
			}

			// 开启时间小于开始时间，则退出该循环
			if record.EnableTime < checkStart {
				break
			}
		}

		return storageIDSet.ToArray(), nil
	}()
	if err != nil {
		return nil, err
	}

	// 兼容原逻辑，storageType 通过 storageMap 获取
	if query.StorageType == "" {
		log.Warnf(ctx, "storageType is empty with %s", tsDB.TableID)
		stg, _ := tsdb.GetStorage(query.StorageID)
		if stg != nil {
			query.StorageType = stg.Type
		}
	}

	if query.StorageType == "" {
		return nil, fmt.Errorf("storageType is empty with %v", query.StorageID)
	}

	query.Measurement = measurement
	query.Measurements = measurements
	query.Field = field
	query.Fields = fields
	if len(tsDB.FieldAlias) > 0 {
		query.FieldAlias = tsDB.FieldAlias
	}
	query.Timezone = timezone

	query.DataSource = q.DataSource
	query.TableID = tsDB.TableID
	query.DataLabel = tsDB.DataLabel
	query.MetricName = metricName
	query.ClusterName = tsDB.ClusterName
	query.TagsKey = tsDB.TagsKey
	query.DB = tsDB.DB
	query.VmRt = tsDB.VmRt
	query.StorageName = tsDB.StorageName
	query.TimeField = tsDB.TimeField
	query.NeedAddTime = tsDB.NeedAddTime
	query.SourceType = tsDB.SourceType

	query.AllConditions = allCondition.MetaDataAllConditions()
	query.Condition = whereList.String()
	query.VmCondition, query.VmConditionNum = allCondition.VMString(query.VmRt, vmMetric, q.IsRegexp)

	// 写入 ES 所需内容
	query.QueryString = q.QueryString
	query.IsPrefix = q.IsPrefix
	query.Source = q.KeepColumns

	query.Collapse = q.Collapse

	query.Scroll = q.Scroll
	query.ResultTableOptions = q.ResultTableOptions

	query.Size = q.Limit
	query.From = q.From

	if len(q.OrderBy) > 0 {
		query.Orders = q.OrderBy.Orders()
	}

	jsonString, _ := json.Marshal(query)
	span.Set("query-json", jsonString)

	return query, nil
}

func (q *Query) ToPromExpr(ctx context.Context, promExprOpt *PromExprOption) (parser.Expr, error) {
	var (
		metricName string
		err        error

		originalOffset time.Duration
		step           time.Duration
		dTmp           model.Duration

		result   parser.Expr
		matchers []*labels.Matcher
	)

	encodeFunc := metadata.GetFieldFormat(ctx).EncodeFunc()

	// 判断是否使用别名作为指标
	metricName = q.ReferenceName
	if promExprOpt != nil {
		// 忽略时间聚合函数开关
		if promExprOpt.IgnoreTimeAggregationEnable {
			// 是否需要忽略时间聚合函数
			if q.IsDomSampled {
				if q.AggregateMethodList != nil {
					// 由于表达式经过存储引擎的时间聚合（就是存储引擎已经计算过一次了），所以二次计算需要把计算移除，例如：sum(count_over_time(metric[1d])) => metric
					// 移除该计算会导致计算周期消失，导致开始时间不会根据计算周期来进行扩展，这里解决方案有两种：
					// 1. 使用 last_over_time 代替原时间聚合 函数，sum(count_over_time(metric[1d])) => last_over_time(metric[1d])，以扩展时间区间；
					// 2. 通过 window 更改 start 的时间，把开始时间往左边扩展一个计算周期，以满足计算范围，因为涉及到多指标，还需要遍历多指标之后，取最大的聚合时间，已知影响是：
					//	  1. 最终计算结果会导致多出一个起始点；
					//    2. 因为多指标共用一个最大的计算周期，会增加较小计算周期的数据量，例如：sum(count_over_time(metric[1d]))  + sum(count_over_time(metric[1m]))，都会使用 1d 来计算；
					// 这里选用方案一，使用 last_over_time 来扩展计算周期，如果因为增加 last_over_time 函数可能会引起的未知问题，需要考虑方案二；
					q.TimeAggregation.Function = LastOT
				}
			}
		}

		// 替换指标名
		if m, ok := promExprOpt.ReferenceNameMetric[q.ReferenceName]; ok {
			metricName = m
		}

		// 增加 Matchers
		for _, m := range promExprOpt.ReferenceNameLabelMatcher[q.ReferenceName] {
			m.Name = encodeFunc(m.Name)
			matchers = append(matchers, m)
		}

		// 替换函数名
		if nf, ok := promExprOpt.FunctionReplace[q.TimeAggregation.Function]; ok {
			q.TimeAggregation.Function = nf
		}

		for aggIdx, aggrVal := range q.AggregateMethodList {
			for mIdx, m := range aggrVal.Dimensions {
				q.AggregateMethodList[aggIdx].Dimensions[mIdx] = encodeFunc(m)
			}

			if nf, ok := promExprOpt.FunctionReplace[aggrVal.Method]; ok {
				q.AggregateMethodList[aggIdx].Method = nf
			}
		}
	}

	if q.AlignInfluxdbResult && q.TimeAggregation.Window != "" {
		dTmp, err = model.ParseDuration(q.Step)
		if err != nil {
			err = errors.WithMessagef(err, "step parse error")
			log.Errorf(ctx, err.Error())
			return nil, err
		}
		step = time.Duration(dTmp)
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
		metricMatcher, err := labels.NewMatcher(labels.MatchRegexp, labels.MetricName, metricName)
		if err != nil {
			return nil, err
		}
		matchers = append(matchers, metricMatcher)
		metricName = ""
	}

	result = &parser.VectorSelector{
		Name:          metricName,
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
