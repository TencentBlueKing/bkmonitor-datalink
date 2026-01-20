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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// PathFinder 路径发现器，用于查找资源之间的关联路径
type PathFinder struct {
	allowedCategories []RelationCategory
	dynamicDirection  TraversalDirection
	maxHops           int
}

// PathFinderOption PathFinder 配置选项
type PathFinderOption func(*PathFinder)

// WithAllowedCategories 设置允许的关系类别
func WithAllowedCategories(categories ...RelationCategory) PathFinderOption {
	return func(pf *PathFinder) {
		if len(categories) > 0 {
			pf.allowedCategories = categories
		}
	}
}

// WithDynamicDirection 设置动态关系方向
func WithDynamicDirection(direction TraversalDirection) PathFinderOption {
	return func(pf *PathFinder) {
		pf.dynamicDirection = direction
	}
}

// WithMaxHops 设置最大跳数
func WithMaxHops(maxHops int) PathFinderOption {
	return func(pf *PathFinder) {
		pf.maxHops = maxHops
	}
}

// NewPathFinder 创建路径发现器
func NewPathFinder(opts ...PathFinderOption) *PathFinder {
	pf := &PathFinder{
		allowedCategories: []RelationCategory{RelationCategoryStatic, RelationCategoryDynamic},
		dynamicDirection:  DirectionBoth,
		maxHops:           DefaultMaxHops,
	}
	for _, opt := range opts {
		opt(pf)
	}
	return pf
}

// FindAllPaths 查找从 source 到 target 的所有路径
func (pf *PathFinder) FindAllPaths(source, target ResourceType, pathResource []ResourceType) ([]cmdb.PathV2, error) {
	if source == target {
		return []cmdb.PathV2{{Steps: []cmdb.PathStepV2{{ResourceType: string(source)}}}}, nil
	}

	var results []cmdb.PathV2
	visited := make(map[ResourceType]bool)
	currentPath := []cmdb.PathStepV2{{ResourceType: string(source)}}
	visited[source] = true

	pf.dfs(source, target, pathResource, 0, visited, currentPath, &results)

	if len(results) == 0 {
		return nil, fmt.Errorf("empty paths with %s => %s through %v", source, target, pathResource)
	}

	return results, nil
}

// dfs 深度优先搜索所有路径
func (pf *PathFinder) dfs(
	current, target ResourceType,
	pathResource []ResourceType,
	pathIdx int,
	visited map[ResourceType]bool,
	currentPath []cmdb.PathStepV2,
	results *[]cmdb.PathV2,
) {
	if len(currentPath) > pf.maxHops+1 {
		return
	}

	if current == target {
		if pf.satisfiesPathConstraint(currentPath, pathResource) {
			pathCopy := make([]cmdb.PathStepV2, len(currentPath))
			copy(pathCopy, currentPath)
			*results = append(*results, cmdb.PathV2{Steps: pathCopy})
		}
		return
	}

	relations := pf.getRelationsForType(current)

	for _, rel := range relations {
		nextType := rel.TargetType
		if visited[nextType] {
			continue
		}

		if len(pathResource) > 0 && pathIdx < len(pathResource) {
			if nextType != pathResource[pathIdx] && nextType != target {
				continue
			}
		}

		visited[nextType] = true
		nextStep := cmdb.PathStepV2{
			ResourceType: string(nextType),
			RelationType: string(rel.Schema.RelationType),
			Category:     string(rel.Schema.Category),
			Direction:    string(rel.Direction),
		}
		currentPath = append(currentPath, nextStep)

		nextPathIdx := pathIdx
		if len(pathResource) > 0 && pathIdx < len(pathResource) && nextType == pathResource[pathIdx] {
			nextPathIdx++
		}

		pf.dfs(nextType, target, pathResource, nextPathIdx, visited, currentPath, results)

		currentPath = currentPath[:len(currentPath)-1]
		visited[nextType] = false
	}
}

// satisfiesPathConstraint 检查路径是否满足 pathResource 约束
func (pf *PathFinder) satisfiesPathConstraint(path []cmdb.PathStepV2, pathResource []ResourceType) bool {
	if len(pathResource) == 0 {
		return true
	}

	pathTypes := make([]ResourceType, 0, len(path))
	for _, step := range path {
		pathTypes = append(pathTypes, ResourceType(step.ResourceType))
	}

	pathIdx := 0
	for _, required := range pathResource {
		if required == "" {
			continue
		}
		found := false
		for ; pathIdx < len(pathTypes); pathIdx++ {
			if pathTypes[pathIdx] == required {
				found = true
				pathIdx++
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

// getRelationsForType 获取指定资源类型的所有可用关系
func (pf *PathFinder) getRelationsForType(resourceType ResourceType) []*RelationQueryInfo {
	var results []*RelationQueryInfo

	for i := range schemaRegistry {
		schema := &schemaRegistry[i]

		if !pf.isRelationCategoryAllowed(schema.Category) {
			continue
		}

		if schema.FromType != resourceType && schema.ToType != resourceType {
			continue
		}

		if schema.Category == RelationCategoryStatic {
			info := pf.buildStaticRelationInfo(schema, resourceType)
			if info != nil {
				results = append(results, info)
			}
		} else {
			infos := pf.buildDynamicRelationInfos(schema, resourceType)
			results = append(results, infos...)
		}
	}

	return results
}

// isRelationCategoryAllowed 检查关系类别是否允许
func (pf *PathFinder) isRelationCategoryAllowed(category RelationCategory) bool {
	for _, c := range pf.allowedCategories {
		if c == category {
			return true
		}
	}
	return false
}

// buildStaticRelationInfo 构建静态关系查询信息
func (pf *PathFinder) buildStaticRelationInfo(schema *RelationSchema, currentType ResourceType) *RelationQueryInfo {
	info := &RelationQueryInfo{
		Schema:    schema,
		KeySuffix: "",
	}

	if schema.FromType == currentType {
		info.Direction = DirectionOutbound
		info.WhereField = fieldSourceID
		info.SelectField = fieldTargetID
		info.TargetField = fieldTargetID
		info.TargetType = schema.ToType
	} else {
		info.Direction = DirectionInbound
		info.WhereField = fieldTargetID
		info.SelectField = fieldSourceID
		info.TargetField = fieldSourceID
		info.TargetType = schema.FromType
	}

	return info
}

// buildDynamicRelationInfos 构建动态关系查询信息
func (pf *PathFinder) buildDynamicRelationInfos(schema *RelationSchema, currentType ResourceType) []*RelationQueryInfo {
	var results []*RelationQueryInfo
	direction := pf.dynamicDirection

	canOutbound := schema.FromType == currentType
	canInbound := schema.ToType == currentType

	if (direction == DirectionOutbound || direction == DirectionBoth) && canOutbound {
		results = append(results, &RelationQueryInfo{
			Schema:      schema,
			Direction:   DirectionOutbound,
			KeySuffix:   "_outbound",
			WhereField:  fieldSourceID,
			SelectField: fieldTargetID,
			TargetField: fieldTargetID,
			TargetType:  schema.ToType,
		})
	}

	if (direction == DirectionInbound || direction == DirectionBoth) && canInbound {
		results = append(results, &RelationQueryInfo{
			Schema:      schema,
			Direction:   DirectionInbound,
			KeySuffix:   "_inbound",
			WhereField:  fieldTargetID,
			SelectField: fieldSourceID,
			TargetField: fieldSourceID,
			TargetType:  schema.FromType,
		})
	}

	return results
}
