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
)

// ConfigAdapter 将 SchemaProvider 的数据转换为 v1beta1 Config 格式
type ConfigAdapter struct {
	provider cmdb.SchemaProvider
}

// NewConfigAdapter 创建 ConfigAdapter
func NewConfigAdapter(provider cmdb.SchemaProvider) *ConfigAdapter {
	return &ConfigAdapter{
		provider: provider,
	}
}

// GetConfig 从 SchemaProvider 获取数据并转换为 v1beta1 Config
// namespace 为空字符串时获取全局配置
//
// 注意：v1beta1 没有 namespace 概念，因此会按资源/关联 Name 去重，
// 跨 namespace 出现相同 Name 时只保留第一个。
func (ca *ConfigAdapter) GetConfig(ctx context.Context, namespace string) (*Config, error) {
	resources, err := ca.provider.ListResourceDefinitions(namespace)
	if err != nil {
		return nil, fmt.Errorf("list resource definitions error: %w", err)
	}

	relations, err := ca.provider.ListRelationDefinitions(namespace)
	if err != nil {
		return nil, fmt.Errorf("list relation definitions error: %w", err)
	}

	if len(resources) == 0 && len(relations) == 0 {
		return nil, fmt.Errorf("no resource or relation definitions found for namespace %q", namespace)
	}

	cfg := &Config{
		Resource: make([]ResourceConf, 0, len(resources)),
		Relation: make([]RelationConf, 0, len(relations)),
	}

	// v1beta1 没有 namespace 概念，按 Name 去重（跨 namespace 相同资源只保留第一个）
	seenResource := make(map[string]bool, len(resources))
	for _, rd := range resources {
		if seenResource[rd.Name] {
			log.Debugf(ctx, "skip duplicate resource %q (namespace=%s), already seen", rd.Name, rd.Namespace)
			continue
		}
		seenResource[rd.Name] = true
		cfg.Resource = append(cfg.Resource, convertResourceDefinition(rd))
	}

	// 关联也按边去重，避免跨 namespace 重复的边
	// v1beta1 使用无向图，所以 "pod->node" 和 "node->pod" 是同一条边
	seenRelation := make(map[string]bool, len(relations))
	for _, rd := range relations {
		relConf, convertErr := convertRelationDefinition(rd)
		if convertErr != nil {
			log.Warnf(ctx, "skip relation %s: %v", rd.Name, convertErr)
			continue
		}
		edgeKey := undirectedEdgeKey(rd.FromResource, rd.ToResource)
		if seenRelation[edgeKey] {
			log.Debugf(ctx, "skip duplicate relation %q (namespace=%s), edge %s already seen", rd.Name, rd.Namespace, edgeKey)
			continue
		}
		seenRelation[edgeKey] = true
		cfg.Relation = append(cfg.Relation, relConf)
	}

	return cfg, nil
}

// convertResourceDefinition 将 cmdb.ResourceDefinition 转换为 v1beta1 ResourceConf
// 映射规则:
//   - FieldDefinition.Required=true  → Index（主键/索引字段）
//   - FieldDefinition.Required=false → Info（信息字段）
func convertResourceDefinition(rd *cmdb.ResourceDefinition) ResourceConf {
	var index cmdb.Index
	var info cmdb.Index

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

// convertRelationDefinition 将 cmdb.RelationDefinition 转换为 v1beta1 RelationConf
// 映射规则:
//   - FromResource → Resources[0]
//   - ToResource   → Resources[1]
func convertRelationDefinition(rd *cmdb.RelationDefinition) (RelationConf, error) {
	if rd.FromResource == "" || rd.ToResource == "" {
		return RelationConf{}, fmt.Errorf("from_resource or to_resource is empty")
	}

	return RelationConf{
		Resources: []cmdb.Resource{
			cmdb.Resource(rd.FromResource),
			cmdb.Resource(rd.ToResource),
		},
	}, nil
}

// undirectedEdgeKey 生成无向边的去重 key
// v1beta1 graph 是无向图，"a->b" 和 "b->a" 是同一条边
func undirectedEdgeKey(a, b string) string {
	if a > b {
		a, b = b, a
	}
	return a + "<>" + b
}
