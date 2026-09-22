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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dominikbraun/graph"
	"github.com/pkg/errors"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// TimeGraph 时序图结构，用于管理时间序列的图数据
// 采用时间分片设计，每个时间戳对应一个独立的图，提高查询效率
// 使用节点共享机制，相同资源信息共享同一个节点ID，节省内存
type TimeGraph struct {
	lock sync.RWMutex // 读写锁，保证并发安全

	nodeBuilder   *NodeBuilder                          // 节点构建器，负责节点的创建和去重
	stringDict    *StringDict                           // 局部字符串字典，避免全局溢出，每个实例独立管理
	timeGraph     map[int64]graph.Graph[uint64, uint64] // 时间分片图，key为时间戳，value为对应的图结构
	edgeTypes     map[int64]map[timeGraphEdgeKey]map[timeGraphEdgeRelation]struct{}
	nodeInfos     map[int64]map[uint64]cmdb.Matcher               // timestamp -> node -> time-specific dimensions
	topologyNodes map[uint64]map[int64]struct{}                   // 共享节点身份 -> 观测时间点
	topologyEdges map[timeGraphTopologyEdgeKey]map[int64]struct{} // 共享关系身份 -> 有效时间点
	partialTimes  map[int64]string                                // 时间点 -> 数据不完整原因
	maxNodes      int
	maxEdges      int
	maxResults    int
	maxNodeInfos  int
	edgeCount     int
	nodeInfoCount int
	relations     []TimeGraphRelationConfig
}

type timeGraphEdgeKey struct {
	source uint64
	target uint64
}

// 同一节点对可以承载不同方向的多种关系，遍历时必须匹配同一条关系的身份。
type timeGraphEdgeRelation struct {
	relationType string
	metricName   string
	category     string
	direction    string
}

const (
	timeGraphFromPrefix = "from_"
	timeGraphToPrefix   = "to_"
)

// NewTimeGraph 创建一个新的时序图实例
// 返回: 新创建的 TimeGraph 指针
// 注意: 每个实例都有自己独立的字符串字典，避免全局字典溢出问题
func NewTimeGraph() *TimeGraph {
	return NewTimeGraphWithConfig(defaultTimeGraphConfig())
}

// NewTimeGraphWithConfig 创建使用独立资源身份规则的图实例，避免读取进程级的
// 旧配置。
func NewTimeGraphWithConfig(cfg *TimeGraphConfig) *TimeGraph {
	maxNodes, maxEdges, maxResults, maxNodeInfos := effectiveMaxGraphNodes(), effectiveMaxGraphEdges(), effectiveMaxGraphResults(), effectiveMaxGraphNodeInfos()
	var relations []TimeGraphRelationConfig
	if cfg != nil {
		if cfg.MaxNodes > 0 {
			maxNodes = cfg.MaxNodes
		}
		if cfg.MaxEdges > 0 {
			maxEdges = cfg.MaxEdges
		}
		if cfg.MaxResults > 0 {
			maxResults = cfg.MaxResults
		}
		if cfg.MaxNodeInfos > 0 {
			maxNodeInfos = cfg.MaxNodeInfos
		}
		relations = append(relations, cfg.Relation...)
	}
	stringDict := NewStringDict() // 每个TimeGraph实例有自己的字符串字典
	return &TimeGraph{
		nodeBuilder:   NewNodeBuilderWithConfig(stringDict, cfg), // 传递局部StringDict给NodeBuilder
		stringDict:    stringDict,
		timeGraph:     make(map[int64]graph.Graph[uint64, uint64]),
		edgeTypes:     make(map[int64]map[timeGraphEdgeKey]map[timeGraphEdgeRelation]struct{}),
		nodeInfos:     make(map[int64]map[uint64]cmdb.Matcher),
		topologyNodes: make(map[uint64]map[int64]struct{}),
		topologyEdges: make(map[timeGraphTopologyEdgeKey]map[int64]struct{}),
		partialTimes:  make(map[int64]string),
		maxNodes:      maxNodes,
		maxEdges:      maxEdges,
		maxResults:    maxResults,
		maxNodeInfos:  maxNodeInfos,
		relations:     relations,
	}
}

// Clean 清理时序图的所有数据
// 清空所有时间分片的图数据，重置节点构建器和字符串字典
// 参数:
//   - ctx: 上下文对象
//
// 优化: 复用 map，减少内存分配
func (q *TimeGraph) Clean(ctx context.Context) {
	q.lock.Lock()
	defer q.lock.Unlock()

	q.nodeBuilder.Clean()
	q.stringDict = NewStringDict() // 重新创建新的字符串字典，实现完全清理
	q.nodeBuilder.stringDict = q.stringDict

	// 清空 map 而不是重新创建，保留底层哈希表结构，减少内存分配
	for k := range q.timeGraph {
		delete(q.timeGraph, k)
	}
	for k := range q.edgeTypes {
		delete(q.edgeTypes, k)
	}
	for k := range q.nodeInfos {
		delete(q.nodeInfos, k)
	}
	for k := range q.topologyNodes {
		delete(q.topologyNodes, k)
	}
	for k := range q.topologyEdges {
		delete(q.topologyEdges, k)
	}
	for k := range q.partialTimes {
		delete(q.partialTimes, k)
	}
	q.edgeCount = 0
	q.nodeInfoCount = 0
}

func (q *TimeGraph) markPartialRange(start, end time.Time, step time.Duration, reason string) {
	if reason == "" {
		reason = "backend_partial"
	}
	if step <= 0 || end.Before(start) {
		return
	}
	startMs, endMs, stepMs := start.UnixMilli(), end.UnixMilli(), step.Milliseconds()
	if stepMs <= 0 || endMs < startMs {
		return
	}
	pointCount := uint64(endMs-startMs)/uint64(stepMs) + 1
	q.lock.Lock()
	defer q.lock.Unlock()
	for index := uint64(0); index < pointCount; index++ {
		timestamp := startMs + int64(index)*stepMs
		q.partialTimes[timestamp] = reason
	}
}

