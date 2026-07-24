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
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// SurrealResponseParser 解析 SurrealDB 查询响应
type SurrealResponseParser struct {
	queryStart int64
	queryEnd   int64
	// maxEdgesPerHop 与查询端的“上限加一”策略配合，用于识别单跳边扩散超限。
	maxEdgesPerHop int
}

// NewSurrealResponseParser 创建响应解析器
func NewSurrealResponseParser(queryStart, queryEnd int64) *SurrealResponseParser {
	return &SurrealResponseParser{
		queryStart:     queryStart,
		queryEnd:       queryEnd,
		maxEdgesPerHop: effectiveMaxEdgesPerHop(),
	}
}

// Parse 解析 SurrealDB 响应为多个 LivenessGraph
// 响应格式: [{"result": [{"result": {...}}, ...]}]
// 每条记录（每个起始实体）对应一个独立的 LivenessGraph
func (p *SurrealResponseParser) Parse(rawResponse []map[string]any) ([]*LivenessGraph, error) {
	var graphs []*LivenessGraph

	if len(rawResponse) == 0 {
		return nil, fmt.Errorf("response: expected at least one statement result")
	}

	// 获取第一个查询的结果
	firstResult, ok := rawResponse[0][ResponseFieldResult]
	if !ok {
		return nil, fmt.Errorf("response[0].%s: missing field", ResponseFieldResult)
	}

	results, ok := responseArray(firstResult)
	if !ok {
		return nil, fmt.Errorf("response[0].%s: expected array, got %T", ResponseFieldResult, firstResult)
	}

	// 遍历每个结果记录，每条记录生成一个独立的 LivenessGraph
	for rowIndex, r := range results {
		record, ok := r.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("response[0].%s[%d]: expected object, got %T", ResponseFieldResult, rowIndex, r)
		}

		// 获取 result 字段（包含 root 和 hopN）
		innerResult, exists := record[ResponseFieldResult]
		if !exists {
			return nil, fmt.Errorf("response[0].%s[%d].%s: missing field", ResponseFieldResult, rowIndex, ResponseFieldResult)
		}
		resultData, ok := innerResult.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("response[0].%s[%d].%s: expected object, got %T", ResponseFieldResult, rowIndex, ResponseFieldResult, innerResult)
		}

		// 解析 root 节点
		rootValue, exists := resultData[ResponseFieldRoot]
		if !exists {
			return nil, fmt.Errorf("response[0].%s[%d].%s.%s: missing field", ResponseFieldResult, rowIndex, ResponseFieldResult, ResponseFieldRoot)
		}
		rootData, ok := rootValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("response[0].%s[%d].%s.%s: expected object, got %T", ResponseFieldResult, rowIndex, ResponseFieldResult, ResponseFieldRoot, rootValue)
		}

		rootNode, err := p.parseEntity(rootData)
		if err != nil {
			return nil, fmt.Errorf("response[0].%s[%d].%s.%s: %w", ResponseFieldResult, rowIndex, ResponseFieldResult, ResponseFieldRoot, err)
		}

		// 为每条记录创建一个新的图
		graph := NewLivenessGraph(p.queryStart, p.queryEnd)
		graph.AddNode(rootNode)
		graph.RootID = rootNode.ResourceID

		// 解析 hop1, hop2, ... 的关系
		for key, value := range resultData {
			if !strings.HasPrefix(key, ResponseFieldHopPrefix) {
				continue
			}

			hopData, ok := value.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("response[0].%s[%d].%s.%s: expected object, got %T", ResponseFieldResult, rowIndex, ResponseFieldResult, key, value)
			}

			if err := p.parseHopRelationsAt(graph, rootNode.ResourceID, hopData, fmt.Sprintf("response[0].%s[%d].%s.%s", ResponseFieldResult, rowIndex, ResponseFieldResult, key)); err != nil {
				return nil, err
			}
		}

		graphs = append(graphs, graph)
	}

	return graphs, nil
}

func responseArray(value any) ([]any, bool) {
	if result, ok := value.([]any); ok {
		return result, true
	}
	if typed, ok := value.([]map[string]any); ok {
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, item)
		}
		return result, true
	}
	return nil, false
}

