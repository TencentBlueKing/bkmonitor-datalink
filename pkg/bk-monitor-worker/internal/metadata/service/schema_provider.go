// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"fmt"
	"sort"
)

// SchemaProvider 提供资源和关系的元数据定义
type SchemaProvider interface {
	// GetResourceDefinition 获取资源定义
	// 返回资源的主键字段列表
	GetResourceDefinition(namespace, resourceType string) (*ResourceDefinition, error)

	// GetRelationDefinition 获取关系定义
	// 返回关系的元数据和必填字段列表
	GetRelationDefinition(namespace, fromResource, toResource string) (*RelationDefinition, error)

	// ListRelationDefinitions 列出所有关系定义
	ListRelationDefinitions(namespace string) ([]*RelationDefinition, error)
}

// ResourceDefinition 资源类型定义
type ResourceDefinition struct {
	Namespace string            `json:"namespace"` // 命名空间
	Name      string            `json:"name"`      // 资源类型名称
	Fields    []FieldDefinition `json:"fields"`    // 字段定义列表
	Labels    map[string]string `json:"labels"`    // 标签
}

// FieldDefinition 字段定义
type FieldDefinition struct {
	Namespace string `json:"namespace"` // 字段命名空间（如 k8s, bkmonitor）
	Name      string `json:"name"`      // 字段名称
	Required  bool   `json:"required"`  // 是否必填（主键）
}

// GetPrimaryKeys 获取资源的主键字段列表
func (rd *ResourceDefinition) GetPrimaryKeys() []string {
	keys := make([]string, 0)
	for _, field := range rd.Fields {
		if field.Required {
			keys = append(keys, field.Name)
		}
	}
	return keys
}

// RelationDefinition 关联关系定义
type RelationDefinition struct {
	Namespace    string            `json:"namespace"`      // 命名空间
	Name         string            `json:"name"`           // 关联名称
	FromResource string            `json:"from_resource"`  // 源资源类型
	ToResource   string            `json:"to_resource"`    // 目标资源类型
	Category     string            `json:"category"`       // 关联类别: static/dynamic
	IsBelongsTo  bool              `json:"is_belongs_to"`  // 是否为从属关系（单向关系）
	Labels       map[string]string `json:"labels"`         // 标签
}

// IsDirectional 判断关系是否为单向（有方向性）
// IsBelongsTo=true 表示单向关系，使用 _to_ 连接
// IsBelongsTo=false 表示双向关系，使用 _with_ 连接
func (rd *RelationDefinition) IsDirectional() bool {
	return rd.IsBelongsTo
}

// GetRelationName 获取关系指标/表名称
// 双向关系（IsBelongsTo=false）：按字母序排序，使用 {resource1}_with_{resource2}（resource1 < resource2）
// 单向关系（IsBelongsTo=true）：按流量方向，使用 {from}_to_{to}
func (rd *RelationDefinition) GetRelationName() string {
	if rd.IsDirectional() {
		// 单向关系：使用 _to_，保持 from -> to 方向
		return fmt.Sprintf("%s_to_%s", rd.FromResource, rd.ToResource)
	}
	// 双向关系：使用 _with_，按字母序排序
	resources := []string{rd.FromResource, rd.ToResource}
	sort.Strings(resources)
	return fmt.Sprintf("%s_with_%s", resources[0], resources[1])
}

// GetRequiredFields 获取关系的必填字段列表
// 包含两端资源的主键字段
func (rd *RelationDefinition) GetRequiredFields(
	fromResourceDef *ResourceDefinition,
	toResourceDef *ResourceDefinition,
) []string {
	fields := make([]string, 0)

	// 添加源资源的主键
	if fromResourceDef != nil {
		fields = append(fields, fromResourceDef.GetPrimaryKeys()...)
	}

	// 添加目标资源的主键
	if toResourceDef != nil {
		fields = append(fields, toResourceDef.GetPrimaryKeys()...)
	}

	return fields
}