func (q *TimeGraph) partialTimesCopy() map[int64]string {
	q.lock.RLock()
	defer q.lock.RUnlock()
	result := make(map[int64]string, len(q.partialTimes))
	for timestamp, reason := range q.partialTimes {
		result[timestamp] = reason
	}
	return result
}

// Stat 获取时序图的统计信息
// 返回: 格式化的统计信息字符串，包含节点总数和每个时间戳的边数
// 格式示例:
//
//	节点总数: 100
//	时序边数: 1763636985: 50
//	时序边数: 1763637285: 50
//
// 优化: 按时间戳排序输出，预分配字符串构建器容量
func (q *TimeGraph) Stat() string {
	q.lock.RLock()
	defer q.lock.RUnlock()

	// 预分配容量，减少内存重新分配
	nodeCount := q.nodeBuilder.Length()
	graphCount := len(q.timeGraph)
	estimatedSize := 32 + // "节点总数: X\n"
		20*graphCount + // 每个时间戳大约20字节
		64 // 额外缓冲
	var s strings.Builder
	s.Grow(estimatedSize)

	s.WriteString(fmt.Sprintf("节点总数: %d\n", nodeCount))

	// 按时间戳排序输出，提高可读性
	if graphCount > 0 {
		timestamps := make([]int64, 0, graphCount)
		for t := range q.timeGraph {
			timestamps = append(timestamps, t)
		}
		sort.Slice(timestamps, func(i, j int) bool {
			return timestamps[i] < timestamps[j]
		})

		for _, t := range timestamps {
			g := q.timeGraph[t]
			num, _ := g.Size()
			s.WriteString(fmt.Sprintf("时序边数: %d: %d\n", t, num))
		}
	}

	return s.String()
}

// GetNodesByResourceType 根据资源类型获取所有节点信息
// 参数:
//   - resourceType: 资源类型，如 "pod", "container", "node" 等
//
// 返回: 该资源类型下所有节点的匹配器列表，每个匹配器包含节点的维度信息
func (q *TimeGraph) GetNodesByResourceType(resourceType cmdb.Resource) []cmdb.Matcher {
	return q.nodeBuilder.ResourceNodeInfo(resourceType)
}

// AddTimeRelation 添加时间关系，在指定时间戳上建立源资源到目标资源的关系
// 参数:
//   - ctx: 上下文对象
//   - source: 源资源类型
//   - target: 目标资源类型
//   - info: 资源匹配器，包含资源的维度信息（如 bcs_cluster_id, namespace, pod 等）
//   - timestamps: 时间戳列表，可以同时为多个时间戳添加相同的关系
//
// 返回: 错误信息，如果成功则为 nil
// 注意:
//   - 如果 info 为空或 timestamps 为空，直接返回 nil，不添加任何关系
//   - 相同的关系在相同时间戳上重复添加会被忽略（不会报错）
//   - 节点会根据资源信息自动去重，相同信息的资源共享同一个节点ID
//
// 优化: 批量创建时间图，减少 map 查找次数和锁内操作
func (q *TimeGraph) AddTimeRelation(ctx context.Context, source, target cmdb.Resource, info cmdb.Matcher, timestamps ...int64) error {
	return q.AddTimeRelationWithRelation(ctx, cmdb.Relation{V: []cmdb.Resource{source, target}}, info, timestamps...)
}

// AddTimeNode 只添加资源节点，不创建关系边。资源 info 指标通过此方法在遍历
// 开始前为关系节点补充版本、环境等非主键属性。
func (q *TimeGraph) AddTimeNode(ctx context.Context, resource cmdb.Resource, info cmdb.Matcher, timestamps ...int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if resource == "" || len(info) == 0 || len(timestamps) == 0 {
		return nil
	}

	node, err := q.nodeBuilder.GetID(resource, info)
	if err != nil {
		return err
	}

	q.lock.Lock()
	defer q.lock.Unlock()
	if q.maxNodes > 0 && q.nodeBuilder.Length() > q.maxNodes {
		return &ResultLimitError{Reason: "max_graph_nodes", Count: q.nodeBuilder.Length(), Limit: q.maxNodes}
	}
	for _, timestamp := range timestamps {
		if err := ctx.Err(); err != nil {
			return err
		}
		if q.timeGraph[timestamp] == nil {
			q.timeGraph[timestamp] = graph.New(func(t uint64) uint64 { return t }, graph.Directed())
		}
		if q.edgeTypes[timestamp] == nil {
			q.edgeTypes[timestamp] = make(map[timeGraphEdgeKey]map[timeGraphEdgeRelation]struct{})
		}
		if q.nodeInfos[timestamp] == nil {
			q.nodeInfos[timestamp] = make(map[uint64]cmdb.Matcher)
		}
		if err = q.setNodeInfo(timestamp, node, mergeMatcher(q.nodeInfos[timestamp][node], info)); err != nil {
			return err
		}
		if q.topologyNodes[node] == nil {
			q.topologyNodes[node] = make(map[int64]struct{})
		}
		q.topologyNodes[node][timestamp] = struct{}{}
		if err = q.timeGraph[timestamp].AddVertex(node); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
			return err
		}
	}
	return nil
}

