// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/set"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

// TimeGraph 时序图结构，用于管理时间序列的图数据
// 采用时间分片设计，每个时间戳对应一个独立的图，提高查询效率
// 使用节点共享机制，相同资源信息共享同一个节点ID，节省内存
type TimeGraph struct {
	lock sync.RWMutex // 读写锁，保证并发安全

	nodeBuilder *NodeBuilder                          // 节点构建器，负责节点的创建和去重
	stringDict  *StringDict                           // 局部字符串字典，避免全局溢出，每个实例独立管理
	timeGraph   map[int64]graph.Graph[uint64, uint64] // 时间分片图，key为时间戳，value为对应的图结构
}

// NewTimeGraph 创建一个新的时序图实例
// 返回: 新创建的 TimeGraph 指针
// 注意: 每个实例都有自己独立的字符串字典，避免全局字典溢出问题
func NewTimeGraph() *TimeGraph {
	stringDict := NewStringDict() // 每个TimeGraph实例有自己的字符串字典
	return &TimeGraph{
		nodeBuilder: NewNodeBuilder(stringDict), // 传递局部StringDict给NodeBuilder
		stringDict:  stringDict,
		timeGraph:   make(map[int64]graph.Graph[uint64, uint64]),
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

	// 清空 map 而不是重新创建，保留底层哈希表结构，减少内存分配
	for k := range q.timeGraph {
		delete(q.timeGraph, k)
	}
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
	// 提前返回，避免不必要的操作
	if len(info) == 0 || len(timestamps) == 0 {
		return nil
	}

	// 先获取节点ID，避免在锁内进行复杂操作
	sourceNode, err := q.nodeBuilder.GetID(source, info)
	if err != nil {
		return err
	}
	targetNode, err := q.nodeBuilder.GetID(target, info)
	if err != nil {
		return err
	}

	q.lock.Lock()
	defer q.lock.Unlock()

	// 批量创建缺失的时间图，减少重复的 map 查找
	// 使用局部变量缓存 graph.New 的结果，避免重复创建函数对象
	newGraphFunc := func(t uint64) uint64 { return t }
	for _, timestamp := range timestamps {
		if q.timeGraph[timestamp] == nil {
			q.timeGraph[timestamp] = graph.New(newGraphFunc, graph.Directed())
		}
	}

	// 批量添加节点和边
	for _, timestamp := range timestamps {
		g := q.timeGraph[timestamp]

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
	}

	return nil
}

// MakeQueryTs 根据关系信息生成时序查询对象
// 参数:
//   - ctx: 上下文对象
//   - spaceUID: 空间UID
//   - info: 资源匹配器，包含查询的维度信息
//   - start: 查询开始时间
//   - end: 查询结束时间
//   - step: 查询步长
//   - relation: 资源关系，包含源资源、目标资源和指标名称
//
// 返回: 时序查询对象指针，如果关系没有对应的指标则返回 nil
// 生成的查询特点:
//   - 使用 count_over_time 进行时间聚合
//   - 使用 COUNT 方法进行维度聚合
//   - 对于 info 中存在的维度使用等值条件，不存在的使用非等值条件
//
// 优化: 预分配切片容量，减少内存重新分配
func (q *TimeGraph) MakeQueryTs(ctx context.Context, spaceUID string, info map[string]string, start time.Time, end time.Time, step time.Duration, relation cmdb.Relation) (*structured.QueryTs, error) {
	source, target, metric := relation.Info()
	if metric == "" {
		return nil, nil
	}

	indexSet := set.New[string](ResourcesIndex(source, target)...)
	indexes := indexSet.ToArray()
	sort.Strings(indexes)

	// 预分配切片容量，减少内存重新分配
	indexCount := len(indexes)
	fieldList := make([]structured.ConditionField, 0, indexCount)
	for _, index := range indexes {
		if v, ok := info[index]; ok {
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

	dimensions := indexSet.ToArray()
	sort.Strings(dimensions)

	// 预分配 conditionList 容量
	conditionList := make([]string, 0, indexCount-1)
	for i := 1; i < len(fieldList); i++ {
		conditionList = append(conditionList, structured.ConditionAnd)
	}

	query := &structured.Query{
		FieldName: metric,
		TimeAggregation: structured.TimeAggregation{
			Function: structured.CountOT,
			Window:   structured.Window(step.String()),
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
	sourceNodes := q.findNodesByPartialMatcher(sourceType, sourceMatcher)
	if len(sourceNodes) == 0 {
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
		g := q.timeGraph[timestamp]
		if g == nil {
			continue
		}

		// 对每个源节点，查找到目标节点的最短路径
		for _, sourceNode := range sourceNodes {
			// 获取源节点信息，验证资源类型
			sourceResource, _ := q.nodeBuilder.Info(sourceNode)
			if sourceResource != sourceType {
				continue
			}

			// 查找从源节点到目标节点的最短路径
			shortestPath := q.findShortestPathToAnyTarget(g, sourceNode, targetNodes)
			if len(shortestPath) == 0 {
				continue
			}

			// 将节点ID路径转换为资源类型和维度信息路径
			pathNodes := make([]cmdb.PathNode, 0, len(shortestPath))
			for _, nodeID := range shortestPath {
				resourceType, nodeInfo := q.nodeBuilder.Info(nodeID)
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
		}
	}

	return results, nil
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

// findShortestPathToAnyTarget 查找从源节点到任意目标节点的最短路径
// 返回找到的最短路径（节点ID列表），如果不存在路径则返回空切片
func (q *TimeGraph) findShortestPathToAnyTarget(g graph.Graph[uint64, uint64], sourceNode uint64, targetNodes []uint64) []uint64 {
	if len(targetNodes) == 0 {
		return nil
	}

	var shortestPath []uint64
	shortestLength := -1

	// 对每个目标节点，尝试查找最短路径
	for _, targetNode := range targetNodes {
		path, err := graph.ShortestPath(g, sourceNode, targetNode)
		if err != nil {
			continue
		}

		pathLength := len(path) - 1
		if shortestLength < 0 || pathLength < shortestLength {
			shortestPath = path
			shortestLength = pathLength
		}
	}

	return shortestPath
}
