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

func (ca *ConfigAdapter) GetConfigs(ctx context.Context) (map[string]*Config, error) {
	if ca.provider == nil {
		return nil, fmt.Errorf("schema provider is not initialized")
	}

	resourcesByNs, err := ca.provider.ListAllResourceDefinitions()
	if err != nil {
		return nil, fmt.Errorf("list all resource definitions: %w", err)
	}
	if len(resourcesByNs) == 0 {
		return nil, fmt.Errorf("no namespaces found")
	}

	relationsByNs, err := ca.provider.ListAllRelationDefinitions()
	if err != nil {
		return nil, fmt.Errorf("list all relation definitions: %w", err)
	}

	configs := make(map[string]*Config, len(resourcesByNs))
	for ns, resourceDefs := range resourcesByNs {
		configs[ns] = buildConfig(ctx, ns, resourceDefs, relationsByNs[ns])
	}

	log.Infof(ctx, "v1beta1 config adapter built %d namespaces", len(configs))
	return configs, nil
}

func (ca *ConfigAdapter) GetConfigForNamespace(ctx context.Context, namespace string) (*Config, error) {
	if ca.provider == nil {
		return nil, fmt.Errorf("schema provider is not initialized")
	}

	resourceDefs, err := ca.provider.ListResourceDefinitions(namespace)
	if err != nil {
		return nil, fmt.Errorf("list resource definitions for namespace %q: %w", namespace, err)
	}

	relationDefs, err := ca.provider.ListRelationDefinitions(namespace)
	if err != nil {
		return nil, fmt.Errorf("list relation definitions for namespace %q: %w", namespace, err)
	}

	return buildConfig(ctx, namespace, resourceDefs, relationDefs), nil
}

func buildConfig(ctx context.Context, ns string, resourceDefs []*relation.ResourceDefinition, relationDefs []*relation.RelationDefinition) *Config {
	resources := make([]ResourceConf, 0, len(resourceDefs))
	for _, rd := range resourceDefs {
		resources = append(resources, convertResourceDefinition(rd))
	}

	relations := make([]RelationConf, 0, len(relationDefs))
	for _, rd := range relationDefs {
		if r, ok := convertRelationDefinition(rd); ok {
			relations = append(relations, r)
		} else {
			log.Warnf(ctx, "skipping invalid relation %q in namespace %q: from=%q to=%q",
				rd.Name, ns, rd.FromResource, rd.ToResource)
		}
	}

	return &Config{
		Resource: resources,
		Relation: relations,
	}
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

func convertRelationDefinition(rd *relation.RelationDefinition) (RelationConf, bool) {
	if rd.FromResource == "" || rd.ToResource == "" {
		return RelationConf{}, false
	}
	return RelationConf{
		Resources: []cmdb.Resource{
			cmdb.Resource(rd.FromResource),
			cmdb.Resource(rd.ToResource),
		},
	}, true
}