// AddTimeRelationWithRelation 添加随时间变化的关系边，并保留查询该边时使用的
// 关系身份。同一对节点可能存在多种关系，因此关系身份不能只依赖普通图边。
func (q *TimeGraph) AddTimeRelationWithRelation(ctx context.Context, relation cmdb.Relation, info cmdb.Matcher, timestamps ...int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(relation.V) != 2 {
		return nil
	}
	source, target := relation.V[0], relation.V[1]
	// 提前返回，避免不必要的操作
	if len(info) == 0 || len(timestamps) == 0 {
		return nil
	}

	// 动态关系使用 from_/to_ 标签，静态关系直接使用资源字段。构造节点身份前
	// 必须先拆分两端标签，否则把同一份 matcher 用于两端会合并不同的动态节点。
	dynamic := relation.Category == string(RelationCategoryDynamic)
	sourcePrefix, targetPrefix := q.relationEndpointPrefixes(relation, source, target)
	direction := relation.Direction
	if dynamic {
		direction = string(DirectionOutbound)
		if sourcePrefix == timeGraphToPrefix {
			direction = string(DirectionInbound)
		}
	}
	sourceInfo := q.relationEndpointInfo(info, source, sourcePrefix, dynamic)
	targetInfo := q.relationEndpointInfo(info, target, targetPrefix, dynamic)
	if len(sourceInfo) == 0 {
		sourceInfo = info
	}
	if len(targetInfo) == 0 {
		targetInfo = info
	}

	// 先获取节点ID，避免在锁内进行复杂操作
	sourceNode, err := q.nodeBuilder.GetID(source, sourceInfo)
	if err != nil {
		return err
	}
	targetNode, err := q.nodeBuilder.GetID(target, targetInfo)
	if err != nil {
		return err
	}

	q.lock.Lock()
	defer q.lock.Unlock()
	if q.maxNodes > 0 && q.nodeBuilder.Length() > q.maxNodes {
		return &ResultLimitError{Reason: "max_graph_nodes", Count: q.nodeBuilder.Length(), Limit: q.maxNodes}
	}

	// 批量创建缺失的时间图，减少重复的 map 查找
	// 使用局部变量缓存 graph.New 的结果，避免重复创建函数对象
	newGraphFunc := func(t uint64) uint64 { return t }
	for _, timestamp := range timestamps {
		if q.timeGraph[timestamp] == nil {
			q.timeGraph[timestamp] = graph.New(newGraphFunc, graph.Directed())
		}
		if q.edgeTypes[timestamp] == nil {
			q.edgeTypes[timestamp] = make(map[timeGraphEdgeKey]map[timeGraphEdgeRelation]struct{})
		}
		if q.nodeInfos[timestamp] == nil {
			q.nodeInfos[timestamp] = make(map[uint64]cmdb.Matcher)
		}
	}

	// 批量添加节点和边
	for _, timestamp := range timestamps {
		if err = ctx.Err(); err != nil {
			return err
		}
		g := q.timeGraph[timestamp]
		if err = q.setNodeInfo(timestamp, sourceNode, mergeMatcher(q.nodeInfos[timestamp][sourceNode], sourceInfo)); err != nil {
			return err
		}
		if err = q.setNodeInfo(timestamp, targetNode, mergeMatcher(q.nodeInfos[timestamp][targetNode], targetInfo)); err != nil {
			return err
		}
		if q.topologyNodes[sourceNode] == nil {
			q.topologyNodes[sourceNode] = make(map[int64]struct{})
		}
		q.topologyNodes[sourceNode][timestamp] = struct{}{}
		if q.topologyNodes[targetNode] == nil {
			q.topologyNodes[targetNode] = make(map[int64]struct{})
		}
		q.topologyNodes[targetNode][timestamp] = struct{}{}

		// 添加源节点，忽略已存在的节点
		if err = g.AddVertex(sourceNode); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
			return err
		}

		// 添加目标节点，忽略已存在的节点
		if err = g.AddVertex(targetNode); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
			return err
		}

		// 添加边，忽略已存在的边
		if err = g.AddEdge(sourceNode, targetNode); err != nil && !errors.Is(err, graph.ErrEdgeAlreadyExists) {
			return err
		}
		edgeKey := timeGraphEdgeKey{source: sourceNode, target: targetNode}
		if _, exists := q.edgeTypes[timestamp][edgeKey]; !exists {
			if q.maxEdges > 0 && q.edgeCount >= q.maxEdges {
				return &ResultLimitError{Reason: "max_graph_edges", Count: q.edgeCount + 1, Limit: q.maxEdges}
			}
			q.edgeTypes[timestamp][edgeKey] = make(map[timeGraphEdgeRelation]struct{})
			q.edgeCount++
		}
		q.edgeTypes[timestamp][edgeKey][timeGraphEdgeRelation{
			relationType: relation.RelationType,
			metricName:   relation.MetricName,
			direction:    direction,
		}] = struct{}{}
		topologyEdgeKey := timeGraphTopologyEdgeKey{
			source: sourceNode,
			target: targetNode,
			relation: timeGraphEdgeRelation{
				relationType: relation.RelationType,
				metricName:   relation.MetricName,
				category:     relation.Category,
				direction:    direction,
			},
		}
		if q.topologyEdges[topologyEdgeKey] == nil {
			q.topologyEdges[topologyEdgeKey] = make(map[int64]struct{})
		}
		q.topologyEdges[topologyEdgeKey][timestamp] = struct{}{}
	}

	return nil
}

func (q *TimeGraph) relationEndpointInfo(info cmdb.Matcher, resource cmdb.Resource, prefix string, dynamic bool) cmdb.Matcher {
	fields := q.nodeBuilder.resourceIndexes(resource)
	fields = append(fields, q.nodeBuilder.resourceInfoFields(resource)...)
	result := make(cmdb.Matcher, len(fields))
	for _, field := range fields {
		if dynamic {
			if value, ok := info[prefix+field]; ok {
				result[field] = value
				continue
			}
		}
		if value, ok := info[field]; ok {
			result[field] = value
		}
	}
	return result
}

