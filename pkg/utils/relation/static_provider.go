// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"sync"
)

// StaticSchemaProvider 静态 Schema 提供器
// 从提供的静态数据初始化，提供向后兼容性
type StaticSchemaProvider struct {
	resourceDefinitions map[string]*ResourceDefinition // key: name
	relationDefinitions map[string]*RelationDefinition // key: name
	mu                  sync.RWMutex
}

// StaticProviderConfig 静态提供器配置
type StaticProviderConfig struct {
	// ResourcePrimaryKeys 资源类型的主键配置
	// map[资源类型] -> []主键字段名
	ResourcePrimaryKeys map[string][]string
	// RelationSchemas 关联关系的 Schema 配置
	RelationSchemas []RelationSchema
}

// NewStaticSchemaProvider 创建静态 Schema 提供器
func NewStaticSchemaProvider(config StaticProviderConfig) *StaticSchemaProvider {
	provider := &StaticSchemaProvider{
		resourceDefinitions: make(map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]*RelationDefinition),
	}

	// 从 ResourcePrimaryKeys 初始化资源定义
	for resourceType, keys := range config.ResourcePrimaryKeys {
		fields := make([]FieldDefinition, len(keys))
		for i, key := range keys {
			fields[i] = FieldDefinition{
				Name:     key,
				Required: true,
			}
		}

		rd := &ResourceDefinition{
			Namespace: NamespaceAll,
			Name:      resourceType,
			Fields:    fields,
			Labels:    make(map[string]string),
			Spec:      make(map[string]interface{}),
		}
		provider.resourceDefinitions[rd.Name] = rd
	}

	// 从 RelationSchemas 初始化关联定义
	for _, schema := range config.RelationSchemas {
		category := "static"
		if schema.Category == RelationCategoryDynamic {
			category = "dynamic"
		}

		rd := &RelationDefinition{
			Namespace:    NamespaceAll,
			Name:         string(schema.RelationName),
			FromResource: string(schema.FromType),
			ToResource:   string(schema.ToType),
			Category:     category,
			IsBelongsTo:  schema.IsBelongsTo,
			Labels:       make(map[string]string),
			Spec:         make(map[string]interface{}),
		}
		provider.relationDefinitions[rd.Name] = rd
	}

	return provider
}

// normalizeNamespace 将 "" 规范化为 NamespaceAll，统一全局 namespace 表示
func normalizeNamespace(namespace string) string {
	if namespace == "" {
		return NamespaceAll
	}
	return namespace
}

// GetResourceDefinition 获取资源定义
func (sp *StaticSchemaProvider) GetResourceDefinition(namespace, name string) (*ResourceDefinition, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	// 静态提供器只支持全局资源(namespace 统一为 NamespaceAll)
	if normalizeNamespace(namespace) != NamespaceAll {
		return nil, ErrResourceDefinitionNotFound
	}

	rd, ok := sp.resourceDefinitions[name]
	if !ok {
		return nil, ErrResourceDefinitionNotFound
	}

	return rd, nil
}

// ListResourceDefinitions 列出资源定义
func (sp *StaticSchemaProvider) ListResourceDefinitions(namespace string) ([]*ResourceDefinition, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	// 静态提供器只支持全局资源(namespace 统一为 NamespaceAll)
	if normalizeNamespace(namespace) != NamespaceAll {
		return []*ResourceDefinition{}, nil
	}

	result := make([]*ResourceDefinition, 0, len(sp.resourceDefinitions))
	for _, rd := range sp.resourceDefinitions {
		result = append(result, rd)
	}

	return result, nil
}

func (sp *StaticSchemaProvider) ListAllResourceDefinitions() (map[string][]*ResourceDefinition, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	defs := make([]*ResourceDefinition, 0, len(sp.resourceDefinitions))
	for _, rd := range sp.resourceDefinitions {
		defs = append(defs, rd)
	}
	return map[string][]*ResourceDefinition{NamespaceAll: defs}, nil
}

