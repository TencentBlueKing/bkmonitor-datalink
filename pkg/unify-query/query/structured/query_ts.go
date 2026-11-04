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
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/prometheus/common/model"
	"github.com/prometheus/prometheus/model/labels"
	"github.com/prometheus/prometheus/promql/parser"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	queryMod "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const (
	// Error messages
	ErrUnknownOperatorMsg = "unknown operator: %s"

	NamedOutputsV1 = "named_outputs/v1"
)

var outputReferencePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// QueryOutput 声明 named_outputs/v1 中的一个命名输出。
type QueryOutput struct {
	// ReferenceName 输出引用名，在同一 output_list 中必须唯一。
	ReferenceName string `json:"reference_name" example:"A"`
	// Expression 输出表达式；非 legacy 输出仅允许 query reference identity 表达式。
	Expression string `json:"expression" example:"A"`
}

type QueryTs struct {
	// TsDBMap 查询路由匹配中的 tsDB 列表 key:reference_name
	TsDBMap map[string]TsDBs `json:"tsdb_map,omitempty"`
	// SpaceUid 空间ID
	SpaceUid string `json:"space_uid,omitempty"`
	// QueryList 查询实例
	QueryList []*Query `json:"query_list,omitempty"`
	// MetricMerge 表达式：支持所有PromQL语法
	MetricMerge string `json:"metric_merge,omitempty" example:"a"`
	// ResponseContract 显式选择命名多输出响应契约；为空时保持旧单输出行为。
	ResponseContract string `json:"response_contract,omitempty" example:"named_outputs/v1" enums:"named_outputs/v1"`
	// LegacyOutputRef 指向与 MetricMerge 等价的输出，供旧服务兼容降级。
	LegacyOutputRef string `json:"legacy_output_ref,omitempty" example:"C"`
	// OutputList 按请求顺序声明命名输出。
	OutputList []QueryOutput `json:"output_list,omitempty"`
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

	// Reference 查询开始时间是否需要对齐，
	// 例如：
	// true:  range: 10:03 - 10:23 window: 10m -> 10:03 - 10:10, 10:10 - 10:20, 10:20 - 10:23
	// false: range: 10:03 - 10:23 window: 10m -> 10:00 - 10:10, 10:10 - 10:20, 10:20 - 10:23
	Reference bool `json:"reference,omitempty"`

	// NotTimeAlign 查询开始时间和聚合是否需要对齐
	// 例如
	// true:  range: 10:03 - 10:23 window: 10m -> 10:03 - 10:13, 10:13 - 10:23
	// false: range: 10:03 - 10:23 window: 10m -> 10:00 - 10:10, 10:10 - 10:20, 10:20 - 10:23
	NotTimeAlign bool `json:"not_time_align"`

	// 增加公共限制
	// Limit 点数限制数量
	Limit int `json:"limit,omitempty" example:"0"`
	// From 翻页起始位置。Doris SearchAfter 在缺少稳定游标字段时会降级为 offset 分页。
	From int `json:"from,omitempty" example:"0"`

	// Scroll 是否启用 Scroll 查询
	Scroll string `json:"scroll,omitempty"`
	// SliceMax 最大切片数量
	SliceMax int `json:"slice_max,omitempty"`
	// IsMultiFrom 是否启用 MultiFrom 查询
	IsMultiFrom bool `json:"is_multi_from,omitempty"`
	// IsSearchAfter 是否启用 SearchAfter 查询。仅用于 /query/raw 原始查询：Elasticsearch 使用原生游标；Doris 优先使用 __unique_key__，缺少时尝试 dtEventTimeStamp、gseIndex、iterationIndex 组合游标，再缺少时降级为 offset 分页；不能与 scroll 同时使用。
	IsSearchAfter bool `json:"is_search_after,omitempty"`
	// ClearCache 是否强制清理已存在的缓存会话
	ClearCache bool `json:"clear_cache,omitempty"`

	ResultTableOptions metadata.ResultTableOptions `json:"result_table_options,omitempty"`

	// HighLight 是否开启高亮
	HighLight *metadata.HighLight `json:"highlight,omitempty"`

	// DryRun 是否启用 DryRun
	DryRun bool `json:"dry_run,omitempty"`

	// IsMergeDB 是否启用合并 db 特性
	IsMergeDB bool `json:"is_merge_db,omitempty"`

	// IsESBatch 是否启用 query_raw 的 ES 批量查询
	IsESBatch bool `json:"is_es_batch,omitempty"`

	// AddDimensions 额外添加的聚合维度，会与每个 function.dimensions 合并
	AddDimensions []string `json:"add_dimensions,omitempty"`
}