func (q *TimeGraph) primaryMatcher(resource cmdb.Resource, info cmdb.Matcher) cmdb.Matcher {
	primary := q.nodeBuilder.resourceIndexes(resource)
	result := make(cmdb.Matcher, len(primary))
	for _, field := range primary {
		if value, ok := info[field]; ok {
			result[field] = value
		}
	}
	if len(result) != len(primary) {
		return nil
	}
	return result
}

func (q *TimeGraph) primaryMatcherKey(resource cmdb.Resource, info cmdb.Matcher) string {
	matcher := q.primaryMatcher(resource, info)
	if len(matcher) == 0 {
		return ""
	}
	return generateResourceIdentityKey(ResourceType(resource), q.nodeBuilder.resourceIndexes(resource), matcher)
}

func (q *TimeGraph) relationEndpointPrefixes(relation cmdb.Relation, source, target cmdb.Resource) (string, string) {
	if relation.Category != string(RelationCategoryDynamic) {
		return "", ""
	}
	// 方向是关系路径规划的一部分。同类型动态关系无法仅凭资源类型判断当前
	// source 对应原始指标的 from_ 还是 to_ 端点，因此这里必须使用规划方向。
	switch relation.Direction {
	case string(DirectionInbound):
		return timeGraphToPrefix, timeGraphFromPrefix
	case string(DirectionOutbound):
		return timeGraphFromPrefix, timeGraphToPrefix
	}
	for _, configured := range q.relations {
		if configured.Category != string(RelationCategoryDynamic) {
			continue
		}
		if relation.MetricName != "" && configured.MetricName != relation.MetricName && configured.RelationType != relation.RelationType {
			continue
		}
		if len(configured.Resources) != 2 {
			continue
		}
		if configured.Resources[0] == source && configured.Resources[1] == target {
			return timeGraphFromPrefix, timeGraphToPrefix
		}
		if configured.Resources[0] == target && configured.Resources[1] == source {
			return timeGraphToPrefix, timeGraphFromPrefix
		}
	}
	return timeGraphFromPrefix, timeGraphToPrefix
}

func (q *TimeGraph) setNodeInfo(timestamp int64, node uint64, info cmdb.Matcher) error {
	if q.nodeInfos[timestamp] == nil {
		q.nodeInfos[timestamp] = make(map[uint64]cmdb.Matcher)
	}
	if _, exists := q.nodeInfos[timestamp][node]; !exists {
		if q.maxNodeInfos > 0 && q.nodeInfoCount >= q.maxNodeInfos {
			return &ResultLimitError{Reason: "max_graph_node_infos", Count: q.nodeInfoCount + 1, Limit: q.maxNodeInfos}
		}
		q.nodeInfoCount++
	}
	q.nodeInfos[timestamp][node] = cloneMatcher(info)
	return nil
}

// MakeQueryTs 根据关系定义构造时序查询。未配置 MetricName 时，按两端资源
// 类型排序生成默认指标名；这里同时使用 step 作为 range 采样步长和回溯窗口。
// 需要分开设置两者时，使用 MakeQueryTsWithWindow。
func (q *TimeGraph) MakeQueryTs(ctx context.Context, spaceUID string, info map[string]string, start time.Time, end time.Time, step time.Duration, relation cmdb.Relation) (*structured.QueryTs, error) {
	return q.MakeQueryTsWithWindow(ctx, spaceUID, info, start, end, step, step, relation)
}

// MakeQueryTsWithWindow 让 range 采样步长与 count_over_time 使用的回溯窗口
// 保持独立。
func (q *TimeGraph) MakeQueryTsWithWindow(ctx context.Context, spaceUID string, info map[string]string, start time.Time, end time.Time, step, window time.Duration, relation cmdb.Relation) (*structured.QueryTs, error) {
	if len(relation.V) != 2 {
		return nil, nil
	}
	source, target := relation.V[0], relation.V[1]
	metric := relation.MetricName
	if metric == "" {
		resources := []string{string(source), string(target)}
		sort.Strings(resources)
		metric = fmt.Sprintf("%s_relation", strings.Join(resources, "_with_"))
	}

	sourcePrefix, targetPrefix := q.relationEndpointPrefixes(relation, source, target)
	indexes, values := q.relationQueryFields(info, source, target, sourcePrefix, targetPrefix, relation.Category == string(RelationCategoryDynamic))
	if len(indexes) == 0 {
		return nil, fmt.Errorf("relation %s -> %s has no configured primary fields", source, target)
	}

	// 预分配切片容量，减少内存重新分配
	indexCount := len(indexes)
	fieldList := make([]structured.ConditionField, 0, indexCount)
	for _, index := range indexes {
		if v, ok := values[index]; ok {
			fieldList = append(fieldList, structured.ConditionField{
				DimensionName: index,
				Value:         []string{v},
				Operator:      structured.ConditionEqual,
			})
		} else {
			fieldList = append(fieldList, structured.ConditionField{
				DimensionName: index,
				Value:         []string{""},
				Operator:      structured.ConditionNotEqual,
			})
		}
	}

	dimensions := append([]string(nil), indexes...)

	// 预分配 conditionList 容量
	conditionList := make([]string, 0, max(indexCount-1, 0))
	for i := 1; i < len(fieldList); i++ {
		conditionList = append(conditionList, structured.ConditionAnd)
	}

	query := &structured.Query{
		FieldName: metric,
		TimeAggregation: structured.TimeAggregation{
			Function: structured.CountOT,
			Window:   structured.Window(window.String()),
		},
		AggregateMethodList: structured.AggregateMethodList{
			{
				Method:     structured.COUNT,
				Dimensions: dimensions,
			},
		},
		Conditions: structured.Conditions{
			FieldList:     fieldList,
			ConditionList: conditionList,
		},
		ReferenceName: metadata.DefaultReferenceName,
	}

	return &structured.QueryTs{
		SpaceUid:    spaceUID,
		QueryList:   []*structured.Query{query},
		MetricMerge: metadata.DefaultReferenceName,
		Start:       cast.ToString(start.Unix()),
		End:         cast.ToString(end.Unix()),
		Step:        step.String(),
	}, nil
}

