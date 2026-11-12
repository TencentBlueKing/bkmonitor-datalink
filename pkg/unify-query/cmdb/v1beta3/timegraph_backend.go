// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	prommodel "github.com/prometheus/common/model"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type timeGraphTargetInfoShowKey struct{}

type timeGraphLegacyCompatibilityKey struct{}

func timeGraphLegacyCompatibility(ctx context.Context) bool {
	legacy, _ := ctx.Value(timeGraphLegacyCompatibilityKey{}).(bool)
	return legacy
}

func withTimeGraphTargetInfoShow(ctx context.Context, show bool) context.Context {
	return context.WithValue(ctx, timeGraphTargetInfoShowKey{}, show)
}

func timeGraphTargetInfoShow(ctx context.Context) bool {
	show, _ := ctx.Value(timeGraphTargetInfoShowKey{}).(bool)
	return show
}

type timeGraphLegacyResult struct {
	source             cmdb.Resource
	sourceMatcher      cmdb.Matcher
	paths              []string
	target             cmdb.Resource
	matchers           cmdb.Matchers
	candidatePathCount int
	rawResultCount     int
}

type timeGraphRangeResult struct {
	source             cmdb.Resource
	sourceMatcher      cmdb.Matcher
	paths              []string
	target             cmdb.Resource
	matchers           []cmdb.MatchersWithTimestamp
	candidatePathCount int
	rawResultCount     int
	bucketCount        int
	targetCount        int
}

