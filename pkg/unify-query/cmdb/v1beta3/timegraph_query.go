// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"fmt"
	"sort"
	"strconv"
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
	queryCtx := metadata.WithQueryMetadata(metadata.InitHashID(ctx))
	metadata.SetUser(queryCtx, &user)
	return queryCtx
}

type timeGraphVMQuery func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error)

type timeGraphVMQueryWithPartial func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, bool, error)

type timeGraphQueryReference func(context.Context, *structured.QueryTs) (metadata.QueryReference, error)

// 缺少物理路由与已执行查询但没有序列不同，不能当作完整的空结果。
// 共享拓扑由取数器按已读且无数据处理并把整个计算网格标记为 partial（见
// relationNoRouteReason）；兼容入口仍收到该错误，由响应适配层降级为部分结果。
type relationNoPhysicalRouteError struct{ metric string }

func (e *relationNoPhysicalRouteError) Error() string {
	return fmt.Sprintf("required relation has no physical query route: %s", e.metric)
}

// relationNoRouteReason 返回共享拓扑可吸收的缺路由原因；兼容入口或其他错误返回空串。
func (loader *timeGraphMatrixLoader) relationNoRouteReason(err error) string {
	var routeErr *relationNoPhysicalRouteError
	if !loader.topology || !errors.As(err, &routeErr) {
		return ""
	}
	return "relation_no_physical_route:" + routeErr.metric
}

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
	if timeGraphQueryStage(ctx) == "relation-edge" && queryRef.Count() == 0 {
		return "", nil, &relationNoPhysicalRouteError{metric: timeGraphLoadMetricName(queryTs)}
	}
	if timeGraphQueryStage(ctx) == "source-info" && queryRef.Count() == 0 {
		metadata.SetStatus(ctx, metadata.QueryTsPartial, "source_info_no_route")
	}
	expr, err := queryTs.ToPromExpr(ctx, nil)
	if err != nil {
		return "", nil, errors.WithMessage(err, "to prom expr")
	}
	params := metadata.GetQueryParams(ctx)
	if params.IsDirectQuery() {
		var vmExpand *metadata.VmExpand
		vmExpand, err = query.ToVmExpandForExpression(ctx, queryRef, expr)
		if err != nil {
			return "", nil, err
		}
		metadata.SetExpand(ctx, vmExpand)
	} else {
		metadata.SetExpand(ctx, query.ToVmExpand(ctx, queryRef))
	}
	statement := expr.String()
	span.Set("query-expression-length", len(statement))
	return statement, params, nil
}

func validateTimeGraphPrecision(start, end time.Time, step time.Duration) error {
	// QueryTs 和 VM 网关以整数秒传递时间戳与步长。
	if start.Nanosecond() != 0 || end.Nanosecond() != 0 {
		return errors.New("TimeGraph timestamp must have whole-second precision")
	}
	if step < time.Second || step%time.Second != 0 {
		return errors.New("TimeGraph step must be a positive whole number of seconds")
	}
	return nil
}