func (q *TimeGraph) relationQueryFields(info cmdb.Matcher, source, target cmdb.Resource, sourcePrefix, targetPrefix string, dynamic bool) ([]string, map[string]string) {
	fields := make(map[string]struct{})
	values := make(map[string]string)
	addFields := func(resource cmdb.Resource, prefix string, allowBare bool) {
		for _, field := range q.nodeBuilder.resourceIndexes(resource) {
			label := field
			if dynamic {
				label = prefix + field
			}
			if value, ok := info[label]; ok {
				values[label] = value
			} else if dynamic && allowBare {
				if value, ok := info[field]; ok {
					values[label] = value
				}
			}
			fields[label] = struct{}{}
		}
	}
	addFields(source, sourcePrefix, true)
	addFields(target, targetPrefix, false)
	indexes := make([]string, 0, len(fields))
	for field := range fields {
		indexes = append(indexes, field)
	}
	sort.Strings(indexes)
	return indexes, values
}

// MakeResourceInfoQueryTs 构造资源 info 关系查询。与普通关系指标不同，结果
// 必须保留配置中的非主键字段，供后续图遍历过滤或投影。
func (q *TimeGraph) MakeResourceInfoQueryTs(spaceUID string, resource cmdb.Resource, sourceInfo, expandInfo map[string]string, start, end time.Time, step time.Duration) (*structured.QueryTs, error) {
	return q.makeResourceInfoQueryTs(spaceUID, resource, sourceInfo, expandInfo, nil, start, end, step, step)
}

// MakeResourceInfoQueryTsWithWindow 是 MakeResourceInfoQueryTs 的 range 版本。
// primaryMatchers 不为空时，查询只匹配关系指标中发现的完整主键元组。
func (q *TimeGraph) MakeResourceInfoQueryTsWithWindow(spaceUID string, resource cmdb.Resource, sourceInfo, expandInfo map[string]string, primaryMatchers []cmdb.Matcher, start, end time.Time, step, window time.Duration) (*structured.QueryTs, error) {
	return q.makeResourceInfoQueryTs(spaceUID, resource, sourceInfo, expandInfo, primaryMatchers, start, end, step, window)
}

func (q *TimeGraph) makeResourceInfoQueryTs(spaceUID string, resource cmdb.Resource, sourceInfo, expandInfo map[string]string, primaryMatchers []cmdb.Matcher, start, end time.Time, step, window time.Duration) (*structured.QueryTs, error) {
	if resource == "" {
		return nil, nil
	}

	primaryFields := q.nodeBuilder.resourceIndexes(resource)
	infoFields := q.nodeBuilder.resourceInfoFields(resource)
	primarySet := make(map[string]struct{}, len(primaryFields))
	fieldSet := make(map[string]struct{}, len(primaryFields)+len(infoFields))
	for _, field := range primaryFields {
		primarySet[field] = struct{}{}
		fieldSet[field] = struct{}{}
	}
	for _, field := range infoFields {
		fieldSet[field] = struct{}{}
	}
	fields := make([]string, 0, len(fieldSet))
	for field := range fieldSet {
		fields = append(fields, field)
	}
	sort.Strings(fields)

	fieldList := make([]structured.ConditionField, 0, len(primaryFields)+len(expandInfo))
	conditionList := make([]string, 0, max(len(fieldList)-1, 0))
	if len(primaryMatchers) > 0 {
		// 每个 matcher 表示一个完整的主键元组，构造为
		// (k1=v1 AND k2=v2) OR (k1=v1 AND k2=v2)，避免按字段分别过滤时产生
		// 笛卡尔积式的误匹配。
		for _, matcher := range primaryMatchers {
			for fieldIndex, field := range primaryFields {
				value, ok := matcher[field]
				if !ok {
					continue
				}
				if len(fieldList) > 0 {
					if fieldIndex == 0 {
						// 每个元组的第一个字段与前一个元组使用 OR 连接，同一元组内字段
						// 使用 AND 连接。
						conditionList = append(conditionList, structured.ConditionOr)
					} else {
						conditionList = append(conditionList, structured.ConditionAnd)
					}
				}
				fieldList = append(fieldList, structured.ConditionField{DimensionName: field, Value: []string{value}, Operator: structured.ConditionEqual})
			}
		}
	} else {
		for _, field := range primaryFields {
			if value, ok := sourceInfo[field]; ok {
				fieldList = append(fieldList, structured.ConditionField{DimensionName: field, Value: []string{value}, Operator: structured.ConditionEqual})
			} else {
				fieldList = append(fieldList, structured.ConditionField{DimensionName: field, Value: []string{""}, Operator: structured.ConditionNotEqual})
			}
		}
	}
	expandFields := make([]string, 0, len(expandInfo))
	for field := range expandInfo {
		expandFields = append(expandFields, field)
	}
	sort.Strings(expandFields)
	for _, field := range expandFields {
		value := expandInfo[field]
		if _, isPrimary := primarySet[field]; !isPrimary {
			if len(fieldList) > 0 {
				conditionList = append(conditionList, structured.ConditionAnd)
			}
			fieldList = append(fieldList, structured.ConditionField{
				DimensionName: field,
				Value:         []string{value},
				Operator:      structured.ConditionEqual,
			})
		}
	}
	if len(primaryMatchers) == 0 {
		sort.SliceStable(fieldList, func(i, j int) bool {
			return fieldList[i].DimensionName < fieldList[j].DimensionName
		})
		conditionList = conditionList[:0]
		for i := 1; i < len(fieldList); i++ {
			conditionList = append(conditionList, structured.ConditionAnd)
		}
	}

	query := &structured.Query{
		FieldName: fmt.Sprintf("%s_info_relation", resource),
		TimeAggregation: structured.TimeAggregation{
			Function: structured.CountOT,
			Window:   structured.Window(window.String()),
		},
		AggregateMethodList: structured.AggregateMethodList{{
			Method:     structured.COUNT,
			Dimensions: fields,
		}},
		Conditions:    structured.Conditions{FieldList: fieldList, ConditionList: conditionList},
		ReferenceName: metadata.DefaultReferenceName,
	}
	return &structured.QueryTs{
		SpaceUid:    spaceUID,
		QueryList:   []*structured.Query{query},
		MetricMerge: metadata.DefaultReferenceName,
		Start:       cast.ToString(start.Unix()),
		End:         cast.ToString(end.Unix()),
		Step:        step.String(),
	}, nil
}

