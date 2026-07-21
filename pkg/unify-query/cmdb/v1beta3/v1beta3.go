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
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
	ants "github.com/panjf2000/ants/v2"
)

var (
	defaultModel *Model
	modelMutex   sync.Mutex
)

// defaultPathQueryMaxRouting 限制单个 relation 请求内最多并发多少条 SurrealDB path。
// 这里不直接使用 len(paths)，是为了避免一个 source->target 有很多候选路径时把
// BKBase query_sync 同时打满；path-by-path 的目标是降低单条大 SQL 的复杂度，
// 不是把压力无上限地转成更多并发请求。
const defaultPathQueryMaxRouting = 4

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

type graphQueryRunner func(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error)

var sourceInferencePriority = map[ResourceType]int{
	ResourceTypeBiz:          0,
	ResourceType("business"): 1,
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
		MaxHops:            computeMaxHops(source, target, pathResource),
		LookBackDelta:      lbd,
		LookBackDeltaSet:   lookBackDelta != "",
	}
	req.Normalize()

	_, paths, matchers, err := m.QueryLivenessGraph(ctx, req)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	responsePaths := convertResourcePathToResources(paths)

	span.Set("paths-count", len(responsePaths))
	span.Set("matchers-count", len(matchers))

	return cmdb.Resource(req.SourceType), cmdb.Matcher(req.SourceInfo), responsePaths, cmdb.Resource(req.TargetType), matchers, nil
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
	if start > end {
		return "", nil, nil, "", nil, fmt.Errorf("start_time must be less than or equal to end_time")
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
		MaxHops:            computeMaxHops(source, target, pathResource),
		LookBackDelta:      rangeQueryLookBackDelta(lbd, start, end, stepMs, lookBackDelta != ""),
		LookBackDeltaSet:   lookBackDelta != "",
	}
	req.Normalize()

	graphs, candidatePaths, _, err := m.queryLivenessGraph(ctx, req, true, graphQueryModeRange, start, end, stepMs)
	if err != nil {
		return "", nil, nil, "", nil, err
	}

	provider := m.getSchemaProvider()
	selectedPath := resourcePathForRangeQuery(graphs, candidatePaths, req, start, end, stepMs)
	extractionPathResource := targetExtractionPathResource(req)
	if len(selectedPath) > 0 {
		// 旧 VM range 会按候选路径顺序停在第一条有数据的路径上。这里先选出同一条命中路径，
		// 再用它限制 target_list 抽取，避免把低优先级路径上的 target 混入响应。
		extractionPathResource = selectedPath
	}

	result = buildTargetMatchersTimeSeriesWithOptions(
		graphs,
		req.TargetType,
		extractionPathResource,
		start,
		end,
		stepMs,
		provider,
		req.SchemaNamespace(),
		req.TargetInfoShow,
		shouldIncludeRootTarget(req),
	)

	paths := resourceTypesToPath(selectedPath)

	span.Set("paths-count", len(paths))
	span.Set("result-count", len(result))

	return cmdb.Resource(req.SourceType), cmdb.Matcher(req.SourceInfo), paths, cmdb.Resource(req.TargetType), result, nil
}

// QueryLivenessGraph 执行图查询，返回图数据、路径和目标 Matchers
func (m *Model) QueryLivenessGraph(ctx context.Context, req *QueryRequest) (graphs []*LivenessGraph, paths []resourcePath, matchers cmdb.Matchers, err error) {
	return m.queryLivenessGraph(ctx, req, true, graphQueryModeInstant, 0, 0, 0)
}

type graphQueryMode string

const (
	graphQueryModeInstant graphQueryMode = "instant"
	graphQueryModeRange   graphQueryMode = "range"
)

