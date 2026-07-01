// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

var (
	defaultModel *Model
	modelMutex   sync.Mutex
)

func GetModel(ctx context.Context) (cmdb.CMDB, error) {
	modelMutex.Lock()
	defer modelMutex.Unlock()
	if defaultModel == nil {
		client := NewBKBaseSurrealDBClient()
		model, err := NewModel(ctx, client)
		if err != nil {
			return nil, err
		}
		// 为默认 Model 注入 binding 解析器
		model.SetResolver(GetBindingResolver())
		defaultModel = model
	}
	return defaultModel, nil
}

type GraphQueryExecutor interface {
	Execute(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error)
}

// GraphQueryExecutorWithBinding 是 GraphQueryExecutor 的可选扩展，
// 允许 executor 接受 binding 上下文（database / namespace）。
// BKBaseSurrealDBClient 实现了这个扩展接口。
type GraphQueryExecutorWithBinding interface {
	GraphQueryExecutor
	ExecuteWithBinding(ctx context.Context, spaceUID string, binding BindingInfo, dsl string, start, end int64) ([]*LivenessGraph, error)
}

// Model v2 CMDB 实现，基于 SurrealDB 图查询
type Model struct {
	executor         GraphQueryExecutor
	resolver         *BindingResolver
	schemaProvider   SchemaProvider
	schemaProviderMu sync.RWMutex
}

// NewModel 创建 Model 实例。resolver 可由调用方后续通过 SetResolver 注入。
func NewModel(ctx context.Context, executor GraphQueryExecutor) (*Model, error) {
	return &Model{executor: executor, schemaProvider: GetSchemaProvider()}, nil
}

// SetExecutor 设置查询执行器（用于测试）
func (m *Model) SetExecutor(executor GraphQueryExecutor) {
	m.executor = executor
}

// SetResolver 注入 binding 解析器（生产路径用；测试可以留空）
func (m *Model) SetResolver(resolver *BindingResolver) {
	m.resolver = resolver
}

