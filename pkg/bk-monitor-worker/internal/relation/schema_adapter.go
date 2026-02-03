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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
)

// SchemaProviderAdapter 适配器，将 service.SchemaProvider 适配到 relation 包使用的接口
type SchemaProviderAdapter struct {
	provider service.SchemaProvider
}

// NewSchemaProviderAdapter 创建适配器
func NewSchemaProviderAdapter(provider service.SchemaProvider) *SchemaProviderAdapter {
	return &SchemaProviderAdapter{
		provider: provider,
	}
}

// GetResourceDefinition 获取资源定义
func (a *SchemaProviderAdapter) GetResourceDefinition(namespace, resourceType string) (ResourceDefinition, error) {
	def, err := a.provider.GetResourceDefinition(namespace, resourceType)
	if err != nil {
		return nil, err
	}
	return &resourceDefinitionAdapter{def: def}, nil
}

// GetRelationDefinition 获取关系定义
func (a *SchemaProviderAdapter) GetRelationDefinition(namespace, fromResource, toResource string) (RelationDefinition, error) {
	def, err := a.provider.GetRelationDefinition(namespace, fromResource, toResource)
	if err != nil {
		return nil, err
	}
	return &relationDefinitionAdapter{def: def, provider: a.provider}, nil
}

// resourceDefinitionAdapter ResourceDefinition 的适配器
type resourceDefinitionAdapter struct {
	def *service.ResourceDefinition
}

func (r *resourceDefinitionAdapter) GetPrimaryKeys() []string {
	return r.def.GetPrimaryKeys()
}

// relationDefinitionAdapter RelationDefinition 的适配器
type relationDefinitionAdapter struct {
	def      *service.RelationDefinition
	provider service.SchemaProvider
}

func (r *relationDefinitionAdapter) GetRelationName() string {
	return r.def.GetRelationName()
}

func (r *relationDefinitionAdapter) GetRequiredFields(fromResourceDef, toResourceDef ResourceDefinition) []string {
	// 转换回 service 包的类型
	var fromDef, toDef *service.ResourceDefinition

	if fromAdapter, ok := fromResourceDef.(*resourceDefinitionAdapter); ok {
		fromDef = fromAdapter.def
	}
	if toAdapter, ok := toResourceDef.(*resourceDefinitionAdapter); ok {
		toDef = toAdapter.def
	}

	return r.def.GetRequiredFields(fromDef, toDef)
}
