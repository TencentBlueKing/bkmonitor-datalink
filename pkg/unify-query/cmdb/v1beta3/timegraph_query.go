// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/pkg/errors"
	pl "github.com/prometheus/prometheus/promql"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/prometheus"
)

const (
	timeGraphQueryMaxRouting = 2
)

var timeGraphQueryTimeout = time.Minute

// timeGraphMatrixQuery 是图构建阶段唯一的外部查询边界。
// 生产环境保持为空并访问 VM；契约测试注入确定性的响应，跳过
// prepareTimeGraphVMQuery，主要覆盖 QueryTs 构造、建图和遍历，不覆盖路由解析、
// Expand 或 PromQL 渲染。
type timeGraphMatrixQuery func(context.Context, *structured.QueryTs) (pl.Matrix, error)

type timeGraphForceSourceInfoKey struct{}

type timeGraphQueryStageKey struct{}

func withTimeGraphForceSourceInfo(ctx context.Context) context.Context {
	return context.WithValue(ctx, timeGraphForceSourceInfoKey{}, true)
}

func timeGraphForceSourceInfo(ctx context.Context) bool {
	force, _ := ctx.Value(timeGraphForceSourceInfoKey{}).(bool)
	return force
}

func withTimeGraphQueryStage(ctx context.Context, stage string) context.Context {
	return context.WithValue(ctx, timeGraphQueryStageKey{}, stage)
}

func timeGraphQueryStage(ctx context.Context) string {
	stage, _ := ctx.Value(timeGraphQueryStageKey{}).(string)
	return stage
}

// timeGraphRelationKey 唯一标识一条待查询的关系边。
// 除了两端资源类型，还必须保留关系类型、指标、类别和方向，避免不同关系
// 因为资源类型相同而被合并。
type timeGraphRelationKey struct {
	source       cmdb.Resource
	target       cmdb.Resource
	relationType string
	metricName   string
	category     string
	direction    string
}

// newTimeGraphSubqueryContext 为每次 VM 子查询创建独立的 metadata 上下文。
// InitHashID 会创建新的 metadata ID，用户信息按 ID 存储，因此需要把用户副本
// 写入新 ID。SetUser 会修改传入的 User 对象，不能复用父请求的 User 指针，
// 以免子查询改写父请求。
func newTimeGraphSubqueryContext(ctx context.Context) context.Context {
	user := *metadata.GetUser(ctx)
	queryCtx := metadata.InitHashID(ctx)
	metadata.SetUser(queryCtx, &user)
	return queryCtx
}

type timeGraphVMQuery func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error)

type timeGraphVMQueryWithPartial func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, bool, error)

type timeGraphQueryReference func(context.Context, *structured.QueryTs) (metadata.QueryReference, error)

func (m *Model) prepareTimeGraphVMQuery(ctx context.Context, queryTs *structured.QueryTs) (string, *metadata.QueryParams, error) {
	var (
		queryRef metadata.QueryReference
		err      error
	)
	ctx, span := trace.NewSpan(ctx, "timegraph-prepare-vm-query")
	defer span.End(&err)
	if queryTs != nil {
		span.Set("query-space-uid", queryTs.SpaceUid)
		span.Set("query-start", queryTs.Start)
		span.Set("query-end", queryTs.End)
		span.Set("query-step", queryTs.Step)
		span.Set("query-count", len(queryTs.QueryList))
	}
	if m.timeGraphQueryReference != nil {
		queryRef, err = m.timeGraphQueryReference(ctx, queryTs)
	} else {
		queryRef, err = queryTs.ToQueryReference(ctx)
	}
	if err != nil {
		return "", nil, errors.WithMessage(err, "to query reference")
	}
	metadata.SetExpand(ctx, query.ToVmExpand(ctx, queryRef))

	expr, err := queryTs.ToPromExpr(ctx, nil)
	if err != nil {
		return "", nil, errors.WithMessage(err, "to prom expr")
	}
	span.Set("query-expression-length", len(expr.String()))
	return expr.String(), metadata.GetQueryParams(ctx), nil
}

// buildTimeGraphFromRelations 在一个查询时间窗口内，将关系指标装载到临时的
// 内存 TimeGraph 中。
func (m *Model) buildTimeGraphFromRelations(ctx context.Context, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, sourceInfo, sourceExpandInfo cmdb.Matcher, relations []cmdb.Relation, lookBackDelta string) (*TimeGraph, error) {
	return m.buildTimeGraphFromRelationsWithQueryAndRootRelations(ctx, spaceUID, start, end, step, sourceType, sourceInfo, sourceExpandInfo, nil, relations, lookBackDelta, nil)
}

func (m *Model) buildTimeGraphFromRelationsWithQuery(ctx context.Context, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, sourceInfo, sourceExpandInfo cmdb.Matcher, relations []cmdb.Relation, lookBackDelta string, matrixQuery timeGraphMatrixQuery) (*TimeGraph, error) {
	return m.buildTimeGraphFromRelationsWithQueryAndRootRelations(ctx, spaceUID, start, end, step, sourceType, sourceInfo, sourceExpandInfo, nil, relations, lookBackDelta, matrixQuery)
}

// buildTimeGraphFromRelationsWithRootRelations 只允许 rootRelations 对应的第一跳
// 关系使用 sourceInfo。该入口用于完整关系路径查询，避免把起点条件带到后续跳数。
func (m *Model) buildTimeGraphFromRelationsWithRootRelations(ctx context.Context, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, sourceInfo, sourceExpandInfo cmdb.Matcher, rootRelations map[timeGraphRelationKey]struct{}, relations []cmdb.Relation, lookBackDelta string) (*TimeGraph, error) {
	return m.buildTimeGraphFromRelationsWithQueryAndRootRelations(ctx, spaceUID, start, end, step, sourceType, sourceInfo, sourceExpandInfo, rootRelations, relations, lookBackDelta, nil)
}

