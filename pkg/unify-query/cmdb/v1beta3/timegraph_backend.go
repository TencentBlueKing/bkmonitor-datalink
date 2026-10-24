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
	"strconv"
	"strings"
	"time"

	prommodel "github.com/prometheus/common/model"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
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

// timeGraphQuerier 定义 v1beta3 TimeGraph 查询契约。
// 接口保持精简，让请求归一化和旧版响应适配与关系指标读取、内存图遍历相互独立。
type timeGraphQuerier interface {
	QueryPathResources(context.Context, string, string, string, cmdb.Resource, []cmdb.Resource, [][]cmdb.Resource, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
	QueryPathResourcesRange(context.Context, string, string, string, string, string, cmdb.Resource, []cmdb.Resource, [][]cmdb.Resource, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
}

type relationPathTimeGraphQuerier interface {
	QueryRelationPathResources(context.Context, string, string, string, cmdb.Resource, []cmdb.Resource, []cmdb.RelationPath, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
	QueryRelationPathResourcesRange(context.Context, string, string, string, string, string, cmdb.Resource, []cmdb.Resource, []cmdb.RelationPath, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
}

type sourceExpandRelationTimeGraphQuerier interface {
	queryRelationPathResourcesWithSourceExpand(context.Context, string, string, string, cmdb.Resource, []cmdb.Resource, []cmdb.RelationPath, cmdb.Matcher, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
	queryRelationPathResourcesRangeWithSourceExpand(context.Context, string, string, string, string, string, cmdb.Resource, []cmdb.Resource, []cmdb.RelationPath, cmdb.Matcher, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
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

func (m *Model) getTimeGraphQuerier(ctx context.Context, spaceUID string) (querier timeGraphQuerier, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-resolve-querier")
	defer span.End(&err)
	span.Set("space-uid", spaceUID)
	resolver := m.timeGraphResolver
	if resolver == nil {
		return nil, fmt.Errorf("timegraph resolver is not configured")
	}

	model, err := resolver(ctx, spaceUID)
	if err != nil {
		return nil, err
	}
	querier, ok := model.(timeGraphQuerier)
	if !ok {
		return nil, fmt.Errorf("relation model does not support TimeGraph query")
	}
	span.Set("querier-type", fmt.Sprintf("%T", querier))
	return querier, nil
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
		MaxHops:             computeMaxHops(source, target, pathResource),
		LegacyCompatibility: true,
		DisableRootLimit:    true,
	}
	if req.SourceType == "" {
		inferred, err := inferSourceTypeFromInfo(req, m.getSchemaProvider())
		if err != nil {
			return nil, nil, err
		}
		req.SourceType = inferred
	}
	req.Normalize()

	provider := m.getSchemaProvider()
	if err := validateSchemaProvider(provider, req.SchemaNamespace()); err != nil {
		return nil, nil, err
	}
	if !req.LegacyCompatibility {
		if err := validateSourceExpandInfoFields(req, provider); err != nil {
			return nil, nil, err
		}
	}
	// Legacy callers do not supply max_hops. Search the finite schema paths,
	// retaining the normal-mode safety budget and making any truncation visible.
	req.MaxHops = effectivePathSearchMaxHops()
	// 保持现有 v1beta3 旧接口的兼容语义：先用主键字段确定源资源，再用
	// source_expand_info 按非主键节点属性过滤已经确定的源资源。
	req.SourceInfo = sourcePrimaryKeySubset(req, provider)
	if req.SourceInfo == nil && len(req.SourceExpandInfo) > 0 {
		req.SourceInfo = make(map[string]string, len(req.SourceExpandInfo))
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
		return nil, nil, err
	}
	if pathFinder.depthLimited {
		metadata.SetStatus(ctx, metadata.QueryTsPartial, "path_search_depth_limit")
	}
	// Report the actual candidate depth, not the unlimited search sentinel.
	req.MaxHops = 0
	for _, path := range paths {
		req.MaxHops = max(req.MaxHops, len(path.Steps)-1)
	}
	return req, sortPathsForQuery(paths), nil
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

// 旧路径查询遇到首个有效结果即停止；其他候选路径失败不会直接中断查询。
func queryFirstTimeGraphPath(ctx context.Context, paths []resourcePath, query func(resourcePath) ([]cmdb.PathResourcesResult, error)) (results []cmdb.PathResourcesResult, lastSuccess int, err error) {
	lastSuccess = -1
	reasons := make(map[string]struct{})
	collectQuality := func() {
		if status := metadata.GetStatus(ctx); status != nil && status.Code == metadata.QueryTsPartial {
			for _, reason := range strings.Split(status.Message, ";") {
				if reason != "" {
					reasons[reason] = struct{}{}
				}
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
	var failures []error
	for index, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, lastSuccess, err
		}
		results, err := query(path)
		collectQuality()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, lastSuccess, err
			}
			var routeErr *relationNoPhysicalRouteError
			if timeGraphLegacyCompatibility(ctx) && errors.As(err, &routeErr) {
				reasons["relation_no_physical_route:"+routeErr.metric] = struct{}{}
				lastSuccess = index
				continue
			}
			failures = append(failures, err)
			continue
		}
		lastSuccess = index
		if len(results) > 0 {
			return results, lastSuccess, nil
		}
	}
	if len(failures) > 0 {
		if len(failures) == len(paths) {
			return nil, lastSuccess, fmt.Errorf("all relation paths failed: %w", errors.Join(failures...))
		}
		return nil, lastSuccess, fmt.Errorf("relation paths failed without an alternative hit: %w", errors.Join(failures...))
	}
	return nil, lastSuccess, nil
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

func timeGraphQueryTimestamp(ts string) (string, error) {
	// The legacy VM gateway serializes time.Time with Unix(), including inputs
	// supplied in milliseconds, microseconds or nanoseconds. Match that wire
	// precision here, without relaxing the public topology/path contracts.
	_, _, timestamp, err := function.QueryTimestamp(ts, ts)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(timestamp.Unix(), 10), nil
}

func timeGraphTargetMatcher(
	path []cmdb.PathNode,
	targetType ResourceType,
	provider SchemaProvider,
	namespace string,
	targetInfoShow bool,
) (string, cmdb.Matcher, bool) {
	if len(path) == 0 {
		return "", nil, false
	}
	target := path[len(path)-1]
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
	defer func() {
		if result.candidatePathCount > 0 {
			metric.CMDBRelationCandidatePathCountObserve(
				ctx,
				metric.CMDBRelationRouteTimeGraph,
				metric.CMDBRelationQueryModeInstant,
				"first_path",
				result.candidatePathCount,
			)
		}
		metric.CMDBRelationTimeGraphResultCountObserve(ctx, metric.CMDBRelationQueryModeInstant, result.rawResultCount)
		pathResult := metric.CMDBRelationResultSuccess
		if err != nil {
			pathResult = metric.CMDBRelationResultFailed
		} else if status := metadata.GetStatus(ctx); status != nil && status.Code == metadata.QueryTsPartial {
			pathResult = metric.CMDBRelationResultPartial
		} else if result.rawResultCount == 0 {
			pathResult = metric.CMDBRelationResultEmpty
		}
		metric.CMDBRelationPathResultInc(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeInstant, pathResult)
	}()

	req, paths, err := m.buildTimeGraphRequest(ctx, spaceUID, target, source, indexMatcher, expandMatcher, expandShow, pathResource)
	if err != nil {
		return timeGraphLegacyResult{}, err
	}
	queryTimestamp, err := timeGraphQueryTimestamp(timestamp)
	if err != nil {
		return timeGraphLegacyResult{}, fmt.Errorf("parse TimeGraph timestamp: %w", err)
	}
	querier, err := m.getTimeGraphQuerier(ctx, spaceUID)
	if err != nil {
		return timeGraphLegacyResult{}, err
	}
	ctx = withTimeGraphTargetInfoShow(ctx, req.TargetInfoShow)
	ctx = context.WithValue(ctx, timeGraphLegacyCompatibilityKey{}, true)

	candidatePaths := resourcePathsToTimeGraphPaths(paths)
	result.candidatePathCount = len(candidatePaths)
	results, lastSuccess, err := queryFirstTimeGraphPath(ctx, paths, func(path resourcePath) ([]cmdb.PathResourcesResult, error) {
		paths := []resourcePath{path}
		if expandedQuerier, ok := querier.(sourceExpandRelationTimeGraphQuerier); ok {
			return expandedQuerier.queryRelationPathResourcesWithSourceExpand(
				ctx,
				lookBackDelta,
				spaceUID,
				queryTimestamp,
				cmdb.Resource(req.SourceType),
				[]cmdb.Resource{cmdb.Resource(req.TargetType)},
				resourcePathsToTimeGraphRelationPaths(paths),
				cmdb.Matcher(req.SourceInfo),
				cmdb.Matcher(req.SourceExpandInfo),
			)
		} else if relationQuerier, ok := querier.(relationPathTimeGraphQuerier); ok {
			return relationQuerier.QueryRelationPathResources(
				ctx,
				lookBackDelta,
				spaceUID,
				queryTimestamp,
				cmdb.Resource(req.SourceType),
				[]cmdb.Resource{cmdb.Resource(req.TargetType)},
				resourcePathsToTimeGraphRelationPaths(paths),
				cmdb.Matcher(req.SourceInfo),
			)
		} else {
			return querier.QueryPathResources(
				ctx,
				lookBackDelta,
				spaceUID,
				queryTimestamp,
				cmdb.Resource(req.SourceType),
				[]cmdb.Resource{cmdb.Resource(req.TargetType)},
				resourcePathsToTimeGraphPaths(paths),
				cmdb.Matcher(req.SourceInfo),
			)
		}
	})
	if err != nil {
		return timeGraphLegacyResult{}, err
	}
	result.rawResultCount = len(results)

	provider := m.getSchemaProvider()
	bestRank := len(candidatePaths)
	matchersByID := make(map[string]cmdb.Matcher)
	for _, result := range results {
		rank := cmdb.PathRank(result.Path, candidatePaths)
		if rank < bestRank {
			bestRank = rank
			matchersByID = make(map[string]cmdb.Matcher)
		}
		if rank != bestRank {
			continue
		}
		key, matcher, ok := timeGraphTargetMatcher(result.Path, req.TargetType, provider, req.SchemaNamespace(), req.TargetInfoShow)
		if ok {
			matchersByID[key] = matcher
		}
	}

	matchers := make(cmdb.Matchers, 0, len(matchersByID))
	for _, matcher := range matchersByID {
		matchers = append(matchers, matcher)
	}
	sort.SliceStable(matchers, func(i, j int) bool {
		return fmt.Sprint(matchers[i]) < fmt.Sprint(matchers[j])
	})
	if err := validateTargetCount(len(matchers)); err != nil {
		return timeGraphLegacyResult{}, err
	}

	selectedPath := []string(nil)
	if bestRank < len(paths) {
		selectedPath = resourceTypesToPath(resourcePathToResourceTypes(paths[bestRank]))
	}
	if len(selectedPath) == 0 && len(results) > 0 {
		selectedPath = cmdb.PathResourceTypes(results[0].Path)
	}
	if len(selectedPath) == 0 && lastSuccess >= 0 {
		selectedPath = resourceTypesToPath(resourcePathToResourceTypes(paths[lastSuccess]))
	}

	result = timeGraphLegacyResult{
		source:             cmdb.Resource(req.SourceType),
		sourceMatcher:      cmdb.Matcher(req.SourceInfo),
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
	defer func() {
		if result.candidatePathCount > 0 {
			metric.CMDBRelationCandidatePathCountObserve(
				ctx,
				metric.CMDBRelationRouteTimeGraph,
				metric.CMDBRelationQueryModeRange,
				"first_path",
				result.candidatePathCount,
			)
		}
		metric.CMDBRelationTimeGraphResultCountObserve(ctx, metric.CMDBRelationQueryModeRange, result.rawResultCount)
		metric.CMDBRelationTimeGraphBucketCountObserve(ctx, metric.CMDBRelationQueryModeRange, result.bucketCount)
		pathResult := metric.CMDBRelationResultSuccess
		if err != nil {
			pathResult = metric.CMDBRelationResultFailed
		} else if status := metadata.GetStatus(ctx); status != nil && status.Code == metadata.QueryTsPartial {
			pathResult = metric.CMDBRelationResultPartial
		} else if result.rawResultCount == 0 {
			pathResult = metric.CMDBRelationResultEmpty
		}
		metric.CMDBRelationPathResultInc(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeRange, pathResult)
	}()

	req, paths, err := m.buildTimeGraphRequest(ctx, spaceUID, target, source, indexMatcher, expandMatcher, expandShow, pathResource)
	if err != nil {
		return timeGraphRangeResult{}, err
	}
	_, startTime, endTime, err := function.QueryTimestamp(startTimestamp, endTimestamp)
	if err != nil {
		return timeGraphRangeResult{}, fmt.Errorf("parse TimeGraph timestamps: %w", err)
	}
	window := time.Minute
	if step != "" {
		duration, parseErr := prommodel.ParseDuration(step)
		if parseErr != nil {
			return timeGraphRangeResult{}, fmt.Errorf("parse step: %w", parseErr)
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
		return timeGraphRangeResult{}, err
	}
	startMs, endMs := startTime.UnixMilli(), endTime.UnixMilli()
	stepMs := stepDuration.Milliseconds()
	// 使用解析后的步长继续调用 TimeGraph；不能把调用方传入的空字符串再次
	// 传到底层，否则底层会重新解析空 duration 并报错。
	normalizedStep := stepDuration.String()
	if lookBackDelta != "" {
		lookBack, err := time.ParseDuration(lookBackDelta)
		if err != nil {
			return timeGraphRangeResult{}, fmt.Errorf("parse look back delta: %w", err)
		}
		if lookBack <= 0 {
			return timeGraphRangeResult{}, fmt.Errorf("look back delta must be positive")
		}
	}
	// Preserve the original aggregation window independently of the wire step.
	lookBackDelta = window.String()
	if _, err := validateRangeBuckets(startMs, endMs, stepMs); err != nil {
		return timeGraphRangeResult{}, err
	}
	start := strconv.FormatInt(startTime.Unix(), 10)
	end := strconv.FormatInt(endTime.Unix(), 10)
	querier, err := m.getTimeGraphQuerier(ctx, spaceUID)
	if err != nil {
		return timeGraphRangeResult{}, err
	}
	ctx = withTimeGraphTargetInfoShow(ctx, req.TargetInfoShow)
	ctx = context.WithValue(ctx, timeGraphLegacyCompatibilityKey{}, true)
	ctx = metadata.WithExactTimeGrid(ctx)
	ctx = metadata.WithLeftOpenTimeWindow(ctx)

	candidatePaths := resourcePathsToTimeGraphPaths(paths)
	result.candidatePathCount = len(candidatePaths)
	results, lastSuccess, err := queryFirstTimeGraphPath(ctx, paths, func(path resourcePath) ([]cmdb.PathResourcesResult, error) {
		paths := []resourcePath{path}
		if expandedQuerier, ok := querier.(sourceExpandRelationTimeGraphQuerier); ok {
			return expandedQuerier.queryRelationPathResourcesRangeWithSourceExpand(
				ctx,
				lookBackDelta,
				spaceUID,
				normalizedStep,
				start,
				end,
				cmdb.Resource(req.SourceType),
				[]cmdb.Resource{cmdb.Resource(req.TargetType)},
				resourcePathsToTimeGraphRelationPaths(paths),
				cmdb.Matcher(req.SourceInfo),
				cmdb.Matcher(req.SourceExpandInfo),
			)
		} else if relationQuerier, ok := querier.(relationPathTimeGraphQuerier); ok {
			return relationQuerier.QueryRelationPathResourcesRange(
				ctx,
				lookBackDelta,
				spaceUID,
				normalizedStep,
				start,
				end,
				cmdb.Resource(req.SourceType),
				[]cmdb.Resource{cmdb.Resource(req.TargetType)},
				resourcePathsToTimeGraphRelationPaths(paths),
				cmdb.Matcher(req.SourceInfo),
			)
		} else {
			return querier.QueryPathResourcesRange(
				ctx,
				lookBackDelta,
				spaceUID,
				normalizedStep,
				start,
				end,
				cmdb.Resource(req.SourceType),
				[]cmdb.Resource{cmdb.Resource(req.TargetType)},
				resourcePathsToTimeGraphPaths(paths),
				cmdb.Matcher(req.SourceInfo),
			)
		}
	})
	if err != nil {
		return timeGraphRangeResult{}, err
	}
	result.rawResultCount = len(results)

	provider := m.getSchemaProvider()
	bestRank := len(candidatePaths)
	timeSeries := make(map[int64]map[string]cmdb.Matcher)
	for _, result := range results {
		rank := cmdb.PathRank(result.Path, candidatePaths)
		if rank < bestRank {
			bestRank = rank
			timeSeries = make(map[int64]map[string]cmdb.Matcher)
		}
		if rank != bestRank {
			continue
		}
		key, matcher, ok := timeGraphTargetMatcher(result.Path, req.TargetType, provider, req.SchemaNamespace(), req.TargetInfoShow)
		if !ok {
			continue
		}
		timestampMs := normalizeTimeGraphResultTimestamp(result.Timestamp)
		bucket, ok := timeGraphRangeBucket(timestampMs, startMs, endMs, stepMs)
		if !ok {
			continue
		}
		if timeSeries[bucket] == nil {
			timeSeries[bucket] = make(map[string]cmdb.Matcher)
		}
		timeSeries[bucket][key] = matcher
	}

	series := make([]cmdb.MatchersWithTimestamp, 0, len(timeSeries))
	for timestamp, matchersByID := range timeSeries {
		matchers := make(cmdb.Matchers, 0, len(matchersByID))
		for _, matcher := range matchersByID {
			matchers = append(matchers, matcher)
		}
		sort.SliceStable(matchers, func(i, j int) bool {
			return fmt.Sprint(matchers[i]) < fmt.Sprint(matchers[j])
		})
		series = append(series, cmdb.MatchersWithTimestamp{Timestamp: timestamp, Matchers: matchers})
	}
	sort.SliceStable(series, func(i, j int) bool { return series[i].Timestamp < series[j].Timestamp })
	if _, err := validateRangeTargetCounts(series); err != nil {
		return timeGraphRangeResult{}, err
	}

	selectedPath := []string(nil)
	if bestRank < len(paths) {
		selectedPath = resourceTypesToPath(resourcePathToResourceTypes(paths[bestRank]))
	}
	if len(selectedPath) == 0 && len(results) > 0 {
		selectedPath = cmdb.PathResourceTypes(results[0].Path)
	}
	if len(selectedPath) == 0 && lastSuccess >= 0 {
		selectedPath = resourceTypesToPath(resourcePathToResourceTypes(paths[lastSuccess]))
	}

	result = timeGraphRangeResult{
		source:             cmdb.Resource(req.SourceType),
		sourceMatcher:      cmdb.Matcher(req.SourceInfo),
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