// GetRelationDefinition 获取关联定义
func (sp *StaticSchemaProvider) GetRelationDefinition(namespace, name string) (*RelationDefinition, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	// 静态提供器只支持全局关联(namespace 统一为 NamespaceAll)
	if normalizeNamespace(namespace) != NamespaceAll {
		return nil, ErrRelationDefinitionNotFound
	}

	rd, ok := sp.relationDefinitions[name]
	if !ok {
		return nil, ErrRelationDefinitionNotFound
	}

	return rd, nil
}

// ListRelationDefinitions 列出关联定义
func (sp *StaticSchemaProvider) ListRelationDefinitions(namespace string) ([]*RelationDefinition, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	// 静态提供器只支持全局关联(namespace 统一为 NamespaceAll)
	if normalizeNamespace(namespace) != NamespaceAll {
		return []*RelationDefinition{}, nil
	}

	result := make([]*RelationDefinition, 0, len(sp.relationDefinitions))
	for _, rd := range sp.relationDefinitions {
		result = append(result, rd)
	}

	return result, nil
}

func (sp *StaticSchemaProvider) ListAllRelationDefinitions() (map[string][]*RelationDefinition, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	defs := make([]*RelationDefinition, 0, len(sp.relationDefinitions))
	for _, rd := range sp.relationDefinitions {
		defs = append(defs, rd)
	}
	return map[string][]*RelationDefinition{NamespaceAll: defs}, nil
}

// GetResourcePrimaryKeys 获取资源主键字段列表
func (sp *StaticSchemaProvider) GetResourcePrimaryKeys(resourceType ResourceType) []string {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	rd, ok := sp.resourceDefinitions[string(resourceType)]
	if !ok {
		return []string{}
	}

	return rd.GetPrimaryKeys()
}

// GetRelationSchema 获取关联 Schema
func (sp *StaticSchemaProvider) GetRelationSchema(relationType RelationName) (*RelationSchema, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	rd, ok := sp.relationDefinitions[string(relationType)]
	if !ok {
		return nil, ErrRelationDefinitionNotFound
	}

	schema := ToRelationSchema(rd)
	return &schema, nil
}

// ListRelationSchemas 列出所有关联 Schema
func (sp *StaticSchemaProvider) ListRelationSchemas() []RelationSchema {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	result := make([]RelationSchema, 0, len(sp.relationDefinitions))
	for _, rd := range sp.relationDefinitions {
		result = append(result, ToRelationSchema(rd))
	}
	return result
}

// FindRelationByResourceTypes 根据资源类型和方向类型查找关联定义
func (sp *StaticSchemaProvider) FindRelationByResourceTypes(namespace, fromResource, toResource string, directionType DirectionType) (*RelationDefinition, bool) {
	defs, err := sp.ListRelationDefinitions(namespace)
	if err != nil {
		return nil, false
	}

	for _, def := range defs {
		switch directionType {
		case DirectionTypeDirectional:
			// 单向关联：必须严格匹配 from -> to
			if def.IsDirectional && def.FromResource == fromResource && def.ToResource == toResource {
				return def, true
			}
		case DirectionTypeBidirectional:
			// 双向关联：匹配任意方向
			if !def.IsDirectional &&
				((def.FromResource == fromResource && def.ToResource == toResource) ||
					(def.FromResource == toResource && def.ToResource == fromResource)) {
				return def, true
			}
		}
	}

	return nil, false
}

// Subscribe registers a callback for schema changes
// StaticSchemaProvider never changes, so callbacks are never invoked
func (sp *StaticSchemaProvider) Name() string {
	return "static"
}

func (sp *StaticSchemaProvider) ListNamespaces() ([]string, error) {
	return []string{NamespaceAll}, nil
}

func (sp *StaticSchemaProvider) Subscribe(callback SchemaChangeCallback) error {
	if callback == nil {
		return nil // StaticProvider never calls callbacks anyway
	}
	// StaticProvider doesn't support subscriptions since data never changes
	return nil
}

// Ensure StaticSchemaProvider implements SchemaProvider
var _ SchemaProvider = (*StaticSchemaProvider)(nil)
