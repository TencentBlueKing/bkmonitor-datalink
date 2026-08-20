// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import "fmt"

type resourcePathStep struct {
	ResourceType string
	RelationType string
	Category     string
	Direction    string
}

type resourcePath struct {
	Steps []resourcePathStep
}

// PathFinder 路径发现器，用于查找资源之间的关联路径
type PathFinder struct {
	schemaProvider    SchemaProvider
	namespace         string
	allowedCategories []RelationCategory
	dynamicDirection  TraversalDirection
	maxHops           int
}

// PathFinderOption PathFinder 配置选项
type PathFinderOption func(*PathFinder)

// WithSchemaProvider 设置 SchemaProvider
func WithSchemaProvider(provider SchemaProvider) PathFinderOption {
	return func(pf *PathFinder) {
		if provider != nil {
			pf.schemaProvider = provider
		}
	}
}

// WithNamespace sets the ResourceDefinition / RelationDefinition namespace for schema lookup.
func WithNamespace(namespace string) PathFinderOption {
	return func(pf *PathFinder) {
		pf.namespace = namespace
	}
}

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
// 如果不提供 schemaProvider,将使用默认的 StaticSchemaProvider
func NewPathFinder(opts ...PathFinderOption) *PathFinder {
	pf := &PathFinder{
		schemaProvider:    GetSchemaProvider(),
		namespace:         "",
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
func (pf *PathFinder) FindAllPaths(source, target ResourceType, pathResource []ResourceType) ([]resourcePath, error) {
	if source == target && len(pathResource) == 0 {
		// 显式 source==target 优先解释为“查真实自关联边”；
		// 只有 schema 中完全没有自关联时，才回退成单节点信息展示路径。
		paths := pf.findSelfRelationPaths(source)
		if len(paths) > 0 {
			return paths, nil
		}
		return []resourcePath{{Steps: []resourcePathStep{{ResourceType: string(source)}}}}, nil
	}

	pathConstraint, directOnly := normalizePathResource(source, target, pathResource)
	var results []resourcePath
	visited := make(map[ResourceType]bool)
	currentPath := []resourcePathStep{{ResourceType: string(source)}}
	visited[source] = true

	pf.dfs(source, target, pathConstraint, visited, currentPath, &results)
	if directOnly {
		results = filterDirectPaths(results)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("empty paths with %s => %s through %v", source, target, pathResource)
	}

	return results, nil
}

func (pf *PathFinder) findSelfRelationPaths(resourceType ResourceType) []resourcePath {
	var results []resourcePath
	for _, rel := range pf.getRelationsForType(resourceType) {
		if rel.TargetType != resourceType {
			continue
		}
		results = append(results, resourcePath{Steps: []resourcePathStep{
			{ResourceType: string(resourceType)},
			{
				ResourceType: string(resourceType),
				RelationType: string(rel.Schema.RelationType),
				Category:     string(rel.Schema.Category),
				Direction:    string(rel.Direction),
			},
		}})
	}
	return results
}

func normalizePathResource(source, target ResourceType, pathResource []ResourceType) ([]ResourceType, bool) {
	if len(pathResource) == 0 {
		return nil, false
	}

	pathConstraint := make([]ResourceType, 0, len(pathResource))
	hasDirectOnlyConstraint := false
	hasFullEndpointConstraint := len(pathResource) >= 2 && pathResource[0] == source && pathResource[len(pathResource)-1] == target
	for _, resourceType := range pathResource {
		if resourceType == "" {
			// 旧 VM 客户端用空字符串表达“只允许 source->target 直连”。
			// 它不是资源类型，后续连续片段匹配必须忽略这个哨兵。
			hasDirectOnlyConstraint = true
			continue
		}
		if resourceType == source || resourceType == target {
			// 调用方有时会把完整资源路径原样传回来，例如 [source, ..., target]。
			// 路径搜索本身已经固定两端点，这里只保留中间资源约束，避免把端点重复参与连续片段判断。
			continue
		}
		pathConstraint = append(pathConstraint, resourceType)
	}

	directOnly := len(pathConstraint) == 0 && (hasDirectOnlyConstraint || hasFullEndpointConstraint)
	return pathConstraint, directOnly
}

func filterDirectPaths(paths []resourcePath) []resourcePath {
	result := make([]resourcePath, 0, len(paths))
	for _, path := range paths {
		if len(path.Steps) == 2 {
			result = append(result, path)
		}
	}
	return result
}

// dfs 深度优先搜索所有路径
func (pf *PathFinder) dfs(
	current, target ResourceType,
	pathResource []ResourceType,
	visited map[ResourceType]bool,
	currentPath []resourcePathStep,
	results *[]resourcePath,
) {
	if len(currentPath) > pf.maxHops+1 {
		return
	}

	if current == target && len(currentPath) > 1 {
		if pf.satisfiesPathConstraint(currentPath, pathResource) {
			pathCopy := make([]resourcePathStep, len(currentPath))
			copy(pathCopy, currentPath)
			*results = append(*results, resourcePath{Steps: pathCopy})
		}
		return
	}

	relations := pf.getRelationsForType(current)

	for _, rel := range relations {
		nextType := rel.TargetType
		wasVisited := visited[nextType]
		// nextType == target 时允许“访问已访问过的终点”来保留
		// source -> ... -> target 这种显式闭环；递归开头会在命中 target 后直接 return，
		// 因此不会继续从 target 扩散成无限环路。
		if wasVisited && nextType != target {
			continue
		}

		if !wasVisited {
			visited[nextType] = true
		}
		nextStep := resourcePathStep{
			ResourceType: string(nextType),
			RelationType: string(rel.Schema.RelationType),
			Category:     string(rel.Schema.Category),
			Direction:    string(rel.Direction),
		}
		currentPath = append(currentPath, nextStep)

		pf.dfs(nextType, target, pathResource, visited, currentPath, results)

		currentPath = currentPath[:len(currentPath)-1]
		if !wasVisited {
			visited[nextType] = false
		}
	}
}

// satisfiesPathConstraint 检查路径是否满足 pathResource 约束
func (pf *PathFinder) satisfiesPathConstraint(path []resourcePathStep, pathResource []ResourceType) bool {
	if len(pathResource) == 0 {
		return true
	}

	pathTypes := make([]ResourceType, 0, len(path))
	for _, step := range path {
		pathTypes = append(pathTypes, ResourceType(step.ResourceType))
	}

	return containsContiguousResourcePath(pathTypes, pathResource)
}

func containsContiguousResourcePath(pathTypes []ResourceType, pathResource []ResourceType) bool {
	constraint := make([]ResourceType, 0, len(pathResource))
	for _, required := range pathResource {
		// 空 path_resource 在入口层表示“只走直连”，不是一个真实资源类型；
		// 连续性判断只看规范化后的资源约束。
		if required != "" {
			constraint = append(constraint, required)
		}
	}
	if len(constraint) == 0 {
		return true
	}
	if len(constraint) > len(pathTypes) {
		return false
	}
	for start := 0; start <= len(pathTypes)-len(constraint); start++ {
		matched := true
		for offset, required := range constraint {
			// 旧 VM 的 path_resource 是连续片段约束，不能在两个指定资源之间跳过额外资源。
			if pathTypes[start+offset] != required {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

// getRelationsForType 获取指定资源类型的所有可用关系
func (pf *PathFinder) getRelationsForType(resourceType ResourceType) []*RelationQueryInfo {
	var results []*RelationQueryInfo

	// 从 SchemaProvider 获取所有关联 Schema
	schemas := pf.schemaProvider.ListRelationSchemas(pf.namespace)

	for i := range schemas {
		schema := &schemas[i]

		if !pf.isRelationCategoryAllowed(schema.Category) {
			continue
		}

		if schema.FromType != resourceType && schema.ToType != resourceType {
			continue
		}

		if schema.Category == RelationCategoryStatic {
			results = append(results, pf.buildStaticRelationInfos(schema, resourceType)...)
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

// buildStaticRelationInfos 构建静态关系查询信息
func (pf *PathFinder) buildStaticRelationInfos(schema *RelationSchema, currentType ResourceType) []*RelationQueryInfo {
	info := &RelationQueryInfo{
		Schema:    schema,
		KeySuffix: "",
	}

	if schema.FromType == currentType {
		info.Direction = DirectionOutbound
		info.WhereField = fieldIn
		info.SelectField = fieldOut
		info.TargetField = fieldOut
		info.TargetType = schema.ToType
		if schema.ToType == currentType && !schema.IsDirectional {
			// 非定向自关联同一张关系表既可从 in->out 走，也可从 out->in 走。
			// 这里拆成两个带不同 key 后缀的 transition，避免 SQL 结果 map 中同名字段互相覆盖。
			reverseInfo := &RelationQueryInfo{
				Schema:      schema,
				Direction:   DirectionInbound,
				KeySuffix:   "_inbound",
				WhereField:  fieldOut,
				SelectField: fieldIn,
				TargetField: fieldIn,
				TargetType:  schema.FromType,
			}
			info.KeySuffix = "_outbound"
			return []*RelationQueryInfo{info, reverseInfo}
		}
	} else {
		if schema.IsDirectional {
			return nil
		}
		info.Direction = DirectionInbound
		info.WhereField = fieldOut
		info.SelectField = fieldIn
		info.TargetField = fieldIn
		info.TargetType = schema.FromType
	}

	return []*RelationQueryInfo{info}
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
			WhereField:  fieldIn,
			SelectField: fieldOut,
			TargetField: fieldOut,
			TargetType:  schema.ToType,
		})
	}

	if (direction == DirectionInbound || direction == DirectionBoth) && canInbound {
		results = append(results, &RelationQueryInfo{
			Schema:      schema,
			Direction:   DirectionInbound,
			KeySuffix:   "_inbound",
			WhereField:  fieldOut,
			SelectField: fieldIn,
			TargetField: fieldIn,
			TargetType:  schema.FromType,
		})
	}

	return results
}
