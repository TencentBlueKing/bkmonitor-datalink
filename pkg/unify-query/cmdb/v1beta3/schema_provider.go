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
	"sort"
	"strings"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// SchemaProvider v1beta3 的 Schema 提供器接口
// 注意：此接口使用 v1beta3 的类型系统，与 relation.SchemaProvider 不同
type SchemaProvider interface {
	// GetResourcePrimaryKeys 获取资源类型的主键字段列表
	GetResourcePrimaryKeys(namespace string, resourceType ResourceType) []string
	// ListRelationSchemas 列出所有关联 Schema
	ListRelationSchemas(namespace string) []RelationSchema
}

// v1beta3SchemaProviderAdapter v1beta3 SchemaProvider 适配器
// 将 relation.SchemaProvider 的方法适配到 v1beta3 的类型系统
type v1beta3SchemaProviderAdapter struct {
	provider relation.SchemaProvider
}

var (
	defaultSchemaProviderMu sync.RWMutex
	defaultSchemaProvider   SchemaProvider = NewStaticSchemaProvider()
)

// InitSchemaProvider injects the relation schema provider initialized by the
// service layer. Passing nil falls back to the static default schema.
func InitSchemaProvider(provider relation.SchemaProvider) {
	nextProvider := SchemaProvider(NewStaticSchemaProvider())
	if provider != nil {
		nextProvider = NewSchemaProviderFromRelation(provider)
	}

	defaultSchemaProviderMu.Lock()
	defaultSchemaProvider = nextProvider
	defaultSchemaProviderMu.Unlock()

	modelMutex.Lock()
	model := defaultModel
	modelMutex.Unlock()
	if model != nil {
		model.SetSchemaProvider(nextProvider)
	}
}

func GetSchemaProvider() SchemaProvider {
	defaultSchemaProviderMu.RLock()
	defer defaultSchemaProviderMu.RUnlock()
	return defaultSchemaProvider
}

var relationSchemaOrder = map[RelationType]int{
	RelationNodeWithSystem:                     0,
	RelationNodeWithPod:                        1,
	RelationJobWithPod:                         2,
	RelationPodWithReplicaSet:                  3,
	RelationPodWithStatefulSet:                 4,
	RelationDaemonSetWithPod:                   5,
	RelationDeploymentWithReplicaSet:           6,
	RelationPodWithService:                     7,
	RelationIngressWithService:                 8,
	RelationK8sAddressWithService:              9,
	RelationDomainWithService:                  10,
	RelationAPMServiceInstanceWithPod:          11,
	RelationAPMServiceInstanceWithSystem:       12,
	RelationAPMServiceWithAPMServiceInstance:   13,
	RelationContainerWithPod:                   14,
	RelationDataSourceWithPod:                  15,
	RelationDataSourceWithNode:                 16,
	RelationBKLogConfigWithDataSource:          17,
	RelationBizWithSet:                         18,
	RelationModuleWithSet:                      19,
	RelationHostWithModule:                     20,
	RelationHostWithSystem:                     21,
	RelationAppVersionWithContainer:            22,
	RelationAppVersionWithSystem:               23,
	RelationContainerWithEnvironment:           24,
	RelationEnvironmentWithSystem:              25,
	RelationAppVersionWithGitCommit:            26,
	RelationPodToPod:                           27,
	RelationPodToSystem:                        28,
	RelationSystemToPod:                        29,
	RelationSystemToSystem:                     30,
	RelationServiceToService:                   31,
	RelationType("apm_service_to_apm_service"): 32,
}

func normalizeSchemaNamespace(namespace string) string {
	if namespace == "" {
		return relation.NamespaceAll
	}
	return namespace
}

func (a *v1beta3SchemaProviderAdapter) GetResourcePrimaryKeys(namespace string, resourceType ResourceType) []string {
	ns := normalizeSchemaNamespace(namespace)
	resourceDef, err := a.provider.GetResourceDefinition(ns, string(resourceType))
	if err == nil {
		return resourceDef.GetPrimaryKeys()
	}
	if ns != relation.NamespaceAll {
		resourceDef, err = a.provider.GetResourceDefinition(relation.NamespaceAll, string(resourceType))
		if err == nil {
			return resourceDef.GetPrimaryKeys()
		}
	}
	return []string{}
}

func (a *v1beta3SchemaProviderAdapter) ListRelationSchemas(namespace string) []RelationSchema {
	ns := normalizeSchemaNamespace(namespace)
	definitions, err := a.provider.ListRelationDefinitions(ns)
	if err != nil {
		definitions = nil
	}
	if len(definitions) == 0 && ns != relation.NamespaceAll {
		definitions, err = a.provider.ListRelationDefinitions(relation.NamespaceAll)
		if err != nil {
			definitions = nil
		}
	}
	schemas := make([]relation.RelationSchema, 0, len(definitions))
	for _, definition := range definitions {
		schema := relation.ToRelationSchema(definition)
		schemas = append(schemas, schema)
	}
	result := make([]RelationSchema, len(schemas))
	for i, schema := range schemas {
		result[i] = RelationSchema{
			RelationType: normalizeRelationName(schema.RelationName),
			Category:     RelationCategory(schema.Category),
			FromType:     ResourceType(schema.FromType),
			ToType:       ResourceType(schema.ToType),
			IsBelongsTo:  schema.IsBelongsTo,
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		leftOrder, leftOK := relationSchemaOrder[result[i].RelationType]
		rightOrder, rightOK := relationSchemaOrder[result[j].RelationType]
		if leftOK && rightOK {
			return leftOrder < rightOrder
		}
		if leftOK != rightOK {
			return leftOK
		}
		return result[i].RelationType < result[j].RelationType
	})
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
		RelationType: normalizeRelationName(schema.RelationName),
		Category:     RelationCategory(schema.Category),
		FromType:     ResourceType(schema.FromType),
		ToType:       ResourceType(schema.ToType),
		IsBelongsTo:  schema.IsBelongsTo,
	}, nil
}

func normalizeRelationName(name relation.RelationName) RelationType {
	relationName := string(name)
	if _, bareName, ok := strings.Cut(relationName, ":"); ok {
		relationName = bareName
	}
	return RelationType(relationName)
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
