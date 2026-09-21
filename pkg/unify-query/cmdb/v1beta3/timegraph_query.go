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
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/query"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
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
// 生产环境保持为空并访问 VM；契约测试注入确定性的 VM 响应，仍然走真实的
// 路径规划、查询构造和图遍历流程。
type timeGraphMatrixQuery func(context.Context, *structured.QueryTs) (pl.Matrix, error)

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
// InitHashID 会替换上下文中的用户元数据，因此必须先复制用户值，再把副本
// 写回子上下文，才能同时保留租户、空间和业务信息且不修改父请求。
func newTimeGraphSubqueryContext(ctx context.Context) context.Context {
	user := *metadata.GetUser(ctx)
	queryCtx := metadata.InitHashID(ctx)
	metadata.SetUser(queryCtx, &user)
	return queryCtx
}

type timeGraphVMQuery func(context.Context, *structured.QueryTs, string, bool, time.Time, time.Time, time.Duration) (pl.Matrix, error)

type timeGraphQueryReference func(context.Context, *structured.QueryTs) (metadata.QueryReference, error)

func (m *Model) prepareTimeGraphVMQuery(ctx context.Context, queryTs *structured.QueryTs) (string, *metadata.QueryParams, error) {
	var (
		queryRef metadata.QueryReference
		err      error
	)
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

func (m *Model) buildTimeGraphFromRelationsWithQueryAndRootRelations(ctx context.Context, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, sourceInfo, sourceExpandInfo cmdb.Matcher, rootRelations map[timeGraphRelationKey]struct{}, relations []cmdb.Relation, lookBackDelta string, matrixQuery timeGraphMatrixQuery) (*TimeGraph, error) {
	var err error
	ctx, span := trace.NewSpan(ctx, "build-time-graph-from-relations")
	defer span.End(&err)
	span.Set("space-uid", spaceUID)
	span.Set("relation-count", len(relations))
	span.Set("query-start", start.Unix())
	span.Set("query-end", end.Unix())
	span.Set("query-step-seconds", step.Seconds())

	tg := NewTimeGraphWithConfig(m.timeGraphConfig(spaceUID))
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

	instant := start.Equal(end)
	// step 只控制 range 查询的采样间隔，lookBack 只控制 count_over_time 的
	// 回溯窗口；两者必须分开，否则稀疏的 range 查询会产生错误的采样结果。
	queryStep := step
	queryMatrix := func(queryCtx context.Context, queryTs *structured.QueryTs) (pl.Matrix, error) {
		if matrixQuery != nil {
			return matrixQuery(queryCtx, queryTs)
		}
		if m.timeGraphVMQuery != nil {
			expr, relationQueryParams, queryErr := m.prepareTimeGraphVMQuery(queryCtx, queryTs)
			if queryErr != nil {
				return nil, queryErr
			}
			return m.timeGraphVMQuery(
				queryCtx,
				queryTs,
				expr,
				instant,
				relationQueryParams.AlignStart,
				relationQueryParams.End,
				relationQueryParams.Step,
			)
		}
		expr, relationQueryParams, queryErr := m.prepareTimeGraphVMQuery(queryCtx, queryTs)
		if queryErr != nil {
			return nil, queryErr
		}

		var instance tsdb.Instance
		if relationQueryParams.IsDirectQuery() {
			instance = prometheus.GetTsDbInstance(queryCtx, &metadata.Query{
				StorageType: metadata.VictoriaMetricsStorageType,
			})
			if instance == nil {
				return nil, fmt.Errorf("%s storage get error", metadata.VictoriaMetricsStorageType)
			}
		} else {
			instance = prometheus.NewInstance(queryCtx, promql.GlobalEngine, &prometheus.QueryRangeStorage{
				QueryMaxRouting: timeGraphQueryMaxRouting,
				Timeout:         timeGraphQueryTimeout,
			}, lookBack, timeGraphQueryMaxRouting)
		}

		if instant {
			vector, queryErr := instance.DirectQuery(queryCtx, expr, relationQueryParams.End)
			if queryErr != nil {
				return nil, errors.WithMessage(queryErr, "direct query")
			}
			return vectorToMatrix(vector), nil
		}
		matrix, _, queryErr := instance.DirectQueryRange(
			queryCtx,
			expr,
			relationQueryParams.AlignStart,
			relationQueryParams.End,
			relationQueryParams.Step,
		)
		if queryErr != nil {
			return nil, errors.WithMessage(queryErr, "direct query range")
		}
		return matrix, nil
	}

	if len(sourceExpandInfo) > 0 || len(relations) == 0 {
		infoCtx := newTimeGraphSubqueryContext(ctx)
		metadata.GetQueryParams(infoCtx).SetIsSkipK8s(true)
		queryTs, queryErr := tg.MakeResourceInfoQueryTsWithWindow(
			spaceUID, sourceType, sourceInfo, sourceExpandInfo, nil, start, end, queryStep, lookBack,
		)
		if queryErr != nil {
			return nil, errors.WithMessage(queryErr, "make source info query ts")
		}
		if queryTs != nil {
			matrix, queryErr := queryMatrix(infoCtx, queryTs)
			if queryErr != nil {
				return nil, errors.WithMessage(queryErr, "query source info relation")
			}
			for _, series := range matrix {
				if err := infoCtx.Err(); err != nil {
					return nil, err
				}
				info := make(cmdb.Matcher, len(series.Metric))
				for _, label := range series.Metric {
					info[label.Name] = label.Value
				}
				timestamps := make([]int64, len(series.Points))
				for i, point := range series.Points {
					timestamps[i] = point.T
				}
				if err = tg.AddTimeNode(infoCtx, sourceType, info, timestamps...); err != nil {
					return nil, errors.WithMessage(err, "add source info node")
				}
			}
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
		// 只有路径第一跳需要用起点 matcher 缩小 VM 查询范围；后续关系的
		// 起点由前一跳返回的节点决定，不能继续复用根节点条件。
		isRootRelation := len(relation.V) == 2 && relation.V[0] == sourceType
		if rootRelations != nil {
			_, isRootRelation = rootRelations[timeGraphRelationKeyFor(relation)]
		}
		if !isRootRelation {
			relationSourceInfo = nil
		}
		queryTs, queryErr := tg.MakeQueryTsWithWindow(relationCtx, spaceUID, relationSourceInfo, start, end, queryStep, lookBack, relation)
		if queryErr != nil {
			return nil, errors.WithMessagef(queryErr, "make query ts error for relation %v", relation)
		}
		if queryTs == nil {
			continue
		}

		matrix, queryErr := queryMatrix(relationCtx, queryTs)
		if queryErr != nil {
			return nil, queryErr
		}
		for _, series := range matrix {
			if err := relationCtx.Err(); err != nil {
				return nil, err
			}
			info := make(cmdb.Matcher, len(series.Metric))
			for _, label := range series.Metric {
				info[label.Name] = label.Value
			}
			timestamps := make([]int64, len(series.Points))
			for i, point := range series.Points {
				timestamps[i] = point.T
			}
			if err = tg.AddTimeRelationWithRelation(relationCtx, relation, info, timestamps...); err != nil {
				return nil, errors.WithMessage(err, "add time relation")
			}

			if !timeGraphTargetInfoShow(ctx) {
				continue
			}
			dynamic := relation.Category == string(RelationCategoryDynamic)
			_, targetPrefix := tg.relationEndpointPrefixes(relation, relation.V[0], relation.V[1])
			targetInfo := tg.relationEndpointInfo(info, relation.V[1], targetPrefix, dynamic)
			if len(targetInfo) == 0 {
				targetInfo = info
			}
			targetType := relation.V[1]
			targetKey := tg.primaryMatcherKey(targetType, targetInfo)
			if targetKey == "" {
				continue
			}
			if targetMatchersByType[targetType] == nil {
				targetMatchersByType[targetType] = make(map[string]cmdb.Matcher)
			}
			targetMatchersByType[targetType][targetKey] = tg.primaryMatcher(targetType, targetInfo)
			for _, point := range series.Points {
				if targetIDsByTimestamp[point.T] == nil {
					targetIDsByTimestamp[point.T] = make(map[cmdb.Resource]map[string]struct{})
				}
				if targetIDsByTimestamp[point.T][targetType] == nil {
					targetIDsByTimestamp[point.T][targetType] = make(map[string]struct{})
				}
				targetIDsByTimestamp[point.T][targetType][targetKey] = struct{}{}
			}
		}
	}

	if timeGraphTargetInfoShow(ctx) {
		for targetType, matchers := range targetMatchersByType {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			primaryMatchers := make([]cmdb.Matcher, 0, len(matchers))
			for _, matcher := range matchers {
				primaryMatchers = append(primaryMatchers, matcher)
			}
			infoCtx := newTimeGraphSubqueryContext(ctx)
			metadata.GetQueryParams(infoCtx).SetIsSkipK8s(true)
			queryTs, queryErr := tg.MakeResourceInfoQueryTsWithWindow(
				spaceUID, targetType, nil, nil, primaryMatchers, start, end, queryStep, lookBack,
			)
			if queryErr != nil {
				return nil, errors.WithMessage(queryErr, "make target info query ts")
			}
			if queryTs == nil {
				continue
			}
			matrix, queryErr := queryMatrix(infoCtx, queryTs)
			if queryErr != nil {
				return nil, errors.WithMessage(queryErr, "query target info relation")
			}
			for _, series := range matrix {
				if err := infoCtx.Err(); err != nil {
					return nil, err
				}
				info := make(cmdb.Matcher, len(series.Metric))
				for _, label := range series.Metric {
					info[label.Name] = label.Value
				}
				timestamps := make([]int64, 0, len(series.Points))
				key := tg.primaryMatcherKey(targetType, info)
				for _, point := range series.Points {
					if _, ok := targetIDsByTimestamp[point.T][targetType][key]; ok {
						timestamps = append(timestamps, point.T)
					}
				}
				if len(timestamps) > 0 {
					if err = tg.AddTimeNode(infoCtx, targetType, info, timestamps...); err != nil {
						return nil, errors.WithMessage(err, "add target info node")
					}
				}
			}
		}
	}
	return tg, nil
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

// rootTimeGraphRelationKeys 找出所有路径从 sourceType 出发的第一跳关系。
// 只有这些根边使用调用方的 source matcher；后续边必须使用前一跳发现的节点
// 继续查询，不能重复套用起点条件。
func (m *Model) rootTimeGraphRelationKeys(namespace string, sourceType cmdb.Resource, paths []cmdb.RelationPath) map[timeGraphRelationKey]struct{} {
	result := make(map[timeGraphRelationKey]struct{})
	for _, path := range paths {
		if len(path.Steps) < 2 || path.Steps[0].ResourceType != sourceType {
			continue
		}
		rootPath := cmdb.RelationPath{Steps: append([]cmdb.RelationPathStep(nil), path.Steps[:2]...)}
		for _, relation := range m.buildRelationsFromRelationPathsForNamespace(namespace, []cmdb.RelationPath{rootPath}) {
			result[timeGraphRelationKeyFor(relation)] = struct{}{}
		}
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
	for _, configured := range cfg.Relation {
		if len(configured.Resources) != 2 {
			continue
		}
		if step.RelationType != "" && !sameRelationType(configured.RelationType, step.RelationType) {
			continue
		}
		if !sameResourcePair(configured.Resources, source, target) {
			continue
		}
		metricName := configured.MetricName
		if step.MetricName != "" {
			metricName = step.MetricName
		}
		result = append(result, cmdb.Relation{
			V:            []cmdb.Resource{source, target},
			RelationType: configured.RelationType,
			MetricName:   metricName,
			Category:     configured.Category,
			Direction:    step.Direction,
		})
	}
	if len(result) > 0 {
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

func (m *Model) resolveTimeGraphPaths(ctx context.Context, spaceUID string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, requested [][]cmdb.Resource) ([]cmdb.RelationPath, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(requested) > 0 {
		paths := make([][]cmdb.Resource, 0, len(requested))
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
				paths = append(paths, normalizedPath)
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
			paths = append(paths, normalizedPath)
		}
		if len(paths) > 0 {
			// 旧接口只提供资源类型路径，没有关系方向、类别和指标名，保持原有
			// 兼容转换；自动发现路径则在下方完整保留 PathFinder 的关系计划。
			return cmdb.RelationPathsFromResourcePaths(paths), nil
		}
	}

	pathFinder := NewPathFinder(
		WithSchemaProvider(m.getSchemaProvider()),
		WithNamespace(spaceUID),
		WithMaxHops(MaxAllowedHops),
	)
	var paths []cmdb.RelationPath
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

func (m *Model) queryTimeGraph(ctx context.Context, lookBackDelta, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher, sourceExpandInfo cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	paths, err := m.resolveTimeGraphPaths(ctx, spaceUID, sourceType, targetTypes, pathResources)
	if err != nil {
		return nil, err
	}
	return m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, start, end, step, sourceType, targetTypes, paths, matcher, sourceExpandInfo)
}

func (m *Model) queryRelationTimeGraph(ctx context.Context, lookBackDelta, spaceUID string, start, end time.Time, step time.Duration, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher, sourceExpandInfo cmdb.Matcher) (results []cmdb.PathResourcesResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-query-relation")
	defer span.End(&err)
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
	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Timestamp == results[j].Timestamp {
			return results[i].TargetType < results[j].TargetType
		}
		return results[i].Timestamp < results[j].Timestamp
	})
	return results, nil
}

func (m *Model) queryPathResourcesWithSourceExpand(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher, sourceExpandInfo cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
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
	timestampValue, err := cast.ToInt64E(timestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse timestamp")
	}
	return m.queryTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(timestampValue, 0), time.Unix(timestampValue, 0), 5*time.Minute, sourceType, targetTypes, pathResources, matcher, sourceExpandInfo)
}

func (m *Model) QueryPathResources(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	return m.queryPathResourcesWithSourceExpand(ctx, lookBackDelta, spaceUID, timestamp, sourceType, targetTypes, pathResources, matcher, nil)
}

func (m *Model) queryRelationPathResourcesWithSourceExpand(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher, sourceExpandInfo cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
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
	timestampValue, err := cast.ToInt64E(timestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse timestamp")
	}
	return m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(timestampValue, 0), time.Unix(timestampValue, 0), 5*time.Minute, sourceType, targetTypes, paths, matcher, sourceExpandInfo)
}

func (m *Model) QueryRelationPathResources(ctx context.Context, lookBackDelta, spaceUID, timestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
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
	timestampValue, err := cast.ToInt64E(timestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse timestamp")
	}
	return m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(timestampValue, 0), time.Unix(timestampValue, 0), 5*time.Minute, sourceType, targetTypes, paths, matcher, nil)
}