func (m *Model) buildTimeGraphFromRelationsWithQueryAndRootRelations(ctx context.Context, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, sourceInfo, sourceExpandInfo cmdb.Matcher, rootRelations map[timeGraphRelationKey]struct{}, relations []cmdb.Relation, lookBackDelta string, matrixQuery timeGraphMatrixQuery) (graph *TimeGraph, err error) {
	return m.buildTimeGraph(ctx, spaceUID, start, end, step, sourceType, sourceInfo, sourceExpandInfo, rootRelations, relations, lookBackDelta, matrixQuery, nil)
}

func (m *Model) buildTimeGraph(ctx context.Context, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, sourceInfo, sourceExpandInfo cmdb.Matcher, rootRelations map[timeGraphRelationKey]struct{}, relations []cmdb.Relation, lookBackDelta string, matrixQuery timeGraphMatrixQuery, topologyGrid *TopologyGrid) (graph *TimeGraph, err error) {
	ctx, span := trace.NewSpan(ctx, "build-time-graph-from-relations")
	defer span.End(&err)
	started := time.Now()
	defer func() {
		metric.CMDBTimeGraphStageObserve(ctx, "build", metric.CMDBTimeGraphErrorResult(err), time.Since(started))
	}()
	span.Set("space-uid", spaceUID)
	span.Set("relation-count", len(relations))
	span.Set("query-start", start.Unix())
	span.Set("query-end", end.Unix())
	span.Set("query-step-seconds", step.Seconds())
	span.Set("source-type", sourceType)
	span.Set("source-matcher-count", len(sourceInfo))
	span.Set("source-expand-matcher-count", len(sourceExpandInfo))
	span.Set("root-relation-count", len(rootRelations))

	_, configSpan := trace.NewSpan(ctx, "timegraph-build-config")
	config := m.timeGraphConfig(spaceUID)
	configSpan.Set("space-uid", spaceUID)
	configSpan.Set("resource-config-count", len(config.Resource))
	configSpan.Set("relation-config-count", len(config.Relation))
	configSpan.Set("max-graph-nodes", config.MaxNodes)
	configSpan.Set("max-graph-edges", config.MaxEdges)
	configSpan.Set("max-graph-results", config.MaxResults)
	configSpan.Set("max-graph-node-infos", config.MaxNodeInfos)
	var configErr error
	configSpan.End(&configErr)
	_, storageSpan := trace.NewSpan(ctx, "timegraph-create-storage")
	var tg *TimeGraph
	if topologyGrid != nil {
		tg, err = newSharedTimeGraph(config, *topologyGrid)
		if err != nil {
			storageSpan.End(&err)
			return nil, err
		}
	} else {
		tg = NewTimeGraphWithConfig(config)
	}
	storageSpan.Set("shared", tg.shared != nil)
	storageSpan.End(&err)
	sourceInfoQueryCount, relationEdgeQueryCount, targetInfoQueryCount := 0, 0, 0
	loader := &timeGraphMatrixLoader{model: m, graph: tg, start: start, end: end, step: step, override: matrixQuery, topology: topologyGrid != nil}
	defer func() {
		observeTimeGraphBuild(ctx, span, tg, loader, started, err)
		span.Set("source-info-query-count", sourceInfoQueryCount)
		span.Set("relation-edge-query-count", relationEdgeQueryCount)
		span.Set("target-info-query-count", targetInfoQueryCount)
	}()
	if tg.shared != nil {
		span.Set("graph-storage-mode", "shared")
	} else {
		span.Set("graph-storage-mode", "time-buckets")
	}
	span.Set("graph-max-nodes", tg.maxNodes)
	span.Set("graph-max-edges", tg.maxEdges)
	span.Set("graph-max-results", tg.maxResults)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var lookBack time.Duration
	if lookBackDelta != "" {
		lookBack, err = time.ParseDuration(lookBackDelta)
		if err != nil {
			return nil, errors.WithMessage(err, "parse look back delta")
		}
		if lookBack <= 0 {
			return nil, errors.New("look back delta must be positive")
		}
	} else {
		lookBack = time.Duration(DefaultLookBackDelta) * time.Millisecond
	}
	span.Set("query-lookback-seconds", lookBack.Seconds())
	span.Set("graph-max-node-infos", tg.maxNodeInfos)

	// step 只控制 range 查询的采样间隔，lookBack 只控制 count_over_time 的
	// 回溯窗口；两者必须分开，否则稀疏的 range 查询会产生错误的采样结果。
	queryStep := step
	loader.lookBack = lookBack
	queryMatrix := loader.query

	if len(sourceExpandInfo) > 0 || len(relations) == 0 || timeGraphForceSourceInfo(ctx) {
		sourceInfoQueryCount, err = loader.addSourceInfo(ctx, spaceUID, sourceType, sourceInfo, sourceExpandInfo)
		if err != nil {
			return nil, err
		}
	}

	// targetMatchersByType 保存关系指标实际产生的目标节点身份。
	// targetIDsByTimestamp 进一步限制 info 指标：只有同一时间点确实出现在
	// 关系中的节点才能补充属性，避免凭空创建孤立节点。
	targetMatchersByType := make(map[cmdb.Resource]map[string]cmdb.Matcher)
	targetIDsByTimestamp := make(map[int64]map[cmdb.Resource]map[string]struct{})
	for _, relation := range relations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(relation.V) != 2 {
			continue
		}

		relationCtx := newTimeGraphSubqueryContext(ctx)
		metadata.GetQueryParams(relationCtx).SetIsSkipK8s(true)
		relationSourceInfo := sourceInfo
		// 仅候选路径的首跳关系下推 source matcher；非首跳关系先查询候选边，
		// 再由内存图遍历限定可达节点。
		isRootRelation := len(relation.V) == 2 && relation.V[0] == sourceType
		if rootRelations != nil {
			_, isRootRelation = rootRelations[timeGraphRelationKeyFor(relation)]
		}
		if !isRootRelation {
			relationSourceInfo = nil
		}
		relationQueryCtx, relationQuerySpan := trace.NewSpan(relationCtx, "timegraph-build-relation-query")
		relationQuerySpan.Set("source-type", relation.V[0])
		relationQuerySpan.Set("target-type", relation.V[1])
		relationQuerySpan.Set("relation-type", relation.RelationType)
		relationQuerySpan.Set("metric-name", relation.MetricName)
		relationQuerySpan.Set("root-relation", isRootRelation)
		queryTs, queryErr := tg.MakeQueryTsWithWindow(relationQueryCtx, spaceUID, relationSourceInfo, start, end, queryStep, lookBack, relation)
		relationQuerySpan.Set("query-generated", queryTs != nil)
		if queryTs != nil {
			relationQuerySpan.Set("query-count", len(queryTs.QueryList))
		}
		relationQuerySpan.End(&queryErr)
		if queryErr != nil {
			return nil, errors.WithMessagef(queryErr, "make query ts error for relation %v", relation)
		}
		if queryTs == nil {
			continue
		}

		relationEdgeQueryCount++
		matrix, _, queryErr := queryMatrix(withTimeGraphQueryStage(relationCtx, "relation-edge"), queryTs)
		if queryErr != nil {
			return nil, queryErr
		}
		if err = tg.applyRelationMatrix(relationCtx, matrix, relation, targetMatchersByType, targetIDsByTimestamp); err != nil {
			return nil, err
		}
	}

	if timeGraphTargetInfoShow(ctx) {
		targetInfoQueryCount, err = loader.addTargetInfo(ctx, spaceUID, targetMatchersByType, targetIDsByTimestamp)
		if err != nil {
			return nil, err
		}
	}

	return tg, nil
}

