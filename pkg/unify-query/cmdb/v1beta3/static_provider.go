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
	"sync"
)

// StaticSchemaProvider 静态 Schema 提供器
// 从内置的硬编码数据(resourcePrimaryKeys 和 schemaRegistry)初始化
// 提供向后兼容性,支持已有的 K8s 资源类型和关联关系
type StaticSchemaProvider struct {
	resourceDefinitions map[string]*ResourceDefinition // key: name
	relationDefinitions map[string]*RelationDefinition // key: name
	relationSchemas     []RelationSchema
	mu                  sync.RWMutex
}

// NewStaticSchemaProvider 创建静态 Schema 提供器
// 从现有的 resourcePrimaryKeys 和 schemaRegistry 初始化
func NewStaticSchemaProvider() *StaticSchemaProvider {
	provider := &StaticSchemaProvider{
		resourceDefinitions: make(map[string]*ResourceDefinition),
		relationDefinitions: make(map[string]*RelationDefinition),
		relationSchemas:     make([]RelationSchema, 0, len(schemaRegistry)),
	}

	// 从 resourcePrimaryKeys 初始化资源定义
	for resourceType, keys := range resourcePrimaryKeys {
		fields := make([]FieldDefinition, len(keys))
		for i, key := range keys {
			fields[i] = FieldDefinition{
				Name:     key,
				Required: true,
			}
		}

		rd := &ResourceDefinition{
			Namespace: "", // 全局资源
			Name:      string(resourceType),
			Fields:    fields,
			Labels:    make(map[string]string),
			Spec:      make(map[string]interface{}),
		}
		provider.resourceDefinitions[rd.Name] = rd
	}

	// 从 schemaRegistry 初始化关联定义
	for _, schema := range schemaRegistry {
		category := "static"
		if schema.Category == RelationCategoryDynamic {
			category = "dynamic"
		}

		rd := &RelationDefinition{
			Namespace:    "", // 全局关联
			Name:         string(schema.RelationType),
			FromResource: string(schema.FromType),
			ToResource:   string(schema.ToType),
			Category:     category,
			IsBelongsTo:  schema.IsBelongsTo,
			Labels:       make(map[string]string),
			Spec:         make(map[string]interface{}),
		}
		provider.relationDefinitions[rd.Name] = rd
		provider.relationSchemas = append(provider.relationSchemas, schema)
	}

	return provider
}

// GetResourceDefinition 获取资源定义
func (sp *StaticSchemaProvider) GetResourceDefinition(namespace, name string) (*ResourceDefinition, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	// 静态提供器只支持全局资源(namespace = "")
	if namespace != "" {
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

	// 静态提供器只支持全局资源(namespace = "")
	if namespace != "" {
		return []*ResourceDefinition{}, nil
	}

	result := make([]*ResourceDefinition, 0, len(sp.resourceDefinitions))
	for _, rd := range sp.resourceDefinitions {
		result = append(result, rd)
	}

	return result, nil
}

// GetRelationDefinition 获取关联定义
func (sp *StaticSchemaProvider) GetRelationDefinition(namespace, name string) (*RelationDefinition, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	// 静态提供器只支持全局关联(namespace = "")
	if namespace != "" {
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

	// 静态提供器只支持全局关联(namespace = "")
	if namespace != "" {
		return []*RelationDefinition{}, nil
	}

	result := make([]*RelationDefinition, 0, len(sp.relationDefinitions))
	for _, rd := range sp.relationDefinitions {
		result = append(result, rd)
	}

	return result, nil
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
func (sp *StaticSchemaProvider) GetRelationSchema(relationType RelationType) (*RelationSchema, error) {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	for i := range sp.relationSchemas {
		if sp.relationSchemas[i].RelationType == relationType {
			return &sp.relationSchemas[i], nil
		}
	}

	return nil, ErrRelationDefinitionNotFound
}

// ListRelationSchemas 列出所有关联 Schema
func (sp *StaticSchemaProvider) ListRelationSchemas() []RelationSchema {
	sp.mu.RLock()
	defer sp.mu.RUnlock()

	// 返回副本以避免外部修改
	result := make([]RelationSchema, len(sp.relationSchemas))
	copy(result, sp.relationSchemas)
	return result
}

// Ensure StaticSchemaProvider implements SchemaProvider
var _ SchemaProvider = (*StaticSchemaProvider)(nil)