func (m *Model) buildTimeGraph(ctx context.Context, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, sourceInfo, sourceExpandInfo cmdb.Matcher, rootRelations map[timeGraphRelationKey]struct{}, relations []cmdb.Relation, lookBackDelta string, matrixQuery timeGraphMatrixQuery, topologyGrid *TopologyGrid) (graph *TimeGraph, err error) {
	ctx = metadata.WithQueryMetadata(ctx)
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
	var configErr error
	configSpan.End(&configErr)
	_, storageSpan := trace.NewSpan(ctx, "timegraph-create-storage")
	var tg *TimeGraph
	if topologyGrid != nil {
		tg = newSharedTimeGraph(config, *topologyGrid)
	} else {
		tg = NewTimeGraphWithConfig(config)
	}
	storageSpan.Set("shared", tg.shared != nil)
	var storageErr error
	storageSpan.End(&storageErr)
	// 成功时由调用方持有图；失败时必须释放已构建的存储。
	// 先注册清理，再注册构建观察器，使失败计数在清理前可见。
	defer func() {
		if err != nil {
			tg.Clean(ctx)
		}
	}()

	sourceInfoQueryCount, relationEdgeQueryCount, targetInfoQueryCount := 0, 0, 0
	loader := &timeGraphMatrixLoader{model: m, graph: tg, start: start, end: end, step: step, override: matrixQuery, topology: topologyGrid != nil}
	defer func() { loader.lastMatrix = timeGraphMatrixSlot{} }()
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

	// step 只控制 range 查询的采样间隔，lookBack 只控制 count_over_time 的
	// 回溯窗口；两者必须分开，否则稀疏的 range 查询会产生错误的采样结果。
	queryStep := step
	loader.lookBack = lookBack

	// The legacy root-only query has no expression without an Info projection.
	if timeGraphLegacyCompatibility(ctx) && len(relations) == 0 &&
		(!timeGraphTargetInfoShow(ctx) || len(tg.nodeBuilder.resourceInfoFields(sourceType)) == 0) {
		return nil, errors.New("query ts to prom expr error: 表达式配置不能为空")
	}
	if len(sourceExpandInfo) > 0 || (topologyGrid == nil && (len(relations) == 0 || timeGraphForceSourceInfo(ctx))) {
		if tg.shared != nil && len(sourceExpandInfo) > 0 {
			tg.shared.sourceInfoBits = make(map[uint64]timeBitmap)
		}
		sourceInfoQueryCount, err = loader.addSourceInfo(ctx, spaceUID, sourceType, sourceInfo, sourceExpandInfo)
		if err != nil {
			return nil, err
		}
	}

	if plan, ok := ctx.Value(timeGraphFetchPlanKey{}).(timeGraphFetchPlan); topologyGrid != nil && ok {
		relationEdgeQueryCount, err = loader.loadSelectedTopology(ctx, spaceUID, sourceType, sourceInfo, rootRelations, relations, plan)
		if err != nil {
			return nil, err
		}
		return tg, nil
	}

	// targetMatchersByType 保存关系指标实际产生的目标节点身份。
	// targetIDsByTimestamp 进一步限制 info 指标：只有同一时间点确实出现在
	// 关系中的节点才能补充属性，避免凭空创建孤立节点。
	targetMatchersByType := make(map[cmdb.Resource]map[string]cmdb.Matcher)
	targetIDsByTimestamp := make(map[int64]map[cmdb.Resource]map[string]struct{})
	loadRelation := func(relation cmdb.Relation, relationSourceInfo cmdb.Matcher, rootRelation bool) (queried, observed bool, loadErr error) {
		if err := ctx.Err(); err != nil {
			return false, false, err
		}
		if len(relation.V) != 2 {
			return false, false, nil
		}

		relationCtx := newTimeGraphSubqueryContext(ctx)
		metadata.GetQueryParams(relationCtx).SetIsSkipK8s(true)
		relationQueryCtx, relationQuerySpan := trace.NewSpan(relationCtx, "timegraph-build-relation-query")
		relationQuerySpan.Set("source-type", relation.V[0])
		relationQuerySpan.Set("target-type", relation.V[1])
		relationQuerySpan.Set("relation-type", relation.RelationType)
		relationQuerySpan.Set("metric-name", relation.MetricName)
		relationQuerySpan.Set("root-relation", rootRelation)
		queryTs, queryErr := tg.MakeQueryTsWithWindow(relationQueryCtx, spaceUID, relationSourceInfo, start, end, queryStep, lookBack, relation)
		relationQuerySpan.Set("query-generated", queryTs != nil)
		if queryTs != nil {
			relationQuerySpan.Set("query-count", len(queryTs.QueryList))
		}
		relationQuerySpan.End(&queryErr)
		if queryErr != nil {
			return false, false, errors.WithMessagef(queryErr, "make query ts error for relation %v", relation)
		}
		if queryTs == nil {
			return false, false, nil
		}

		relationEdgeQueryCount++
		loadErr = loader.queryAndApply(withTimeGraphQueryStage(relationCtx, "relation-edge"), queryTs, func(shardCtx context.Context, matrix pl.Matrix) error {
			for _, series := range matrix {
				if len(series.Points) > 0 {
					observed = true
					break
				}
			}
			return tg.applyRelationMatrix(shardCtx, matrix, relation, targetMatchersByType, targetIDsByTimestamp)
		})
		return true, observed, loadErr
	}

	if path, ok := ctx.Value(timeGraphLegacyPathKey{}).(cmdb.RelationPath); topologyGrid == nil && ok {
		relationEdgeQueryCount, err = loader.loadLegacyPath(ctx, spaceUID, path, sourceInfo, targetMatchersByType, targetIDsByTimestamp)
		if err != nil {
			return nil, err
		}
	} else {
		// 只有首跳探测覆盖所有可能观察到根的关系端点时才能用它判空。
		// 否则有向入边虽不能从根向外遍历，却仍可能让根出现在关系结果中。
		queriedRoots := make(map[timeGraphRelationKey]struct{})
		if topologyGrid != nil && topologyRootProbeCoversEndpoints(sourceType, relations) {
			rootObserved := false
			for _, relation := range relations {
				if len(relation.V) != 2 || relation.V[0] != sourceType {
					continue
				}
				queried, observed, loadErr := loadRelation(relation, sourceInfo, true)
				if loadErr != nil {
					return nil, loadErr
				}
				if queried {
					queriedRoots[timeGraphRelationKeyFor(relation)] = struct{}{}
				}
				rootObserved = rootObserved || observed
			}
			if !rootObserved && len(tg.partialTimes) == 0 {
				return tg, nil
			}
		}

		for _, relation := range relations {
			if len(relation.V) != 2 {
				continue
			}
			key := timeGraphRelationKeyFor(relation)
			if topologyGrid != nil {
				if _, queried := queriedRoots[key]; queried {
					// 资源类型成环时可能到达同类型的另一个实例，此关系仍需无根条件读取一次。
					if _, safe := rootRelations[key]; safe || len(sourceInfo) == 0 {
						continue
					}
				}
			}
			relationSourceInfo := sourceInfo
			isRootRelation := relation.V[0] == sourceType
			if rootRelations != nil {
				_, isRootRelation = rootRelations[key]
			}
			if !isRootRelation || topologyGrid != nil {
				relationSourceInfo = nil
			}
			if _, _, loadErr := loadRelation(relation, relationSourceInfo, isRootRelation && topologyGrid == nil); loadErr != nil {
				return nil, loadErr
			}
		}
	}

	if topologyGrid == nil && timeGraphTargetInfoShow(ctx) {
		var infoErr error
		targetInfoQueryCount, infoErr = loader.addTargetInfo(ctx, spaceUID, targetMatchersByType, targetIDsByTimestamp)
		if infoErr != nil {
			var limitErr *ResultLimitError
			var samplesErr pl.ErrTooManySamples
			var canceledErr pl.ErrQueryCanceled
			var timeoutErr pl.ErrQueryTimeout
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if errors.Is(infoErr, context.Canceled) || errors.Is(infoErr, context.DeadlineExceeded) ||
				errors.Is(infoErr, metadata.ErrBackendResponseTooLarge) || errors.As(infoErr, &limitErr) ||
				errors.As(infoErr, &samplesErr) || errors.As(infoErr, &canceledErr) || errors.As(infoErr, &timeoutErr) {
				return nil, infoErr
			}
			// 属性补全失败时保留关系结果，并向调用方标记不完整；错误细节留在 trace。
			tg.markPartialRange(start, end, step, "target_info_failed")
			span.Set("optional-target-info-failed", true)
		}
	}

	return tg, nil
}