// ValidateNamedOutputs 在任何路由和存储 I/O 前校验命名多输出契约。
func (q *QueryTs) ValidateNamedOutputs(maxOutputs int) error {
	if q.ResponseContract == "" {
		if len(q.OutputList) == 0 && q.LegacyOutputRef == "" {
			return nil
		}
		return fmt.Errorf("response_contract is required when named output fields are present")
	}
	if q.ResponseContract != NamedOutputsV1 {
		return fmt.Errorf("unsupported response_contract: %s", q.ResponseContract)
	}
	if strings.TrimSpace(q.MetricMerge) == "" {
		return fmt.Errorf("metric_merge is required")
	}
	if strings.TrimSpace(q.LegacyOutputRef) == "" {
		return fmt.Errorf("legacy_output_ref is required")
	}
	if len(q.OutputList) == 0 || len(q.OutputList) > maxOutputs {
		return fmt.Errorf("output_list length must be between 1 and %d", maxOutputs)
	}

	queryReferences := make(map[string]struct{}, len(q.QueryList))
	for _, query := range q.QueryList {
		if query == nil {
			return fmt.Errorf("query_list contains nil query")
		}
		queryReferences[query.ReferenceName] = struct{}{}
	}
	seen := make(map[string]struct{}, len(q.OutputList))
	legacyFound := false
	for _, output := range q.OutputList {
		if !outputReferencePattern.MatchString(output.ReferenceName) {
			return fmt.Errorf("invalid output reference: %s", output.ReferenceName)
		}
		if _, ok := seen[output.ReferenceName]; ok {
			return fmt.Errorf("duplicate output reference: %s", output.ReferenceName)
		}
		seen[output.ReferenceName] = struct{}{}
		legacyFound = legacyFound || output.ReferenceName == q.LegacyOutputRef
	}
	if !legacyFound {
		return fmt.Errorf("legacy_output_ref %s is missing from output_list", q.LegacyOutputRef)
	}

	metricMerge, err := parser.ParseExpr(q.MetricMerge)
	if err != nil {
		return fmt.Errorf("metric_merge expression is invalid: %w", err)
	}
	for _, output := range q.OutputList {
		expr, parseErr := parser.ParseExpr(output.Expression)
		if parseErr != nil {
			return fmt.Errorf("output %s expression is invalid: %w", output.ReferenceName, parseErr)
		}
		if output.ReferenceName == q.LegacyOutputRef {
			if expr.String() != metricMerge.String() {
				return fmt.Errorf("legacy output expression must be equivalent to metric_merge")
			}
			continue
		}

		selector, ok := expr.(*parser.VectorSelector)
		if !ok || selector.Name == "" || len(selector.LabelMatchers) != 1 ||
			selector.OriginalOffset != 0 || selector.Offset != 0 ||
			selector.Timestamp != nil || selector.StartOrEnd != 0 {
			return fmt.Errorf("output %s must be a query reference identity expression", output.ReferenceName)
		}
		if _, ok = queryReferences[selector.Name]; !ok {
			return fmt.Errorf("output %s identity expression must reference query reference", output.ReferenceName)
		}
	}
	return nil
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

func parseLookBackDeltaDuration(s string) (time.Duration, error) {
	duration, err := model.ParseDuration(s)
	if err == nil {
		return time.Duration(duration), nil
	}
	return time.ParseDuration(s)
}

func (q *QueryTs) ToTime(ctx context.Context) error {
	unit, startTime, endTime, err := function.QueryTimestamp(q.Start, q.End)
	if err != nil {
		return err
	}

	timezone := q.Timezone
	reference := q.Reference
	alianStart := startTime

	step := StepParse(q.Step)

	// 如果关闭了对齐，则无需对齐开始时间
	if !q.NotTimeAlign {
		// 根据 timezone 来对齐开始时间
		alianStart = function.TimeOffset(startTime, timezone, step)
	}

	queryParams := metadata.GetQueryParams(ctx).SetTime(alianStart, startTime, endTime, step, unit, timezone).SetIsReference(reference)
	if q.LookBackDelta != "" {
		lookBackDelta, err := parseLookBackDeltaDuration(q.LookBackDelta)
		if err != nil {
			return err
		}
		queryParams.SetLookBackDelta(lookBackDelta)
	}
	return nil
}

// ToQueryReference 保留历史时间分段，生成查询列表。
func (q *QueryTs) ToQueryReference(ctx context.Context) (metadata.QueryReference, error) {
	plan, err := q.toQueryPlan(ctx, timeSeriesQuery)
	return plan.Queries, err
}

// toQueryPlan 继承请求参数，汇总各逻辑查询的执行列表、历史路由和规划统计。
func (q *QueryTs) toQueryPlan(ctx context.Context, mode queryMode) (QueryPlan, error) {
	// 优先解析 queryTs 里面的时间元素
	err := q.ToTime(ctx)
	if err != nil {
		return QueryPlan{}, err
	}

	astBranchCount := queryASTBranchCount(q.MetricMerge, len(q.QueryList))
	queryReference := make(metadata.QueryReference)
	routeReference := make(metadata.QueryReference)
	var stats QueryPlanStats
	for _, query := range q.QueryList {
		if query.ASTBranchCount == 0 {
			query.ASTBranchCount = astBranchCount
		}
		// 兼容 SaaS 命名（bk_data / bk_log_search / bk_apm）-> 内部命名（bkdata / bklog / bkapm）
		query.DataSource = normalizeDataSource(query.DataSource)

		// 时间复用
		query.Timezone = q.Timezone
		query.Start = q.Start
		query.End = q.End

		// 排序复用
		query.OrderBy = q.OrderBy

		// dry-run 复用
		if q.DryRun {
			query.DryRun = q.DryRun
		}

		// is_merge_db 复用
		if q.IsMergeDB {
			query.IsMergeDB = q.IsMergeDB
		}

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
			q.IsMultiFrom = false
		}

		if q.IsMultiFrom {
			q.From = 0
		}

		if query.From == 0 && q.From > 0 {
			query.From = q.From
		}

		if query.Limit == 0 && q.Limit > 0 {
			query.Limit = q.Limit
		}

		// 复用字段配置，没有特殊配置的情况下使用公共配置
		if len(query.KeepColumns) == 0 && len(q.ResultColumns) != 0 {
			query.KeepColumns = q.ResultColumns
		}

		// 应用 add_dimensions 到每个聚合方法
		if len(q.AddDimensions) > 0 {
			for i := range query.AggregateMethodList {
				query.AggregateMethodList[i].Dimensions = MergeDimensions(query.AggregateMethodList[i].Dimensions, q.AddDimensions)
			}
		}

		tsDBs := q.TsDBMap[query.ReferenceName]
		metricPlan, err := query.toQueryMetricPlan(ctx, q.SpaceUid, tsDBs, mode)
		if err != nil {
			return QueryPlan{}, err
		}
		if _, ok := queryReference[query.ReferenceName]; !ok {
			queryReference[query.ReferenceName] = make([]*metadata.QueryMetric, 0)
		}

		queryReference[query.ReferenceName] = append(queryReference[query.ReferenceName], metricPlan.queries)
		routeReference[query.ReferenceName] = append(routeReference[query.ReferenceName], metricPlan.routes)
		stats.ReusedTargets += metricPlan.stats.ReusedTargets
		stats.MergedQueries += metricPlan.stats.MergedQueries
	}

	metadata.SetQueryReference(ctx, queryReference)
	return QueryPlan{Queries: queryReference, Routes: routeReference, Stats: stats}, nil
}

