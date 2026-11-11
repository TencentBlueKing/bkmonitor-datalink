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
	prommodel "github.com/prometheus/common/model"
	"github.com/spf13/cast"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// TimeGraph 时序图结构，用于管理时间序列的图数据
// 路径查询保留逐时间点图，拓扑查询直接保存共享身份和时间位。
// 两种存储复用节点身份规则，按入口选择，不同时物化。
type TimeGraph struct {
	shared *sharedTimeGraph // 拓扑入口直接保存时间位，不创建逐桶图。
	lock   sync.RWMutex     // 读写锁，保证并发安全

	nodeBuilder   *NodeBuilder                          // 节点构建器，负责节点的创建和去重
	timeGraph     map[int64]graph.Graph[uint64, uint64] // 时间分片图，key为时间戳，value为对应的图结构
	edgeTypes     map[int64]map[timeGraphEdgeKey]map[timeGraphEdgeRelation]struct{}
	nodeInfos     map[int64]map[uint64]cmdb.Matcher // timestamp -> node -> time-specific dimensions
	partialTimes  map[int64]string                  // 时间点 -> 数据不完整原因
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

// NewTimeGraphWithConfig 创建使用独立资源身份规则的图实例，避免读取进程级的
// 旧配置。
func NewTimeGraphWithConfig(cfg *TimeGraphConfig) *TimeGraph {
	q := newTimeGraphWithConfig(cfg)
	q.timeGraph = make(map[int64]graph.Graph[uint64, uint64])
	q.edgeTypes = make(map[int64]map[timeGraphEdgeKey]map[timeGraphEdgeRelation]struct{})
	q.nodeInfos = make(map[int64]map[uint64]cmdb.Matcher)
	return q
}

// 图规模不设上限：规模由请求输入决定，区间查询再按段控制时间分桶图的工作集，
// 容量按规格表配置实例资源。
func newTimeGraphWithConfig(cfg *TimeGraphConfig) *TimeGraph {
	var relations []TimeGraphRelationConfig
	if cfg != nil {
		relations = append(relations, cfg.Relation...)
	}
	return &TimeGraph{
		nodeBuilder:  newNodeBuilder(cfg),
		partialTimes: make(map[int64]string),
		relations:    relations,
	}
}

// Clean 清理时序图的所有数据
// 清空所有时间分片的图数据，重置节点构建器
// 参数:
//   - ctx: 上下文对象
//
// 优化: 复用 map，减少内存分配
func (q *TimeGraph) Clean(ctx context.Context) {
	ctx, span := trace.NewSpan(ctx, "timegraph-clean")
	var cleanupErr error
	defer finishTimeGraphStage(ctx, span, "cleanup", time.Now(), &cleanupErr)
	q.lock.Lock()
	defer q.lock.Unlock()

	span.Set("nodes-before-clean", q.nodeBuilder.Length())
	span.Set("edges-before-clean", q.edgeCount)
	span.Set("parent-canceled", ctx.Err() != nil)
	q.nodeBuilder.Clean()

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
	for k := range q.partialTimes {
		delete(q.partialTimes, k)
	}
	if q.shared != nil {
		q.shared.clear()
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
		reasons := strings.Split(q.partialTimes[timestamp], ";")
		found := false
		for _, existing := range reasons {
			found = found || existing == reason
		}
		if !found {
			reasons = append(reasons, reason)
		}
		sort.Strings(reasons)
		q.partialTimes[timestamp] = strings.Trim(strings.Join(reasons, ";"), ";")
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
	if q.shared != nil {
		active, err := q.shared.timeBits(timestamps)
		if err != nil {
			return err
		}
		q.setSharedNodeInfo(node, info, active)
		return nil
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
		q.setNodeInfo(timestamp, node, mergeMatcher(q.nodeInfos[timestamp][node], info))
		if err = q.timeGraph[timestamp].AddVertex(node); err != nil && !errors.Is(err, graph.ErrVertexAlreadyExists) {
			return err
		}
	}
	return nil
}

// AddTimeRelationWithRelation 添加随时间变化的关系边，并保留查询该边时使用的
// 关系身份。同一对节点可能存在多种关系，因此关系身份不能只依赖普通图边。
func (q *TimeGraph) AddTimeRelationWithRelation(ctx context.Context, relation cmdb.Relation, info cmdb.Matcher, timestamps ...int64) error {
	return q.addTimeRelationWithEndpointBuffers(ctx, relation, info, nil, nil, timestamps...)
}

// 构图器在复用端点缓冲前已保存属性副本，后续写入不会改动图中节点。
func (q *TimeGraph) addTimeRelationWithEndpointBuffers(ctx context.Context, relation cmdb.Relation, info, sourceBuffer, targetBuffer cmdb.Matcher, timestamps ...int64) error {
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
	sourceInfo := q.relationEndpointInfoInto(info, source, sourcePrefix, dynamic, sourceBuffer)
	targetInfo := q.relationEndpointInfoInto(info, target, targetPrefix, dynamic, targetBuffer)
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

	if q.shared != nil {
		return q.addSharedRelation(ctx, relation, direction, sourceNode, targetNode, sourceInfo, targetInfo, timestamps)
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
		q.setNodeInfo(timestamp, sourceNode, mergeMatcher(q.nodeInfos[timestamp][sourceNode], sourceInfo))
		q.setNodeInfo(timestamp, targetNode, mergeMatcher(q.nodeInfos[timestamp][targetNode], targetInfo))

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
			q.edgeTypes[timestamp][edgeKey] = make(map[timeGraphEdgeRelation]struct{})
			q.edgeCount++
		}
		q.edgeTypes[timestamp][edgeKey][timeGraphEdgeRelation{
			relationType: relation.RelationType,
			metricName:   relation.MetricName,
			category:     relation.Category,
			direction:    direction,
		}] = struct{}{}

	}

	return nil
}

func (q *TimeGraph) relationEndpointInfo(info cmdb.Matcher, resource cmdb.Resource, prefix string, dynamic bool) cmdb.Matcher {
	return q.relationEndpointInfoInto(info, resource, prefix, dynamic, nil)
}

// 非空 scratch 只由当前同步 Matrix 应用独占使用。
func (q *TimeGraph) relationEndpointInfoInto(info cmdb.Matcher, resource cmdb.Resource, prefix string, dynamic bool, scratch cmdb.Matcher) cmdb.Matcher {
	primary := q.nodeBuilder.resourceIndexView(resource)
	attributes := q.nodeBuilder.resourceInfoFields(resource)
	result := scratch
	if result == nil {
		result = make(cmdb.Matcher, len(primary)+len(attributes))
	} else {
		clear(result)
	}
	for _, fields := range [2]cmdb.Index{primary, attributes} {
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

func (q *TimeGraph) setNodeInfo(timestamp int64, node uint64, info cmdb.Matcher) {
	if q.nodeInfos[timestamp] == nil {
		q.nodeInfos[timestamp] = make(map[uint64]cmdb.Matcher)
	}
	if _, exists := q.nodeInfos[timestamp][node]; !exists {
		q.nodeInfoCount++
	}
	q.nodeInfos[timestamp][node] = cloneMatcher(info)
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
			Window:   timeGraphQueryWindow(window),
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

// timeGraphQueryWindow preserves existing whole-second formatting while making
// fractional windows parseable by Prometheus (1s500ms instead of 1.5s).
func timeGraphQueryWindow(window time.Duration) structured.Window {
	if window%time.Second == 0 {
		return structured.Window(window.String())
	}
	return structured.Window(prommodel.Duration(window).String())
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
	expandFields := make([]string, 0, len(expandInfo))
	for field := range expandInfo {
		expandFields = append(expandFields, field)
	}
	sort.Strings(expandFields)
	appendExpandConditions := func() {
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
	}
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
			// 属性条件约束每个 OR 分组中的完整主键元组。
			appendExpandConditions()
		}
	} else {
		for _, field := range primaryFields {
			if value, ok := sourceInfo[field]; ok {
				fieldList = append(fieldList, structured.ConditionField{DimensionName: field, Value: []string{value}, Operator: structured.ConditionEqual})
			} else {
				fieldList = append(fieldList, structured.ConditionField{DimensionName: field, Value: []string{""}, Operator: structured.ConditionNotEqual})
			}
		}
		appendExpandConditions()
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
			Window:   timeGraphQueryWindow(window),
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

// RelationTarget 是兼容入口的答案单元：某个评估时刻沿候选类型路径可达的
// 一个目标实例。中间实例只用于判断可达，不进入结果。
type RelationTarget struct {
	Timestamp  int64         // 时间戳
	TargetType cmdb.Resource // 目标资源类型
	Target     cmdb.PathNode // 目标实例，维度取该时刻的节点信息
}

// FindRelationTargets 按时刻沿候选类型路径逐层求可达目标。每层只保留去重后的
// 实例集合，不枚举实例路径，工作量与路径条数无关。兼容入口的候选路径各层资源
// 类型互不相同（自关联只有一跳），节点类型就是它所在的层，集合去重与逐条路径
// 不回访同一实例的约束得到相同的终点集合。
func (q *TimeGraph) FindRelationTargets(
	ctx context.Context,
	sourceType cmdb.Resource,
	targetTypes []cmdb.Resource,
	sourceMatcher cmdb.Matcher,
	expectedPaths []cmdb.RelationPath,
) (results []RelationTarget, err error) {
	ctx, span := trace.NewSpan(ctx, "timegraph-find-relation-targets")
	defer span.End(&err)
	span.Set("source-type", sourceType)
	span.Set("target-types", targetTypes)
	span.Set("source-matcher-count", len(sourceMatcher))
	span.Set("expected-path-count", len(expectedPaths))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sourceType == "" || len(targetTypes) == 0 {
		return nil, nil
	}
	for _, expectedPath := range expectedPaths {
		if err := validateRelationTargetPath(expectedPath.Steps); err != nil {
			return nil, err
		}
	}

	targetTypeSet := make(map[cmdb.Resource]struct{}, len(targetTypes))
	for _, targetType := range targetTypes {
		targetTypeSet[targetType] = struct{}{}
	}

	q.lock.RLock()
	defer q.lock.RUnlock()
	span.Set("graph-timepoint-count", len(q.timeGraph))

	queryTimestamps := make([]int64, 0, len(q.timeGraph))
	for timestamp := range q.timeGraph {
		queryTimestamps = append(queryTimestamps, timestamp)
	}
	sort.Slice(queryTimestamps, func(i, j int) bool { return queryTimestamps[i] < queryTimestamps[j] })

	sourceCandidates := q.findNodesByResourceType(sourceType)
	if len(sourceCandidates) == 0 {
		return nil, nil
	}

	results = make([]RelationTarget, 0)
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

		reached := make(map[cmdb.Resource]map[uint64]struct{})
		for _, expectedPath := range expectedPaths {
			if len(expectedPath.Steps) == 0 || expectedPath.Steps[0].ResourceType != sourceType {
				continue
			}
			targetType := expectedPath.Steps[len(expectedPath.Steps)-1].ResourceType
			if _, ok := targetTypeSet[targetType]; !ok {
				continue
			}
			nodes, err := q.reachTypedRelationNodes(ctx, sourceNodes, expectedPath.Steps, adjacency, q.edgeTypes[timestamp])
			if err != nil {
				return nil, err
			}
			if len(nodes) == 0 {
				continue
			}
			if reached[targetType] == nil {
				reached[targetType] = make(map[uint64]struct{}, len(nodes))
			}
			for _, node := range nodes {
				reached[targetType][node] = struct{}{}
			}
		}

		reachedTypes := make([]cmdb.Resource, 0, len(reached))
		for targetType := range reached {
			reachedTypes = append(reachedTypes, targetType)
		}
		sort.Slice(reachedTypes, func(i, j int) bool { return reachedTypes[i] < reachedTypes[j] })
		for _, targetType := range reachedTypes {
			frame := make([]RelationTarget, 0, len(reached[targetType]))
			for node := range reached[targetType] {
				resourceType, nodeInfo := q.infoAt(timestamp, node)
				frame = append(frame, RelationTarget{
					Timestamp:  timestamp,
					TargetType: targetType,
					Target:     cmdb.PathNode{ResourceType: resourceType, Dimensions: nodeInfo},
				})
			}
			// 帧内顺序只为输出确定；排序键每个目标只计算一次。
			sortByTextKey(frame, func(target RelationTarget) string { return fmt.Sprint(target.Target.Dimensions) })
			results = append(results, frame...)
		}
	}

	span.Set("source-candidate-count", len(sourceCandidates))
	span.Set("result-count", len(results))
	resultTimestamps := make(map[int64]struct{}, len(results))
	for _, result := range results {
		resultTimestamps[result.Timestamp] = struct{}{}
	}
	span.Set("result-timestamp-count", len(resultTimestamps))
	return results, nil
}

// validateRelationTargetPath 拒绝回访资源类型的路径。逐层集合按类型区分层，
// 回访类型时无法表达“同一条路径不回到更早实例”的约束；兼容入口的路径规划
// 只产生简单路径，这里具名拒绝其它来源的形状，不静默改变答案。
func validateRelationTargetPath(steps []cmdb.RelationPathStep) error {
	if len(steps) == 2 && steps[0].ResourceType == steps[1].ResourceType {
		return nil
	}
	seen := make(map[cmdb.Resource]struct{}, len(steps))
	for _, step := range steps {
		if _, exists := seen[step.ResourceType]; exists {
			return fmt.Errorf("relation path revisits resource type %s", step.ResourceType)
		}
		seen[step.ResourceType] = struct{}{}
	}
	return nil
}

// reachTypedRelationNodes 从同一时刻的根集合出发逐层推进，每层只保留按身份
// 去重的实例。实际自环占用一次显式跳数，可到达根自身。
func (q *TimeGraph) reachTypedRelationNodes(
	ctx context.Context,
	roots []uint64,
	expectedPath []cmdb.RelationPathStep,
	adjacency map[uint64]map[uint64]graph.Edge[uint64],
	edgeTypes map[timeGraphEdgeKey]map[timeGraphEdgeRelation]struct{},
) ([]uint64, error) {
	frontier := make(map[uint64]struct{}, len(roots))
	for _, root := range roots {
		frontier[root] = struct{}{}
	}
	for index := 1; index < len(expectedPath); index++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next := make(map[uint64]struct{})
		for current := range frontier {
			for neighbor := range adjacency[current] {
				if _, done := next[neighbor]; done {
					continue
				}
				// 只按类型筛选，不复制节点属性。
				if q.nodeBuilder.resourceName(neighbor) != expectedPath[index].ResourceType {
					continue
				}
				if !relationEdgeMatches(edgeTypes, timeGraphEdgeKey{source: current, target: neighbor}, expectedPath[index]) {
					continue
				}
				next[neighbor] = struct{}{}
			}
		}
		if len(next) == 0 {
			return nil, nil
		}
		frontier = next
	}

	nodes := make([]uint64, 0, len(frontier))
	for node := range frontier {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i] < nodes[j] })
	return nodes, nil
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
