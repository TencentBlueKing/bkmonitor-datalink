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
	"fmt"
	"strings"
)

// SurrealResponseParser 解析 SurrealDB 查询响应
type SurrealResponseParser struct {
	queryStart int64
	queryEnd   int64
}

// NewSurrealResponseParser 创建响应解析器
func NewSurrealResponseParser(queryStart, queryEnd int64) *SurrealResponseParser {
	return &SurrealResponseParser{
		queryStart: queryStart,
		queryEnd:   queryEnd,
	}
}

// Parse 解析 SurrealDB 响应为多个 LivenessGraph
// 响应格式: [{"result": [{"result": {...}}, ...]}]
// 每条记录（每个起始实体）对应一个独立的 LivenessGraph
func (p *SurrealResponseParser) Parse(rawResponse []map[string]any) ([]*LivenessGraph, error) {
	var graphs []*LivenessGraph

	if len(rawResponse) == 0 {
		return graphs, nil
	}

	// 获取第一个查询的结果
	firstResult, ok := rawResponse[0][ResponseFieldResult]
	if !ok {
		return graphs, nil
	}

	results, ok := firstResult.([]any)
	if !ok {
		return graphs, nil
	}

	// 遍历每个结果记录，每条记录生成一个独立的 LivenessGraph
	for _, r := range results {
		record, ok := r.(map[string]any)
		if !ok {
			continue
		}

		// 获取 result 字段（包含 root 和 hopN）
		resultData, ok := record[ResponseFieldResult].(map[string]any)
		if !ok {
			continue
		}

		// 解析 root 节点
		rootData, ok := resultData[ResponseFieldRoot].(map[string]any)
		if !ok {
			continue
		}

		rootNode, err := p.parseEntity(rootData)
		if err != nil {
			// 创建一个带错误的空图
			graph := NewLivenessGraph(p.queryStart, p.queryEnd)
			graph.AddTraversalError(fmt.Sprintf("parse root: %v", err))
			graphs = append(graphs, graph)
			continue
		}

		// 为每条记录创建一个新的图
		graph := NewLivenessGraph(p.queryStart, p.queryEnd)
		graph.AddNode(rootNode)

		// 解析 hop1, hop2, ... 的关系
		for key, value := range resultData {
			if !strings.HasPrefix(key, ResponseFieldHopPrefix) {
				continue
			}

			hopData, ok := value.(map[string]any)
			if !ok {
				continue
			}

			p.parseHopRelations(graph, rootNode.ResourceID, hopData)
		}

		graphs = append(graphs, graph)
	}

	return graphs, nil
}

// parseEntity 解析实体数据为 NodeLiveness
func (p *SurrealResponseParser) parseEntity(data map[string]any) (*NodeLiveness, error) {
	entityID, ok := data[ResponseFieldEntityID].(string)
	if !ok || entityID == "" {
		return nil, fmt.Errorf("missing %s", ResponseFieldEntityID)
	}

	entityType, _ := data[ResponseFieldEntityType].(string)

	// 解析 entity_data 为 labels
	labels := make(map[string]string)
	if entityData, ok := data[ResponseFieldEntityData].(map[string]any); ok {
		for k, v := range entityData {
			if s, ok := v.(string); ok {
				labels[k] = s
			} else if v != nil {
				labels[k] = fmt.Sprintf("%v", v)
			}
		}
	}

	// 解析 liveness 时间段
	rawPeriods := p.parseLivenessPeriods(data[ResponseFieldLiveness])

	return &NodeLiveness{
		ResourceID:   entityID,
		ResourceType: ResourceType(entityType),
		Labels:       labels,
		RawPeriods:   rawPeriods,
	}, nil
}

// parseHopRelations 解析单跳的所有关系
func (p *SurrealResponseParser) parseHopRelations(graph *LivenessGraph, fromID string, hopData map[string]any) {
	for relationKey, relationsValue := range hopData {
		relations, ok := relationsValue.([]any)
		if !ok {
			continue
		}

		for _, rel := range relations {
			relData, ok := rel.(map[string]any)
			if !ok {
				continue
			}

			edge, targetNode, nestedHops, err := p.parseRelation(fromID, relationKey, relData)
			if err != nil {
				graph.AddTraversalError(fmt.Sprintf("parse relation %s: %v", relationKey, err))
				continue
			}

			// 添加目标节点（如果不存在）
			if graph.GetNode(targetNode.ResourceID) == nil {
				graph.AddNode(targetNode)
			}

			// 添加边
			graph.AddEdge(edge)

			// 递归解析嵌套的 hop（hop2, hop3, ...）
			for _, nestedHop := range nestedHops {
				p.parseHopRelations(graph, targetNode.ResourceID, nestedHop)
			}
		}
	}
}

// parseRelation 解析单个关系
// 返回值：边、目标节点、嵌套的hop数据列表、错误
func (p *SurrealResponseParser) parseRelation(fromID, relationKey string, data map[string]any) (*EdgeLiveness, *NodeLiveness, []map[string]any, error) {
	relationID, ok := data[ResponseFieldRelationID].(string)
	if !ok || relationID == "" {
		return nil, nil, nil, fmt.Errorf("missing %s", ResponseFieldRelationID)
	}

	relationType, _ := data[ResponseFieldRelationType].(string)
	relationCategory, _ := data[ResponseFieldRelationCategory].(string)
	direction, _ := data[ResponseFieldDirection].(string)

	// 解析关系的 liveness
	relationLiveness := p.parseLivenessPeriods(data[ResponseFieldRelationLiveness])

	// 解析 target 实体
	targetData, ok := data[ResponseFieldTarget].(map[string]any)
	if !ok {
		return nil, nil, nil, fmt.Errorf("missing %s", ResponseFieldTarget)
	}

	targetNode, err := p.parseEntity(targetData)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse target: %w", err)
	}

	// 提取 target 中嵌套的 hop（hop2, hop3, ...）
	var nestedHops []map[string]any
	for key, value := range targetData {
		if strings.HasPrefix(key, ResponseFieldHopPrefix) {
			if hopData, ok := value.(map[string]any); ok {
				nestedHops = append(nestedHops, hopData)
			}
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
	for _, item := range arr {
		periodData, ok := item.(map[string]any)
		if !ok {
			continue
		}

		start := p.toInt64(periodData[FieldPeriodStart])
		end := p.toInt64(periodData[FieldPeriodEnd])

		if start <= end {
			periods = append(periods, &VisiblePeriod{
				Start: start,
				End:   end,
			})
		}
	}

	return periods
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
	case string:
		var i int64
		fmt.Sscanf(n, "%d", &i)
		return i
	}
	return 0
}
