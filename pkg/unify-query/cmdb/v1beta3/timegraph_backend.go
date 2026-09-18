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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
)

// timeGraphQuerier is implemented by the v1beta1 model's TSDB-backed
// TimeGraph extension. It is intentionally kept as a small adapter contract so
// v1beta3 owns request normalization and path planning while TimeGraph owns
// relation metric reads and in-memory traversal.
type timeGraphQuerier interface {
	QueryPathResources(context.Context, string, string, string, cmdb.Resource, []cmdb.Resource, [][]cmdb.Resource, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
	QueryPathResourcesRange(context.Context, string, string, string, string, string, cmdb.Resource, []cmdb.Resource, [][]cmdb.Resource, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
}

type relationPathTimeGraphQuerier interface {
	QueryRelationPathResources(context.Context, string, string, string, cmdb.Resource, []cmdb.Resource, []cmdb.RelationPath, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
	QueryRelationPathResourcesRange(context.Context, string, string, string, string, string, cmdb.Resource, []cmdb.Resource, []cmdb.RelationPath, cmdb.Matcher) ([]cmdb.PathResourcesResult, error)
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

func (m *Model) getTimeGraphQuerier(ctx context.Context, spaceUID string) (timeGraphQuerier, error) {
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
	return querier, nil
}

func (m *Model) buildTimeGraphRequest(
	spaceUID string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (*QueryRequest, []resourcePath, error) {
	if source == "" || target == "" {
		return nil, nil, fmt.Errorf("timegraph backend requires explicit source_type and target_type")
	}
	if len(expandMatcher) > 0 {
		return nil, nil, fmt.Errorf("timegraph backend does not support source_expand_info")
	}

	req := &QueryRequest{
		SpaceUID:            spaceUID,
		SourceType:          FromCMDBResource(source),
		SourceInfo:          matcherToMap(indexMatcher.Rename()),
		TargetType:          FromCMDBResource(target),
		TargetTypeExplicit:  true,
		TargetInfoShow:      expandShow,
		PathResource:        toResourceTypes(pathResource),
		MaxHops:             computeMaxHops(source, target, pathResource),
		LegacyCompatibility: true,
		DisableRootLimit:    true,
	}
	req.Normalize()

	provider := m.getSchemaProvider()
	if err := validateSchemaProvider(provider, req.SchemaNamespace()); err != nil {
		return nil, nil, err
	}
	// Keep the same compatibility behavior as the existing v1beta3 legacy API:
	// only primary-key fields identify the source resource.
	req.SourceInfo = sourcePrimaryKeySubset(req, provider)

	pathFinder := NewPathFinder(
		WithAllowedCategories(req.AllowedRelationTypes...),
		WithDynamicDirection(req.DynamicRelationDirection),
		WithMaxHops(req.MaxHops),
		WithSchemaProvider(provider),
		WithNamespace(req.SchemaNamespace()),
	)
	paths, err := pathFinder.FindAllPaths(req.SourceType, req.TargetType, req.PathResource)
	if err != nil {
		return nil, nil, err
	}
	return req, paths, nil
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
	timestampMs, err := parseTimestamp(ts)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(timestampMs/1000, 10), nil
}

func timeGraphPathRank(path []cmdb.PathNode, candidates [][]cmdb.Resource) int {
	if len(path) == 0 {
		return len(candidates)
	}
	for index, candidate := range candidates {
		if len(candidate) != len(path) {
			continue
		}
		matched := true
		for step, node := range path {
			if node.ResourceType != candidate[step] {
				matched = false
				break
			}
		}
		if matched {
			return index
		}
	}
	return len(candidates)
}

func timeGraphPathTypes(path []cmdb.PathNode) []string {
	result := make([]string, 0, len(path))
	for _, node := range path {
		result = append(result, string(node.ResourceType))
	}
	return result
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
	key := GenerateResourceID(targetType, map[string]string(target.Dimensions))
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
	defer func() {
		if result.candidatePathCount > 0 {
			metric.CMDBRelationCandidatePathCountObserve(
				ctx,
				metric.CMDBRelationRouteTimeGraph,
				metric.CMDBRelationQueryModeInstant,
				"all_paths",
				result.candidatePathCount,
			)
		}
		metric.CMDBRelationTimeGraphResultCountObserve(ctx, metric.CMDBRelationQueryModeInstant, result.rawResultCount)
		pathResult := metric.CMDBRelationResultSuccess
		if err != nil {
			pathResult = metric.CMDBRelationResultFailed
		} else if result.rawResultCount == 0 {
			pathResult = metric.CMDBRelationResultEmpty
		}
		metric.CMDBRelationPathResultInc(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeInstant, pathResult)
	}()

	req, paths, err := m.buildTimeGraphRequest(spaceUID, target, source, indexMatcher, expandMatcher, expandShow, pathResource)
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

	candidatePaths := resourcePathsToTimeGraphPaths(paths)
	result.candidatePathCount = len(candidatePaths)
	var results []cmdb.PathResourcesResult
	if relationQuerier, ok := querier.(relationPathTimeGraphQuerier); ok {
		results, err = relationQuerier.QueryRelationPathResources(
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
		results, err = querier.QueryPathResources(
			ctx,
			lookBackDelta,
			spaceUID,
			queryTimestamp,
			cmdb.Resource(req.SourceType),
			[]cmdb.Resource{cmdb.Resource(req.TargetType)},
			candidatePaths,
			cmdb.Matcher(req.SourceInfo),
		)
	}
	if err != nil {
		return timeGraphLegacyResult{}, err
	}
	result.rawResultCount = len(results)

	provider := m.getSchemaProvider()
	bestRank := len(candidatePaths)
	matchersByID := make(map[string]cmdb.Matcher)
	for _, result := range results {
		rank := timeGraphPathRank(result.Path, candidatePaths)
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

	selectedPath := []string(nil)
	if bestRank < len(paths) {
		selectedPath = resourceTypesToPath(resourcePathToResourceTypes(paths[bestRank]))
	}
	if len(selectedPath) == 0 && len(results) > 0 {
		selectedPath = timeGraphPathTypes(results[0].Path)
	}
	if len(selectedPath) == 0 && len(paths) > 0 {
		selectedPath = resourceTypesToPath(resourcePathToResourceTypes(paths[0]))
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
	defer func() {
		if result.candidatePathCount > 0 {
			metric.CMDBRelationCandidatePathCountObserve(
				ctx,
				metric.CMDBRelationRouteTimeGraph,
				metric.CMDBRelationQueryModeRange,
				"all_paths",
				result.candidatePathCount,
			)
		}
		metric.CMDBRelationTimeGraphResultCountObserve(ctx, metric.CMDBRelationQueryModeRange, result.rawResultCount)
		metric.CMDBRelationTimeGraphBucketCountObserve(ctx, metric.CMDBRelationQueryModeRange, result.bucketCount)
		pathResult := metric.CMDBRelationResultSuccess
		if err != nil {
			pathResult = metric.CMDBRelationResultFailed
		} else if result.rawResultCount == 0 {
			pathResult = metric.CMDBRelationResultEmpty
		}
		metric.CMDBRelationPathResultInc(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeRange, pathResult)
	}()

	req, paths, err := m.buildTimeGraphRequest(spaceUID, target, source, indexMatcher, expandMatcher, expandShow, pathResource)
	if err != nil {
		return timeGraphRangeResult{}, err
	}
	startMs, err := parseTimestamp(startTimestamp)
	if err != nil {
		return timeGraphRangeResult{}, fmt.Errorf("parse TimeGraph start timestamp: %w", err)
	}
	endMs, err := parseTimestamp(endTimestamp)
	if err != nil {
		return timeGraphRangeResult{}, fmt.Errorf("parse TimeGraph end timestamp: %w", err)
	}
	stepMs, err := parseStep(step)
	if err != nil {
		return timeGraphRangeResult{}, err
	}
	if _, err := validateRangeBuckets(startMs, endMs, stepMs); err != nil {
		return timeGraphRangeResult{}, err
	}
	start, err := timeGraphQueryTimestamp(startTimestamp)
	if err != nil {
		return timeGraphRangeResult{}, err
	}
	end, err := timeGraphQueryTimestamp(endTimestamp)
	if err != nil {
		return timeGraphRangeResult{}, err
	}
	querier, err := m.getTimeGraphQuerier(ctx, spaceUID)
	if err != nil {
		return timeGraphRangeResult{}, err
	}

	candidatePaths := resourcePathsToTimeGraphPaths(paths)
	result.candidatePathCount = len(candidatePaths)
	var results []cmdb.PathResourcesResult
	if relationQuerier, ok := querier.(relationPathTimeGraphQuerier); ok {
		results, err = relationQuerier.QueryRelationPathResourcesRange(
			ctx,
			lookBackDelta,
			spaceUID,
			step,
			start,
			end,
			cmdb.Resource(req.SourceType),
			[]cmdb.Resource{cmdb.Resource(req.TargetType)},
			resourcePathsToTimeGraphRelationPaths(paths),
			cmdb.Matcher(req.SourceInfo),
		)
	} else {
		results, err = querier.QueryPathResourcesRange(
			ctx,
			lookBackDelta,
			spaceUID,
			step,
			start,
			end,
			cmdb.Resource(req.SourceType),
			[]cmdb.Resource{cmdb.Resource(req.TargetType)},
			candidatePaths,
			cmdb.Matcher(req.SourceInfo),
		)
	}
	if err != nil {
		return timeGraphRangeResult{}, err
	}
	result.rawResultCount = len(results)

	provider := m.getSchemaProvider()
	bestRank := len(candidatePaths)
	timeSeries := make(map[int64]map[string]cmdb.Matcher)
	for _, result := range results {
		rank := timeGraphPathRank(result.Path, candidatePaths)
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
		selectedPath = timeGraphPathTypes(results[0].Path)
	}
	if len(selectedPath) == 0 && len(paths) > 0 {
		selectedPath = resourceTypesToPath(resourcePathToResourceTypes(paths[0]))
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