// timeGraphMatrixLoader 统一取数、网格校验和完整性状态，并按请求累计 Matrix 预算。
// 构图器只负责把已校验的序列写入所选存储。
type timeGraphMatrixLoader struct {
	model                  *Model
	graph                  *TimeGraph
	start, end             time.Time
	step, lookBack         time.Duration
	override               timeGraphMatrixQuery
	topology               bool
	queryCount, pointCount int
	queryDuration          time.Duration
}

func (loader *timeGraphMatrixLoader) query(queryCtx context.Context, queryTs *structured.QueryTs) (matrix pl.Matrix, partial bool, err error) {
	m, tg := loader.model, loader.graph
	start, end, queryStep, lookBack := loader.start, loader.end, loader.step, loader.lookBack
	instant, matrixQuery := start.Equal(end), loader.override

	queryCtx, span := trace.NewSpan(queryCtx, "timegraph-query-matrix")
	defer span.End(&err)
	if metadata.IsExactTimeGrid(queryCtx) {
		// 所有取数阶段共用请求网格，不让 QueryTs 按 step 向下移动起点。
		queryTs.NotTimeAlign = true
	}
	queryStarted := time.Now()
	stage := timeGraphQueryStage(queryCtx)
	defer func() {
		outcome := metric.CMDBTimeGraphErrorResult(err)
		if err == nil {
			if partial {
				outcome = metric.CMDBRelationResultPartial
			} else if len(matrix) == 0 {
				outcome = metric.CMDBRelationResultEmpty
			}
		}
		duration := time.Since(queryStarted)
		loader.queryDuration += duration
		span.Set("matrix-cumulative-point-count", loader.pointCount)
		SetTimeGraphLimitTrace(span, err)
		metric.CMDBTimeGraphStageObserve(queryCtx, stage, outcome, duration)
	}()
	loader.queryCount++
	span.Set("query-mode", map[bool]string{true: "instant", false: "range"}[instant])
	span.Set("query-source", "vm")
	wrapQueryErr := false
	if stage != "" {
		span.Set("query-stage", stage)
	}
	if queryTs != nil {
		span.Set("query-space-uid", queryTs.SpaceUid)
		span.Set("query-start", queryTs.Start)
		span.Set("query-end", queryTs.End)
		span.Set("query-step", queryTs.Step)
		span.Set("query-count", len(queryTs.QueryList))
	}

	var relationQueryParams *metadata.QueryParams
	switch {
	case matrixQuery != nil:
		span.Set("query-source", "test-matrix")
		matrix, err = matrixQuery(queryCtx, queryTs)
	case m.timeGraphVMQueryWithPartial != nil:
		span.Set("query-source", "test-vm-with-partial")
		var expr string
		expr, relationQueryParams, err = m.prepareTimeGraphVMQuery(queryCtx, queryTs)
		if err == nil {
			matrix, partial, err = m.timeGraphVMQueryWithPartial(
				queryCtx,
				queryTs,
				expr,
				instant,
				relationQueryParams.AlignStart,
				relationQueryParams.End,
				relationQueryParams.Step,
			)
		}
	case m.timeGraphVMQuery != nil:
		span.Set("query-source", "test-vm")
		var expr string
		expr, relationQueryParams, err = m.prepareTimeGraphVMQuery(queryCtx, queryTs)
		if err == nil {
			matrix, err = m.timeGraphVMQuery(
				queryCtx,
				queryTs,
				expr,
				instant,
				relationQueryParams.AlignStart,
				relationQueryParams.End,
				relationQueryParams.Step,
			)
		}
	default:
		wrapQueryErr = true
		var expr string
		expr, relationQueryParams, err = m.prepareTimeGraphVMQuery(queryCtx, queryTs)
		if err == nil {
			var instance tsdb.Instance
			if relationQueryParams.IsDirectQuery() {
				instance = prometheus.GetTsDbInstance(queryCtx, &metadata.Query{
					StorageType: metadata.VictoriaMetricsStorageType,
				})
				if instance == nil {
					err = fmt.Errorf("%s storage get error", metadata.VictoriaMetricsStorageType)
				}
			} else {
				instance = prometheus.NewInstance(queryCtx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
					QueryMaxRouting: timeGraphQueryMaxRouting,
					Timeout:         timeGraphQueryTimeout,
				}, lookBack, timeGraphQueryMaxRouting)
			}
			if err == nil && instant {
				var vector pl.Vector
				vector, partial, err = queryTimeGraphInstant(queryCtx, instance, expr, relationQueryParams.End)
				if err == nil {
					matrix = vectorToMatrix(vector)
				}
			} else if err == nil {
				matrix, partial, err = instance.DirectQueryRange(
					queryCtx,
					expr,
					relationQueryParams.AlignStart,
					relationQueryParams.End,
					relationQueryParams.Step,
				)
			}
		}
	}
	pointCount, err := loader.validateMatrix(queryCtx, matrix, err)
	// Prometheus 聚合后端通过子查询 metadata 报告部分成功，VM 则返回 partial 位。
	// 合并当前子查询的两种信号，让空结果的快照、指标和 trace 同样保留不完整状态。
	if status := metadata.GetStatus(queryCtx); status != nil {
		partial = partial || status.Code == metadata.QueryTsPartial
	}
	if err == nil && partial {
		partialStart, partialEnd, partialStep := start, end, queryStep
		if relationQueryParams != nil {
			partialStart, partialEnd, partialStep = relationQueryParams.AlignStart, relationQueryParams.End, relationQueryParams.Step
		}
		if instant {
			partialStart = partialEnd
		}
		tg.markPartialRange(partialStart, partialEnd, partialStep, "backend_partial")
	}
	span.Set("matrix-series-count", len(matrix))
	span.Set("matrix-point-count", pointCount)
	span.Set("matrix-partial", partial)
	if err == nil {
		metric.CMDBTimeGraphSizeObserve(queryCtx, stage, "series", len(matrix))
		metric.CMDBTimeGraphSizeObserve(queryCtx, stage, "points", pointCount)
	}
	if err != nil {
		if wrapQueryErr {
			if instant {
				err = errors.WithMessage(err, "direct query")
			} else {
				err = errors.WithMessage(err, "direct query range")
			}
		}
		return nil, partial, err
	}
	return matrix, partial, nil
}

