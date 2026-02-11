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
	"errors"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// CompositeSchemaProvider 组合 Schema 提供器
// 支持多个提供器的级联查找，按优先级顺序查询
// 典型用法：RedisSchemaProvider(高优先级) -> StaticSchemaProvider(兜底)
type CompositeSchemaProvider struct {
	providers []SchemaProvider // 按优先级排序，索引越小优先级越高
	mu        sync.RWMutex
	ctx       context.Context
}

// NewCompositeSchemaProvider 创建组合 Schema 提供器
// providers 参数按优先级顺序传入，索引越小优先级越高
// 例如: NewCompositeSchemaProvider(redisProvider, staticProvider)
// 会先查询 redisProvider，如果找不到再查询 staticProvider
func NewCompositeSchemaProvider(providers ...SchemaProvider) *CompositeSchemaProvider {
	return &CompositeSchemaProvider{
		providers: providers,
		ctx:       context.Background(),
	}
}

// AddProvider 添加提供器（追加到最低优先级）
func (csp *CompositeSchemaProvider) AddProvider(provider SchemaProvider) {
	csp.mu.Lock()
	defer csp.mu.Unlock()
	csp.providers = append(csp.providers, provider)
}

// GetResourceDefinition 获取资源定义
// 按优先级顺序查询各个提供器，返回第一个找到的结果
func (csp *CompositeSchemaProvider) GetResourceDefinition(namespace, name string) (*ResourceDefinition, error) {
	_, span := trace.NewSpan(csp.ctx, "composite_provider.get_resource_definition")
	var err error
	defer span.End(&err)

	span.Set("resource.namespace", namespace)
	span.Set("resource.name", name)
	span.Set("providers.count", len(csp.providers))

	csp.mu.RLock()
	defer csp.mu.RUnlock()

	var lastErr error
	for i, provider := range csp.providers {
		rd, providerErr := provider.GetResourceDefinition(namespace, name)
		if providerErr == nil {
			span.Set("provider.index", i)
			return rd, nil
		}
		if !errors.Is(providerErr, ErrResourceDefinitionNotFound) {
			lastErr = providerErr
		}
	}

	if lastErr != nil {
		err = lastErr
		return nil, err
	}
	err = ErrResourceDefinitionNotFound
	return nil, err
}

// ListResourceDefinitions 列出资源定义
// 合并所有提供器的结果，去重（以高优先级为准）
func (csp *CompositeSchemaProvider) ListResourceDefinitions(namespace string) ([]*ResourceDefinition, error) {
	_, span := trace.NewSpan(csp.ctx, "composite_provider.list_resource_definitions")
	var err error
	defer span.End(&err)

	span.Set("resource.namespace", namespace)

	csp.mu.RLock()
	defer csp.mu.RUnlock()

	seen := make(map[string]bool) // key: namespace:name
	result := make([]*ResourceDefinition, 0)

	for _, provider := range csp.providers {
		list, listErr := provider.ListResourceDefinitions(namespace)
		if listErr != nil {
			continue
		}

		for _, rd := range list {
			key := makeResourceCacheKey(rd.Namespace, rd.Name)
			if !seen[key] {
				seen[key] = true
				result = append(result, rd)
			}
		}
	}

	span.Set("result.count", len(result))
	return result, nil
}

// GetRelationDefinition 获取关联定义
// 按优先级顺序查询各个提供器，返回第一个找到的结果
func (csp *CompositeSchemaProvider) GetRelationDefinition(namespace, name string) (*RelationDefinition, error) {
	_, span := trace.NewSpan(csp.ctx, "composite_provider.get_relation_definition")
	var err error
	defer span.End(&err)

	span.Set("relation.namespace", namespace)
	span.Set("relation.name", name)
	span.Set("providers.count", len(csp.providers))

	csp.mu.RLock()
	defer csp.mu.RUnlock()

	var lastErr error
	for i, provider := range csp.providers {
		rd, providerErr := provider.GetRelationDefinition(namespace, name)
		if providerErr == nil {
			span.Set("provider.index", i)
			return rd, nil
		}
		if !errors.Is(providerErr, ErrRelationDefinitionNotFound) {
			lastErr = providerErr
		}
	}

	if lastErr != nil {
		err = lastErr
		return nil, err
	}
	err = ErrRelationDefinitionNotFound
	return nil, err
}

// ListRelationDefinitions 列出关联定义
// 合并所有提供器的结果，去重（以高优先级为准）
func (csp *CompositeSchemaProvider) ListRelationDefinitions(namespace string) ([]*RelationDefinition, error) {
	_, span := trace.NewSpan(csp.ctx, "composite_provider.list_relation_definitions")
	var err error
	defer span.End(&err)

	span.Set("relation.namespace", namespace)

	csp.mu.RLock()
	defer csp.mu.RUnlock()

	seen := make(map[string]bool) // key: namespace:name
	result := make([]*RelationDefinition, 0)

	for _, provider := range csp.providers {
		list, listErr := provider.ListRelationDefinitions(namespace)
		if listErr != nil {
			continue
		}

		for _, rd := range list {
			key := makeRelationCacheKey(rd.Namespace, rd.Name)
			if !seen[key] {
				seen[key] = true
				result = append(result, rd)
			}
		}
	}

	span.Set("result.count", len(result))
	return result, nil
}

// GetResourcePrimaryKeys 获取资源主键字段列表
// 按优先级顺序查询各个提供器，返回第一个非空结果
func (csp *CompositeSchemaProvider) GetResourcePrimaryKeys(resourceType ResourceType) []string {
	csp.mu.RLock()
	defer csp.mu.RUnlock()

	for _, provider := range csp.providers {
		keys := provider.GetResourcePrimaryKeys(resourceType)
		if len(keys) > 0 {
			return keys
		}
	}

	return []string{}
}

// GetRelationSchema 获取关联 Schema
// 按优先级顺序查询各个提供器，返回第一个找到的结果
func (csp *CompositeSchemaProvider) GetRelationSchema(relationType RelationType) (*RelationSchema, error) {
	csp.mu.RLock()
	defer csp.mu.RUnlock()

	var lastErr error
	for _, provider := range csp.providers {
		schema, err := provider.GetRelationSchema(relationType)
		if err == nil {
			return schema, nil
		}
		if !errors.Is(err, ErrRelationDefinitionNotFound) {
			lastErr = err
		}
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrRelationDefinitionNotFound
}

// ListRelationSchemas 列出所有关联 Schema
// 合并所有提供器的结果，去重（以高优先级为准）
func (csp *CompositeSchemaProvider) ListRelationSchemas() []RelationSchema {
	csp.mu.RLock()
	defer csp.mu.RUnlock()

	seen := make(map[RelationType]bool)
	result := make([]RelationSchema, 0)

	for _, provider := range csp.providers {
		schemas := provider.ListRelationSchemas()
		for _, schema := range schemas {
			if !seen[schema.RelationType] {
				seen[schema.RelationType] = true
				result = append(result, schema)
			}
		}
	}

	return result
}

// Ensure CompositeSchemaProvider implements SchemaProvider
var _ SchemaProvider = (*CompositeSchemaProvider)(nil)
