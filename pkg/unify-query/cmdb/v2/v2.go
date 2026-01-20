// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v2

import (
	"context"
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

func GetModel(ctx context.Context) (cmdb.CMDBv2, error) {
	if defaultModel == nil {
		modelMutex.Lock()
		defer modelMutex.Unlock()
		if defaultModel == nil {
			client := NewBKBaseSurrealDBClient()
			model, err := NewModel(ctx, client)
			if err != nil {
				return nil, err
			}
			defaultModel = model
		}
	}
	return defaultModel, nil
}

type GraphQueryExecutor interface {
	Execute(ctx context.Context, sql string, start, end int64) ([]*LivenessGraph, error)
}

// Model v2 CMDB 实现，基于 SurrealDB 图查询
type Model struct {
	executor GraphQueryExecutor
}

// NewModel 创建 Model 实例
func NewModel(ctx context.Context, executor GraphQueryExecutor) (*Model, error) {
	return &Model{executor: executor}, nil
}

// SetExecutor 设置查询执行器（用于测试）
func (m *Model) SetExecutor(executor GraphQueryExecutor) {
	m.executor = executor
}

func (m *Model) QueryResourceMatcher(
	ctx context.Context,
	lookBackDelta, spaceUid string,
	ts string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (resSource cmdb.Resource, resIndexMatcher cmdb.Matcher, resPaths []cmdb.PathV2, resTarget cmdb.Resource, resMatchers cmdb.Matchers, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-v2-query-resource-matcher")
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

// QueryResourceMatcherRange 实现 cmdb.CMDBv2 接口（range 查询）
func (m *Model) QueryResourceMatcherRange(
	ctx context.Context,
	lookBackDelta, spaceUid string,
	step string,
	startTs, endTs string,
	target, source cmdb.Resource,
	indexMatcher, expandMatcher cmdb.Matcher,
	expandShow bool,
	pathResource []cmdb.Resource,
) (resSource cmdb.Resource, resIndexMatcher cmdb.Matcher, resPaths []cmdb.PathV2, resTarget cmdb.Resource, result []cmdb.MatchersWithTimestamp, err error) {
	ctx, span := trace.NewSpan(ctx, "cmdb-v2-query-resource-matcher-range")
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

	span.Set("source-type", req.SourceType)
	span.Set("target-type", req.TargetType)
	span.Set("source-info", req.SourceInfo)
	span.Set("max-hops", req.MaxHops)
	span.Set("look-back-delta", req.LookBackDelta)

	builder := NewSurrealQueryBuilder(req)
	sql := builder.Build()
	queryStart, queryEnd := req.GetQueryRange()

	span.Set("query-sql", sql)
	span.Set("query-start", queryStart)
	span.Set("query-end", queryEnd)

	pf := NewPathFinder(
		WithAllowedCategories(req.AllowedRelationTypes...),
		WithDynamicDirection(req.DynamicRelationDirection),
		WithMaxHops(req.MaxHops),
	)
	paths, err = pf.FindAllPaths(req.SourceType, req.TargetType, req.PathResource)
	if err != nil {
		return nil, nil, nil, err
	}

	if m.executor != nil {
		graphs, err = m.executor.Execute(ctx, sql, queryStart, queryEnd)
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
	return d.Milliseconds(), nil
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
