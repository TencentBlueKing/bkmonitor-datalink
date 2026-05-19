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
	if defaultModel == nil {
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
		SpaceUID:      spaceUid,
		Timestamp:     timestamp,
		SourceType:    FromCMDBResource(source),
		SourceInfo:    matcherToMap(indexMatcher),
		TargetType:    FromCMDBResource(target),
		PathResource:  toResourceTypes(pathResource),
		MaxHops:       computeMaxHops(pathResource),
		LookBackDelta: lbd,
	}
	req.Normalize()

	_, pathsV2, matchers, err := m.QueryLivenessGraph(ctx, req)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	paths := convertPathsV2ToStrings(pathsV2)

	span.Set("paths-count", len(paths))
	span.Set("matchers-count", len(matchers))

	return source, indexMatcher, paths, target, matchers, nil
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
		SpaceUID:      spaceUid,
		Timestamp:     timestamp,
		SourceType:    FromCMDBResource(source),
		SourceInfo:    matcherToMap(indexMatcher),
		TargetType:    FromCMDBResource(target),
		PathResource:  toResourceTypes(pathResource),
		MaxHops:       computeMaxHops(pathResource),
		LookBackDelta: lbd,
	}
	req.Normalize()

	_, paths, matchers, err := m.QueryLivenessGraph(ctx, req)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	span.Set("paths-count", len(paths))
	span.Set("matchers-count", len(matchers))

	return source, indexMatcher, paths, target, matchers, nil
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
		SpaceUID:      spaceUid,
		Timestamp:     end,
		SourceType:    FromCMDBResource(source),
		SourceInfo:    matcherToMap(indexMatcher),
		TargetType:    FromCMDBResource(target),
		PathResource:  toResourceTypes(pathResource),
		MaxHops:       computeMaxHops(pathResource),
		LookBackDelta: lbd,
	}
	req.Normalize()

	graphs, pathsV2, _, err := m.QueryLivenessGraph(ctx, req)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	result = buildTargetMatchersTimeSeries(graphs, req.TargetType, start, end, stepMs)

	paths := convertPathsV2ToStrings(pathsV2)

	span.Set("paths-count", len(paths))
	span.Set("result-count", len(result))

	return source, indexMatcher, paths, target, result, nil
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
		SpaceUID:      spaceUid,
		Timestamp:     end,
		SourceType:    FromCMDBResource(source),
		SourceInfo:    matcherToMap(indexMatcher),
		TargetType:    FromCMDBResource(target),
		PathResource:  toResourceTypes(pathResource),
		MaxHops:       computeMaxHops(pathResource),
		LookBackDelta: lbd,
	}
	req.Normalize()

	graphs, paths, _, err := m.QueryLivenessGraph(ctx, req)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	result = buildTargetMatchersTimeSeries(graphs, req.TargetType, start, end, stepMs)

	span.Set("paths-count", len(paths))
	span.Set("result-count", len(result))

	return source, indexMatcher, paths, target, result, nil
}

// QueryLivenessGraph 执行图查询，返回图数据、路径和目标 Matchers
func (m *Model) QueryLivenessGraph(ctx context.Context, req *QueryRequest) (graphs []*LivenessGraph, paths []cmdb.PathV2, matchers cmdb.Matchers, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-v2-query-liveness-graph")
	defer span.End(&err)

	provider := m.getSchemaProvider()
	req.Normalize()

	if err := validateQueryResources(req, provider); err != nil {
		return nil, nil, nil, err
	}

	span.Set("source-type", req.SourceType)
	span.Set("target-type", req.TargetType)
	span.Set("source-info", req.SourceInfo)
	span.Set("max-hops", req.MaxHops)
	span.Set("look-back-delta", req.LookBackDelta)
	span.Set("space-uid", req.SpaceUID)

	builder := NewSurrealQueryBuilderWithSchemaProvider(req, provider)
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
	paths, err = pf.FindAllPaths(req.SourceType, req.TargetType, req.PathResource)
	if err != nil {
		return nil, nil, nil, err
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

	matchers = extractMatchersFromGraphs(graphs, req.TargetType)

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
	if m.resolver != nil && req.SpaceUID != "" {
		if ex, ok := m.executor.(GraphQueryExecutorWithBinding); ok {
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
	return len(pathResource) + 1
}

func extractMatchersFromGraphs(graphs []*LivenessGraph, targetType ResourceType) cmdb.Matchers {
	if len(graphs) == 0 {
		return nil
	}

	seen := make(map[string]bool)
	var result cmdb.Matchers

	for _, g := range graphs {
		for resourceID, matcher := range g.ExtractTargetMatchersWithID(targetType) {
			if !seen[resourceID] {
				seen[resourceID] = true
				result = append(result, matcher)
			}
		}
	}

	return result
}

type targetNodeInfo struct {
	Labels     map[string]string
	RawPeriods []*VisiblePeriod
}

func buildTargetMatchersTimeSeries(graphs []*LivenessGraph, targetType ResourceType, start, end, stepMs int64) []cmdb.MatchersWithTimestamp {
	if len(graphs) == 0 {
		return nil
	}
	if stepMs <= 0 {
		return nil
	}

	targetNodes := make(map[string]*targetNodeInfo)
	for _, g := range graphs {
		for _, node := range g.Nodes {
			if node.ResourceType == targetType {
				if _, exists := targetNodes[node.ResourceID]; !exists {
					targetNodes[node.ResourceID] = &targetNodeInfo{
						Labels:     node.Labels,
						RawPeriods: node.RawPeriods,
					}
				} else {
					targetNodes[node.ResourceID].RawPeriods = append(
						targetNodes[node.ResourceID].RawPeriods,
						node.RawPeriods...,
					)
				}
			}
		}
	}

	if len(targetNodes) == 0 {
		return nil
	}

	var result []cmdb.MatchersWithTimestamp

	for ts := start; ts <= end; ts += stepMs {
		var activeMatchers cmdb.Matchers

		for _, info := range targetNodes {
			if isActiveAt(info.RawPeriods, ts) {
				matcher := make(cmdb.Matcher, len(info.Labels))
				for k, v := range info.Labels {
					matcher[k] = v
				}
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