func (loader *timeGraphMatrixLoader) addSourceInfo(ctx context.Context, spaceUID string, sourceType cmdb.Resource, sourceInfo, sourceExpandInfo cmdb.Matcher) (queryCount int, err error) {
	tg, queryMatrix := loader.graph, loader.query
	start, end, queryStep, lookBack := loader.start, loader.end, loader.step, loader.lookBack

	infoCtx := newTimeGraphSubqueryContext(ctx)
	metadata.GetQueryParams(infoCtx).SetIsSkipK8s(true)
	_, infoQuerySpan := trace.NewSpan(infoCtx, "timegraph-build-resource-info-query")
	infoQuerySpan.Set("query-kind", "source-info")
	infoQuerySpan.Set("resource-type", sourceType)
	queryTs, queryErr := tg.MakeResourceInfoQueryTsWithWindow(
		spaceUID, sourceType, sourceInfo, sourceExpandInfo, nil, start, end, queryStep, lookBack,
	)
	infoQuerySpan.Set("query-generated", queryTs != nil)
	if queryTs != nil {
		infoQuerySpan.Set("query-count", len(queryTs.QueryList))
	}
	infoQuerySpan.End(&queryErr)
	if queryErr != nil {
		return queryCount, errors.WithMessage(queryErr, "make source info query ts")
	}
	if queryTs != nil {
		queryCount++
		matrix, _, queryErr := queryMatrix(withTimeGraphQueryStage(infoCtx, "source-info"), queryTs)
		if queryErr != nil {
			return queryCount, errors.WithMessage(queryErr, "query source info relation")
		}
		if err = tg.applySourceMatrix(infoCtx, matrix, sourceType); err != nil {
			return queryCount, err
		}
	}

	return queryCount, nil
}

func (loader *timeGraphMatrixLoader) addTargetInfo(ctx context.Context, spaceUID string, targetMatchersByType map[cmdb.Resource]map[string]cmdb.Matcher, targetIDsByTimestamp map[int64]map[cmdb.Resource]map[string]struct{}) (queryCount int, err error) {
	tg, queryMatrix := loader.graph, loader.query
	start, end, queryStep, lookBack := loader.start, loader.end, loader.step, loader.lookBack

	for targetType, matchers := range targetMatchersByType {
		if err := ctx.Err(); err != nil {
			return queryCount, err
		}
		primaryMatchers := make([]cmdb.Matcher, 0, len(matchers))
		for _, matcher := range matchers {
			primaryMatchers = append(primaryMatchers, matcher)
		}
		infoCtx := newTimeGraphSubqueryContext(ctx)
		metadata.GetQueryParams(infoCtx).SetIsSkipK8s(true)
		_, infoQuerySpan := trace.NewSpan(infoCtx, "timegraph-build-resource-info-query")
		infoQuerySpan.Set("query-kind", "target-info")
		infoQuerySpan.Set("resource-type", targetType)
		infoQuerySpan.Set("primary-matcher-count", len(primaryMatchers))
		queryTs, queryErr := tg.MakeResourceInfoQueryTsWithWindow(
			spaceUID, targetType, nil, nil, primaryMatchers, start, end, queryStep, lookBack,
		)
		infoQuerySpan.Set("query-generated", queryTs != nil)
		if queryTs != nil {
			infoQuerySpan.Set("query-count", len(queryTs.QueryList))
		}
		infoQuerySpan.End(&queryErr)
		if queryErr != nil {
			return queryCount, errors.WithMessage(queryErr, "make target info query ts")
		}
		if queryTs == nil {
			continue
		}
		queryCount++
		matrix, _, queryErr := queryMatrix(withTimeGraphQueryStage(infoCtx, "target-info"), queryTs)
		if queryErr != nil {
			return queryCount, errors.WithMessage(queryErr, "query target info relation")
		}
		if err = tg.applyTargetMatrix(infoCtx, matrix, targetType, targetIDsByTimestamp); err != nil {
			return queryCount, err
		}
	}

	return queryCount, nil
}

