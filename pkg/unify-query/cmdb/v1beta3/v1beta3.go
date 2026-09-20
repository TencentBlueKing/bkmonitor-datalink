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
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	defaultModel *Model
	modelMutex   sync.Mutex
)

// Model is the v1beta3 TimeGraph relation model. RelationDefinition and the
// relation metric schema are the only sources of graph topology; query
// execution is delegated to the TSDB-backed TimeGraph implementation.
type Model struct {
	timeGraphResolver func(context.Context, string) (cmdb.CMDB, error)
	// timeGraphVMQuery is only set by package tests. It replaces the final VM
	// call after the normal QueryTs preparation and PromQL rendering, so
	// public-entry tests can keep path planning and query rendering real without
	// requiring a VM.
	timeGraphVMQuery timeGraphVMQuery
	// timeGraphQueryReference is a test-only replacement for metadata routing.
	// The surrounding preparation (ToTime, SetExpand and ToPromExpr) remains the
	// same as production; tests only avoid depending on live route metadata.
	timeGraphQueryReference timeGraphQueryReference
	schemaProvider          SchemaProvider
	schemaProviderMu        sync.RWMutex
}

// GetModel returns the serving model used by the v1beta3 HTTP handlers.
func GetModel(ctx context.Context) (cmdb.CMDB, error) {
	modelMutex.Lock()
	defer modelMutex.Unlock()
	if defaultModel == nil {
		model, err := NewModel(ctx)
		if err != nil {
			return nil, err
		}
		model.SetTimeGraphResolver(func(_ context.Context, _ string) (cmdb.CMDB, error) {
			return model, nil
		})
		defaultModel = model
	}
	return defaultModel, nil
}

// NewModel creates a TimeGraph-only model.
func NewModel(_ context.Context) (*Model, error) {
	return &Model{schemaProvider: GetSchemaProvider()}, nil
}

// SetTimeGraphResolver injects the TimeGraph implementation. It is primarily
// used by tests; production uses the model itself as the resolver target.
func (m *Model) SetTimeGraphResolver(resolver func(context.Context, string) (cmdb.CMDB, error)) {
	m.timeGraphResolver = resolver
}

// SetSchemaProvider injects the RelationDefinition-backed schema provider.
func (m *Model) SetSchemaProvider(provider SchemaProvider) {
	if provider == nil {
		return
	}
	m.schemaProviderMu.Lock()
	m.schemaProvider = provider
	m.schemaProviderMu.Unlock()
}

func (m *Model) getSchemaProvider() SchemaProvider {
	m.schemaProviderMu.RLock()
	provider := m.schemaProvider
	m.schemaProviderMu.RUnlock()
	if provider == nil {
		return GetSchemaProvider()
	}
	return provider
}