// parseEntity 解析实体数据为 NodeLiveness
func (p *SurrealResponseParser) parseEntity(data map[string]any) (*NodeLiveness, error) {
	entityID, ok := data[ResponseFieldEntityID].(string)
	if !ok || entityID == "" {
		return nil, fmt.Errorf("missing %s", ResponseFieldEntityID)
	}

	entityType, _ := data[ResponseFieldEntityType].(string)
	if entityType == "" {
		return nil, fmt.Errorf("missing %s", ResponseFieldEntityType)
	}

	// 解析 entity_data 为 labels
	labels := make(map[string]string)
	if entityDataValue, exists := data[ResponseFieldEntityData]; exists {
		entityData, ok := entityDataValue.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: expected object, got %T", ResponseFieldEntityData, entityDataValue)
		}
		for k, v := range entityData {
			if s, ok := v.(string); ok {
				labels[k] = s
			} else if v != nil {
				labels[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	// 解析 liveness 时间段。instant 查询会省略该投影；一旦字段存在，
	// 其结构必须完整，不能把损坏的 period 静默当成“没有存活数据”。
	var rawPeriods []*VisiblePeriod
	if liveness, exists := data[ResponseFieldLiveness]; exists {
		var err error
		rawPeriods, err = p.parseLivenessPeriodsStrict(liveness, ResponseFieldLiveness)
		if err != nil {
			return nil, err
		}
	}

	return &NodeLiveness{
		ResourceID:   entityID,
		ResourceType: ResourceType(entityType),
		Labels:       labels,
		RawPeriods:   rawPeriods,
	}, nil
}

// parseHopRelations 解析单跳的所有关系
func (p *SurrealResponseParser) parseHopRelations(graph *LivenessGraph, fromID string, hopData map[string]any) error {
	return p.parseHopRelationsAt(graph, fromID, hopData, "hop")
}

func (p *SurrealResponseParser) parseHopRelationsAt(graph *LivenessGraph, fromID string, hopData map[string]any, fieldPath string) error {
	for relationKey, relationsValue := range hopData {
		relations, ok := responseArray(relationsValue)
		if !ok {
			return fmt.Errorf("%s.%s: expected array, got %T", fieldPath, relationKey, relationsValue)
		}
		// 查询端会比配置上限多取一条；一旦出现额外记录就返回明确错误，不消费不完整数据。
		if p.maxEdgesPerHop > 0 && len(relations) > p.maxEdgesPerHop {
			return &ResultLimitError{
				Reason: "max_edges_per_hop",
				Count:  len(relations),
				Limit:  p.maxEdgesPerHop,
				Path:   fieldPath + "." + relationKey,
			}
		}

		for relationIndex, rel := range relations {
			relData, ok := rel.(map[string]any)
			if !ok {
				return fmt.Errorf("%s.%s[%d]: expected object, got %T", fieldPath, relationKey, relationIndex, rel)
			}

			edge, targetNode, nestedHops, err := p.parseRelation(fromID, relationKey, relData)
			if err != nil {
				return fmt.Errorf("%s.%s[%d]: %w", fieldPath, relationKey, relationIndex, err)
			}

			// 添加目标节点（如果不存在）
			if graph.GetNode(targetNode.ResourceID) == nil {
				graph.AddNode(targetNode)
			}

			// 添加边
			graph.AddEdge(edge)

			// 递归解析嵌套的 hop（hop2, hop3, ...）
			for nestedIndex, nestedHop := range nestedHops {
				if err := p.parseHopRelationsAt(graph, targetNode.ResourceID, nestedHop, fmt.Sprintf("%s.%s[%d].%s[%d]", fieldPath, relationKey, relationIndex, ResponseFieldTarget, nestedIndex)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// parseRelation 解析单个关系
// 返回值：边、目标节点、嵌套的hop数据列表、错误
func (p *SurrealResponseParser) parseRelation(fromID, relationKey string, data map[string]any) (*EdgeLiveness, *NodeLiveness, []map[string]any, error) {
	relationID, ok := data[ResponseFieldRelationID].(string)
	if !ok || relationID == "" {
		return nil, nil, nil, fmt.Errorf("missing %s", ResponseFieldRelationID)
	}

	relationType, _ := data[ResponseFieldRelationType].(string)
	if relationType == "" {
		return nil, nil, nil, fmt.Errorf("missing %s", ResponseFieldRelationType)
	}
	relationCategory, _ := data[ResponseFieldRelationCategory].(string)
	if relationCategory == "" {
		return nil, nil, nil, fmt.Errorf("missing %s", ResponseFieldRelationCategory)
	}
	direction, _ := data[ResponseFieldDirection].(string)

	// 解析关系的 liveness
	var relationLiveness []*VisiblePeriod
	if liveness, exists := data[ResponseFieldRelationLiveness]; exists {
		var err error
		relationLiveness, err = p.parseLivenessPeriodsStrict(liveness, ResponseFieldRelationLiveness)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	// 解析 target 实体
	targetValue, exists := data[ResponseFieldTarget]
	if !exists {
		return nil, nil, nil, fmt.Errorf("missing %s", ResponseFieldTarget)
	}
	targetData, ok := targetValue.(map[string]any)
	if !ok {
		return nil, nil, nil, fmt.Errorf("%s: expected object, got %T", ResponseFieldTarget, targetValue)
	}

	targetNode, err := p.parseEntity(targetData)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse target: %w", err)
	}

	// 提取 target 中嵌套的 hop（hop2, hop3, ...）
	var nestedHops []map[string]any
	for key, value := range targetData {
		if strings.HasPrefix(key, ResponseFieldHopPrefix) {
			hopData, ok := value.(map[string]any)
			if !ok {
				return nil, nil, nil, fmt.Errorf("%s.%s: expected object, got %T", ResponseFieldTarget, key, value)
			}
			nestedHops = append(nestedHops, hopData)
		}
	}

	edge := &EdgeLiveness{
		RelationID:   relationID,
		RelationType: RelationType(relationType),
		Category:     RelationCategory(relationCategory),
		Direction:    TraversalDirection(direction),
		FromID:       fromID,
		ToID:         targetNode.ResourceID,
		RawPeriods:   relationLiveness,
	}

	return edge, targetNode, nestedHops, nil
}

// parseLivenessPeriods 解析 liveness 数组为 VisiblePeriod 列表
func (p *SurrealResponseParser) parseLivenessPeriods(data any) []*VisiblePeriod {
	arr, ok := data.([]any)
	if !ok || len(arr) == 0 {
		return nil
	}

	periods := make([]*VisiblePeriod, 0, len(arr))
	// 原始毫秒数据相差 1 毫秒视为连续；若检测到秒转毫秒，则相差 1 秒（1000 毫秒）视为连续。
	adjacencyGap := int64(1)
	for _, item := range arr {
		periodData, ok := item.(map[string]any)
		if !ok {
			continue
		}

		start := p.toInt64(periodData[FieldPeriodStart])
		end := p.toInt64(periodData[FieldPeriodEnd])
		// BKBase HTTP 客户端开启 UseNumber 后会把 JSON 数字保留为 json.Number；
		// mock / 单测里又常见 int、float64 或 string。先统一成 int64，再按查询窗口判断是否需要秒转毫秒。
		normalizedStart := p.normalizePeriodTimestamp(start)
		normalizedEnd := p.normalizePeriodTimestamp(end)
		if normalizedStart != start || normalizedEnd != end {
			adjacencyGap = 1000
		}
		start, end = normalizedStart, normalizedEnd

		if start <= end {
			periods = append(periods, &VisiblePeriod{
				Start: start,
				End:   end,
			})
		}
	}

	return mergeVisiblePeriodsWithGap(periods, adjacencyGap)
}

func (p *SurrealResponseParser) parseLivenessPeriodsStrict(data any, fieldPath string) ([]*VisiblePeriod, error) {
	arr, ok := responseArray(data)
	if !ok {
		return nil, fmt.Errorf("%s: expected array, got %T", fieldPath, data)
	}
	if len(arr) == 0 {
		return nil, nil
	}
	periods := make([]*VisiblePeriod, 0, len(arr))
	// 原始毫秒数据相差 1 毫秒视为连续；若检测到秒转毫秒，则相差 1 秒（1000 毫秒）视为连续。
	adjacencyGap := int64(1)
	for index, item := range arr {
		periodData, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s[%d]: expected object, got %T", fieldPath, index, item)
		}
		startValue, exists := periodData[FieldPeriodStart]
		if !exists {
			return nil, fmt.Errorf("%s[%d].%s: missing field", fieldPath, index, FieldPeriodStart)
		}
		endValue, exists := periodData[FieldPeriodEnd]
		if !exists {
			return nil, fmt.Errorf("%s[%d].%s: missing field", fieldPath, index, FieldPeriodEnd)
		}
		start, ok := p.toInt64Strict(startValue)
		if !ok {
			return nil, fmt.Errorf("%s[%d].%s: invalid integer %v", fieldPath, index, FieldPeriodStart, startValue)
		}
		end, ok := p.toInt64Strict(endValue)
		if !ok {
			return nil, fmt.Errorf("%s[%d].%s: invalid integer %v", fieldPath, index, FieldPeriodEnd, endValue)
		}
		normalizedStart := p.normalizePeriodTimestamp(start)
		normalizedEnd := p.normalizePeriodTimestamp(end)
		if normalizedStart != start || normalizedEnd != end {
			adjacencyGap = 1000
		}
		start, end = normalizedStart, normalizedEnd
		if start > end {
			return nil, fmt.Errorf("%s[%d]: period_start must be less than or equal to period_end", fieldPath, index)
		}
		periods = append(periods, &VisiblePeriod{Start: start, End: end})
	}
	return mergeVisiblePeriodsWithGap(periods, adjacencyGap), nil
}

// mergeVisiblePeriods 合并重叠或首尾相邻的可见时段，默认按毫秒时间戳处理。
func mergeVisiblePeriods(periods []*VisiblePeriod) []*VisiblePeriod {
	return mergeVisiblePeriodsWithGap(periods, 1)
}

// mergeVisiblePeriodsWithGap 在不修改输入切片的前提下排序并合并时段；
// adjacencyGap 用于兼容不同时间精度下“相邻时段”的间隔定义。
func mergeVisiblePeriodsWithGap(periods []*VisiblePeriod, adjacencyGap int64) []*VisiblePeriod {
	if len(periods) == 0 {
		return nil
	}
	sorted := make([]*VisiblePeriod, 0, len(periods))
	for _, period := range periods {
		if period == nil || period.Start > period.End {
			continue
		}
		copyPeriod := *period
		sorted = append(sorted, &copyPeriod)
	}
	if len(sorted) == 0 {
		return nil
	}
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Start != sorted[j].Start {
			return sorted[i].Start < sorted[j].Start
		}
		return sorted[i].End < sorted[j].End
	})

	merged := make([]*VisiblePeriod, 0, len(sorted))
	current := sorted[0]
	for _, next := range sorted[1:] {
		mergeable := next.Start <= current.End
		if !mergeable && adjacencyGap > 0 && current.End <= math.MaxInt64-adjacencyGap {
			mergeable = next.Start <= current.End+adjacencyGap
		}
		if mergeable {
			if next.End > current.End {
				current.End = next.End
			}
			continue
		}
		merged = append(merged, current)
		current = next
	}
	return append(merged, current)
}

func (p *SurrealResponseParser) normalizePeriodTimestamp(ts int64) int64 {
	if p.queryEnd >= 1e12 && ts > 0 && ts < 1e12 {
		// v1beta3 对外和 range bucket 都使用毫秒时间戳；实体 liveness 查询可能返回秒级 period。
		// 只在查询窗口明确是毫秒且 period 看起来像秒时转换，避免把已经是毫秒的关系 period 再放大。
		return ts * 1000
	}
	return ts
}

// toInt64 将 any 类型转换为 int64
func (p *SurrealResponseParser) toInt64(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return i
		}
		f, err := n.Float64()
		if err == nil {
			return int64(f)
		}
	case string:
		var i int64
		fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}

func (p *SurrealResponseParser) toInt64Strict(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		if math.IsInf(n, 0) || math.IsNaN(n) || n != math.Trunc(n) || n >= float64(math.MaxInt64) || n < float64(math.MinInt64) {
			return 0, false
		}
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	case json.Number:
		i, err := n.Int64()
		return i, err == nil
	case string:
		i, err := strconv.ParseInt(n, 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}