func queryASTBranchCount(expression string, fallback int) int {
	expr, err := parser.ParseExpr(expression)
	if err != nil {
		return fallback
	}
	count := 0
	parser.Inspect(expr, func(node parser.Node, _ []parser.Node) error {
		if _, ok := node.(*parser.VectorSelector); ok {
			count++
		}
		return nil
	})
	if count == 0 {
		return fallback
	}
	return count
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
	promExprOpt := &PromExprOption{
		ReferenceNameMetric:       make(map[string]string),
		ReferenceNameLabelMatcher: make(map[string][]*labels.Matcher),
	}
	for _, ql := range q.QueryList {
		if len(q.AddDimensions) > 0 {
			for i := range ql.AggregateMethodList {
				ql.AggregateMethodList[i].Dimensions = MergeDimensions(ql.AggregateMethodList[i].Dimensions, q.AddDimensions)
			}
		}

		// 保留查询条件
		matcher, _, err := ql.Conditions.ToProm()
		if err != nil {
			checkErr = err
			return promQLString, checkErr
		}
		promExprOpt.ReferenceNameLabelMatcher[ql.ReferenceName] = matcher

		router, err := ql.ToRouter()
		if err != nil {
			checkErr = err
			return promQLString, checkErr
		}
		promExprOpt.ReferenceNameMetric[ql.ReferenceName] = router.RealMetricName()
	}

	promExpr, err := q.ToPromExpr(ctx, promExprOpt)
	if err != nil {
		checkErr = err
		return promQLString, checkErr
	}

	return promExpr.String(), nil
}

func (q *QueryTs) ToPromExpr(
	ctx context.Context,
	promExprOpt *PromExprOption,
) (parser.Expr, error) {
	return q.toPromExpr(ctx, q.MetricMerge, promExprOpt, false)
}

func (q *QueryTs) ToPromExprFor(
	ctx context.Context,
	expression string,
	promExprOpt *PromExprOption,
) (parser.Expr, error) {
	return q.toPromExpr(ctx, expression, promExprOpt, true)
}