func (m *Model) queryLivenessGraph(
	ctx context.Context,
	req *QueryRequest,
	splitPaths bool,
	mode graphQueryMode,
	rangeStart, rangeEnd, stepMs int64,
) (graphs []*LivenessGraph, paths []resourcePath, matchers cmdb.Matchers, err error) {
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
	// 同类型查询有两种不同语义，必须在路径发现前归一化：
	// 1. target_type 未显式传入时，这是旧接口的信息展示路径，只查 source 自身；
	// 2. target_type 显式等于 source_type 时，这是自关联查询，只允许一跳直连自关联。
	implicitSelfTarget := !req.TargetTypeExplicit && req.SourceType == req.TargetType
	if implicitSelfTarget {
		// 信息展示路径不应展开任何 relation hop；否则会把同类型自关联结果混入
		// “source 自身信息”的响应。
		req.MaxHops = 0
		req.PathResource = nil
	} else if isExplicitDirectSelfTarget(req) {
		// 显式同类型 target 要求查询真实自关联边。空资源占位符是 PathFinder
		// 的“只走直连”约束，避免在 source -> ... -> source 的多跳环路里取数。
		req.MaxHops = 1
		req.PathResource = []ResourceType{""}
	}
	adjustMaxHopsForUnconstrainedPath(req, provider)

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

	pf := NewPathFinder(
		WithAllowedCategories(req.AllowedRelationTypes...),
		WithDynamicDirection(req.DynamicRelationDirection),
		WithMaxHops(req.MaxHops),
		WithSchemaProvider(provider),
		WithNamespace(req.SchemaNamespace()),
	)
	if implicitSelfTarget {
		paths = []resourcePath{{Steps: []resourcePathStep{{ResourceType: string(req.SourceType)}}}}
	} else {
		paths, err = pf.FindAllPaths(req.SourceType, req.TargetType, req.PathResource)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	queryStart, queryEnd := req.GetQueryRange()
	span.Set("query-start", queryStart)
	span.Set("query-end", queryEnd)

	if m.executor != nil {
		start := time.Now()
		runner, err := m.newGraphQueryRunner(ctx, req)
		if err != nil {
			elapsed := time.Since(start).Seconds()
			status := CategorizeError(err)
			ObserveError(req.SpaceUID, status)
			ObserveQueryDuration(req.SpaceUID, status, elapsed)
			return nil, nil, nil, err
		}
		if shouldSplitQueryByPath(req, paths, splitPaths) {
			span.Set("query-mode", "path-by-path")
			span.Set("query-result-mode", string(mode))
			span.Set("candidate-paths-count", len(paths))
			graphs, paths, err = m.executeGraphQueryPathByPath(ctx, req, provider, paths, queryStart, queryEnd, mode, rangeStart, rangeEnd, stepMs, runner)
		} else {
			span.Set("query-mode", "single")
			builder := NewSurrealQueryBuilderWithSchemaProvider(req, provider)
			configureBuilderForGraphQueryMode(builder, mode)
			if implicitSelfTarget {
				builder.request.MaxHops = 0
			}
			sql := builder.Build()
			span.Set("query-sql", sql)
			graphs, err = runner(ctx, sql, queryStart, queryEnd)
		}
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

func shouldSplitQueryByPath(req *QueryRequest, paths []resourcePath, enabled bool) bool {
	// 显式 target 且 source!=target 的关联查询统一走 path-by-path：
	// 1. 即使只有一条关联路径，也能用该 path 的真实 hop 数生成更短 SurrealQL；
	// 2. source==target 的自查询包含信息展示 / 自关联两种语义，暂时保持单 SQL，避免拆分后改变含义。
	return enabled &&
		req != nil &&
		req.SourceType != req.TargetType &&
		req.TargetTypeExplicit &&
		len(paths) > 0
}

func (m *Model) executeGraphQueryPathByPath(
	ctx context.Context,
	req *QueryRequest,
	provider SchemaProvider,
	paths []resourcePath,
	start, end int64,
	mode graphQueryMode,
	rangeStart, rangeEnd, stepMs int64,
	runner graphQueryRunner,
) ([]*LivenessGraph, []resourcePath, error) {
	// 先把执行顺序归一化为“短路径优先”。这只影响 query_sync 的提交顺序，
	// 不改变 PathFinder 原始 paths；全部未命中时仍返回原始 paths 给调用方。
	queryPaths := sortPathsForQuery(paths)

	// ants pool 大小取 min(path 数量, 默认并发上限)。
	// path 数量通常不大，但这里仍显式限流，避免多个 relation 请求叠加时放大
	// BKBase / SurrealDB 压力。
	poolSize := len(queryPaths)
	if poolSize > defaultPathQueryMaxRouting {
		poolSize = defaultPathQueryMaxRouting
	}

	// 每次调用创建一个独立 pool，生命周期绑定当前 relation 请求。
	// 这样不会让跨请求的慢 path 占住全局 worker，也便于后续把上限做成配置。
	p, err := ants.NewPool(poolSize)
	if err != nil {
		return nil, nil, err
	}
	defer p.Release()

	// resultCh 使用 len(queryPaths) 作为缓冲，保证 worker 发送结果时不会因为
	// 主协程正在等待其他 path 而阻塞。
	resultCh := make(chan pathQueryResult, len(queryPaths))
	var workerWg sync.WaitGroup
	for idx, path := range queryPaths {
		// Go 闭包会捕获循环变量；这里复制一份，保证每个 worker 看到自己的
		// path 和优先级下标。
		idx := idx
		path := path
		workerWg.Add(1)
		if err := p.Submit(func() {
			defer workerWg.Done()
			// 每条 path 只生成并执行自己的 SurrealQL。idx 是排序后的优先级，
			// 后面会用它做稳定的图合并和响应 path 选择。
			resultCh <- m.executeOneGraphQueryPath(ctx, req, provider, path, idx, start, end, mode, runner)
		}); err != nil {
			workerWg.Done()
			// Submit 失败也要写入 resultCh，让下面的收敛逻辑能按同一套流程
			// 处理错误，不会因为少一个结果而一直等待。
			resultCh <- pathQueryResult{idx: idx, path: path, err: err}
		}
	}

	// results 按排序后的 path 下标存放结果。这样即使 goroutine 乱序返回，
	// 后续合并 LivenessGraph 和选择响应 path 时仍能沿用稳定的 path 优先级。
	results := make([]*pathQueryResult, len(queryPaths))
	var pathErrors []error
	for completed := 0; completed < len(queryPaths); completed++ {
		// 收到任意一个 path 的结果后，先放回它的优先级槽位。
		result := <-resultCh
		results[result.idx] = &result
		if result.err != nil {
			pathErrors = append(pathErrors, result.err)
		}

	}
	workerWg.Wait()

	var graphFragments []*LivenessGraph
	for _, result := range results {
		if result == nil || result.err != nil {
			continue
		}
		graphFragments = append(graphFragments, result.graphs...)
	}
	mergedGraphs := mergeLivenessGraphsByRoot(graphFragments)

	if mode == graphQueryModeRange {
		candidates := resourcePathCandidatesFromRangeTargetGraphs(
			mergedGraphs,
			req.TargetType,
			targetExtractionPathResource(req),
			shouldIncludeRootTarget(req),
			rangeStart,
			rangeEnd,
			stepMs,
		)
		if selectedPath, ok := selectResourcePathFromCandidates(queryPaths, candidates); ok {
			return mergedGraphs, []resourcePath{selectedPath}, nil
		}
	} else {
		candidates := resourcePathCandidatesFromTargetGraphs(
			mergedGraphs,
			req.TargetType,
			targetExtractionPathResource(req),
			shouldIncludeRootTarget(req),
		)
		if selectedPath, ok := selectResourcePathFromCandidates(queryPaths, candidates); ok {
			return mergedGraphs, []resourcePath{selectedPath}, nil
		}
	}

	if len(mergedGraphs) > 0 {
		// 有 root 图但没有命中 target 时，继续返回合并后的图，方便调用方保留
		// source/root 信息；path 仍保持原始候选集合。
		return mergedGraphs, paths, nil
	}

	if len(pathErrors) > 0 {
		return nil, nil, errors.Join(pathErrors...)
	}

	// 所有 path 都完成且没有命中 target。保持原始候选 paths 返回，便于调用方
	// 继续展示“有哪些可走路径”，同时 target_list 为空。
	return nil, paths, nil
}

// pathQueryResult 是单条 path 查询的收敛结果。
// idx 使用排序后的优先级下标，而不是原始 PathFinder 下标；这样图合并和响应 path
// 选择不受 goroutine 完成顺序影响。
type pathQueryResult struct {
	idx    int
	path   resourcePath
	graphs []*LivenessGraph
	err    error
}

func (m *Model) executeOneGraphQueryPath(
	ctx context.Context,
	req *QueryRequest,
	provider SchemaProvider,
	path resourcePath,
	idx int,
	start, end int64,
	mode graphQueryMode,
	runner graphQueryRunner,
) pathQueryResult {
	// 这里的 SQL 只包含当前 path 的 relation 分支。相比合并所有候选路径的大 SQL，
	// 单 path SQL 更短，也避免 SurrealDB 在一次查询中同时展开多个无关分支。
	builder := NewSurrealQueryBuilderForPath(req, provider, path)
	configureBuilderForGraphQueryMode(builder, mode)
	sql := builder.Build()
	graphs, err := runner(ctx, sql, start, end)
	if err != nil {
		return pathQueryResult{idx: idx, path: path, err: err}
	}
	if err := rejectGraphTraversalErrors(graphs); err != nil {
		return pathQueryResult{idx: idx, path: path, err: err}
	}

	return pathQueryResult{idx: idx, path: path, graphs: graphs}
}

func configureBuilderForGraphQueryMode(builder *SurrealQueryBuilder, mode graphQueryMode) {
	if builder == nil {
		return
	}
	builder.queryMode = mode
	if mode == graphQueryModeInstant {
		builder.WithoutLivenessProjection()
	}
}

func sortPathsForQuery(paths []resourcePath) []resourcePath {
	// 复制一份再排序，避免影响 all-empty 返回时使用的原始 paths。
	sorted := append([]resourcePath(nil), paths...)
	sort.SliceStable(sorted, func(i, j int) bool {
		// relation 查询会在首个命中 path 后短路。短路径通常生成更小的
		// SurrealQL，也更接近旧 VM 直连关系优先命中的执行成本；range 拆分也复用同一优先级。
		if len(sorted[i].Steps) != len(sorted[j].Steps) {
			return len(sorted[i].Steps) < len(sorted[j].Steps)
		}
		return resourcePathSortKey(sorted[i]) < resourcePathSortKey(sorted[j])
	})
	return sorted
}

func resourcePathSortKey(path resourcePath) string {
	parts := make([]string, 0, len(path.Steps))
	for _, step := range path.Steps {
		parts = append(parts, strings.Join([]string{
			step.ResourceType,
			step.RelationType,
			step.Category,
			step.Direction,
		}, "/"))
	}
	return strings.Join(parts, "|")
}

// newGraphQueryRunner 在一次 v1beta3 relation 请求内固定 executor 调用路径。
// path-by-path 会并发执行多条 SQL；binding 元数据只和 space_uid 相关，提前解析一次
// 可以避免每条 path 重复访问 binding resolver，同时保证所有 path 使用同一份路由上下文。
func (m *Model) newGraphQueryRunner(ctx context.Context, req *QueryRequest) (graphQueryRunner, error) {
	if m.resolver != nil {
		if ex, ok := m.executor.(GraphQueryExecutorWithBinding); ok {
			if req.SpaceUID == "" {
				return nil, fmt.Errorf("space_uid is required for binding graph query")
			}
			binding, err := m.resolver.Resolve(ctx, req.SpaceUID)
			if err != nil {
				return nil, err
			}
			return func(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error) {
				graphs, err := ex.ExecuteWithBinding(ctx, req.SpaceUID, *binding, sql, start, end)
				if err != nil {
					return nil, err
				}
				if err := rejectGraphTraversalErrors(graphs); err != nil {
					return nil, err
				}
				return graphs, nil
			}, nil
		}
	}
	return func(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error) {
		graphs, err := m.executor.Execute(ctx, sql, start, end)
		if err != nil {
			return nil, err
		}
		if err := rejectGraphTraversalErrors(graphs); err != nil {
			return nil, err
		}
		return graphs, nil
	}, nil
}

// executeGraphQuery 根据 resolver / executor 能力选择最合适的调用路径。
//
//  1. 若同时具备 resolver 与支持 binding 的 executor，则先 resolve binding，
//     再走 ExecuteWithBinding，DSL 前会加 "USE NS ... DB ...;" 前缀。
//  2. 否则退化到原始 Execute（全局 result_table_id，单测 / 旧路径）。
func (m *Model) executeGraphQuery(ctx context.Context, req *QueryRequest, sql string, start, end int64) ([]*LivenessGraph, error) {
	runner, err := m.newGraphQueryRunner(ctx, req)
	if err != nil {
		return nil, err
	}
	return runner(ctx, sql, start, end)
}

func rejectGraphTraversalErrors(graphs []*LivenessGraph) error {
	var traversalErrors []string
	for _, graph := range graphs {
		if graph == nil || len(graph.TraversalErrors) == 0 {
			continue
		}
		traversalErrors = append(traversalErrors, graph.TraversalErrors...)
	}
	if len(traversalErrors) == 0 {
		return nil
	}
	return fmt.Errorf("parse SurrealDB graph response: %s", strings.Join(traversalErrors, "; "))
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
	ms := d.Milliseconds()
	if ms < 0 {
		// 负 look_back_delta 会把 instant 查询窗口反转成 start > end，后续 SurrealDB 查询只会成功返回空结果。
		// 提前拒绝非法输入，可以让调用方拿到明确的 bad-request 语义。
		return 0, fmt.Errorf("look_back_delta must be greater than or equal to 0, got %q", lbd)
	}
	return ms, nil
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

func rangeQueryLookBackDelta(configured, start, end, stepMs int64, explicitlySet bool) int64 {
	required := end - start
	if stepMs > 0 {
		// 对齐旧 VM range 的 count_over_time 语义：第一个 bucket 也要能读取 (start-step, start] 窗口内的样本。
		required += stepMs
	}
	if required < 0 {
		required = 0
	}
	if !explicitlySet {
		return required
	}
	return maxInt64(configured, required)
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
		return []ResourceType{""}
	}
	return req.PathResource
}

func adjustMaxHopsForUnconstrainedPath(req *QueryRequest, provider SchemaProvider) {
	if req == nil ||
		provider == nil ||
		len(req.PathResource) > 0 ||
		req.SourceType == "" ||
		req.TargetType == "" ||
		req.SourceType == req.TargetType ||
		req.MaxHops >= MaxAllowedHops {
		return
	}

	maxFinder := NewPathFinder(
		WithAllowedCategories(req.AllowedRelationTypes...),
		WithDynamicDirection(req.DynamicRelationDirection),
		WithMaxHops(MaxAllowedHops),
		WithSchemaProvider(provider),
		WithNamespace(req.SchemaNamespace()),
	)
	paths, err := maxFinder.FindAllPaths(req.SourceType, req.TargetType, nil)
	if err != nil {
		return
	}

	for _, path := range paths {
		if hops := len(path.Steps) - 1; hops > req.MaxHops {
			req.MaxHops = hops
		}
	}
	if req.MaxHops > MaxAllowedHops {
		req.MaxHops = MaxAllowedHops
	}
}

type sourceTypeCandidate struct {
	resourceType ResourceType
	keyCount     int
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
	for _, resourceType := range provider.ListResourceTypes(req.SchemaNamespace()) {
		known[resourceType] = struct{}{}
	}
	for _, schema := range provider.ListRelationSchemas(req.SchemaNamespace()) {
		known[schema.FromType] = struct{}{}
		known[schema.ToType] = struct{}{}
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
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].keyCount != candidates[j].keyCount {
			return candidates[i].keyCount > candidates[j].keyCount
		}
		leftPriority := sourceInferenceRank(candidates[i].resourceType)
		rightPriority := sourceInferenceRank(candidates[j].resourceType)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return candidates[i].resourceType < candidates[j].resourceType
	})
	best := candidates[0]
	topCandidates := []sourceTypeCandidate{best}
	for _, candidate := range candidates[1:] {
		if candidate.keyCount != best.keyCount {
			break
		}
		topCandidates = append(topCandidates, candidate)
	}
	if len(topCandidates) == 1 {
		return best.resourceType, nil
	}
	if preferred, ok := preferredAliasSourceType(topCandidates); ok {
		return preferred, nil
	}
	return "", fmt.Errorf("source type is ambiguous for source_info %v", req.SourceInfo)
}

func sourceInferenceRank(resourceType ResourceType) int {
	if rank, ok := sourceInferencePriority[resourceType]; ok {
		return rank
	}
	return 100
}

func preferredAliasSourceType(candidates []sourceTypeCandidate) (ResourceType, bool) {
	if len(candidates) == 0 {
		return "", false
	}
	hasBiz := false
	for _, candidate := range candidates {
		switch candidate.resourceType {
		case ResourceTypeBiz:
			hasBiz = true
		case ResourceType("business"):
		default:
			return "", false
		}
	}
	if hasBiz {
		return ResourceTypeBiz, true
	}
	return ResourceType("business"), true
}

func validateQueryResources(req *QueryRequest, provider SchemaProvider) error {
	if provider == nil {
		provider = GetSchemaProvider()
	}
	namespace := req.SchemaNamespace()
	for _, resourceType := range []ResourceType{req.SourceType, req.TargetType} {
		if resourceType == "" {
			return fmt.Errorf("resource type cannot be empty")
		}
		if !isKnownResource(provider, namespace, resourceType) {
			return fmt.Errorf("unknown resource type %q", resourceType)
		}
	}
	for _, resourceType := range req.PathResource {
		if resourceType == "" {
			continue
		}
		if !isKnownResource(provider, namespace, resourceType) {
			return fmt.Errorf("unknown path resource type %q", resourceType)
		}
	}
	if err := validateSourceInfoFields(req, provider); err != nil {
		return err
	}
	return nil
}

func isKnownResource(provider SchemaProvider, namespace string, resourceType ResourceType) bool {
	return len(provider.GetResourcePrimaryKeys(namespace, resourceType)) > 0 ||
		len(provider.GetResourceFields(namespace, resourceType)) > 0
}

func validateSourceInfoFields(req *QueryRequest, provider SchemaProvider) error {
	if req == nil || len(req.SourceInfo) == 0 {
		return nil
	}

	primaryKeys := provider.GetResourcePrimaryKeys(req.SchemaNamespace(), req.SourceType)
	if len(primaryKeys) == 0 {
		return fmt.Errorf("source type %q has no primary keys for source_info", req.SourceType)
	}

	for _, key := range primaryKeys {
		if _, ok := req.SourceInfo[key]; !ok {
			return fmt.Errorf("source_info missing primary field %q for source type %q", key, req.SourceType)
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

func computeMaxHops(source, target cmdb.Resource, pathResource []cmdb.Resource) int {
	if len(pathResource) == 0 {
		return DefaultMaxHops
	}
	pathConstraint, directOnly := normalizePathResource(FromCMDBResource(source), FromCMDBResource(target), toResourceTypes(pathResource))
	if directOnly {
		return 1
	}
	if len(pathConstraint) == 0 {
		return DefaultMaxHops
	}
	// path_resource 可能只给出部分中间资源。除了约束本身，还要给 source/target 两侧各留出默认 schema
	// 的连接空间；否则 host->system->pod->replicaset->deployment 这类合法路径会因为预算太浅被剪掉。
	maxHops := DefaultMaxHops + len(pathConstraint) + 1
	if maxHops > MaxAllowedHops {
		return MaxAllowedHops
	}
	return maxHops
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
		for _, path := range g.TargetPaths(targetType, pathResource, includeRootTarget) {
			node := path.Target
			if node == nil {
				continue
			}
			resourceID := node.ResourceID
			if !seen[resourceID] {
				seen[resourceID] = true
				matcher := make(cmdb.Matcher, len(node.Labels))
				for k, v := range node.Labels {
					matcher[k] = v
				}
				result = append(result, filterTargetMatcher(matcher, provider, namespace, targetType, targetInfoShow))
			}
		}
	}

	return result
}

type targetPathInfo struct {
	Labels       map[string]string
	ResourcePath []ResourceType
	NodePeriods  [][]*VisiblePeriod
	EdgePeriods  [][]*VisiblePeriod
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
		for _, path := range g.TargetPathsForRange(targetType, pathResource, includeRootTarget) {
			targetNodes[path.Target.ResourceID] = append(targetNodes[path.Target.ResourceID], &targetPathInfo{
				Labels:       path.Target.Labels,
				ResourcePath: path.ResourcePath,
				NodePeriods:  path.NodePeriods,
				EdgePeriods:  path.EdgePeriods,
			})
		}
	}

	if len(targetNodes) == 0 {
		return nil
	}

	targetIDs := make([]string, 0, len(targetNodes))
	for targetID := range targetNodes {
		targetIDs = append(targetIDs, targetID)
	}
	sort.Strings(targetIDs)

	var result []cmdb.MatchersWithTimestamp

	for ts := start; ts <= end; ts += stepMs {
		var activeMatchers cmdb.Matchers

		for _, targetID := range targetIDs {
			paths := targetNodes[targetID]
			if len(paths) == 0 {
				continue
			}
			// 旧 VM range 使用 count_over_time(...[step])，bucket 命中看的是 (ts-step, ts] 窗口内是否有样本，
			// 不是要求路径上所有节点和边都在 ts 这个精确时间点同时存活。
			if isAnyTargetPathActiveInWindow(paths, ts-stepMs, ts) {
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

func isAnyTargetPathActiveInWindow(paths []*targetPathInfo, windowStart, windowEnd int64) bool {
	for _, path := range paths {
		if isTargetPathActiveInWindow(path, windowStart, windowEnd) {
			return true
		}
	}
	return false
}

func isTargetPathActiveInWindow(path *targetPathInfo, windowStart, windowEnd int64) bool {
	for _, periods := range path.NodePeriods {
		if !hasPeriodOverlapWindow(periods, windowStart, windowEnd) {
			return false
		}
	}
	for _, periods := range path.EdgePeriods {
		if !hasPeriodOverlapWindow(periods, windowStart, windowEnd) {
			return false
		}
	}
	return true
}

func hasPeriodOverlapWindow(periods []*VisiblePeriod, windowStart, windowEnd int64) bool {
	for _, p := range periods {
		if p == nil {
			continue
		}
		if p.End > windowStart && p.Start <= windowEnd {
			return true
		}
	}
	return false
}

func FromCMDBResource(r cmdb.Resource) ResourceType {
	return ResourceType(r)
}

func convertResourcePathToResources(paths []resourcePath) []string {
	if len(paths) == 0 {
		return nil
	}
	path := paths[0]
	if len(path.Steps) == 0 {
		return nil
	}
	result := make([]string, 0, len(path.Steps))
	for _, step := range path.Steps {
		if step.ResourceType != "" {
			result = append(result, step.ResourceType)
		}
	}
	return result
}

func resourcePathForRangeQuery(graphs []*LivenessGraph, paths []resourcePath, req *QueryRequest, start, end, stepMs int64) []ResourceType {
	if req != nil {
		// range 响应里的 path 和 target_list 必须来自同一条命中资源路径；
		// 因此这里保留 ResourceType 形态，供调用方继续限制 target 抽取。
		candidates := resourcePathCandidatesFromRangeTargetGraphs(
			graphs,
			req.TargetType,
			targetExtractionPathResource(req),
			shouldIncludeRootTarget(req),
			start,
			end,
			stepMs,
		)
		if path := selectResourcePathCandidate(paths, candidates); len(path) > 0 {
			return path
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return resourcePathTypes(paths[0])
}

func resourcePathCandidatesFromRangeTargetGraphs(
	graphs []*LivenessGraph,
	targetType ResourceType,
	pathResource []ResourceType,
	includeRootTarget bool,
	start, end, stepMs int64,
) [][]ResourceType {
	if stepMs <= 0 {
		return nil
	}

	var candidates [][]ResourceType
	for _, graph := range graphs {
		for _, path := range graph.TargetPathsForRange(targetType, pathResource, includeRootTarget) {
			info := &targetPathInfo{
				ResourcePath: path.ResourcePath,
				NodePeriods:  path.NodePeriods,
				EdgePeriods:  path.EdgePeriods,
			}
			if len(info.ResourcePath) == 0 || !isTargetPathActiveInAnyBucket(info, start, end, stepMs) {
				continue
			}
			candidates = append(candidates, info.ResourcePath)
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	return candidates
}

func resourcePathCandidatesFromTargetGraphs(
	graphs []*LivenessGraph,
	targetType ResourceType,
	pathResource []ResourceType,
	includeRootTarget bool,
) [][]ResourceType {
	var candidates [][]ResourceType
	for _, graph := range graphs {
		for _, path := range graph.TargetPaths(targetType, pathResource, includeRootTarget) {
			if len(path.ResourcePath) == 0 {
				continue
			}
			candidates = append(candidates, path.ResourcePath)
		}
	}
	return candidates
}

func selectResourcePathFromCandidates(paths []resourcePath, candidates [][]ResourceType) (resourcePath, bool) {
	selected := selectResourcePathCandidate(paths, candidates)
	if len(selected) == 0 {
		return resourcePath{}, false
	}
	selectedKey := resourcePathKey(selected)
	for _, path := range paths {
		if resourcePathKey(resourcePathTypes(path)) == selectedKey {
			return path, true
		}
	}
	return resourcePath{Steps: resourcePathStepsFromTypes(selected)}, true
}

func selectResourcePathCandidate(paths []resourcePath, candidates [][]ResourceType) []ResourceType {
	if len(candidates) == 0 {
		return nil
	}

	candidateSet := make(map[string][]ResourceType, len(candidates))
	for _, candidate := range candidates {
		if len(candidate) == 0 {
			continue
		}
		candidateSet[resourcePathKey(candidate)] = candidate
	}

	// 对齐旧 VM 行为：有 target 数据时，path 优先返回候选路径顺序中的第一条命中路径。
	for _, path := range paths {
		resources := resourcePathTypes(path)
		if len(resources) == 0 {
			continue
		}
		if _, ok := candidateSet[resourcePathKey(resources)]; ok {
			return resources
		}
	}

	sort.Slice(candidates, func(i, j int) bool {
		if len(candidates[i]) != len(candidates[j]) {
			return len(candidates[i]) < len(candidates[j])
		}
		return resourcePathKey(candidates[i]) < resourcePathKey(candidates[j])
	})
	return candidates[0]
}

func resourcePathTypes(path resourcePath) []ResourceType {
	resources := make([]ResourceType, 0, len(path.Steps))
	for _, step := range path.Steps {
		if step.ResourceType != "" {
			resources = append(resources, ResourceType(step.ResourceType))
		}
	}
	return resources
}

func resourcePathStepsFromTypes(resources []ResourceType) []resourcePathStep {
	steps := make([]resourcePathStep, 0, len(resources))
	for _, resource := range resources {
		if resource == "" {
			continue
		}
		steps = append(steps, resourcePathStep{ResourceType: string(resource)})
	}
	return steps
}

func isTargetPathActiveInAnyBucket(path *targetPathInfo, start, end, stepMs int64) bool {
	for ts := start; ts <= end; ts += stepMs {
		if isTargetPathActiveInWindow(path, ts-stepMs, ts) {
			return true
		}
	}
	return false
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

func resourcePathKey(resources []ResourceType) string {
	parts := make([]string, 0, len(resources))
	for _, resource := range resources {
		parts = append(parts, string(resource))
	}
	return strings.Join(parts, "\x00")
}
