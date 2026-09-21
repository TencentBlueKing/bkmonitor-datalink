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

// Model 是 v1beta3 TimeGraph 关系模型。图拓扑只来自 RelationDefinition 和
// 关系指标 schema，查询执行委托给基于 TSDB 的 TimeGraph 实现。
type Model struct {
	timeGraphResolver func(context.Context, string) (cmdb.CMDB, error)
	// timeGraphVMQuery 仅由包内测试设置。它替换 QueryTs 准备和 PromQL 渲染之后
	// 的最后一次 VM 调用，使公开入口测试无需连接 VM 也能覆盖真实的路径规划和
	// 查询渲染。
	timeGraphVMQuery timeGraphVMQuery
	// timeGraphQueryReference 仅用于测试，替代 metadata 路由解析。周围的
	// ToTime、SetExpand 和 ToPromExpr 流程与生产保持一致，测试只绕过实时路由元数据。
	timeGraphQueryReference timeGraphQueryReference
	schemaProvider          SchemaProvider
	schemaProviderMu        sync.RWMutex
}

// GetModel 返回 v1beta3 HTTP handler 使用的服务模型。
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

// NewModel 创建只提供 TimeGraph 能力的模型。
func NewModel(_ context.Context) (*Model, error) {
	return &Model{schemaProvider: GetSchemaProvider()}, nil
}

// SetTimeGraphResolver 注入 TimeGraph 实现，主要用于测试；生产环境以模型自身
// 作为 resolver 目标。
func (m *Model) SetTimeGraphResolver(resolver func(context.Context, string) (cmdb.CMDB, error)) {
	m.timeGraphResolver = resolver
}

// SetSchemaProvider 注入由 RelationDefinition 支持的 schema provider。
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

// QueryResourceMatcher 通过 TimeGraph 实现 instant 关系查询接口。
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

// QueryResourceMatcherRange 通过 TimeGraph 实现 range 关系查询接口，同时保持
// v1beta3 的响应结构不变。
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

func parseStepDuration(step string) (time.Duration, error) {
	// 旧接口允许省略 step，统一按一分钟处理，并把规范化后的 duration 继续
	// 传给底层查询，避免校验和实际执行使用两套步长。
	if step == "" {
		return time.Minute, nil
	}
	duration, err := time.ParseDuration(step)
	if err != nil {
		return 0, err
	}
	if duration <= 0 {
		return 0, fmt.Errorf("step must be positive")
	}
	return duration, nil
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

// inferSourceTypeFromInfo 保留 v1beta3 旧契约：允许省略 source type，并根据
// source_info 中的主键元组推断资源类型。优先选择主键匹配最完整的候选；在常见
// 情况下结果是确定的，也能兼容同一 namespace 中主键结构不同的旧客户端。
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

// adjustMaxHopsForUnconstrainedPath 允许服务端发现超过历史两跳默认值的路径，
// 同时仍然执行硬性的安全上限约束，最大跳数由 MaxAllowedHops 配置。
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