// timeGraphMatrixLoader 统一取数、网格校验和完整性状态，并按请求累计 Matrix 规模。
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
	physicalQueryCount     int
	reusedQueryCount       int
	lastMatrix             timeGraphMatrixSlot
}

func (loader *timeGraphMatrixLoader) query(queryCtx context.Context, queryTs *structured.QueryTs) (pl.Matrix, bool, error) {
	return loader.queryWithQuality(queryCtx, queryTs, true)
}

func (loader *timeGraphMatrixLoader) queryWithQuality(queryCtx context.Context, queryTs *structured.QueryTs, applyQuality bool) (matrix pl.Matrix, partial bool, err error) {
	if metadata.BackendResponseLimit(queryCtx) > 0 {
		// 一次物理响应的聚合子查询共用拒收标记，防止响应超过分批边界被误判为 partial；
		// 不同身份分片各有标记，先前的大分片失败不会污染后续小分片。
		queryCtx = metadata.WithBackendResponseLimit(queryCtx, metadata.BackendResponseLimit(queryCtx))
	}
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
	loadMetricName := timeGraphLoadMetricName(queryTs)
	var loadSize metric.TimeGraphLoadSize
	reuseBefore := loader.reusedQueryCount
	physicalBefore := loader.physicalQueryCount
	span.Set("metric-name", loadMetricName)
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
		if loader.physicalQueryCount > physicalBefore {
			metric.CMDBTimeGraphLoadObserve(queryCtx, stage, outcome, duration, loadSize)
		}
		span.Set("matrix-reused", loader.reusedQueryCount != reuseBefore)
		span.Set("matrix-returned", loadSize.Returned)
		span.Set("matrix-returned-series", loadSize.Series)
		span.Set("matrix-returned-points", loadSize.Points)
		span.Set("matrix-label-bytes", loadSize.LabelBytes)
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
		loader.lastMatrix = timeGraphMatrixSlot{}
		loader.physicalQueryCount++
		matrix, err = matrixQuery(queryCtx, queryTs)
	case m.timeGraphVMQueryWithPartial != nil:
		span.Set("query-source", "test-vm-with-partial")
		var expr string
		expr, relationQueryParams, err = m.prepareTimeGraphVMQuery(queryCtx, queryTs)
		if err == nil {
			matrix, partial, err = loader.loadPrepared(queryCtx, queryTs, expr, relationQueryParams, func() (pl.Matrix, bool, error) {
				return m.timeGraphVMQueryWithPartial(
					queryCtx,
					queryTs,
					expr,
					instant,
					relationQueryParams.AlignStart,
					relationQueryParams.End,
					relationQueryParams.Step,
				)
			})
		}
	case m.timeGraphVMQuery != nil:
		span.Set("query-source", "test-vm")
		var expr string
		expr, relationQueryParams, err = m.prepareTimeGraphVMQuery(queryCtx, queryTs)
		if err == nil {
			matrix, partial, err = loader.loadPrepared(queryCtx, queryTs, expr, relationQueryParams, func() (pl.Matrix, bool, error) {
				value, queryErr := m.timeGraphVMQuery(
					queryCtx,
					queryTs,
					expr,
					instant,
					relationQueryParams.AlignStart,
					relationQueryParams.End,
					relationQueryParams.Step,
				)
				return value, false, queryErr
			})
		}
	default:
		wrapQueryErr = true
		var expr string
		expr, relationQueryParams, err = m.prepareTimeGraphVMQuery(queryCtx, queryTs)
		if err == nil {
			matrix, partial, err = loader.loadPrepared(queryCtx, queryTs, expr, relationQueryParams, func() (pl.Matrix, bool, error) {
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
				return matrix, partial, err
			})
		}
	}
	// 共享拓扑中关系缺少物理路由时不访问后端：该关系按已读且无数据处理，以它的原因把
	// 整个计算网格标记为 partial，其余关系照常读取。所有拓扑取数路径（首跳探测、取数组、
	// 前沿分批）都经过这里，返回成功也不会触发身份拆分重试。
	partialReason := "backend_partial"
	if reason := loader.relationNoRouteReason(err); reason != "" {
		matrix, partial, err = nil, true, nil
		partialReason = reason
		span.Set("relation-no-physical-route", true)
	}
	// 校验可能清空命名返回值；先记录已返回的 Matrix，超过分批边界的拒收也要计入输入成本。
	if err == nil && loader.reusedQueryCount == reuseBefore {
		loadSize = timeGraphReturnedMatrixSize(matrix)
	}
	pointCount, err := loader.validateMatrix(queryCtx, matrix, err)
	if err == nil && loader.topology {
		err = queryCtx.Err()
	}
	// Prometheus 聚合后端通过子查询 metadata 报告部分成功，VM 则返回 partial 位。
	// 合并当前子查询的两种信号，让空结果的快照、指标和 trace 同样保留不完整状态。
	if status := metadata.GetStatus(queryCtx); status != nil {
		partial = partial || status.Code == metadata.QueryTsPartial
		if status.Code == metadata.QueryTsPartial && status.Message == "source_info_no_route" {
			partialReason = status.Message
		}
	}
	if err == nil && partial && applyQuality {
		partialStart, partialEnd, partialStep := start, end, queryStep
		if relationQueryParams != nil {
			partialStart, partialEnd, partialStep = relationQueryParams.AlignStart, relationQueryParams.End, relationQueryParams.Step
		}
		if instant {
			partialStart = partialEnd
		}
		tg.markPartialRange(partialStart, partialEnd, partialStep, partialReason)
	}
	span.Set("matrix-series-count", len(matrix))
	span.Set("matrix-point-count", pointCount)
	span.Set("matrix-partial", partial)
	if err == nil {
		metric.CMDBTimeGraphSizeObserve(queryCtx, stage, "series", len(matrix))
		metric.CMDBTimeGraphSizeObserve(queryCtx, stage, "points", pointCount)
	}
	if err != nil {
		loader.lastMatrix = timeGraphMatrixSlot{}
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

// queryAndApply 读取一次并在写图完成前持有这份响应。单次后端响应超过分批边界时，
// 把精确网格按时间对半重读，直到单个评估点：各点按自己的窗口独立计算，两半不重叠，
// 依次写图与一次读取得到相同的图和质量状态。单个评估点仍过大时返回终态错误。
func (loader *timeGraphMatrixLoader) queryAndApply(ctx context.Context, queryTs *structured.QueryTs, apply func(context.Context, pl.Matrix) error) error {
	matrix, _, err := loader.query(ctx, queryTs)
	if err == nil {
		return apply(ctx, matrix)
	}
	if !isBackendResponseTooLarge(err) {
		return err
	}
	halves, ok := loader.splitQueryTime(ctx, queryTs)
	if !ok {
		return &UnsplittableResponseError{Metric: timeGraphLoadMetricName(queryTs), Limit: metadata.BackendResponseLimit(ctx), cause: err}
	}
	for _, half := range halves {
		// 独立子查询：被拒收的读取留下的质量状态不影响替代它的两半。
		if err := loader.queryAndApply(frontierSubqueryContext(ctx), half, apply); err != nil {
			return err
		}
	}
	return nil
}

// splitQueryTime 把一次读取在请求网格上对半拆成两个不重叠的时间段：前段 ⌈n/2⌉ 点，
// 后段沿用原终点（兼容区间的末段终点可以不在网格上）。只有精确网格的区间读取可拆；
// 即时读取或只剩一个评估点时不能再拆。
func (loader *timeGraphMatrixLoader) splitQueryTime(ctx context.Context, queryTs *structured.QueryTs) ([2]*structured.QueryTs, bool) {
	var halves [2]*structured.QueryTs
	stepSeconds := int64(loader.step / time.Second)
	if queryTs == nil || loader.start.Equal(loader.end) || !metadata.IsExactTimeGrid(ctx) || stepSeconds <= 0 || loader.step%time.Second != 0 {
		return halves, false
	}
	start, startErr := strconv.ParseInt(queryTs.Start, 10, 64)
	end, endErr := strconv.ParseInt(queryTs.End, 10, 64)
	if startErr != nil || endErr != nil || end < start {
		return halves, false
	}
	points := (end-start)/stepSeconds + 1
	if points < 2 {
		return halves, false
	}
	firstPoints := (points + 1) / 2
	bounds := [2][2]int64{{start, start + (firstPoints-1)*stepSeconds}, {start + firstPoints*stepSeconds, end}}
	for i, bound := range bounds {
		half := *queryTs
		half.QueryList = make([]*structured.Query, len(queryTs.QueryList))
		for j, item := range queryTs.QueryList {
			copied := *item
			half.QueryList[j] = &copied
		}
		half.Start, half.End = strconv.FormatInt(bound[0], 10), strconv.FormatInt(bound[1], 10)
		halves[i] = &half
	}
	return halves, true
}

func (loader *timeGraphMatrixLoader) addSourceInfo(ctx context.Context, spaceUID string, sourceType cmdb.Resource, sourceInfo, sourceExpandInfo cmdb.Matcher) (queryCount int, err error) {
	tg := loader.graph
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
		if err = loader.queryAndApply(withTimeGraphQueryStage(infoCtx, "source-info"), queryTs, func(shardCtx context.Context, matrix pl.Matrix) error {
			return tg.applySourceMatrix(shardCtx, matrix, sourceType)
		}); err != nil {
			return queryCount, errors.WithMessage(err, "query source info relation")
		}
	}

	return queryCount, nil
}