func (m *Model) queryPathResourcesRangeWithSourceExpand(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher, sourceExpandInfo cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
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
	start, err := cast.ToInt64E(startTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse start timestamp")
	}
	end, err := cast.ToInt64E(endTimestamp)
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
	if _, err := validateRangeBuckets(start*1000, end*1000, stepMs); err != nil {
		return nil, err
	}
	return m.queryTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(start, 0), time.Unix(end, 0), stepDuration, sourceType, targetTypes, pathResources, matcher, sourceExpandInfo)
}

func (m *Model) QueryPathResourcesRange(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, pathResources [][]cmdb.Resource, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
	return m.queryPathResourcesRangeWithSourceExpand(ctx, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp, sourceType, targetTypes, pathResources, matcher, nil)
}

func (m *Model) queryRelationPathResourcesRangeWithSourceExpand(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher, sourceExpandInfo cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
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
	start, err := cast.ToInt64E(startTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse start timestamp")
	}
	end, err := cast.ToInt64E(endTimestamp)
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
	if _, err := validateRangeBuckets(start*1000, end*1000, stepMs); err != nil {
		return nil, err
	}
	return m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(start, 0), time.Unix(end, 0), stepDuration, sourceType, targetTypes, paths, matcher, sourceExpandInfo)
}

func (m *Model) QueryRelationPathResourcesRange(ctx context.Context, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string, sourceType cmdb.Resource, targetTypes []cmdb.Resource, paths []cmdb.RelationPath, matcher cmdb.Matcher) ([]cmdb.PathResourcesResult, error) {
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
	start, err := cast.ToInt64E(startTimestamp)
	if err != nil {
		return nil, errors.WithMessage(err, "parse start timestamp")
	}
	end, err := cast.ToInt64E(endTimestamp)
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
	if _, err := validateRangeBuckets(start*1000, end*1000, stepDuration.Milliseconds()); err != nil {
		return nil, err
	}
	return m.queryRelationTimeGraph(ctx, lookBackDelta, spaceUID, time.Unix(start, 0), time.Unix(end, 0), stepDuration, sourceType, targetTypes, paths, matcher, nil)
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