func (q *QueryTs) toPromExpr(
	ctx context.Context,
	expression string,
	promExprOpt *PromExprOption,
	copyInputs bool,
) (parser.Expr, error) {
	var (
		err     error
		result  parser.Expr
		expr    parser.Expr
		exprMap = make(map[string]*PromExpr, len(q.QueryList))
	)
	if copyInputs {
		promExprOpt = clonePromExprOption(promExprOpt)
	}

	if expression == "" {
		return nil, metadata.NewMessage(
			metadata.MsgParserUnifyQuery,
			"表达式配置不能为空",
		).Error(ctx, err)
	}

	// 先解析表达式
	if result, err = parser.ParseExpr(expression); err != nil {
		return nil, metadata.NewMessage(
			metadata.MsgParserUnifyQuery,
			"表达式 %s 解析失败",
			expression,
		).Error(ctx, err)
	}

	// 获取指标查询的表达式
	for _, sourceQuery := range q.QueryList {
		if sourceQuery == nil {
			return nil, fmt.Errorf("query_list contains nil query")
		}
		query := sourceQuery
		if copyInputs {
			query = cloneQueryForPromExpr(sourceQuery)
		}
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

func cloneQueryForPromExpr(source *Query) *Query {
	query := *source
	query.AggregateMethodList = append(AggregateMethodList(nil), source.AggregateMethodList...)
	for index := range query.AggregateMethodList {
		method := &query.AggregateMethodList[index]
		method.Dimensions = append(Dimensions(nil), method.Dimensions...)
		method.VArgsList = append([]any(nil), method.VArgsList...)
		if method.ArgsList != nil {
			method.ArgsList = make(Args, len(source.AggregateMethodList[index].ArgsList))
			for key, value := range source.AggregateMethodList[index].ArgsList {
				method.ArgsList[key] = value
			}
		}
	}
	query.TimeAggregation.VargsList = append([]any(nil), source.TimeAggregation.VargsList...)
	return &query
}

func clonePromExprOption(source *PromExprOption) *PromExprOption {
	if source == nil {
		return nil
	}
	option := &PromExprOption{
		ReferenceNameMetric:         make(map[string]string, len(source.ReferenceNameMetric)),
		ReferenceNameLabelMatcher:   make(map[string][]*labels.Matcher, len(source.ReferenceNameLabelMatcher)),
		FunctionReplace:             make(map[string]string, len(source.FunctionReplace)),
		IgnoreTimeAggregationEnable: source.IgnoreTimeAggregationEnable,
	}
	for key, value := range source.ReferenceNameMetric {
		option.ReferenceNameMetric[key] = value
	}
	for key, matchers := range source.ReferenceNameLabelMatcher {
		clonedMatchers := make([]*labels.Matcher, 0, len(matchers))
		for _, matcher := range matchers {
			if matcher == nil {
				clonedMatchers = append(clonedMatchers, nil)
				continue
			}
			copyOfMatcher := *matcher
			clonedMatchers = append(clonedMatchers, &copyOfMatcher)
		}
		option.ReferenceNameLabelMatcher[key] = clonedMatchers
	}
	for key, value := range source.FunctionReplace {
		option.FunctionReplace[key] = value
	}
	return option
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
	// ExactTotal 显式请求 ES 返回精确命中总数（track_total_hits）；默认关闭
	ExactTotal bool `json:"exact_total,omitempty"`
	// Timestamp @-modifier 标记
	Timestamp *int64 `json:"timestamp,omitempty"`
	// StartOrEnd @-modifier 标记，start or end
	StartOrEnd parser.ItemType `json:"start_or_end,omitempty"`
	// VectorOffset
	VectorOffset time.Duration `json:"vector_offset,omitempty"`
	// 以下字段来自 PromQL AST，仅用于让存储路由预选对齐 selector 的真实覆盖时间。
	RouteSelectorOffset time.Duration `json:"-"`
	RouteSubqueryOffset time.Duration `json:"-"`
	RouteSubqueryRange  time.Duration `json:"-"`
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
	// TableIDConditions 表标签条件（Body table_id_conditions 与 PromQL __bk_query_label_selector_<维度> 写入此字段，用于路由选表）；AllConditions 形态，内层 AND、外层 OR。
	TableIDConditions AllConditions `json:"table_id_conditions,omitempty"`
	// KeepColumns 保留字段
	KeepColumns KeepColumns `json:"keep_columns,omitempty" swaggerignore:"true"`
	// ASTBranchCount records the selector branch count for observation only.
	ASTBranchCount int `json:"-" swaggerignore:"true"`

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
	// SQL doris sql 解析
	SQL string `json:"sql"`
	// IsPrefix 是否启用前缀匹配
	IsPrefix bool `json:"is_prefix"`

	// NotPromFunc 不使用 PromQL 的函数
	NotPromFunc bool `json:"-" swaggerignore:"true"`

	// ResultTableOptions
	ResultTableOptions metadata.ResultTableOptions `json:"-"`
	// Scroll
	Scroll   string `json:"-"`
	SliceMax int    `json:"-"`
	// DryRun
	DryRun    bool `json:"-"`
	IsMergeDB bool `json:"-"`
	// Collapse
	Collapse *metadata.Collapse `json:"collapse,omitempty"`
}

func (q *Query) ToRouter() (*Route, error) {
	router := &Route{
		dataSource: q.DataSource,
		metricName: q.FieldName,
	}
	router.db, router.measurement = q.TableID.Split()
	return router, nil
}

func (q *Query) windowDuration(window Window) time.Duration {
	if window == "" {
		return 0
	}
	duration, err := model.ParseDuration(string(window))
	if err != nil {
		return 0
	}
	return time.Duration(duration)
}

func (q *Query) selectorWindowDuration() time.Duration {
	var matrixRange time.Duration
	var subqueryRange time.Duration

	addWindow := func(window Window, isSubQuery bool) {
		duration := q.windowDuration(window)
		if duration <= 0 {
			return
		}
		if isSubQuery {
			subqueryRange += duration
			return
		}
		if duration > matrixRange {
			matrixRange = duration
		}
	}

	addWindow(q.TimeAggregation.Window, q.TimeAggregation.IsSubQuery)
	for _, aggregateMethod := range q.AggregateMethodList {
		addWindow(aggregateMethod.Window, aggregateMethod.IsSubQuery)
	}
	if q.RouteSubqueryRange > 0 {
		subqueryRange = q.RouteSubqueryRange
	}
	if subqueryRange > 0 {
		return subqueryRange + matrixRange
	}
	return matrixRange
}

func (q *Query) publicSignedOffsetDuration() time.Duration {
	if q.VectorOffset != 0 {
		if q.OffsetForward {
			return -q.VectorOffset
		}
		return q.VectorOffset
	}
	if q.Offset == "" {
		return 0
	}
	offsetText := q.Offset
	sign := time.Duration(1)
	if strings.HasPrefix(offsetText, "-") {
		sign = -1
		offsetText = strings.TrimPrefix(offsetText, "-")
	}
	offset, err := model.ParseDuration(offsetText)
	if err != nil {
		return 0
	}
	duration := sign * time.Duration(offset)
	if q.OffsetForward {
		return -duration
	}
	return duration
}

func (q *Query) routeOverlapDuration(lookBackDelta time.Duration) (backward, forward time.Duration) {
	// 普通 instant selector 使用 lookback；range/subquery selector 使用真实窗口，
	// subquery 窗口和内层 matrix range 需要相加，不能只取最大值。
	backward = q.selectorWindowDuration()
	if backward == 0 {
		backward = lookBackDelta
	}

	if q.RouteSelectorOffset != 0 || q.RouteSubqueryOffset != 0 || q.RouteSubqueryRange != 0 {
		offset := q.RouteSelectorOffset + q.RouteSubqueryOffset
		if offset < 0 {
			forward = -offset
		} else {
			backward += offset
		}
	} else {
		offset := q.publicSignedOffsetDuration()
		if offset < 0 {
			forward = -offset
		} else {
			backward += offset
		}
	}
	return backward, forward
}

func (q *Query) routeLookbackDuration(lookBackDelta time.Duration) time.Duration {
	backward, _ := q.routeOverlapDuration(lookBackDelta)
	return backward
}

func (q *Query) routeTimeRange(qp *metadata.QueryParams) (time.Time, time.Time) {
	// `@` modifier 会覆盖外层 range query 的 eval start/end，route 预选要跟随这个锚点。
	if qp == nil {
		return time.Time{}, time.Time{}
	}
	if q.Timestamp != nil {
		t := time.UnixMilli(*q.Timestamp)
		return t, t
	}
	switch q.StartOrEnd {
	case parser.START:
		return qp.Start, qp.Start
	case parser.END:
		return qp.End, qp.End
	default:
		return qp.Start, qp.End
	}
}

func (q *Query) Aggregates() (aggs metadata.Aggregates, err error) {
	if len(q.AggregateMethodList) == 0 {
		return aggs, err
	}

	// 非时间聚合函数使用透传的方式
	if q.NotPromFunc {
		aggs, err = q.AggregateMethodList.ToQry(q.Timezone)
		return aggs, err
	}

	// PromQL 聚合方式需要找到 TimeAggregation 共同判断
	if q.TimeAggregation.Function == "" {
		return aggs, err
	}

	// 只支持第一层级的将采样，所以时间聚合函数一定要在指标之后
	if q.TimeAggregation.NodeIndex > 2 {
		return aggs, err
	}

	if len(q.AggregateMethodList) < 1 {
		return aggs, err
	}

	am := q.AggregateMethodList[0]
	// 将采样不支持 without
	if am.Without {
		return aggs, err
	}

	window, err := model.ParseDuration(string(q.TimeAggregation.Window))
	if err != nil {
		return aggs, err
	}

	step, err := model.ParseDuration(q.Step)
	if err != nil {
		return aggs, err
	}

	// 如果 step < window 则不进行降采样聚合处理,因为计算出来的数据不准确
	if step < window {
		return aggs, err
	}

	key := fmt.Sprintf("%s%s", am.Method, q.TimeAggregation.Function)
	if name, ok := domSampledFunc[key]; ok {
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

	return aggs, err
}

// ToQueryMetric 通过 spaceUid 转换成可查询结构体
func (q *Query) ToQueryMetric(ctx context.Context, spaceUid string, tsDBs TsDBs) (*metadata.QueryMetric, error) {
	plan, err := q.toQueryMetricPlan(ctx, spaceUid, tsDBs, timeSeriesQuery)
	return plan.queries, err
}

// toQueryMetricPlan 解析一个逻辑查询；Raw 按存储目标执行，时序查询按历史时间分段执行。
func (q *Query) toQueryMetricPlan(ctx context.Context, spaceUid string, tsDBs TsDBs, mode queryMode) (queryMetricPlan, error) {
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
	plan := queryMetricPlan{queries: queryMetric, routes: queryMetric}
	if mode == rawQuery {
		plan.routes = &metadata.QueryMetric{ReferenceName: referenceName, MetricName: metricName}
	}

	var aggregates metadata.Aggregates
	aggregates, err = q.Aggregates()
	if err != nil {
		return queryMetricPlan{}, err
	}

	// 注入时区和时区偏移，用于聚合处理
	var timeZoneOffset int64
	qp := metadata.GetQueryParams(ctx)
	if qp.Timezone != "" && qp.Timezone != "UTC" {
		utcAlignStart := function.TimeOffset(qp.Start, "UTC", qp.Step)
		// 不同时区对齐时间的差值
		timeZoneOffset = qp.AlignStart.UnixMilli() - utcAlignStart.UnixMilli()
	}
	for idx, agg := range aggregates {
		if agg.Window > 0 {
			agg.TimeZone = qp.Timezone
			agg.TimeZoneOffset = timeZoneOffset
			aggregates[idx] = agg
		}
	}

	allConditions, err := q.Conditions.AnalysisConditions()
	if err != nil {
		return queryMetricPlan{}, err
	}

	// 如果 DataSource 为空，则自动补充
	if q.DataSource == "" {
		q.DataSource = BkMonitor
	}

	// 如果是 BkSql 查询无需获取 tsdb 路由关系
	if q.DataSource == BkData {
		var metric *metadata.QueryMetric
		metric, err = q.toBkDataQueryMetric(ctx, spaceUid, aggregates, allConditions)
		return queryMetricPlan{queries: metric, routes: metric}, err
	}

	isSkipField := false
	if metricName == "" || q.DataSource == BkLog || q.DataSource == BkApm {
		isSkipField = true
	}
	if len(tsDBs) == 0 {
		tsDBs, err = GetTsDBList(ctx, &TsDBOption{
			SpaceUid:          spaceUid,
			TableID:           tableID,
			FieldName:         metricName,
			IsRegexp:          q.IsRegexp,
			AllConditions:     allConditions,
			IsSkipSpace:       metadata.GetUser(ctx).IsSkipSpace(),
			IsSkipK8s:         metadata.GetQueryParams(ctx).IsSkipK8s,
			IsSkipField:       isSkipField,
			TableIDConditions: q.TableIDConditions,
		})
		if err != nil {
			return queryMetricPlan{}, err
		}
	}

	queryMetric.QueryList = make([]*metadata.Query, 0, len(tsDBs))

	span.Set("tsdb-num", len(tsDBs))

	// 构建 query map 使得相同的 storage 可以进行合并查询
	queryMap := make(map[queryMergeTarget]*metadata.Query)
	var queryMergePairs []string

	// 查询路由匹配中的 tsDB 列表
	routeBaseStart, routeBaseEnd := q.routeTimeRange(qp)
	routeLookback, routeLookForward := q.routeOverlapDuration(qp.LookBackDelta)
	for _, tsDB := range tsDBs {
		storageRanges := tsDB.GetStorageIDRangesWithDirectionalOverlap(routeBaseStart, routeBaseEnd, routeLookback, routeLookForward)
		targets := make(map[rawStorageTarget]struct{})

		for _, storageRange := range storageRanges {
			query, buildErr := q.buildStorageQuery(ctx, tsDB, storageRange, aggregates, allConditions)
			if buildErr != nil {
				err = buildErr
				return queryMetricPlan{}, err
			}
			if query == nil {
				continue
			}

			routedQuery := *query
			if !storageRange.IsZero() {
				routedQuery.RouteStart = storageRange.Start
				routedQuery.RouteEnd = storageRange.End
				routedQuery.RouteEndInclusive = storageRange.EndInclusive
			}
			if !storageRange.QueryIsZero() {
				routedQuery.RouteQueryStart = storageRange.QueryStart
				routedQuery.RouteQueryEnd = storageRange.QueryEnd
			}
			if mode == rawQuery {
				plan.routes.QueryList = append(plan.routes.QueryList, &routedQuery)
				target := rawTarget(query)
				if _, exists := targets[target]; exists {
					plan.stats.ReusedTargets++
					continue
				}
				targets[target] = struct{}{}
			} else {
				query = &routedQuery
			}

			// 判断是否跳过合并操作
			if !query.GetMergeDBStatus() {
				queryMetric.QueryList = append(queryMetric.QueryList, query)
				continue
			}

			target := mergeTarget(query, mode)
			if oq, ok := queryMap[target]; ok {
				plan.stats.MergedQueries++
				queryMergePairs = append(queryMergePairs, fmt.Sprintf("%s->%s", oq.TableID, query.TableID))
				// merge db 只合并 DB 列表，主 query 固定选择 TableID 较小的记录，避免上游 tsDB 顺序变化导致结果不稳定。
				if query.TableID < oq.TableID {
					query.DirectTables = append(append([]metadata.PhysicalTable(nil), oq.DirectTables...), query.DirectTables...)
					query.DBs = append(oq.DBs, query.DB)
					queryMap[target] = query
				} else {
					oq.DirectTables = append(oq.DirectTables, query.DirectTables...)
					oq.DBs = append(oq.DBs, query.DB)
				}
			} else {
				query.DBs = []string{query.DB}
				queryMap[target] = query
			}
		}
	}

	if len(queryMergePairs) > 0 {
		span.Set("query_merge_pairs", queryMergePairs)
	}

	span.Set("query_map_length", len(queryMap))

	storageKeys := make([]queryMergeTarget, 0, len(queryMap))
	for key := range queryMap {
		storageKeys = append(storageKeys, key)
	}
	sort.Slice(storageKeys, func(i, j int) bool {
		return compareMergeTargets(storageKeys[i], storageKeys[j]) < 0
	})
	for _, key := range storageKeys {
		qry := queryMap[key]
		sort.Strings(qry.DBs)
		if opt := q.ResultTableOptions.GetOption(qry.TableUUID()); opt != nil {
			qry.ResultTableOption = opt
		}
		queryMetric.QueryList = append(queryMetric.QueryList, qry)
	}

	span.Set("query_metric_length", len(queryMetric.QueryList))

	return plan, nil
}

func (q *Query) queryCostRangeProfile() (hasRangeFunction bool, window, step time.Duration) {
	topLevelStep := queryCostDuration(q.Step)
	if topLevelStep <= 0 {
		topLevelStep = promql.GetDefaultStep()
	}
	step = topLevelStep

	type rangeCandidate struct {
		function   string
		window     Window
		isSubQuery bool
		step       string
	}
	candidates := make([]rangeCandidate, 0, len(q.AggregateMethodList)+1)
	candidates = append(candidates, rangeCandidate{
		function:   q.TimeAggregation.Function,
		window:     q.TimeAggregation.Window,
		isSubQuery: q.TimeAggregation.IsSubQuery,
		step:       q.TimeAggregation.Step,
	})
	for _, aggregate := range q.AggregateMethodList {
		candidates = append(candidates, rangeCandidate{
			function:   aggregate.Method,
			window:     aggregate.Window,
			isSubQuery: aggregate.IsSubQuery,
			step:       aggregate.Step,
		})
	}

	bestDensity := float64(-1)
	enclosingStep := topLevelStep
	// Range candidates are stored from inner to outer. Walk them backwards so
	// an enclosing subquery step is applied to every range function inside it.
	for i := len(candidates) - 1; i >= 0; i-- {
		candidate := candidates[i]
		if candidate.function == "" || candidate.window == "" {
			continue
		}
		hasRangeFunction = true
		parsedWindow := queryCostDuration(string(candidate.window))
		if parsedWindow <= 0 {
			continue
		}
		candidateStep := enclosingStep
		if candidate.isSubQuery {
			if candidate.step == "" || candidate.step == "0s" {
				candidateStep = promql.GetDefaultStep()
			} else {
				candidateStep = queryCostDuration(candidate.step)
			}
		}
		if candidateStep <= 0 {
			continue
		}
		density := float64(parsedWindow) / float64(candidateStep)
		if density > bestDensity {
			bestDensity = density
			window = parsedWindow
			step = candidateStep
		}
		if candidate.isSubQuery {
			enclosingStep = candidateStep
		}
	}
	return hasRangeFunction, window, step
}

func queryCostDuration(value string) time.Duration {
	duration, err := model.ParseDuration(value)
	if err != nil {
		return 0
	}
	return time.Duration(duration)
}

func (q *Query) BuildMetadataQuery(
	ctx context.Context,
	tsDB *queryMod.TsDBV2,
	queryConditions [][]ConditionField,
) *metadata.Query {
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

	if q.Offset != "" {
		dTmp, err := model.ParseDuration(q.Offset)
		if err != nil {
			metadata.NewMessage(
				metadata.MsgParserUnifyQuery,
				"offset %s 格式异常 %s",
				q.Offset, err.Error(),
			).Warn(ctx)
			return nil
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
		// 拼接指标
		query.MetricNames = function.GetRealMetricName(q.DataSource, tsDB.TableID, expandMetricNames...)
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
		// 拼接指标
		query.MetricNames = function.GetRealMetricName(q.DataSource, tsDB.TableID, expandMetricNames...)
	// 单指标单表，指标名为表名，值为指定字段 value
	case redis.BkSplitMeasurement:
		// measurement: usage, field: value  => usage_value
		measurement, measurements = metricName, expandMetricNames
		field, fields = promql.StaticField, []string{promql.StaticField}
		// 拼接指标
		query.MetricNames = function.GetRealMetricName("", "", expandMetricNames...)
	default:
		field, fields = metricName, expandMetricNames
		// 拼接指标
		query.MetricNames = function.GetRealMetricName(q.DataSource, tsDB.TableID, expandMetricNames...)
	}

	filterConditions := make([][]ConditionField, 0)
	satisfy, tKeys := judgeFilter(tsDB.Filters)
	// 满足压缩条件
	if satisfy {
		filterConditions = compressFilterCondition(tKeys, tsDB.Filters)
	} else {
		for _, filter := range tsDB.Filters {
			cond := make([]ConditionField, 0, len(filter))
			keys := make([]string, 0, len(filter))
			for key := range filter {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, k := range keys {
				v := filter[k]
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

	// 合并查询以及空间过滤条件到 condition 里面
	allCondition = MergeConditionField(queryConditions, filterConditions)

	if len(queryConditions) > 1 || len(filterConditions) > 1 {
		query.IsHasOr = true
	}

	query.StorageType = tsDB.StorageType
	query.Measurement = measurement
	query.Measurements = measurements
	query.Field = field
	query.Fields = fields
	if len(tsDB.FieldAlias) > 0 {
		query.FieldAlias = tsDB.FieldAlias
	}

	query.MeasurementType = tsDB.MeasurementType
	query.DataSource = q.DataSource
	query.TableID = tsDB.TableID
	query.DataLabel = tsDB.DataLabel
	query.ClusterName = tsDB.ClusterName
	query.TagsKey = tsDB.TagsKey
	query.DB = tsDB.DB
	query.PhysicalTables = tsDB.PhysicalTables
	query.VmRt = tsDB.VmRt
	query.CmdbLevelVmRt = tsDB.CmdbLevelVmRt
	query.StorageName = tsDB.StorageName
	query.TimeField = tsDB.TimeField
	query.NeedAddTime = tsDB.NeedAddTime
	query.SourceType = tsDB.SourceType

	query.AllConditions = allCondition.MetaDataAllConditions()
	query.Condition = whereList.String()
	query.VmCondition, query.VmConditionNum = allCondition.VMString(query.VmRt, vmMetric, q.IsRegexp)

	// 写入 ES / Doris 所需内容
	query.SQL = q.SQL
	query.QueryString = q.QueryString
	query.IsPrefix = q.IsPrefix
	query.Source = q.KeepColumns

	query.Collapse = q.Collapse

	query.Scroll = q.Scroll
	query.DryRun = q.DryRun
	query.IsMergeDB = q.IsMergeDB

	query.Size = q.Limit
	query.From = q.From
	query.ExactTotal = q.ExactTotal

	if len(q.OrderBy) > 0 {
		query.Orders = q.OrderBy.Orders()
	}

	jsonString, _ := json.Marshal(query)
	span.Set("query-json", jsonString)

	return query
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
	if encodeFunc == nil {
		encodeFunc = func(q string) string {
			return q
		}
	}

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
			// 如果是正则，证明里面存在特殊字符，所以不能转换
			if q.IsRegexp {
				metricName = m
			} else {
				metricName = encodeFunc(m)
			}
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
			return nil, metadata.NewMessage(
				metadata.MsgQueryTs,
				"step %s 解析失败",
				q.Step,
			).Error(ctx, err)
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

	routeMs, err := q.TableIDConditions.ToPromMatchers(ctx, encodeFunc)
	if err != nil {
		return nil, err
	}
	matchers = append(matchers, routeMs...)

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
				return nil, metadata.NewMessage(
					metadata.MsgQueryTs,
					"查询失败",
				).Error(ctx, err)
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