func (loader *timeGraphMatrixLoader) addTargetInfo(ctx context.Context, spaceUID string, targetMatchersByType map[cmdb.Resource]map[string]cmdb.Matcher, targetIDsByTimestamp map[int64]map[cmdb.Resource]map[string]struct{}) (queryCount int, err error) {
	tg := loader.graph
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
		if err = loader.queryAndApply(withTimeGraphQueryStage(infoCtx, "target-info"), queryTs, func(shardCtx context.Context, matrix pl.Matrix) error {
			return tg.applyTargetMatrix(shardCtx, matrix, targetType, targetIDsByTimestamp)
		}); err != nil {
			return queryCount, errors.WithMessage(err, "query target info relation")
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
	startMillis, endMillis, stepMillis := loader.start.UnixMilli(), loader.end.UnixMilli(), loader.step.Milliseconds()
	if metadata.BackendResponseLimitExceeded(queryCtx) {
		err = &ResultLimitError{Reason: "max_response_bytes", Count: int(metadata.BackendResponseLimit(queryCtx)) + 1, Limit: int(metadata.BackendResponseLimit(queryCtx))}
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
		loader.pointCount += len(series.Points)
		if err == nil && metadata.IsExactTimeGrid(queryCtx) {
			for _, point := range series.Points {
				if point.T < startMillis || point.T > endMillis || (point.T-startMillis)%stepMillis != 0 {
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

// QueryRelationTimeGraph executes validated, normalized relation query inputs
// and returns the targets reachable along the paths at each timestamp.
func (m *Model) QueryRelationTimeGraph(ctx context.Context, lookBackDelta, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher, sourceExpandInfo cmdb.Matcher) ([]RelationTarget, error) {
	results, _, err := m.queryRelationTargets(ctx, lookBackDelta, spaceUID, start, end, step, sourceType, targetTypes, paths, matcher, sourceExpandInfo)
	return results, err
}

// queryRelationTargets 同时返回所建时间分桶图的规模（各时刻边数与节点信息数之和），
// 供区间查询按已建图的规模调整分段。
func (m *Model) queryRelationTargets(ctx context.Context, lookBackDelta, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher, sourceExpandInfo cmdb.Matcher) (results []RelationTarget, graphPoints int, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-query-relation")
	defer span.End(&err)
	span.Set("look-back-delta", lookBackDelta)
	span.Set("source-matcher-count", len(matcher))
	span.Set("source-expand-matcher-count", len(sourceExpandInfo))
	span.Set("target-type-count", len(targetTypes))
	span.Set("relation-path-count", len(paths))
	if !start.Equal(end) {
		distance := uint64(end.UnixMilli()) - uint64(start.UnixMilli())
		span.Set("range-points", int(distance/uint64(step.Milliseconds()))+1)
	}
	var cancel context.CancelFunc
	ctx, cancel = withTimeGraphQueryContext(ctx)
	defer cancel()
	// 兼容入口与 topology 使用同一单次响应边界：超过边界的读取由取数器拆分重读，
	// 不只依赖网关的拒收。
	if metadata.BackendResponseLimit(ctx) <= 0 {
		ctx = metadata.WithBackendResponseLimit(ctx, timeGraphBackendResponseBytes)
	}
	span.Set("backend-response-byte-limit", metadata.BackendResponseLimit(ctx))
	span.Set("space-uid", spaceUID)
	span.Set("source-type", sourceType)
	span.Set("target-types", targetTypes)
	span.Set("path-count", len(paths))
	span.Set("query-start", start.Unix())
	span.Set("query-end", end.Unix())
	span.Set("query-step-seconds", step.Seconds())
	for _, path := range paths {
		// 构图前拒绝，避免为答不了的形状付完全部取数。
		if err := validateRelationTargetPath(path.Steps); err != nil {
			return nil, 0, err
		}
		for index := 1; index < len(path.Steps); index++ {
			source, target := path.Steps[index-1].ResourceType, path.Steps[index].ResourceType
			if len(m.timeGraphRelationCandidates(spaceUID, source, target, path.Steps[index])) == 0 {
				return nil, 0, fmt.Errorf("path relation %s -> %s is not allowed by schema", source, target)
			}
		}
	}
	relations := m.buildRelationsFromRelationPathsForNamespace(spaceUID, paths)
	span.Set("relation-count", len(relations))
	rootRelations := m.rootTimeGraphRelationKeys(spaceUID, sourceType, paths)
	if timeGraphLegacyCompatibility(ctx) && len(paths) == 1 && len(paths[0].Steps) > 1 {
		ctx = context.WithValue(ctx, timeGraphLegacyPathKey{}, paths[0])
	}

	tg, err := m.buildTimeGraph(ctx, spaceUID, start, end, step, sourceType, matcher, sourceExpandInfo, rootRelations, relations, lookBackDelta, nil, nil)
	if err != nil {
		if timeGraphLegacyCompatibility(ctx) {
			return nil, 0, err
		}
		return nil, 0, errors.WithMessage(err, "build time graph")
	}
	defer tg.Clean(ctx)

	results, queryErr := tg.FindRelationTargets(ctx, sourceType, targetTypes, matcher, paths)
	if queryErr != nil {
		return nil, 0, errors.WithMessagef(queryErr, "find targets from %s", sourceType)
	}
	if partial := tg.partialTimesCopy(); len(partial) > 0 {
		reasons := make(map[string]struct{})
		if status := metadata.GetStatus(ctx); status != nil && status.Code == metadata.QueryTsPartial {
			for _, reason := range strings.Split(status.Message, ";") {
				if reason != "" {
					reasons[reason] = struct{}{}
				}
			}
		}
		for _, reason := range partial {
			for _, single := range strings.Split(reason, ";") {
				if single != "" {
					reasons[single] = struct{}{}
				}
			}
		}
		ordered := make([]string, 0, len(reasons))
		for reason := range reasons {
			ordered = append(ordered, reason)
		}
		sort.Strings(ordered)
		metadata.SetStatus(ctx, metadata.QueryTsPartial, strings.Join(ordered, ";"))
	}
	span.Set("raw-result-count", len(results))
	uniqueTimestamps := make(map[int64]struct{}, len(results))
	uniqueTargetTypes := make(map[cmdb.Resource]struct{}, len(results))
	for _, result := range results {
		uniqueTimestamps[result.Timestamp] = struct{}{}
		uniqueTargetTypes[result.TargetType] = struct{}{}
	}
	span.Set("result-timestamp-count", len(uniqueTimestamps))
	span.Set("result-target-type-count", len(uniqueTargetTypes))
	// 时间分桶图按时刻计边和节点信息；Clean 在返回值求值后执行。
	return results, tg.edgeCount + tg.nodeInfoCount, nil
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

func withTimeGraphQueryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	ctx = metadata.WithStrictQueryFailures(ctx)
	return context.WithTimeout(ctx, timeGraphQueryTimeout)
}