// PathResourcesResult 路径资源查询结果
// 按时间和目标资源类型分组，每个结果包含从源到目标的完整路径
// 路径中的每个节点都包含资源类型和完整的维度信息
type PathResourcesResult struct {
	Timestamp  int64           // 时间戳
	TargetType cmdb.Resource   // 目标资源类型
	Path       []cmdb.PathNode // 路径上的所有节点，包含资源类型和维度信息（从源到目标）
}

// FindShortestPath 查找从源资源类型到目标资源类型的最短路径
// 参数:
//   - ctx: 上下文对象
//   - sourceType: 源资源类型
//   - targetType: 目标资源类型
//   - sourceMatcher: 源节点的匹配条件，只需要满足部分维度即可（如只指定 namespace）
//
// 返回: 路径结果列表，按时间戳排序
// 每个结果包含:
//   - 时间戳：路径所在的时间点
//   - 目标资源类型：路径的目标资源类型
//   - 路径：从源到目标的完整路径，路径中每个节点包含资源类型和完整的维度信息
//
// 注意:
//   - 遍历 TimeGraph 中的所有时间戳，如果某个时间戳上找不到路径，该时间戳不会出现在结果中
//   - 直接查找从 sourceType 到 targetType 的最短路径，不需要指定中间路径
//   - 部分匹配：只要 sourceMatcher 中的键值对在节点信息中存在且匹配，即认为满足条件
//   - 结果按时间戳排序
func (q *TimeGraph) FindShortestPath(ctx context.Context, sourceType cmdb.Resource, targetType cmdb.Resource, sourceMatcher cmdb.Matcher) ([]PathResourcesResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sourceType == "" || targetType == "" {
		return nil, nil
	}

	q.lock.RLock()
	defer q.lock.RUnlock()

	// 获取所有时间戳并排序
	queryTimestamps := make([]int64, 0, len(q.timeGraph))
	for t := range q.timeGraph {
		queryTimestamps = append(queryTimestamps, t)
	}
	sort.Slice(queryTimestamps, func(i, j int) bool {
		return queryTimestamps[i] < queryTimestamps[j]
	})

	// 1. 找到满足部分条件的源节点
	sourceCandidates := q.findNodesByResourceType(sourceType)
	if len(sourceCandidates) == 0 {
		return nil, nil
	}

	// 2. 找到所有目标资源类型的节点
	targetNodes := q.findNodesByResourceType(targetType)
	if len(targetNodes) == 0 {
		return nil, nil
	}

	// 3. 在每个时间戳的图中查找从源到目标的最短路径
	var results []PathResourcesResult

	for _, timestamp := range queryTimestamps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g := q.timeGraph[timestamp]
		if g == nil {
			continue
		}
		adjacency, err := g.AdjacencyMap()
		if err != nil {
			continue
		}
		sourceNodes := q.findNodesByPartialMatcherAt(timestamp, sourceType, sourceMatcher, sourceCandidates, adjacency)
		if len(sourceNodes) == 0 {
			continue
		}

		// 对每个源节点，查找到每个目标节点的最短路径
		for _, sourceNode := range sourceNodes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			// 获取源节点信息，验证资源类型
			sourceResource, _ := q.nodeBuilder.Info(sourceNode)
			if sourceResource != sourceType {
				continue
			}

			// 对每个目标节点，查找最短路径
			for _, targetNode := range targetNodes {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				// 查找从源节点到目标节点的最短路径
				path, err := graph.ShortestPath(g, sourceNode, targetNode)
				if err != nil {
					continue
				}

				if len(path) == 0 {
					continue
				}

				// 将节点ID路径转换为资源类型和维度信息路径
				pathNodes := make([]cmdb.PathNode, 0, len(path))
				for _, nodeID := range path {
					resourceType, nodeInfo := q.infoAt(timestamp, nodeID)
					pathNodes = append(pathNodes, cmdb.PathNode{
						ResourceType: resourceType,
						Dimensions:   nodeInfo,
					})
				}

				results = append(results, PathResourcesResult{
					Timestamp:  timestamp,
					TargetType: targetType,
					Path:       pathNodes,
				})
				if limit := q.maxResults; limit > 0 && len(results) > limit {
					return nil, &ResultLimitError{Reason: "max_graph_results", Count: len(results), Limit: limit}
				}
			}
		}
	}

	return results, nil
}

// FindPathResources 按构图时使用的资源类型路径约束遍历结果。
// 与 FindShortestPath 不同，它不会让 BFS 拼接不同候选路径中的边，从而产生
// 规划中不存在的路径。
func (q *TimeGraph) FindPathResources(
	ctx context.Context,
	sourceType cmdb.Resource,
	targetTypes []cmdb.Resource,
	sourceMatcher cmdb.Matcher,
	expectedPaths [][]cmdb.Resource,
) ([]PathResourcesResult, error) {
	relationPaths := make([]cmdb.RelationPath, 0, len(expectedPaths))
	for _, expectedPath := range expectedPaths {
		steps := make([]cmdb.RelationPathStep, 0, len(expectedPath))
		for _, resourceType := range expectedPath {
			steps = append(steps, cmdb.RelationPathStep{ResourceType: resourceType})
		}
		relationPaths = append(relationPaths, cmdb.RelationPath{Steps: steps})
	}
	return q.FindRelationPathResources(ctx, sourceType, targetTypes, sourceMatcher, relationPaths)
}

