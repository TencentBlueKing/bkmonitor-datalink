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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// SchemaProvider v1beta3 的 Schema 提供器接口
// 注意：此接口使用 v1beta3 的类型系统，与 relation.SchemaProvider 不同
type SchemaProvider interface {
	// GetResourcePrimaryKeys 获取资源类型的主键字段列表
	GetResourcePrimaryKeys(resourceType ResourceType) []string
	// ListRelationSchemas 列出所有关联 Schema
	ListRelationSchemas() []RelationSchema
}

// v1beta3SchemaProviderAdapter v1beta3 SchemaProvider 适配器
// 将 relation.SchemaProvider 的方法适配到 v1beta3 的类型系统
type v1beta3SchemaProviderAdapter struct {
	provider relation.SchemaProvider
}

func (a *v1beta3SchemaProviderAdapter) GetResourcePrimaryKeys(resourceType ResourceType) []string {
	return a.provider.GetResourcePrimaryKeys(relation.ResourceType(resourceType))
}

func (a *v1beta3SchemaProviderAdapter) ListRelationSchemas() []RelationSchema {
	schemas := a.provider.ListRelationSchemas()
	result := make([]RelationSchema, len(schemas))
	for i, schema := range schemas {
		result[i] = RelationSchema{
			RelationType: RelationType(schema.RelationName),
			Category:     RelationCategory(schema.Category),
			FromType:     ResourceType(schema.FromType),
			ToType:       ResourceType(schema.ToType),
			IsBelongsTo:  schema.IsBelongsTo,
		}
	}
	return result
}

// GetRelationSchema 获取 v1beta3 格式的关联 Schema
func (a *v1beta3SchemaProviderAdapter) GetRelationSchema(relationType RelationType) (*RelationSchema, error) {
	relName := relation.RelationName(relationType)
	schema, err := a.provider.GetRelationSchema(relName)
	if err != nil {
		return nil, err
	}
	return &RelationSchema{
		RelationType: RelationType(schema.RelationName),
		Category:     RelationCategory(schema.Category),
		FromType:     ResourceType(schema.FromType),
		ToType:       ResourceType(schema.ToType),
		IsBelongsTo:  schema.IsBelongsTo,
	}, nil
}

// NewSchemaProviderFromRelation 创建 v1beta3 SchemaProvider from relation.SchemaProvider
func NewSchemaProviderFromRelation(provider relation.SchemaProvider) SchemaProvider {
	return &v1beta3SchemaProviderAdapter{provider: provider}
}

// GetUnderlyingProvider 获取底层的 relation.SchemaProvider
func GetUnderlyingProvider(sp SchemaProvider) relation.SchemaProvider {
	if adapter, ok := sp.(*v1beta3SchemaProviderAdapter); ok {
		return adapter.provider
	}
	return nil
}