func (loader *timeGraphMatrixLoader) validateMatrix(queryCtx context.Context, matrix pl.Matrix, err error) (pointCount int, validationErr error) {
	queryCtx, span := trace.NewSpan(queryCtx, "timegraph-validate-matrix")
	defer finishTimeGraphStage(queryCtx, span, "matrix-validation", time.Now(), &validationErr)
	defer func() {
		span.Set("matrix-series-count", len(matrix))
		span.Set("matrix-counted-point-count", pointCount)
		span.Set("matrix-cumulative-point-count", loader.pointCount)
	}()
	tg := loader.graph
	start, end, queryStep := loader.start, loader.end, loader.step
	if loader.topology && metadata.BackendResponseLimitExceeded(queryCtx) {
		err = &ResultLimitError{Reason: "max_response_bytes", Count: int(metadata.BackendResponseLimit(queryCtx)) + 1, Limit: int(metadata.BackendResponseLimit(queryCtx))}
	}
	if loader.topology && err == nil && len(matrix) > tg.maxNodes {
		err = &ResultLimitError{Reason: "max_topology_matrix_series", Count: len(matrix), Limit: tg.maxNodes}
	}
	pointCount = 0
	for _, series := range matrix {
		if err != nil {
			break
		}
		if err = queryCtx.Err(); err != nil {
			break
		}
		pointCount += len(series.Points)
		if loader.topology && err == nil {
			loader.pointCount += len(series.Points)
			limit := positiveTopologyLimit(MaxSharedTopologyMatrixPoints, 1000000)
			if loader.pointCount > limit {
				err = &ResultLimitError{Reason: "max_topology_matrix_points", Count: loader.pointCount, Limit: limit}
			}
		}
		if err == nil && metadata.IsExactTimeGrid(queryCtx) {
			for _, point := range series.Points {
				if point.T < start.UnixMilli() || point.T > end.UnixMilli() || (point.T-start.UnixMilli())%queryStep.Milliseconds() != 0 {
					err = errors.New("backend sample timestamp does not match topology time grid")
					break
				}
			}
		}
	}
	return pointCount, err
}

// queryTimeGraphInstant 优先保留后端完整性状态；完整拓扑不接受无法报告该状态的后端。
func queryTimeGraphInstant(ctx context.Context, instance tsdb.Instance, expr string, end time.Time) (pl.Vector, bool, error) {
	if statusAware, ok := instance.(tsdb.InstantQueryWithPartial); ok {
		return statusAware.DirectQueryWithPartial(ctx, expr, end)
	}
	if metadata.IsExactTimeGrid(ctx) {
		return nil, false, errors.New("instant backend does not report result completeness")
	}
	vector, err := instance.DirectQuery(ctx, expr, end)
	return vector, false, err
}

// buildRelationsFromPaths 从路径中提取相邻边，并对重复关系去重。
func (m *Model) buildRelationsFromPaths(paths [][]cmdb.Resource) []cmdb.Relation {
	return m.buildRelationsFromRelationPathsForNamespace("", cmdb.RelationPathsFromResourcePaths(paths))
}

func (m *Model) buildRelationsFromRelationPaths(paths []cmdb.RelationPath) []cmdb.Relation {
	return m.buildRelationsFromRelationPathsForNamespace("", paths)
}

func (m *Model) buildRelationsFromRelationPathsForNamespace(namespace string, paths []cmdb.RelationPath) []cmdb.Relation {
	seen := make(map[timeGraphRelationKey]struct{})
	relations := make([]cmdb.Relation, 0)
	for _, path := range paths {
		for i := 1; i < len(path.Steps); i++ {
			source := path.Steps[i-1].ResourceType
			target := path.Steps[i].ResourceType
			if source == "" || target == "" {
				continue
			}
			candidates := m.timeGraphRelationCandidates(namespace, source, target, path.Steps[i])
			for _, candidate := range candidates {
				key := timeGraphRelationKeyFor(candidate)
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				relations = append(relations, cmdb.Relation{
					V:            []cmdb.Resource{source, target},
					RelationType: candidate.RelationType,
					MetricName:   candidate.MetricName,
					Category:     candidate.Category,
					Direction:    candidate.Direction,
				})
			}
		}
	}
	return relations
}

func timeGraphRelationKeyFor(relation cmdb.Relation) timeGraphRelationKey {
	key := timeGraphRelationKey{relationType: relation.RelationType, metricName: relation.MetricName, category: relation.Category, direction: relation.Direction}
	if len(relation.V) == 2 {
		key.source = relation.V[0]
		key.target = relation.V[1]
	}
	return key
}

// rootTimeGraphRelationKeys 找出只用于首跳的关系。关系查询会去重，因此同时
// 出现在后续跳数（包括其他候选路径）的关系不能下推起点条件，否则会漏边。
func (m *Model) rootTimeGraphRelationKeys(namespace string, sourceType cmdb.Resource, paths []cmdb.RelationPath) map[timeGraphRelationKey]struct{} {
	result := make(map[timeGraphRelationKey]struct{})
	nonRoot := make(map[timeGraphRelationKey]struct{})
	for _, path := range paths {
		if len(path.Steps) < 2 || path.Steps[0].ResourceType != sourceType {
			continue
		}
		for i := 1; i < len(path.Steps); i++ {
			for _, relation := range m.timeGraphRelationCandidates(namespace, path.Steps[i-1].ResourceType, path.Steps[i].ResourceType, path.Steps[i]) {
				key := timeGraphRelationKeyFor(relation)
				if i == 1 {
					result[key] = struct{}{}
				} else {
					nonRoot[key] = struct{}{}
				}
			}
		}
	}
	for key := range nonRoot {
		delete(result, key)
	}
	return result
}