func (m *Model) buildTimeGraphRequest(
	ctx context.Context,
	spaceUID string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (*QueryRequest, []resourcePath, error) {
	req := &QueryRequest{
		SpaceUID:            spaceUID,
		SourceType:          FromCMDBResource(source),
		SourceInfo:          matcherToMap(indexMatcher.Rename()),
		SourceExpandInfo:    matcherToMap(expandMatcher),
		TargetType:          FromCMDBResource(target),
		TargetTypeExplicit:  target != "",
		TargetInfoShow:      expandShow,
		PathResource:        toResourceTypes(pathResource),
		LegacyCompatibility: true,
	}
	if req.SourceType == "" {
		inferred, err := inferSourceTypeFromInfo(req, m.getSchemaProvider())
		if err != nil {
			return nil, nil, fmt.Errorf("get resource error: resource is empty with %+v", req.SourceInfo)
		}
		req.SourceType = inferred
	}
	// Same order as the old engine: source inference first, then the space.
	if spaceUID == "" {
		return nil, nil, errors.New("space uid is empty")
	}
	req.Normalize()

	provider := m.getSchemaProvider()
	if err := validateSchemaProvider(provider, req.SchemaNamespace()); err != nil {
		return nil, nil, err
	}
	// Legacy callers enumerate all simple schema paths, without a hop ceiling.
	// Every backend query keeps the request deadline, cancellation and the single-response boundary.
	req.MaxHops = int(^uint(0) >> 1)
	// 保持现有 v1beta3 旧接口的兼容语义：先用主键字段确定源资源，再用
	// source_expand_info 按非主键节点属性过滤已经确定的源资源。
	primaryKeys := provider.GetResourcePrimaryKeys(req.SchemaNamespace(), req.SourceType)
	req.SourceInfo = sourcePrimaryKeySubset(req.SourceInfo, primaryKeys)
	if len(primaryKeys) == 0 {
		return req, nil, fmt.Errorf("get index matcher error: index is empty with %+v", req.SourceType)
	}
	for key, value := range req.SourceExpandInfo {
		if sourceValue, exists := req.SourceInfo[key]; exists && sourceValue != value {
			return nil, nil, fmt.Errorf("source_expand_info conflicts with source_info for field %q", key)
		}
		req.SourceInfo[key] = value
	}
	if !req.TargetTypeExplicit {
		return req, []resourcePath{{Steps: []resourcePathStep{{ResourceType: string(req.SourceType)}}}}, nil
	}

	pathFinder := NewPathFinder(
		WithAllowedCategories(req.AllowedRelationTypes...),
		WithDynamicDirection(req.DynamicRelationDirection),
		WithMaxHops(req.MaxHops),
		WithSchemaProvider(provider),
		WithNamespace(req.SchemaNamespace()),
		WithLegacyPathConstraints(),
	)
	paths, err := pathFinder.FindAllPathsContext(ctx, req.SourceType, req.TargetType, req.PathResource)
	if err != nil {
		return req, nil, fmt.Errorf("get path error: %w", err)
	}
	// Report the actual candidate depth, not the unlimited search sentinel.
	req.MaxHops = 0
	for _, path := range paths {
		req.MaxHops = max(req.MaxHops, len(path.Steps)-1)
	}
	return req, sortPathsForQuery(paths), nil
}

// legacySourceMatcher is the source_info of the old response: the source's primary
// keys present in the request's source_info. source_expand_info only filters.
func (m *Model) legacySourceMatcher(req *QueryRequest, indexMatcher cmdb.Matcher) cmdb.Matcher {
	return cmdb.Matcher(sourcePrimaryKeySubset(
		indexMatcher.Rename(), m.getSchemaProvider().GetResourcePrimaryKeys(req.SchemaNamespace(), req.SourceType),
	))
}

func sortPathsForQuery(paths []resourcePath) []resourcePath {
	ordered := append([]resourcePath(nil), paths...)
	sort.SliceStable(ordered, func(i, j int) bool {
		if len(ordered[i].Steps) != len(ordered[j].Steps) {
			return len(ordered[i].Steps) < len(ordered[j].Steps)
		}
		left := fmt.Sprint(resourcePathToResourceTypes(ordered[i]))
		right := fmt.Sprint(resourcePathToResourceTypes(ordered[j]))
		if left != right {
			return left < right
		}
		return resourcePathSortKey(ordered[i]) < resourcePathSortKey(ordered[j])
	})
	return ordered
}

func resourcePathSortKey(path resourcePath) string {
	parts := make([]string, 0, len(path.Steps))
	for _, step := range path.Steps {
		parts = append(parts, strings.Join([]string{step.ResourceType, step.RelationType, step.Category, step.Direction, step.MetricName}, "/"))
	}
	return strings.Join(parts, "|")
}

// 旧路径查询遇到首个有效结果即停止；其他候选路径失败不会直接中断查询。query 自行保存
// 候选的结果，返回它是否命中。
func queryFirstTimeGraphPath(ctx context.Context, paths []resourcePath, query func(resourcePath) (bool, error)) (lastSuccess int, err error) {
	lastSuccess = -1
	reasons := make(map[string]struct{})
	collectQuality := func() {
		if status := metadata.GetStatus(ctx); status != nil && status.Code == metadata.QueryTsPartial {
			for _, reason := range splitPartialReasons(status.Message) {
				reasons[reason] = struct{}{}
			}
		}
	}
	collectQuality()
	defer func() {
		if err != nil || len(reasons) == 0 {
			return
		}
		ordered := make([]string, 0, len(reasons))
		for reason := range reasons {
			ordered = append(ordered, reason)
		}
		sort.Strings(ordered)
		metadata.SetStatus(ctx, metadata.QueryTsPartial, strings.Join(ordered, ";"))
	}()
	var failures []any
	for index, path := range paths {
		if err := ctx.Err(); err != nil {
			return lastSuccess, err
		}
		hit, err := query(path)
		collectQuality()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return lastSuccess, err
			}
			// 单个评估点仍超过单次响应边界时首选路径的答案无法确认，换下一条候选可能返回
			// 另一条路径的结果，因此直接失败。
			var unsplittable *UnsplittableResponseError
			if errors.As(err, &unsplittable) {
				return lastSuccess, err
			}
			var routeErr *relationNoPhysicalRouteError
			if timeGraphLegacyCompatibility(ctx) && errors.As(err, &routeErr) {
				reasons["relation_no_physical_route:"+routeErr.metric] = struct{}{}
				lastSuccess = index
				continue
			}
			failures = append(failures, fmt.Errorf("path [%v] do request error: %w", resourcePathToResourceTypes(path), err))
			continue
		}
		lastSuccess = index
		if hit {
			return lastSuccess, nil
		}
	}
	if len(failures) > 0 {
		prefix := "relation paths failed without an alternative hit"
		if len(failures) == len(paths) {
			prefix = "all relation paths failed"
		}
		// 保留旧路径列表文案，同时让取消和限额错误仍可由 errors.Is/As 识别。
		return lastSuccess, fmt.Errorf(prefix+": ["+strings.Repeat("%w ", len(failures)-1)+"%w]", failures...)
	}
	return lastSuccess, nil
}

