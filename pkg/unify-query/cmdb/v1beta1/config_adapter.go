// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"context"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/relation"
)

// ConfigAdapter 配置适配器
// 将 relation.SchemaProvider 的数据转换为 v1beta1 Config 格式
type ConfigAdapter struct {
	provider relation.SchemaProvider
}

// NewConfigAdapter 创建配置适配器
func NewConfigAdapter(provider relation.SchemaProvider) *ConfigAdapter {
	return &ConfigAdapter{
		provider: provider,
	}
}

// GetConfig 从 SchemaProvider 获取并转换配置
// namespace 为空时获取全局配置（__all__）
func (ca *ConfigAdapter) GetConfig(ctx context.Context, namespace string) (*Config, error) {
	if ca.provider == nil {
		return nil, fmt.Errorf("schema provider is not initialized")
	}
	if namespace == "" {
		namespace = relation.NamespaceAll
	}

	// 获取资源定义
	resourceDefs, err := ca.provider.ListResourceDefinitions(namespace)
	if err != nil {
		return nil, fmt.Errorf("list resource definitions: %w", err)
	}

	// 获取关联定义
	relationDefs, err := ca.provider.ListRelationDefinitions(namespace)
	if err != nil {
		return nil, fmt.Errorf("list relation definitions: %w", err)
	}

	// 转换为 Config
	resources := make([]ResourceConf, 0, len(resourceDefs))
	for _, rd := range resourceDefs {
		resources = append(resources, convertResourceDefinition(rd))
	}

	relations := make([]RelationConf, 0, len(relationDefs))
	for _, rd := range relationDefs {
		relations = append(relations, convertRelationDefinition(rd))
	}

	log.Infof(ctx, "v1beta1 config adapter built: %d resources, %d relations from namespace %s",
		len(resources), len(relations), namespace)

	return &Config{
		Resource: resources,
		Relation: relations,
	}, nil
}

// convertResourceDefinition 转换资源定义
// ResourceDefinition.Fields[].Required=true → ResourceConf.Index
// ResourceDefinition.Fields[].Required=false → ResourceConf.Info
func convertResourceDefinition(rd *relation.ResourceDefinition) ResourceConf {
	var index, info cmdb.Index

	for _, field := range rd.Fields {
		if field.Required {
			index = append(index, field.Name)
		} else {
			info = append(info, field.Name)
		}
	}

	return ResourceConf{
		Name:  cmdb.Resource(rd.Name),
		Index: index,
		Info:  info,
	}
}

// convertRelationDefinition 转换关联定义
// RelationDefinition.FromResource → RelationConf.Resources[0]
// RelationDefinition.ToResource → RelationConf.Resources[1]
func convertRelationDefinition(rd *relation.RelationDefinition) RelationConf {
	return RelationConf{
		Resources: []cmdb.Resource{
			cmdb.Resource(rd.FromResource),
			cmdb.Resource(rd.ToResource),
		},
	}
}