func (m *Model) timeGraphRelationCandidates(
	namespace string,
	source, target cmdb.Resource,
	step cmdb.RelationPathStep,
) []cmdb.Relation {
	cfg := m.timeGraphConfig(namespace)

	result := make([]cmdb.Relation, 0)
	configuredPair := false
	for _, configured := range cfg.Relation {
		if len(configured.Resources) != 2 {
			continue
		}
		if !sameResourcePair(configured.Resources, source, target) {
			continue
		}
		configuredPair = true
		if step.RelationType != "" && !sameRelationType(configured.RelationType, step.RelationType) {
			continue
		}
		// 静态单向关系只允许 schema 定义的方向，显式路径不能反转它。
		if configured.Category == string(RelationCategoryStatic) && configured.IsDirectional &&
			(configured.Resources[0] != source || step.Direction == string(DirectionInbound)) {
			continue
		}
		metricName := configured.MetricName
		if step.MetricName != "" {
			metricName = step.MetricName
		}
		candidate := cmdb.Relation{
			V:            []cmdb.Resource{source, target},
			RelationType: configured.RelationType,
			MetricName:   metricName,
			Category:     configured.Category,
			Direction:    step.Direction,
		}
		// 同类型动态边不能通过资源类型推断方向。未指定方向的资源路径与
		// 自动规划保持一致，分别查询 from_ 和 to_ 端点，避免丢失入向关系。
		if source == target && configured.Category == string(RelationCategoryDynamic) &&
			(step.Direction == "" || step.Direction == string(DirectionBoth)) {
			candidate.Direction = string(DirectionOutbound)
			result = append(result, candidate)
			candidate.Direction = string(DirectionInbound)
		}
		result = append(result, candidate)
	}
	if len(result) > 0 || configuredPair {
		// 已知关系不符合约束时不能通过默认指标 fallback 重新合成。
		return result
	}
	return []cmdb.Relation{{
		V:            []cmdb.Resource{source, target},
		RelationType: step.RelationType,
		MetricName:   step.MetricName,
		Category:     step.Category,
		Direction:    step.Direction,
	}}
}

func sameRelationType(left, right string) bool {
	if left == right {
		return true
	}
	if separator := strings.IndexByte(left, ':'); separator >= 0 {
		return left[separator+1:] == right
	}
	return false
}

func sameResourcePair(resources []cmdb.Resource, source, target cmdb.Resource) bool {
	if len(resources) != 2 {
		return false
	}
	return (resources[0] == source && resources[1] == target) || (resources[0] == target && resources[1] == source)
}

func (m *Model) resolveTimeGraphPaths(ctx context.Context, spaceUID string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, requested [][]cmdb.Resource) (paths []cmdb.RelationPath, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-resolve-paths")
	defer span.End(&err)
	span.Set("space-uid", spaceUID)
	span.Set("source-type", sourceType)
	span.Set("target-types", targetTypes)
	span.Set("requested-path-count", len(requested))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(requested) > 0 {
		resourcePaths := make([][]cmdb.Resource, 0, len(requested))
		known := make(map[ResourceType]struct{})
		provider := m.getSchemaProvider()
		for _, resourceType := range provider.ListResourceTypes(spaceUID) {
			known[resourceType] = struct{}{}
		}
		for _, path := range requested {
			normalizedPath := make([]cmdb.Resource, 0, len(path))
			for _, resourceType := range path {
				if resourceType != "" {
					normalizedPath = append(normalizedPath, resourceType)
				}
			}
			if len(normalizedPath) == 0 || normalizedPath[0] != sourceType {
				return nil, fmt.Errorf("invalid path_resource %v: source type must be %q", path, sourceType)
			}
			if len(normalizedPath) == 1 {
				if !containsResource(targetTypes, sourceType) {
					return nil, fmt.Errorf("invalid path_resource %v: target type does not match", path)
				}
				resourcePaths = append(resourcePaths, normalizedPath)
				continue
			}
			if !containsResource(targetTypes, normalizedPath[len(normalizedPath)-1]) {
				return nil, fmt.Errorf("invalid path_resource %v: target type does not match", path)
			}
			for _, resourceType := range normalizedPath {
				if _, ok := known[ResourceType(resourceType)]; !ok {
					return nil, fmt.Errorf("unknown path resource type %q", resourceType)
				}
			}
			resourcePaths = append(resourcePaths, normalizedPath)
		}
		if len(resourcePaths) > 0 {
			// 旧接口只提供资源类型路径，没有关系方向、类别和指标名，保持原有
			// 兼容转换；自动发现路径则在下方完整保留 PathFinder 的关系计划。
			paths = cmdb.RelationPathsFromResourcePaths(resourcePaths)
			span.Set("resolved-path-count", len(paths))
			span.Set("path-source", "request")
			return paths, nil
		}
	}

	pathFinder := NewPathFinder(
		WithSchemaProvider(m.getSchemaProvider()),
		WithNamespace(spaceUID),
		WithMaxHops(MaxAllowedHops),
	)
	for _, targetType := range targetTypes {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		graphPaths, err := pathFinder.FindAllPaths(ResourceType(sourceType), ResourceType(targetType), nil)
		if err != nil {
			continue
		}
		for _, graphPath := range graphPaths {
			paths = append(paths, resourcePathsToTimeGraphRelationPaths([]resourcePath{graphPath})...)
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("no paths found")
	}
	span.Set("resolved-path-count", len(paths))
	span.Set("path-source", "schema")
	return paths, nil
}

func containsResource(resources []cmdb.Resource, target cmdb.Resource) bool {
	for _, resource := range resources {
		if resource == target {
			return true
		}
	}
	return false
}

func (m *Model) queryTimeGraph(ctx context.Context, lookBackDelta, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher, sourceExpandInfo cmdb.Matcher) (results []cmdb.PathResourcesResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-resolve-and-query")
	defer span.End(&err)
	span.Set("space-uid", spaceUID)
	span.Set("source-type", sourceType)
	span.Set("target-types", targetTypes)
	span.Set("requested-path-count", len(pathResources))
	span.Set("source-matcher-count", len(matcher))
	span.Set("source-expand-matcher-count", len(sourceExpandInfo))
	paths, err := m.resolveTimeGraphPaths(ctx, spaceUID, sourceType, targetTypes, pathResources)
	if err != nil {
		return nil, err
	}
	span.Set("resolved-path-count", len(paths))
	results, err = m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, start, end, step, sourceType, targetTypes, paths, matcher, sourceExpandInfo)
	span.Set("result-count", len(results))
	return results, err
}