func resourcePathsToTimeGraphPaths(paths []resourcePath) [][]cmdb.Resource {
	result := make([][]cmdb.Resource, 0, len(paths))
	for _, path := range paths {
		resources := make([]cmdb.Resource, 0, len(path.Steps))
		for _, step := range path.Steps {
			resources = append(resources, cmdb.Resource(step.ResourceType))
		}
		if len(resources) >= 2 {
			result = append(result, resources)
		}
	}
	return result
}

func resourcePathsToTimeGraphRelationPaths(paths []resourcePath) []cmdb.RelationPath {
	result := make([]cmdb.RelationPath, 0, len(paths))
	for _, path := range paths {
		steps := make([]cmdb.RelationPathStep, 0, len(path.Steps))
		for _, step := range path.Steps {
			steps = append(steps, cmdb.RelationPathStep{
				ResourceType: cmdb.Resource(step.ResourceType),
				RelationType: step.RelationType,
				Category:     step.Category,
				Direction:    step.Direction,
				MetricName:   step.MetricName,
			})
		}
		result = append(result, cmdb.RelationPath{Steps: steps})
	}
	return result
}

func resourcePathToResourceTypes(path resourcePath) []ResourceType {
	result := make([]ResourceType, 0, len(path.Steps))
	for _, step := range path.Steps {
		result = append(result, ResourceType(step.ResourceType))
	}
	return result
}

func timeGraphQueryTimestamp(ts string) (time.Time, error) {
	// The legacy VM gateway serializes time.Time with Unix(), including inputs
	// supplied in milliseconds, microseconds or nanoseconds. Match that wire
	// precision here, without relaxing the public topology/path contracts.
	_, _, timestamp, err := function.QueryTimestamp(ts, ts)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(timestamp.Unix(), 0), nil
}

func timeGraphTargetMatcher(
	target cmdb.PathNode,
	targetType ResourceType,
	provider SchemaProvider,
	namespace string,
	targetInfoShow bool,
) (string, cmdb.Matcher, bool) {
	if ResourceType(target.ResourceType) != targetType {
		return "", nil, false
	}
	primaryFields := provider.GetResourcePrimaryKeys(namespace, targetType)
	key := ""
	if len(primaryFields) == 0 {
		// 没有 schema 主键时保留旧逻辑；有主键时只用主键字段生成稳定的
		// 去重键，避免 info 属性变化或字段顺序影响返回结果。
		key = GenerateResourceID(targetType, map[string]string(target.Dimensions))
	} else {
		key = generateResourceIdentityKey(targetType, primaryFields, map[string]string(target.Dimensions))
		if key == "" {
			return "", nil, false
		}
	}
	matcher := filterTargetMatcher(target.Dimensions, provider, namespace, targetType, targetInfoShow)
	return key, matcher, true
}