// FindRelationPathResources 同时按资源类型和关系身份约束遍历结果，避免两条
// 端点相同但关系定义不同的边被合并到同一次遍历中。
func (q *TimeGraph) FindRelationPathResources(
	ctx context.Context,
	sourceType cmdb.Resource,
	targetTypes []cmdb.Resource,
	sourceMatcher cmdb.Matcher,
	expectedPaths []cmdb.RelationPath,
) ([]PathResourcesResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sourceType == "" || len(targetTypes) == 0 {
		return nil, nil
	}

	targetTypeSet := make(map[cmdb.Resource]struct{}, len(targetTypes))
	for _, targetType := range targetTypes {
		targetTypeSet[targetType] = struct{}{}
	}

	q.lock.RLock()
	defer q.lock.RUnlock()

	queryTimestamps := make([]int64, 0, len(q.timeGraph))
	for timestamp := range q.timeGraph {
		queryTimestamps = append(queryTimestamps, timestamp)
	}
	sort.Slice(queryTimestamps, func(i, j int) bool { return queryTimestamps[i] < queryTimestamps[j] })

	sourceCandidates := q.findNodesByResourceType(sourceType)
	if len(sourceCandidates) == 0 {
		return nil, nil
	}

	results := make([]PathResourcesResult, 0)
	seen := make(map[string]struct{})
	for _, timestamp := range queryTimestamps {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		g := q.timeGraph[timestamp]
		if g == nil {
			continue
		}
		adjacency, err := g.AdjacencyMap()
		if err != nil {
			continue
		}
		sourceNodes := q.findNodesByPartialMatcherAt(timestamp, sourceType, sourceMatcher, sourceCandidates, adjacency)
		if len(sourceNodes) == 0 {
			continue
		}

		for _, sourceNode := range sourceNodes {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for _, expectedPath := range expectedPaths {
				if len(expectedPath.Steps) == 0 || expectedPath.Steps[0].ResourceType != sourceType {
					continue
				}
				nodePaths := [][]uint64(nil)
				if len(expectedPath.Steps) == 1 {
					nodePaths = [][]uint64{{sourceNode}}
				} else {
					nodePaths = q.findTypedRelationNodePaths(ctx, sourceNode, expectedPath.Steps, adjacency, q.edgeTypes[timestamp])
					if err := ctx.Err(); err != nil {
						return nil, err
					}
				}
				if len(nodePaths) == 0 {
					continue
				}

				targetType := expectedPath.Steps[len(expectedPath.Steps)-1].ResourceType
				if _, ok := targetTypeSet[targetType]; !ok {
					continue
				}
				for _, path := range nodePaths {
					if err := ctx.Err(); err != nil {
						return nil, err
					}
					key := fmt.Sprintf("%d:%s", timestamp, nodePathKey(path))
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}

					pathNodes := make([]cmdb.PathNode, 0, len(path))
					for _, nodeID := range path {
						resourceType, nodeInfo := q.infoAt(timestamp, nodeID)
						pathNodes = append(pathNodes, cmdb.PathNode{
							ResourceType: resourceType,
							Dimensions:   nodeInfo,
						})
					}
					results = append(results, PathResourcesResult{
						Timestamp:  timestamp,
						TargetType: targetType,
						Path:       pathNodes,
					})
					if limit := q.maxResults; limit > 0 && len(results) > limit {
						return nil, &ResultLimitError{Reason: "max_graph_results", Count: len(results), Limit: limit}
					}
				}
			}
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Timestamp != results[j].Timestamp {
			return results[i].Timestamp < results[j].Timestamp
		}
		if results[i].TargetType != results[j].TargetType {
			return results[i].TargetType < results[j].TargetType
		}
		return nodePathKeyFromPathNodes(results[i].Path) < nodePathKeyFromPathNodes(results[j].Path)
	})
	return results, nil
}

func (q *TimeGraph) findTypedNodePaths(sourceNode uint64, expectedPath []cmdb.Resource, adjacency map[uint64]map[uint64]graph.Edge[uint64]) [][]uint64 {
	steps := make([]cmdb.RelationPathStep, 0, len(expectedPath))
	for _, resourceType := range expectedPath {
		steps = append(steps, cmdb.RelationPathStep{ResourceType: resourceType})
	}
	return q.findTypedRelationNodePaths(context.Background(), sourceNode, steps, adjacency, nil)
}

func (q *TimeGraph) findTypedRelationNodePaths(
	ctx context.Context,
	sourceNode uint64,
	expectedPath []cmdb.RelationPathStep,
	adjacency map[uint64]map[uint64]graph.Edge[uint64],
	edgeTypes map[timeGraphEdgeKey]map[timeGraphEdgeRelation]struct{},
) [][]uint64 {
	// Keep every path state instead of only one path per current node. Two
	// different paths may converge on the same node and must both remain
	// available for the rest of the traversal.
	frontier := [][]uint64{{sourceNode}}
	for index := 1; index < len(expectedPath); index++ {
		if err := ctx.Err(); err != nil {
			return nil
		}
		next := make([][]uint64, 0)
		for _, currentPath := range frontier {
			if err := ctx.Err(); err != nil {
				return nil
			}
			current := currentPath[len(currentPath)-1]
			neighbors := make([]uint64, 0, len(adjacency[current]))
			for neighbor := range adjacency[current] {
				neighbors = append(neighbors, neighbor)
			}
			sort.Slice(neighbors, func(i, j int) bool { return neighbors[i] < neighbors[j] })
			for _, neighbor := range neighbors {
				resourceType, _ := q.nodeBuilder.Info(neighbor)
				if resourceType != expectedPath[index].ResourceType || containsNode(currentPath, neighbor) {
					continue
				}
				if !relationEdgeMatches(edgeTypes, timeGraphEdgeKey{source: current, target: neighbor}, expectedPath[index]) {
					continue
				}
				path := append([]uint64(nil), currentPath...)
				next = append(next, append(path, neighbor))
			}
		}
		frontier = next
		if len(frontier) == 0 {
			return nil
		}
	}

	paths := frontier
	sort.Slice(paths, func(i, j int) bool { return nodePathKey(paths[i]) < nodePathKey(paths[j]) })
	return paths
}