func (m *Model) queryRelationTimeGraph(ctx context.Context, lookBackDelta, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher, sourceExpandInfo cmdb.Matcher) (results []cmdb.PathResourcesResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-query-relation")
	defer span.End(&err)
	span.Set("look-back-delta", lookBackDelta)
	span.Set("source-matcher-count", len(matcher))
	span.Set("source-expand-matcher-count", len(sourceExpandInfo))
	span.Set("target-type-count", len(targetTypes))
	span.Set("relation-path-count", len(paths))
	if start.After(end) {
		return nil, errors.New("start_time must be less than or equal to end_time")
	}
	if step <= 0 {
		return nil, errors.New("step must be greater than 0")
	}
	if !start.Equal(end) {
		points, rangeErr := validateRangeBuckets(start.UnixMilli(), end.UnixMilli(), step.Milliseconds())
		if rangeErr != nil {
			return nil, rangeErr
		}
		span.Set("range-points", points)
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeGraphQueryTimeout)
	defer cancel()
	span.Set("space-uid", spaceUID)
	span.Set("source-type", sourceType)
	span.Set("target-types", targetTypes)
	span.Set("path-count", len(paths))
	span.Set("query-start", start.Unix())
	span.Set("query-end", end.Unix())
	span.Set("query-step-seconds", step.Seconds())
	for _, path := range paths {
		for index := 1; index < len(path.Steps); index++ {
			source, target := path.Steps[index-1].ResourceType, path.Steps[index].ResourceType
			if len(m.timeGraphRelationCandidates(spaceUID, source, target, path.Steps[index])) == 0 {
				return nil, fmt.Errorf("path relation %s -> %s is not allowed by schema", source, target)
			}
		}
	}
	relations := m.buildRelationsFromRelationPathsForNamespace(spaceUID, paths)
	span.Set("relation-count", len(relations))
	rootRelations := m.rootTimeGraphRelationKeys(spaceUID, sourceType, paths)

	tg, err := m.buildTimeGraphFromRelationsWithRootRelations(ctx, spaceUID, start, end, step, sourceType, matcher, sourceExpandInfo, rootRelations, relations, lookBackDelta)
	if err != nil {
		return nil, errors.WithMessage(err, "build time graph")
	}
	defer tg.Clean(ctx)

	results = make([]cmdb.PathResourcesResult, 0)
	pathResults, queryErr := tg.FindRelationPathResources(ctx, sourceType, targetTypes, matcher, paths)
	if queryErr != nil {
		return nil, errors.WithMessagef(queryErr, "find path from %s", sourceType)
	}
	for _, result := range pathResults {
		results = append(results, cmdb.PathResourcesResult{
			Timestamp:  result.Timestamp,
			TargetType: result.TargetType,
			Path:       result.Path,
		})
	}
	span.Set("raw-result-count", len(results))
	uniqueTimestamps := make(map[int64]struct{}, len(results))
	uniqueTargetTypes := make(map[cmdb.Resource]struct{}, len(results))
	pathNodeCount := 0
	for _, result := range results {
		uniqueTimestamps[result.Timestamp] = struct{}{}
		uniqueTargetTypes[result.TargetType] = struct{}{}
		pathNodeCount += len(result.Path)
	}
	span.Set("result-timestamp-count", len(uniqueTimestamps))
	span.Set("result-target-type-count", len(uniqueTargetTypes))
	span.Set("result-path-node-count", pathNodeCount)
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Timestamp == results[j].Timestamp {
			return results[i].TargetType < results[j].TargetType
		}
		return results[i].Timestamp < results[j].Timestamp
	})
	return results, nil
}

func (m *Model) queryPathResourcesWithSourceExpand(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher, sourceExpandInfo cmdb.Matcher) (results []cmdb.PathResourcesResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-query-path-resources")
	defer span.End(&err)
	span.Set("query-mode", "instant")
	span.Set("space-uid", spaceUID)
	span.Set("source-type", sourceType)
	span.Set("target-types", targetTypes)
	span.Set("requested-path-count", len(pathResources))
	span.Set("source-matcher-count", len(matcher))
	span.Set("source-expand-matcher-count", len(sourceExpandInfo))
	if spaceUID == "" {
		return nil, errors.New("space uid is empty")
	}
	if timestamp == "" {
		return nil, errors.New("timestamp is empty")
	}
	if sourceType == "" {
		return nil, errors.New("source type is empty")
	}
	if len(targetTypes) == 0 {
		return nil, errors.New("target types is empty")
	}
	timestampValue, err := parseTimestamp(timestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse timestamp")
	}
	results, err = m.queryTimeGraph(ctx, lookBackDelta, spaceUID, time.UnixMilli(timestampValue), time.UnixMilli(timestampValue), 5*time.Minute, sourceType, targetTypes, pathResources, matcher, sourceExpandInfo)
	span.Set("result-count", len(results))
	return results, err
}

func (m *Model) QueryPathResources(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	return m.queryPathResourcesWithSourceExpand(ctx, lookBackDelta, spaceUID, timestamp, sourceType, targetTypes, pathResources, matcher, nil)
}