// SetSchemaProvider injects the schema used by validation, path discovery and
// SQL generation. Passing nil keeps the current provider unchanged.
func (m *Model) SetSchemaProvider(provider SchemaProvider) {
	if provider != nil {
		m.schemaProviderMu.Lock()
		defer m.schemaProviderMu.Unlock()
		m.schemaProvider = provider
	}
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

func (m *Model) QueryResourceMatcher(
	ctx context.Context,
	lookBackDelta, spaceUid string,
	ts string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (resSource cmdb.Resource, resIndexMatcher cmdb.Matcher, resPaths []string, resTarget cmdb.Resource, resMatchers cmdb.Matchers, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-query-resource-matcher")
	defer span.End(&err)

	span.Set("space-uid", spaceUid)
	span.Set("timestamp", ts)
	span.Set("look-back-delta", lookBackDelta)
	span.Set("source", source)
	span.Set("target", target)
	span.Set("index-matcher", indexMatcher)
	span.Set("path-resource", pathResource)

	timestamp, err := parseTimestamp(ts)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	lbd, err := parseLookBackDelta(lookBackDelta)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	req := &QueryRequest{
		SpaceUID:           spaceUid,
		Timestamp:          timestamp,
		SourceType:         FromCMDBResource(source),
		SourceInfo:         matcherToMap(indexMatcher.Rename()),
		SourceExpandInfo:   matcherToMap(expandMatcher),
		TargetType:         FromCMDBResource(target),
		TargetTypeExplicit: target != "",
		TargetInfoShow:     expandShow,
		PathResource:       toResourceTypes(pathResource),
		MaxHops:            computeMaxHops(pathResource),
		LookBackDelta:      lbd,
	}
	req.Normalize()

	_, pathsV2, matchers, err := m.QueryLivenessGraph(ctx, req)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	paths := convertPathsV2ToStrings(pathsV2)

	span.Set("paths-count", len(paths))
	span.Set("matchers-count", len(matchers))

	return cmdb.Resource(req.SourceType), cmdb.Matcher(req.SourceInfo), paths, cmdb.Resource(req.TargetType), matchers, nil
}

// QueryDynamicPaths 实现 cmdb.CMDB 接口（instant 查询），返回 []PathV2
func (m *Model) QueryDynamicPaths(
	ctx context.Context,
	lookBackDelta, spaceUid string,
	ts string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (resSource cmdb.Resource, resIndexMatcher cmdb.Matcher, resPaths []cmdb.PathV2, resTarget cmdb.Resource, resMatchers cmdb.Matchers, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-v2-query-dynamic-paths")
	defer span.End(&err)

	span.Set("space-uid", spaceUid)
	span.Set("timestamp", ts)
	span.Set("look-back-delta", lookBackDelta)
	span.Set("source", source)
	span.Set("target", target)
	span.Set("index-matcher", indexMatcher)
	span.Set("path-resource", pathResource)

	timestamp, err := parseTimestamp(ts)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	lbd, err := parseLookBackDelta(lookBackDelta)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	req := &QueryRequest{
		SpaceUID:           spaceUid,
		Timestamp:          timestamp,
		SourceType:         FromCMDBResource(source),
		SourceInfo:         matcherToMap(indexMatcher.Rename()),
		SourceExpandInfo:   matcherToMap(expandMatcher),
		TargetType:         FromCMDBResource(target),
		TargetTypeExplicit: target != "",
		TargetInfoShow:     expandShow,
		PathResource:       toResourceTypes(pathResource),
		MaxHops:            computeMaxHops(pathResource),
		LookBackDelta:      lbd,
	}
	req.Normalize()

	_, paths, matchers, err := m.QueryLivenessGraph(ctx, req)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	span.Set("paths-count", len(paths))
	span.Set("matchers-count", len(matchers))

	return cmdb.Resource(req.SourceType), cmdb.Matcher(req.SourceInfo), paths, cmdb.Resource(req.TargetType), matchers, nil
}

// QueryResourceMatcherRange 实现 cmdb.CMDB 接口（range 查询）
func (m *Model) QueryResourceMatcherRange(
	ctx context.Context,
	lookBackDelta, spaceUid string,
	step string,
	startTs, endTs string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (resSource cmdb.Resource, resIndexMatcher cmdb.Matcher, resPaths []string, resTarget cmdb.Resource, result []cmdb.MatchersWithTimestamp, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-query-resource-matcher-range")
	defer span.End(&err)

	span.Set("space-uid", spaceUid)
	span.Set("start-ts", startTs)
	span.Set("end-ts", endTs)
	span.Set("step", step)
	span.Set("look-back-delta", lookBackDelta)
	span.Set("source", source)
	span.Set("target", target)
	span.Set("index-matcher", indexMatcher)
	span.Set("path-resource", pathResource)

	start, err := parseTimestamp(startTs)
	if err != nil {
		return "", nil, nil, "", nil, err
	}
	end, err := parseTimestamp(endTs)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	lbd, err := parseLookBackDelta(lookBackDelta)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	stepMs, err := parseStep(step)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	req := &QueryRequest{
		SpaceUID:           spaceUid,
		Timestamp:          end,
		SourceType:         FromCMDBResource(source),
		SourceInfo:         matcherToMap(indexMatcher.Rename()),
		SourceExpandInfo:   matcherToMap(expandMatcher),
		TargetType:         FromCMDBResource(target),
		TargetTypeExplicit: target != "",
		TargetInfoShow:     expandShow,
		PathResource:       toResourceTypes(pathResource),
		MaxHops:            computeMaxHops(pathResource),
		LookBackDelta:      maxInt64(lbd, end-start),
	}
	req.Normalize()

	graphs, pathsV2, _, err := m.QueryLivenessGraph(ctx, req)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	provider := m.getSchemaProvider()
	result = buildTargetMatchersTimeSeriesWithOptions(
		graphs,
		req.TargetType,
		targetExtractionPathResource(req),
		start,
		end,
		stepMs,
		provider,
		req.SchemaNamespace(),
		req.TargetInfoShow,
		shouldIncludeRootTarget(req),
	)

	paths := convertPathsV2ToStrings(pathsV2)

	span.Set("paths-count", len(paths))
	span.Set("result-count", len(result))

	return cmdb.Resource(req.SourceType), cmdb.Matcher(req.SourceInfo), paths, cmdb.Resource(req.TargetType), result, nil
}

// QueryDynamicPathsRange 实现 cmdb.CMDB 接口（range 查询），返回 []PathV2
func (m *Model) QueryDynamicPathsRange(
	ctx context.Context,
	lookBackDelta, spaceUid string,
	step string,
	startTs, endTs string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (resSource cmdb.Resource, resIndexMatcher cmdb.Matcher, resPaths []cmdb.PathV2, resTarget cmdb.Resource, result []cmdb.MatchersWithTimestamp, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-v2-query-dynamic-paths-range")
	defer span.End(&err)

	span.Set("space-uid", spaceUid)
	span.Set("start-ts", startTs)
	span.Set("end-ts", endTs)
	span.Set("step", step)
	span.Set("look-back-delta", lookBackDelta)
	span.Set("source", source)
	span.Set("target", target)
	span.Set("index-matcher", indexMatcher)
	span.Set("path-resource", pathResource)

	start, err := parseTimestamp(startTs)
	if err != nil {
		return "", nil, nil, "", nil, err
	}
	end, err := parseTimestamp(endTs)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	lbd, err := parseLookBackDelta(lookBackDelta)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	stepMs, err := parseStep(step)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	req := &QueryRequest{
		SpaceUID:           spaceUid,
		Timestamp:          end,
		SourceType:         FromCMDBResource(source),
		SourceInfo:         matcherToMap(indexMatcher.Rename()),
		SourceExpandInfo:   matcherToMap(expandMatcher),
		TargetType:         FromCMDBResource(target),
		TargetTypeExplicit: target != "",
		TargetInfoShow:     expandShow,
		PathResource:       toResourceTypes(pathResource),
		MaxHops:            computeMaxHops(pathResource),
		LookBackDelta:      maxInt64(lbd, end-start),
	}
	req.Normalize()

	graphs, paths, _, err := m.QueryLivenessGraph(ctx, req)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	provider := m.getSchemaProvider()
	result = buildTargetMatchersTimeSeriesWithOptions(
		graphs,
		req.TargetType,
		targetExtractionPathResource(req),
		start,
		end,
		stepMs,
		provider,
		req.SchemaNamespace(),
		req.TargetInfoShow,
		shouldIncludeRootTarget(req),
	)

	span.Set("paths-count", len(paths))
	span.Set("result-count", len(result))

	return cmdb.Resource(req.SourceType), cmdb.Matcher(req.SourceInfo), paths, cmdb.Resource(req.TargetType), result, nil
}

// QueryLivenessGraph 执行图查询，返回图数据、路径和目标 Matchers
func (m *Model) QueryLivenessGraph(ctx context.Context, req *QueryRequest) (graphs []*LivenessGraph, paths []cmdb.PathV2, matchers cmdb.Matchers, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-v2-query-liveness-graph")
	defer span.End(&err)

	provider := m.getSchemaProvider()
	if req.SourceType == "" {
		sourceType, inferErr := inferSourceTypeFromInfo(req, provider)
		if inferErr != nil {
			return nil, nil, nil, inferErr
		}
		req.SourceType = sourceType
	}
	req.Normalize()
	implicitSelfTarget := !req.TargetTypeExplicit && req.SourceType == req.TargetType
	if implicitSelfTarget {
		req.MaxHops = 0
		req.PathResource = nil
	} else if isExplicitDirectSelfTarget(req) {
		req.MaxHops = 1
	}

	if err := validateQueryResources(req, provider); err != nil {
		return nil, nil, nil, err
	}

	span.Set("source-type", req.SourceType)
	span.Set("target-type", req.TargetType)
	span.Set("source-info", req.SourceInfo)
	span.Set("source-expand-info", req.SourceExpandInfo)
	span.Set("target-info-show", req.TargetInfoShow)
	span.Set("max-hops", req.MaxHops)
	span.Set("look-back-delta", req.LookBackDelta)
	span.Set("space-uid", req.SpaceUID)

	builder := NewSurrealQueryBuilderWithSchemaProvider(req, provider)
	if implicitSelfTarget {
		req.MaxHops = 0
	}
	sql := builder.Build()
	queryStart, queryEnd := req.GetQueryRange()

	span.Set("query-sql", sql)
	span.Set("query-start", queryStart)
	span.Set("query-end", queryEnd)

	pf := NewPathFinder(
		WithAllowedCategories(req.AllowedRelationTypes...),
		WithDynamicDirection(req.DynamicRelationDirection),
		WithMaxHops(req.MaxHops),
		WithSchemaProvider(provider),
		WithNamespace(req.SchemaNamespace()),
	)
	if implicitSelfTarget {
		paths = []cmdb.PathV2{{Steps: []cmdb.PathStepV2{{ResourceType: string(req.SourceType)}}}}
	} else {
		paths, err = pf.FindAllPaths(req.SourceType, req.TargetType, req.PathResource)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	if m.executor != nil {
		start := time.Now()
		graphs, err = m.executeGraphQuery(ctx, req, sql, queryStart, queryEnd)
		elapsed := time.Since(start).Seconds()
		status := "ok"
		if err != nil {
			status = CategorizeError(err)
			ObserveError(req.SpaceUID, status)
		}
		ObserveQueryDuration(req.SpaceUID, status, elapsed)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	matchers = extractMatchersFromGraphsWithOptions(
		graphs,
		req.TargetType,
		targetExtractionPathResource(req),
		provider,
		req.SchemaNamespace(),
		req.TargetInfoShow,
		shouldIncludeRootTarget(req),
	)

	span.Set("graphs-count", len(graphs))
	span.Set("paths-count", len(paths))
	span.Set("matchers-count", len(matchers))

	return graphs, paths, matchers, nil
}

// executeGraphQuery 根据 resolver / executor 能力选择最合适的调用路径。
//
//  1. 若同时具备 resolver 与支持 binding 的 executor，则先 resolve binding，
//     再走 ExecuteWithBinding，DSL 前会加 "USE NS ... DB ...;" 前缀。
//  2. 否则退化到原始 Execute（全局 result_table_id，单测 / 旧路径）。
func (m *Model) executeGraphQuery(ctx context.Context, req *QueryRequest, sql string, start, end int64) ([]*LivenessGraph, error) {
	if m.resolver != nil {
		if ex, ok := m.executor.(GraphQueryExecutorWithBinding); ok {
			if req.SpaceUID == "" {
				return nil, fmt.Errorf("space_uid is required for binding graph query")
			}
			binding, err := m.resolver.Resolve(ctx, req.SpaceUID)
			if err != nil {
				return nil, err
			}
			return ex.ExecuteWithBinding(ctx, req.SpaceUID, *binding, sql, start, end)
		}
	}
	return m.executor.Execute(ctx, sql, start, end)
}

// toResourceTypes 将 []cmdb.Resource 转换为 []ResourceType
func toResourceTypes(resources []cmdb.Resource) []ResourceType {
	if len(resources) == 0 {
		return nil
	}
	result := make([]ResourceType, len(resources))
	for i, r := range resources {
		result[i] = ResourceType(r)
	}
	return result
}

func parseTimestamp(ts string) (int64, error) {
	if ts == "" {
		return time.Now().UnixMilli(), nil
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return 0, err
	}
	if sec < 1e12 {
		return sec * 1000, nil
	}
	return sec, nil
}

func parseLookBackDelta(lbd string) (int64, error) {
	if lbd == "" {
		return DefaultLookBackDelta, nil
	}
	d, err := time.ParseDuration(lbd)
	if err != nil {
		return 0, err
	}
	return d.Milliseconds(), nil
}

func parseStep(step string) (int64, error) {
	if step == "" {
		return 60000, nil
	}
	d, err := time.ParseDuration(step)
	if err != nil {
		return 0, err
	}
	stepMs := d.Milliseconds()
	if stepMs <= 0 {
		return 0, fmt.Errorf("step must be greater than 0, got %q", step)
	}
	return stepMs, nil
}

func maxInt64(left, right int64) int64 {
	if left >= right {
		return left
	}
	return right
}

func shouldIncludeRootTarget(req *QueryRequest) bool {
	if req == nil {
		return true
	}
	return !req.TargetTypeExplicit || req.SourceType != req.TargetType
}

func isExplicitDirectSelfTarget(req *QueryRequest) bool {
	return req != nil && req.TargetTypeExplicit && req.SourceType == req.TargetType && len(req.PathResource) == 0
}

func targetExtractionPathResource(req *QueryRequest) []ResourceType {
	if isExplicitDirectSelfTarget(req) {
		return []ResourceType{req.SourceType}
	}
	return req.PathResource
}

func inferSourceTypeFromInfo(req *QueryRequest, provider SchemaProvider) (ResourceType, error) {
	if req == nil {
		return "", fmt.Errorf("query request cannot be nil")
	}
	if len(req.SourceInfo) == 0 {
		return "", fmt.Errorf("source type cannot be inferred from empty source_info")
	}
	if provider == nil {
		provider = GetSchemaProvider()
	}

	known := make(map[ResourceType]struct{})
	for _, schema := range provider.ListRelationSchemas(req.SchemaNamespace()) {
		known[schema.FromType] = struct{}{}
		known[schema.ToType] = struct{}{}
	}

	type sourceTypeCandidate struct {
		resourceType ResourceType
		keyCount     int
	}
	var candidates []sourceTypeCandidate
	for resourceType := range known {
		primaryKeys := provider.GetResourcePrimaryKeys(req.SchemaNamespace(), resourceType)
		if len(primaryKeys) == 0 {
			continue
		}
		matched := true
		for _, key := range primaryKeys {
			if _, ok := req.SourceInfo[key]; !ok {
				matched = false
				break
			}
		}
		if matched {
			candidates = append(candidates, sourceTypeCandidate{resourceType: resourceType, keyCount: len(primaryKeys)})
		}
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("source type cannot be inferred from source_info %v", req.SourceInfo)
	}
	best := candidates[0]
	ambiguous := false
	for _, candidate := range candidates[1:] {
		if candidate.keyCount > best.keyCount {
			best = candidate
			ambiguous = false
			continue
		}
		if candidate.keyCount == best.keyCount {
			ambiguous = true
		}
	}
	if !ambiguous {
		return best.resourceType, nil
	}
	return "", fmt.Errorf("source type is ambiguous for source_info %v", req.SourceInfo)
}

func validateQueryResources(req *QueryRequest, provider SchemaProvider) error {
	if provider == nil {
		provider = GetSchemaProvider()
	}
	known := make(map[ResourceType]struct{})
	for _, schema := range provider.ListRelationSchemas(req.SchemaNamespace()) {
		known[schema.FromType] = struct{}{}
		known[schema.ToType] = struct{}{}
	}
	for _, resourceType := range []ResourceType{req.SourceType, req.TargetType} {
		if resourceType == "" {
			return fmt.Errorf("resource type cannot be empty")
		}
		if _, ok := known[resourceType]; !ok {
			return fmt.Errorf("unknown resource type %q", resourceType)
		}
	}
	for _, resourceType := range req.PathResource {
		if resourceType == "" {
			continue
		}
		if _, ok := known[resourceType]; !ok {
			return fmt.Errorf("unknown path resource type %q", resourceType)
		}
	}
	if err := validateSourceInfoFields(req, provider); err != nil {
		return err
	}
	return nil
}

func validateSourceInfoFields(req *QueryRequest, provider SchemaProvider) error {
	if req == nil || len(req.SourceInfo) == 0 {
		return nil
	}

	primaryKeys := provider.GetResourcePrimaryKeys(req.SchemaNamespace(), req.SourceType)
	if len(primaryKeys) == 0 {
		return fmt.Errorf("source type %q has no primary keys for source_info", req.SourceType)
	}

	allowed := make(map[string]struct{}, len(primaryKeys))
	for _, key := range primaryKeys {
		allowed[key] = struct{}{}
	}
	for key := range req.SourceInfo {
		if _, ok := allowed[key]; !ok {
			return fmt.Errorf("unknown source_info field %q for source type %q", key, req.SourceType)
		}
	}
	return nil
}

func matcherToMap(m cmdb.Matcher) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}

func computeMaxHops(pathResource []cmdb.Resource) int {
	if len(pathResource) == 0 {
		return DefaultMaxHops
	}
	maxHops := DefaultMaxHops + len(pathResource)
	if maxHops > MaxAllowedHops {
		return MaxAllowedHops
	}
	return maxHops
}

func extractMatchersFromGraphs(
	graphs []*LivenessGraph,
	targetType ResourceType,
	pathResource []ResourceType,
) cmdb.Matchers {
	return extractMatchersFromGraphsWithOptions(
		graphs,
		targetType,
		pathResource,
		GetSchemaProvider(),
		"",
		true,
		true,
	)
}

func extractMatchersFromGraphsWithOptions(
	graphs []*LivenessGraph,
	targetType ResourceType,
	pathResource []ResourceType,
	provider SchemaProvider,
	namespace string,
	targetInfoShow bool,
	includeRootTarget bool,
) cmdb.Matchers {
	if len(graphs) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var result cmdb.Matchers

	for _, g := range graphs {
		for resourceID, matcher := range g.ExtractTargetMatchersWithID(targetType, pathResource, includeRootTarget) {
			if !seen[resourceID] {
				seen[resourceID] = true
				result = append(result, filterTargetMatcher(matcher, provider, namespace, targetType, targetInfoShow))
			}
		}
	}

	return result
}

type targetPathInfo struct {
	Labels      map[string]string
	NodePeriods [][]*VisiblePeriod
	EdgePeriods [][]*VisiblePeriod
}

func buildTargetMatchersTimeSeries(
	graphs []*LivenessGraph,
	targetType ResourceType,
	pathResource []ResourceType,
	start, end, stepMs int64,
) []cmdb.MatchersWithTimestamp {
	return buildTargetMatchersTimeSeriesWithOptions(
		graphs,
		targetType,
		pathResource,
		start,
		end,
		stepMs,
		GetSchemaProvider(),
		"",
		true,
		true,
	)
}

func buildTargetMatchersTimeSeriesWithOptions(
	graphs []*LivenessGraph,
	targetType ResourceType,
	pathResource []ResourceType,
	start, end, stepMs int64,
	provider SchemaProvider,
	namespace string,
	targetInfoShow bool,
	includeRootTarget bool,
) []cmdb.MatchersWithTimestamp {
	if len(graphs) == 0 {
		return nil
	}
	if stepMs <= 0 {
		return nil
	}

	targetNodes := make(map[string][]*targetPathInfo)
	for _, g := range graphs {
		for _, path := range g.TargetPaths(targetType, pathResource, includeRootTarget) {
			targetNodes[path.Target.ResourceID] = append(targetNodes[path.Target.ResourceID], &targetPathInfo{
				Labels:      path.Target.Labels,
				NodePeriods: path.NodePeriods,
				EdgePeriods: path.EdgePeriods,
			})
		}
	}

	if len(targetNodes) == 0 {
		return nil
	}

	var result []cmdb.MatchersWithTimestamp

	for ts := start; ts <= end; ts += stepMs {
		var activeMatchers cmdb.Matchers

		for _, paths := range targetNodes {
			if len(paths) == 0 {
				continue
			}
			if isAnyTargetPathActiveAt(paths, ts) {
				info := paths[0]
				matcher := filterTargetMatcher(info.Labels, provider, namespace, targetType, targetInfoShow)
				activeMatchers = append(activeMatchers, matcher)
			}
		}

		if len(activeMatchers) > 0 {
			result = append(result, cmdb.MatchersWithTimestamp{
				Timestamp: ts,
				Matchers:  activeMatchers,
			})
		}
	}

	return result
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
		for k, v := range labels {
			matcher[k] = v
		}
		return matcher
	}
	matcher := make(cmdb.Matcher, len(fields))
	for _, key := range fields {
		if value, ok := labels[key]; ok {
			matcher[key] = value
		}
	}
	return matcher
}

func isAnyTargetPathActiveAt(paths []*targetPathInfo, ts int64) bool {
	for _, path := range paths {
		active := true
		for _, periods := range path.NodePeriods {
			if !isActiveAt(periods, ts) {
				active = false
				break
			}
		}
		if !active {
			continue
		}
		for _, periods := range path.EdgePeriods {
			if !isActiveAt(periods, ts) {
				active = false
				break
			}
		}
		if active {
			return true
		}
	}
	return false
}

func isActiveAt(periods []*VisiblePeriod, ts int64) bool {
	for _, p := range periods {
		if ts >= p.Start && ts <= p.End {
			return true
		}
	}
	return false
}

func FromCMDBResource(r cmdb.Resource) ResourceType {
	return ResourceType(r)
}

func convertPathsV2ToStrings(pathsV2 []cmdb.PathV2) []string {
	if len(pathsV2) == 0 {
		return nil
	}
	result := make([]string, len(pathsV2))
	for i, path := range pathsV2 {
		result[i] = path.String()
	}
	return result
}