func relationEdgeMatches(
	edgeTypes map[timeGraphEdgeKey]map[timeGraphEdgeRelation]struct{},
	edgeKey timeGraphEdgeKey,
	step cmdb.RelationPathStep,
) bool {
	matchDirection := step.Direction != "" && step.Direction != string(DirectionBoth)
	if step.RelationType == "" && step.MetricName == "" && !matchDirection {
		return true
	}
	for relation := range edgeTypes[edgeKey] {
		if matchDirection && relation.direction != step.Direction {
			continue
		}
		if (step.RelationType == "" && step.MetricName == "") ||
			(step.RelationType != "" && relation.relationType == step.RelationType) ||
			(step.MetricName != "" && relation.metricName == step.MetricName) {
			return true
		}
	}
	return false
}

func containsNode(path []uint64, target uint64) bool {
	for _, nodeID := range path {
		if nodeID == target {
			return true
		}
	}
	return false
}

func nodePathKey(path []uint64) string {
	var builder strings.Builder
	for _, nodeID := range path {
		builder.WriteString(fmt.Sprintf("%d/", nodeID))
	}
	return builder.String()
}

func nodePathKeyFromPathNodes(path []cmdb.PathNode) string {
	var builder strings.Builder
	for _, node := range path {
		builder.WriteString(string(node.ResourceType))
		builder.WriteByte('/')
		builder.WriteString(fmt.Sprint(node.Dimensions))
		builder.WriteByte('|')
	}
	return builder.String()
}

// validatePathResourceTypes 验证路径中的节点资源类型是否符合指定的路径顺序
func (q *TimeGraph) validatePathResourceTypes(nodePath []uint64, expectedPath []cmdb.Resource) bool {
	if len(nodePath) != len(expectedPath) {
		return false
	}

	for i, nodeID := range nodePath {
		resourceType, _ := q.nodeBuilder.Info(nodeID)
		if resourceType != expectedPath[i] {
			return false
		}
	}

	return true
}

// findNodesByPartialMatcher 根据部分匹配条件查找节点
// 只要 partialMatcher 中的键值对在节点信息中存在且匹配，即认为满足条件
func (q *TimeGraph) findNodesByPartialMatcher(resourceType cmdb.Resource, partialMatcher cmdb.Matcher) []uint64 {
	if len(partialMatcher) == 0 {
		// 如果没有匹配条件，返回该资源类型的所有节点
		return q.findNodesByResourceType(resourceType)
	}

	var matchedNodes []uint64
	allNodes := q.nodeBuilder.ResourceNodeInfo(resourceType)

	for _, nodeInfo := range allNodes {
		// 检查是否满足部分匹配条件
		if q.matchesPartial(nodeInfo, partialMatcher) {
			// 需要获取节点ID，但 ResourceNodeInfo 只返回 Matcher，需要反向查找
			// 这里我们需要通过尝试获取ID来找到匹配的节点
			// 注意：由于节点已经存在，GetID 会返回现有节点ID
			nodeID, err := q.nodeBuilder.GetID(resourceType, nodeInfo)
			if err == nil {
				matchedNodes = append(matchedNodes, nodeID)
			}
		}
	}

	return matchedNodes
}

func (q *TimeGraph) infoAt(timestamp int64, nodeID uint64) (cmdb.Resource, cmdb.Matcher) {
	resourceType, fallback := q.nodeBuilder.Info(nodeID)
	if info, ok := q.nodeInfos[timestamp][nodeID]; ok {
		return resourceType, cloneMatcher(info)
	}
	return resourceType, fallback
}

func (q *TimeGraph) findNodesByPartialMatcherAt(timestamp int64, resourceType cmdb.Resource, partialMatcher cmdb.Matcher, candidates []uint64, present map[uint64]map[uint64]graph.Edge[uint64]) []uint64 {
	matched := make([]uint64, 0, len(candidates))
	for _, nodeID := range candidates {
		if _, ok := present[nodeID]; !ok {
			continue
		}
		nodeResource, nodeInfo := q.infoAt(timestamp, nodeID)
		if nodeResource == resourceType && q.matchesPartial(nodeInfo, partialMatcher) {
			matched = append(matched, nodeID)
		}
	}
	return matched
}

// matchesPartial 检查节点信息是否满足部分匹配条件
func (q *TimeGraph) matchesPartial(nodeInfo cmdb.Matcher, partialMatcher cmdb.Matcher) bool {
	for key, value := range partialMatcher {
		if nodeValue, ok := nodeInfo[key]; !ok || nodeValue != value {
			return false
		}
	}
	return true
}

// findNodesByResourceType 根据资源类型查找所有节点ID
func (q *TimeGraph) findNodesByResourceType(resourceType cmdb.Resource) []uint64 {
	allNodes := q.nodeBuilder.ResourceNodeInfo(resourceType)
	nodeIDs := make([]uint64, 0, len(allNodes))

	for _, nodeInfo := range allNodes {
		nodeID, err := q.nodeBuilder.GetID(resourceType, nodeInfo)
		if err == nil {
			nodeIDs = append(nodeIDs, nodeID)
		}
	}

	return nodeIDs
}