func (m *Model) queryRelationPathResourcesWithSourceExpand(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher, sourceExpandInfo cmdb.Matcher) (results []cmdb.PathResourcesResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-query-relation-path-resources")
	defer span.End(&err)
	span.Set("query-mode", "instant")
	span.Set("space-uid", spaceUID)
	span.Set("source-type", sourceType)
	span.Set("target-types", targetTypes)
	span.Set("relation-path-count", len(paths))
	span.Set("source-matcher-count", len(matcher))
	span.Set("source-expand-matcher-count", len(sourceExpandInfo))
	if spaceUID == "" {
		return nil, errors.New("space uid is empty")
	}
	if timestamp == "" {
		return nil, errors.New("timestamp is empty")
	}
	if sourceType == "" {
		return nil, errors.New("source type is empty")
	}
	if len(targetTypes) == 0 {
		return nil, errors.New("target types is empty")
	}
	timestampValue, err := parseTimestamp(timestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse timestamp")
	}
	results, err = m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, time.UnixMilli(timestampValue), time.UnixMilli(timestampValue), 5*time.Minute, sourceType, targetTypes, paths, matcher, sourceExpandInfo)
	span.Set("result-count", len(results))
	return results, err
}

func (m *Model) QueryRelationPathResources(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	return m.queryRelationPathResourcesWithSourceExpand(ctx, lookBackDelta, spaceUID, timestamp, sourceType, targetTypes, paths, matcher, nil)
}

func (m *Model) queryPathResourcesRangeWithSourceExpand(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher, sourceExpandInfo cmdb.Matcher) (results []cmdb.PathResourcesResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-query-path-resources-range")
	defer span.End(&err)
	span.Set("query-mode", "range")
	span.Set("space-uid", spaceUID)
	span.Set("source-type", sourceType)
	span.Set("target-types", targetTypes)
	span.Set("requested-path-count", len(pathResources))
	span.Set("source-matcher-count", len(matcher))
	span.Set("source-expand-matcher-count", len(sourceExpandInfo))
	span.Set("requested-step", step)
	span.Set("requested-start", startTimestamp)
	span.Set("requested-end", endTimestamp)
	if spaceUID == "" {
		return nil, errors.New("space uid is empty")
	}
	if startTimestamp == "" || endTimestamp == "" {
		return nil, errors.New("timestamp is empty")
	}
	if sourceType == "" {
		return nil, errors.New("source type is empty")
	}
	if len(targetTypes) == 0 {
		return nil, errors.New("target types is empty")
	}
	start, err := parseTimestamp(startTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse start timestamp")
	}
	end, err := parseTimestamp(endTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse end timestamp")
	}
	stepDuration, err := parseStepDuration(step)
	if err != nil {
		return nil, errors.WithMessage(err, "parse step")
	}
	if stepDuration <= 0 {
		return nil, errors.New("step must be positive")
	}
	stepMs := stepDuration.Milliseconds()
	if _, err := validateRangeBuckets(start, end, stepMs); err != nil {
		return nil, err
	}
	results, err = m.queryTimeGraph(ctx, lookBackDelta, spaceUID, time.UnixMilli(start), time.UnixMilli(end), stepDuration, sourceType, targetTypes, pathResources, matcher, sourceExpandInfo)
	span.Set("result-count", len(results))
	return results, err
}

func (m *Model) QueryPathResourcesRange(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	return m.queryPathResourcesRangeWithSourceExpand(ctx, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp, sourceType, targetTypes, pathResources, matcher, nil)
}

func (m *Model) queryRelationPathResourcesRangeWithSourceExpand(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher, sourceExpandInfo cmdb.Matcher) (results []cmdb.PathResourcesResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-query-relation-path-resources-range")
	defer span.End(&err)
	span.Set("query-mode", "range")
	span.Set("space-uid", spaceUID)
	span.Set("source-type", sourceType)
	span.Set("target-types", targetTypes)
	span.Set("relation-path-count", len(paths))
	span.Set("source-matcher-count", len(matcher))
	span.Set("source-expand-matcher-count", len(sourceExpandInfo))
	span.Set("requested-step", step)
	span.Set("requested-start", startTimestamp)
	span.Set("requested-end", endTimestamp)
	if spaceUID == "" {
		return nil, errors.New("space uid is empty")
	}
	if startTimestamp == "" || endTimestamp == "" {
		return nil, errors.New("timestamp is empty")
	}
	if sourceType == "" {
		return nil, errors.New("source type is empty")
	}
	if len(targetTypes) == 0 {
		return nil, errors.New("target types is empty")
	}
	start, err := parseTimestamp(startTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse start timestamp")
	}
	end, err := parseTimestamp(endTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse end timestamp")
	}
	stepDuration, err := parseStepDuration(step)
	if err != nil {
		return nil, errors.WithMessage(err, "parse step")
	}
	if stepDuration <= 0 {
		return nil, errors.New("step must be positive")
	}
	stepMs := stepDuration.Milliseconds()
	if _, err := validateRangeBuckets(start, end, stepMs); err != nil {
		return nil, err
	}
	results, err = m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, time.UnixMilli(start), time.UnixMilli(end), stepDuration, sourceType, targetTypes, paths, matcher, sourceExpandInfo)
	span.Set("result-count", len(results))
	return results, err
}

func (m *Model) QueryRelationPathResourcesRange(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	return m.queryRelationPathResourcesRangeWithSourceExpand(ctx, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp, sourceType, targetTypes, paths, matcher, nil)
}

func vectorToMatrix(vector pl.Vector) pl.Matrix {
	var matrix pl.Matrix
	for _, sample := range vector {
		matrix = append(matrix, pl.Series{
			Metric: sample.Metric,
			Points: []pl.Point{sample.Point},
		})
	}
	return matrix
}