// QueryResourceMatcher implements the instant relation API through TimeGraph.
func (m *Model) QueryResourceMatcher(
	ctx context.Context,
	lookBackDelta, spaceUID, timestamp string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (resSource cmdb.Resource, resIndexMatcher cmdb.Matcher, resPaths []string, resTarget cmdb.Resource, resMatchers cmdb.Matchers, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-query-resource-matcher")
	defer span.End(&err)

	started := time.Now()
	metric.CMDBRelationRouteInc(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeInstant, metric.CMDBRelationResultStarted)
	defer func() {
		result := metric.CMDBRelationResultSuccess
		if err != nil {
			result = metric.CMDBRelationResultFailed
		} else if len(resMatchers) == 0 {
			result = metric.CMDBRelationResultEmpty
		}
		metric.CMDBRelationRouteInc(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeInstant, result)
		metric.CMDBRelationRouteSecond(ctx, time.Since(started), metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeInstant)
		metric.CMDBRelationTargetCountObserve(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeInstant, "all_paths", len(resMatchers))
	}()

	result, err := m.queryResourceMatcherWithTimeGraph(
		ctx, lookBackDelta, spaceUID, timestamp, target, source,
		indexMatcher, expandMatcher, expandShow, pathResource,
	)
	if err != nil {
		span.Set("failure-stage", "timegraph-query")
		return "", nil, nil, "", nil, err
	}
	span.Set("relation-backend", metric.CMDBRelationRouteTimeGraph)
	span.Set("query-mode", metric.CMDBRelationQueryModeInstant)
	span.Set("candidate-path-count", result.candidatePathCount)
	span.Set("raw-result-count", result.rawResultCount)
	span.Set("target-count", len(result.matchers))
	return result.source, result.sourceMatcher, result.paths, result.target, result.matchers, nil
}

// QueryResourceMatcherRange implements the range relation API through
// TimeGraph while preserving the v1beta3 response shape.
func (m *Model) QueryResourceMatcherRange(
	ctx context.Context,
	lookBackDelta, spaceUID, step string,
	startTimestamp, endTimestamp string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (resSource cmdb.Resource, resIndexMatcher cmdb.Matcher, resPaths []string, resTarget cmdb.Resource, result []cmdb.MatchersWithTimestamp, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-query-resource-matcher-range")
	defer span.End(&err)

	started := time.Now()
	metric.CMDBRelationRouteInc(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeRange, metric.CMDBRelationResultStarted)
	defer func() {
		status := metric.CMDBRelationResultSuccess
		if err != nil {
			status = metric.CMDBRelationResultFailed
		} else if len(result) == 0 {
			status = metric.CMDBRelationResultEmpty
		}
		metric.CMDBRelationRouteInc(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeRange, status)
		metric.CMDBRelationRouteSecond(ctx, time.Since(started), metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeRange)
		targetCount := 0
		for _, bucket := range result {
			targetCount += len(bucket.Matchers)
		}
		metric.CMDBRelationTargetCountObserve(ctx, metric.CMDBRelationRouteTimeGraph, metric.CMDBRelationQueryModeRange, "all_paths", targetCount)
	}()

	timeGraphResult, err := m.queryResourceMatcherRangeWithTimeGraph(
		ctx, lookBackDelta, spaceUID, step, startTimestamp, endTimestamp,
		target, source, indexMatcher, expandMatcher, expandShow, pathResource,
	)
	if err != nil {
		span.Set("failure-stage", "timegraph-query")
		return "", nil, nil, "", nil, err
	}
	span.Set("relation-backend", metric.CMDBRelationRouteTimeGraph)
	span.Set("query-mode", metric.CMDBRelationQueryModeRange)
	span.Set("candidate-path-count", timeGraphResult.candidatePathCount)
	span.Set("raw-result-count", timeGraphResult.rawResultCount)
	span.Set("bucket-count", timeGraphResult.bucketCount)
	span.Set("target-count", timeGraphResult.targetCount)
	return timeGraphResult.source, timeGraphResult.sourceMatcher, timeGraphResult.paths, timeGraphResult.target, timeGraphResult.matchers, nil
}

func toResourceTypes(resources []cmdb.Resource) []ResourceType {
	if len(resources) == 0 {
		return nil
	}
	result := make([]ResourceType, len(resources))
	for i, resource := range resources {
		result[i] = ResourceType(resource)
	}
	return result
}

func parseTimestamp(ts string) (int64, error) {
	if ts == "" {
		return time.Now().UnixMilli(), nil
	}
	value, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, fmt.Errorf("timestamp must be greater than or equal to 0, got %q", ts)
	}
	if value < 1e12 {
		return value * 1000, nil
	}
	return value, nil
}

func parseStep(step string) (int64, error) {
	if step == "" {
		return 60000, nil
	}
	duration, err := time.ParseDuration(step)
	if err != nil {
		return 0, err
	}
	stepMs := duration.Milliseconds()
	if stepMs <= 0 {
		return 0, fmt.Errorf("step must be greater than 0, got %q", step)
	}
	return stepMs, nil
}

func validateRangeBuckets(start, end, stepMs int64) (int, error) {
	if end < start {
		return 0, fmt.Errorf("start_time must be less than or equal to end_time")
	}
	if stepMs <= 0 {
		return 0, fmt.Errorf("step must be greater than 0")
	}
	maxPoints := effectiveMaxRangePoints()
	distance := uint64(end) - uint64(start)
	quotient := distance / uint64(stepMs)
	if quotient >= uint64(maxPoints) {
		return 0, fmt.Errorf("range query has more than %d points", maxPoints)
	}
	return int(quotient) + 1, nil
}

func sourcePrimaryKeySubset(req *QueryRequest, provider SchemaProvider) map[string]string {
	if req == nil {
		return nil
	}
	if len(req.SourceInfo) == 0 || provider == nil {
		return req.SourceInfo
	}
	result := make(map[string]string)
	for _, key := range provider.GetResourcePrimaryKeys(req.SchemaNamespace(), req.SourceType) {
		if value, ok := req.SourceInfo[key]; ok {
			result[key] = value
		}
	}
	return result
}

func validateSourceExpandInfoFields(req *QueryRequest, provider SchemaProvider) error {
	if req == nil || len(req.SourceExpandInfo) == 0 {
		return nil
	}
	if provider == nil {
		provider = GetSchemaProvider()
	}
	allowed := make(map[string]struct{})
	for _, field := range provider.GetResourceFields(req.SchemaNamespace(), req.SourceType) {
		allowed[field] = struct{}{}
	}
	for field := range req.SourceExpandInfo {
		if _, ok := allowed[field]; !ok {
			return fmt.Errorf("unknown source_expand_info field %q for source type %q", field, req.SourceType)
		}
	}
	return nil
}

// inferSourceTypeFromInfo preserves the legacy v1beta3 contract where the
// source type may be omitted and is resolved from source_info's primary key
// tuple. The candidate with the most complete primary-key match wins, which is
// deterministic for the common case and keeps old clients working when a
// namespace contains resources with different key shapes.
func inferSourceTypeFromInfo(req *QueryRequest, provider SchemaProvider) (ResourceType, error) {
	if req == nil || provider == nil {
		return "", fmt.Errorf("cannot infer source type without schema provider")
	}
	info := make(map[string]string, len(req.SourceInfo)+len(req.SourceExpandInfo))
	for key, value := range req.SourceInfo {
		info[key] = value
	}
	for key, value := range req.SourceExpandInfo {
		info[key] = value
	}
	if len(info) == 0 {
		return "", fmt.Errorf("source type cannot be inferred from empty source_info")
	}
	known := make(map[ResourceType]struct{})
	for _, candidate := range provider.ListResourceTypes(req.SchemaNamespace()) {
		known[candidate] = struct{}{}
	}
	for _, schema := range provider.ListRelationSchemas(req.SchemaNamespace()) {
		known[schema.FromType] = struct{}{}
		known[schema.ToType] = struct{}{}
	}
	candidates := make([]ResourceType, 0, len(known))
	for candidate := range known {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
	var best ResourceType
	bestScore := -1
	for _, candidate := range candidates {
		keys := provider.GetResourcePrimaryKeys(req.SchemaNamespace(), candidate)
		if len(keys) == 0 {
			continue
		}
		matched := 0
		for _, key := range keys {
			if _, ok := info[key]; ok {
				matched++
			}
		}
		if matched != len(keys) || matched <= bestScore {
			continue
		}
		best = candidate
		bestScore = matched
	}
	if best == "" {
		return "", fmt.Errorf("cannot infer source_type from source_info %v", req.SourceInfo)
	}
	return best, nil
}

// adjustMaxHopsForUnconstrainedPath lets the server discover paths longer
// than the historical two-hop default, while still enforcing the hard safety
// ceiling configured by MaxAllowedHops.
func adjustMaxHopsForUnconstrainedPath(req *QueryRequest, provider SchemaProvider) error {
	if req == nil || provider == nil || len(req.PathResource) > 0 || req.SourceType == "" || req.TargetType == "" || req.SourceType == req.TargetType {
		return nil
	}
	if req.MaxHops >= MaxAllowedHops {
		return nil
	}
	pathFinder := NewPathFinder(
		WithAllowedCategories(req.AllowedRelationTypes...),
		WithDynamicDirection(req.DynamicRelationDirection),
		WithMaxHops(MaxAllowedHops),
		WithSchemaProvider(provider),
		WithNamespace(req.SchemaNamespace()),
	)
	paths, err := pathFinder.FindAllPaths(req.SourceType, req.TargetType, nil)
	if err != nil {
		return nil
	}
	for _, path := range paths {
		hops := len(path.Steps) - 1
		if hops > req.MaxHops {
			req.MaxHops = hops
		}
	}
	if req.MaxHops > MaxAllowedHops {
		req.MaxHops = MaxAllowedHops
	}
	return nil
}

func validateSchemaProvider(provider SchemaProvider, namespace string, resourceTypes ...ResourceType) error {
	if provider == nil {
		return fmt.Errorf("schema provider is not configured")
	}
	validator, ok := provider.(SchemaProviderValidator)
	if !ok {
		return nil
	}
	if err := validator.ValidateSchema(namespace, resourceTypes...); err != nil {
		return fmt.Errorf("schema provider failed: %w", err)
	}
	return nil
}

func filterTargetMatcher(
	labels map[string]string,
	provider SchemaProvider,
	namespace string,
	targetType ResourceType,
	targetInfoShow bool,
) cmdb.Matcher {
	if labels == nil {
		return nil
	}
	if provider == nil {
		provider = GetSchemaProvider()
	}
	fields := provider.GetResourcePrimaryKeys(namespace, targetType)
	if targetInfoShow {
		fields = provider.GetResourceFields(namespace, targetType)
	}
	if len(fields) == 0 {
		matcher := make(cmdb.Matcher, len(labels))
		for key, value := range labels {
			matcher[key] = value
		}
		return matcher
	}
	matcher := make(cmdb.Matcher, len(fields))
	for _, field := range fields {
		if value, ok := labels[field]; ok {
			matcher[field] = value
		}
	}
	return matcher
}

func validateTargetCount(count int) error {
	limit := effectiveMaxTargets()
	if count <= limit {
		return nil
	}
	return &ResultLimitError{Reason: "max_targets", Count: count, Limit: limit}
}

func validateRangeTargetCounts(result []cmdb.MatchersWithTimestamp) (int64, error) {
	for _, bucket := range result {
		if err := validateTargetCount(len(bucket.Matchers)); err != nil {
			return bucket.Timestamp, err
		}
	}
	return 0, nil
}

func matcherToMap(matcher cmdb.Matcher) map[string]string {
	if matcher == nil {
		return nil
	}
	result := make(map[string]string, len(matcher))
	for key, value := range matcher {
		result[key] = value
	}
	return result
}

func computeMaxHops(source, target cmdb.Resource, pathResource []cmdb.Resource) int {
	if len(pathResource) == 0 {
		return DefaultMaxHops
	}
	pathConstraint, directOnly := normalizePathResource(FromCMDBResource(source), FromCMDBResource(target), toResourceTypes(pathResource))
	if directOnly || len(pathConstraint) == 0 {
		return DefaultMaxHops
	}
	maxHops := DefaultMaxHops + len(pathConstraint) + 1
	if maxHops > MaxAllowedHops {
		return MaxAllowedHops
	}
	return maxHops
}

func resourceTypesToPath(resources []ResourceType) []string {
	result := make([]string, 0, len(resources))
	for _, resource := range resources {
		if resource != "" {
			result = append(result, string(resource))
		}
	}
	return result
}

func FromCMDBResource(resource cmdb.Resource) ResourceType {
	return ResourceType(resource)
}