func (m *Model) queryResourceMatcherWithTimeGraph(
	ctx context.Context,
	lookBackDelta, spaceUID, timestamp string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (result timeGraphLegacyResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-adapt-instant-result")
	defer span.End(&err)
	span.Set("space-uid", spaceUID)
	span.Set("source-type", source)
	span.Set("target-type", target)
	span.Set("path-resource-count", len(pathResource))
	span.Set("source-matcher-count", len(indexMatcher))
	span.Set("source-expand-matcher-count", len(expandMatcher))

	req, paths, err := m.buildTimeGraphRequest(ctx, spaceUID, target, source, indexMatcher, expandMatcher, expandShow, pathResource)
	if err != nil {
		return timeGraphLegacyResult{}, err
	}
	queryTimestamp, err := timeGraphQueryTimestamp(timestamp)
	if err != nil {
		return timeGraphLegacyResult{}, fmt.Errorf("parse TimeGraph timestamp: %w", err)
	}
	ctx = withTimeGraphTargetInfoShow(ctx, req.TargetInfoShow)
	ctx = context.WithValue(ctx, timeGraphLegacyCompatibilityKey{}, true)
	// The old instant query is a bare selector, so an omitted value means the
	// query engine's lookback. The TimeGraph default observes a much longer span
	// and would also return endpoints the source has already left.
	if lookBackDelta == "" {
		lookBackDelta = promql.GetDefaultLookbackDelta().String()
	}

	candidatePaths := resourcePathsToTimeGraphPaths(paths)
	result.candidatePathCount = len(candidatePaths)
	var results []RelationTarget
	lastSuccess, err := queryFirstTimeGraphPath(ctx, paths, func(path resourcePath) (bool, error) {
		pathResults, err := m.QueryRelationTimeGraph(
			ctx, lookBackDelta, spaceUID, queryTimestamp, queryTimestamp, 5*time.Minute,
			cmdb.Resource(req.SourceType), []cmdb.Resource{cmdb.Resource(req.TargetType)},
			resourcePathsToTimeGraphRelationPaths([]resourcePath{path}),
			cmdb.Matcher(req.SourceInfo), cmdb.Matcher(req.SourceExpandInfo),
		)
		if err != nil {
			return false, err
		}
		results = pathResults
		return len(results) > 0, nil
	})
	if err != nil {
		return timeGraphLegacyResult{}, err
	}
	result.rawResultCount = len(results)

	provider := m.getSchemaProvider()
	matchersByID := make(map[string]cmdb.Matcher)
	for _, result := range results {
		key, matcher, ok := timeGraphTargetMatcher(result.Target, req.TargetType, provider, req.SchemaNamespace(), req.TargetInfoShow)
		if ok {
			matchersByID[key] = matcher
		}
	}

	matchers := make(cmdb.Matchers, 0, len(matchersByID))
	for _, matcher := range matchersByID {
		matchers = append(matchers, matcher)
	}
	sortByTextKey(matchers, matcherText)

	selectedPath := legacySelectedPath(paths, lastSuccess)

	result = timeGraphLegacyResult{
		source:             cmdb.Resource(req.SourceType),
		sourceMatcher:      m.legacySourceMatcher(req, indexMatcher),
		paths:              selectedPath,
		target:             cmdb.Resource(req.TargetType),
		matchers:           matchers,
		candidatePathCount: result.candidatePathCount,
		rawResultCount:     result.rawResultCount,
	}
	span.Set("candidate-path-count", result.candidatePathCount)
	span.Set("raw-result-count", result.rawResultCount)
	span.Set("target-count", len(result.matchers))
	return result, nil
}

func (m *Model) queryResourceMatcherRangeWithTimeGraph(
	ctx context.Context,
	lookBackDelta, spaceUID, step, startTimestamp, endTimestamp string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (result timeGraphRangeResult, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-adapt-range-result")
	defer span.End(&err)
	span.Set("space-uid", spaceUID)
	span.Set("source-type", source)
	span.Set("target-type", target)
	span.Set("path-resource-count", len(pathResource))
	span.Set("source-matcher-count", len(indexMatcher))
	span.Set("source-expand-matcher-count", len(expandMatcher))

	req, paths, err := m.buildTimeGraphRequest(ctx, spaceUID, target, source, indexMatcher, expandMatcher, expandShow, pathResource)
	if req != nil {
		result.source = cmdb.Resource(req.SourceType)
		result.sourceMatcher = m.legacySourceMatcher(req, indexMatcher)
		result.target = cmdb.Resource(req.TargetType)
	}
	if err != nil {
		return result, err
	}
	_, startTime, endTime, err := function.QueryTimestamp(startTimestamp, endTimestamp)
	if err != nil {
		return result, fmt.Errorf("parse TimeGraph timestamps: %w", err)
	}
	window := time.Minute
	if step != "" {
		duration, parseErr := prommodel.ParseDuration(step)
		if parseErr != nil {
			return result, fmt.Errorf("parse step: %w", parseErr)
		}
		window = time.Duration(duration)
	}
	// The old VM wire format truncates timestamps and the sampling interval to
	// seconds, but leaves count_over_time's window unchanged (e.g. 1500ms).
	// Use that same grid for validation and output buckets, including the first
	// bucket before an input's millisecond remainder.
	startTime, endTime = time.Unix(startTime.Unix(), 0), time.Unix(endTime.Unix(), 0)
	stepDuration := window.Truncate(time.Second)
	if err := validateTimeGraphPrecision(startTime, endTime, stepDuration); err != nil {
		return result, err
	}
	startMs, endMs := startTime.UnixMilli(), endTime.UnixMilli()
	stepMs := stepDuration.Milliseconds()
	if lookBackDelta != "" {
		lookBack, err := time.ParseDuration(lookBackDelta)
		if err != nil {
			return result, fmt.Errorf("parse look back delta: %w", err)
		}
		if lookBack <= 0 {
			return result, fmt.Errorf("look back delta must be positive")
		}
	}
	// Preserve the original aggregation window independently of the wire step.
	lookBackDelta = window.String()
	points, err := validateRangeBuckets(startMs, endMs, stepMs)
	if err != nil {
		return result, err
	}
	cacheStartMs := startMs - startMs%stepMs
	cacheEndMs := cacheStartMs + int64(points-1)*stepMs
	mayAdjustGrid := stepDuration <= time.Minute && points >= 50 &&
		(cacheStartMs != startMs || cacheEndMs != endMs)
	ctx = withTimeGraphTargetInfoShow(ctx, req.TargetInfoShow)
	ctx = context.WithValue(ctx, timeGraphLegacyCompatibilityKey{}, true)
	ctx = metadata.WithExactTimeGrid(ctx)
	ctx = metadata.WithLeftOpenTimeWindow(ctx)

	candidatePaths := resourcePathsToTimeGraphPaths(paths)
	result.candidatePathCount = len(candidatePaths)
	provider := m.getSchemaProvider()
	// 每段的结果立即并入该候选的时间桶，不跨段保留 RelationTarget；工作集是当前段的结果
	// 与已去重的分桶。只有选中的候选的分桶留到最后构造响应。
	var timeSeries map[int64]map[string]cmdb.Matcher
	lastSuccess, err := queryFirstTimeGraphPath(ctx, paths, func(path resourcePath) (bool, error) {
		pathStart, pathEnd := startTime, endTime
		if mayAdjustGrid {
			useVM, routeErr := m.legacyRangeUsesVM(ctx, req, path, startTime, endTime, stepDuration, window)
			if routeErr != nil {
				return false, routeErr
			}
			if useVM {
				pathStart, pathEnd = time.UnixMilli(cacheStartMs), time.UnixMilli(cacheEndMs)
			}
		}
		// 分桶必须使用本候选的网格，不能沿用前一个候选。
		bucketStart, bucketEnd := pathStart.UnixMilli(), pathEnd.UnixMilli()
		var (
			pathBuckets = make(map[int64]map[string]cmdb.Matcher)
			rawResults  int
			noRouteErr  error
			routed      bool
		)
		segments := newLegacyRangeSegmenter(pathStart, pathEnd, stepDuration)
		// 单次后端响应超过分批边界的读取由取数器在段内按时间对半重读，段只约束时间分桶图的规模。
		for segment, ok := segments.next(); ok; segment, ok = segments.next() {
			segmentResults, graphPoints, queryErr := m.queryRelationTargets(
				ctx, lookBackDelta, spaceUID, segment.start, segment.end, stepDuration,
				cmdb.Resource(req.SourceType), []cmdb.Resource{cmdb.Resource(req.TargetType)},
				resourcePathsToTimeGraphRelationPaths([]resourcePath{path}),
				cmdb.Matcher(req.SourceInfo), cmdb.Matcher(req.SourceExpandInfo),
			)
			var routeErr *relationNoPhysicalRouteError
			if errors.As(queryErr, &routeErr) {
				// 路由按存储切换记录分时段生效：没有路由的时段不读数并标记不完整，
				// 与整段查询一样使用其它时段的结果；所有时段都没有路由才算路径无路由。
				if noRouteErr == nil {
					noRouteErr = queryErr
				}
				addTimeGraphPartialReason(ctx, "relation_no_physical_route:"+routeErr.metric)
				segments.observe(segment, 0)
				continue
			}
			if queryErr != nil {
				return false, queryErr
			}
			routed = true
			rawResults += len(segmentResults)
			bucketRelationTargets(pathBuckets, segmentResults, req, provider, bucketStart, bucketEnd, stepMs)
			segments.observe(segment, graphPoints)
		}
		if !routed && noRouteErr != nil {
			return false, noRouteErr
		}
		timeSeries, result.rawResultCount = pathBuckets, rawResults
		return rawResults > 0, nil
	})
	if err != nil {
		return result, err
	}

	series := make([]cmdb.MatchersWithTimestamp, 0, len(timeSeries))
	for timestamp, matchersByID := range timeSeries {
		matchers := make(cmdb.Matchers, 0, len(matchersByID))
		for _, matcher := range matchersByID {
			matchers = append(matchers, matcher)
		}
		sortByTextKey(matchers, matcherText)
		series = append(series, cmdb.MatchersWithTimestamp{Timestamp: timestamp, Matchers: matchers})
	}
	sort.SliceStable(series, func(i, j int) bool { return series[i].Timestamp < series[j].Timestamp })

	selectedPath := legacySelectedPath(paths, lastSuccess)

	result = timeGraphRangeResult{
		source:             cmdb.Resource(req.SourceType),
		sourceMatcher:      m.legacySourceMatcher(req, indexMatcher),
		paths:              selectedPath,
		target:             cmdb.Resource(req.TargetType),
		matchers:           series,
		candidatePathCount: result.candidatePathCount,
		rawResultCount:     result.rawResultCount,
		bucketCount:        len(series),
		targetCount:        countTimeGraphRangeTargets(series),
	}
	span.Set("candidate-path-count", result.candidatePathCount)
	span.Set("raw-result-count", result.rawResultCount)
	span.Set("bucket-count", result.bucketCount)
	span.Set("target-count", result.targetCount)
	return result, nil
}

// bucketRelationTargets 把一段的目标并入时间桶：同一桶内按主键去重，段之间互不重叠。
func bucketRelationTargets(buckets map[int64]map[string]cmdb.Matcher, results []RelationTarget, req *QueryRequest, provider SchemaProvider, start, end, step int64) {
	for _, target := range results {
		key, matcher, ok := timeGraphTargetMatcher(target.Target, req.TargetType, provider, req.SchemaNamespace(), req.TargetInfoShow)
		if !ok {
			continue
		}
		bucket, ok := timeGraphRangeBucket(normalizeTimeGraphResultTimestamp(target.Timestamp), start, end, step)
		if !ok {
			continue
		}
		if buckets[bucket] == nil {
			buckets[bucket] = make(map[string]cmdb.Matcher)
		}
		buckets[bucket][key] = matcher
	}
}

// legacySelectedPath 返回旧响应的 path。候选路径逐条查询，命中时最后一条
// 成功的候选就是首条有结果的路径；都无结果时沿用最后一条成功候选。
func legacySelectedPath(paths []resourcePath, lastSuccess int) []string {
	if lastSuccess < 0 || lastSuccess >= len(paths) {
		return nil
	}
	return resourceTypesToPath(resourcePathToResourceTypes(paths[lastSuccess]))
}

// legacyRangeSegmentPoints 是区间查询首段的评估点数。各点按自己的窗口独立
// 计算，分段不改变答案；单段只物化这些点的时间分桶图。测试可调大它取得不分段的参照答案。
var legacyRangeSegmentPoints = 64

// legacyRangeSegmentGraphPoints 是单段时间分桶图的目标规模：每个时刻的边数与节点信息数之和。
// 段长按上一段得到的每点规模换算，轻量查询逐段翻倍以减少往返，重查询的工作集
// 保持在这一量级，与总点数无关。
const legacyRangeSegmentGraphPoints = 256 * 1024

type legacyRangeSegment struct {
	start, end time.Time
	points     int
}

// legacyRangeSegmenter 把同一网格切成连续段：段的起点都落在网格上，最后一段沿用
// 原终点；末段只剩一个网格点时并入前段，避免把区间查询退化为即时查询。
type legacyRangeSegmenter struct {
	start, end time.Time
	step       time.Duration
	remaining  int
	size       int
}

func newLegacyRangeSegmenter(start, end time.Time, step time.Duration) *legacyRangeSegmenter {
	segmenter := &legacyRangeSegmenter{start: start, end: end, step: step, size: legacyRangeSegmentPoints}
	switch {
	case step <= 0 || !end.After(start):
		segmenter.remaining = 1
		segmenter.size = 1
	default:
		segmenter.remaining = int(end.Sub(start)/step) + 1
	}
	return segmenter
}

func (s *legacyRangeSegmenter) next() (legacyRangeSegment, bool) {
	if s.remaining <= 0 {
		return legacyRangeSegment{}, false
	}
	size := min(max(s.size, 1), s.remaining)
	if s.remaining-size == 1 {
		size = s.remaining
	}
	segment := legacyRangeSegment{start: s.start, end: s.start.Add(time.Duration(size-1) * s.step), points: size}
	if size == s.remaining {
		segment.end = s.end
	}
	s.start = s.start.Add(time.Duration(size) * s.step)
	s.remaining -= size
	return segment, true
}

// observe 按刚完成一段的图规模决定下一段长度：至多翻倍，不超过单段图规模。空段
// （含缺路由的段）不提供规模信息，下一段保持它的长度，连续空段之后也不会放大到单段规模之外。
func (s *legacyRangeSegmenter) observe(segment legacyRangeSegment, graphPoints int) {
	next := max(segment.points, 1)
	if graphPoints > 0 {
		perPoint := (graphPoints + segment.points - 1) / segment.points
		next = min(2*next, legacyRangeSegmentGraphPoints/perPoint)
	}
	s.size = max(next, 2)
}

// addTimeGraphPartialReason 把一个不完整原因并入当前查询的质量状态。
func addTimeGraphPartialReason(ctx context.Context, reason string) {
	reasons := map[string]struct{}{reason: {}}
	if status := metadata.GetStatus(ctx); status != nil && status.Code == metadata.QueryTsPartial {
		for _, existing := range splitPartialReasons(status.Message) {
			reasons[existing] = struct{}{}
		}
	}
	ordered := make([]string, 0, len(reasons))
	for existing := range reasons {
		ordered = append(ordered, existing)
	}
	sort.Strings(ordered)
	metadata.SetStatus(ctx, metadata.QueryTsPartial, strings.Join(ordered, ";"))
}

// sortByTextKey 按文本键稳定排序，每个元素的键只计算一次，比较时不再格式化。
func sortByTextKey[T any](items []T, key func(T) string) {
	keys := make([]string, len(items))
	for i, item := range items {
		keys[i] = key(item)
	}
	sort.Stable(textKeyedItems[T]{keys: keys, items: items})
}

func matcherText(matcher cmdb.Matcher) string { return fmt.Sprint(matcher) }

type textKeyedItems[T any] struct {
	keys  []string
	items []T
}

func (s textKeyedItems[T]) Len() int           { return len(s.items) }
func (s textKeyedItems[T]) Less(i, j int) bool { return s.keys[i] < s.keys[j] }
func (s textKeyedItems[T]) Swap(i, j int) {
	s.keys[i], s.keys[j] = s.keys[j], s.keys[i]
	s.items[i], s.items[j] = s.items[j], s.items[i]
}

func countTimeGraphRangeTargets(series []cmdb.MatchersWithTimestamp) int {
	count := 0
	for _, bucket := range series {
		count += len(bucket.Matchers)
	}
	return count
}

func normalizeTimeGraphResultTimestamp(timestamp int64) int64 {
	if timestamp > 0 && timestamp < 1e12 {
		return timestamp * 1000
	}
	return timestamp
}

func timeGraphRangeBucket(timestamp, start, end, step int64) (int64, bool) {
	if timestamp < start-step || timestamp > end {
		return 0, false
	}
	if timestamp <= start {
		return start, true
	}
	offset := timestamp - start
	bucket := start + ((offset+step-1)/step)*step
	if bucket > end {
		return 0, false
	}
	return bucket, true
}
