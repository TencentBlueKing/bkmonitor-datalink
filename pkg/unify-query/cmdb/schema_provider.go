// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdb

import (
	"errors"
)

// SchemaProvider 资源和关联定义的抽象接口
// 用于从不同来源(静态配置、Redis等)获取资源类型定义和关联关系定义
// 该接口定义在 cmdb 根包中，供 v1beta1 和 v1beta3 共同使用
type SchemaProvider interface {
	// GetResourceDefinition 获取单个资源定义
	GetResourceDefinition(namespace, name string) (*ResourceDefinition, error)
	// ListResourceDefinitions 列出指定命名空间下的所有资源定义
	// 如果 namespace 为空字符串,返回全局资源定义
	ListResourceDefinitions(namespace string) ([]*ResourceDefinition, error)
	// GetRelationDefinition 获取单个关联定义
	GetRelationDefinition(namespace, name string) (*RelationDefinition, error)
	// ListRelationDefinitions 列出指定命名空间下的所有关联定义
	// 如果 namespace 为空字符串,返回全局关联定义
	ListRelationDefinitions(namespace string) ([]*RelationDefinition, error)
}

// ResourceDefinition 资源类型定义
// 数据格式与 metadata (bk-monitor) 写入 Redis 的格式一致
type ResourceDefinition struct {
	Namespace string                 `json:"namespace"` // 命名空间(空字符串表示全局)
	Name      string                 `json:"name"`      // 资源类型名称
	Fields    []FieldDefinition      `json:"fields"`    // 字段定义列表
	Labels    map[string]string      `json:"labels"`    // 标签
	Spec      map[string]interface{} `json:"spec"`      // 原始 spec 数据
}

// FieldDefinition 字段定义
type FieldDefinition struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
}

// GetPrimaryKeys 获取主键字段列表(必填字段)
func (rd *ResourceDefinition) GetPrimaryKeys() []string {
	keys := make([]string, 0)
	for _, field := range rd.Fields {
		if field.Required {
			keys = append(keys, field.Name)
		}
	}
	return keys
}

// GetInfoFields 获取信息字段列表(非必填字段)
func (rd *ResourceDefinition) GetInfoFields() []string {
	fields := make([]string, 0)
	for _, field := range rd.Fields {
		if !field.Required {
			fields = append(fields, field.Name)
		}
	}
	return fields
}

// RelationDefinition 关联关系定义
type RelationDefinition struct {
	Namespace    string                 `json:"namespace"`     // 命名空间
	Name         string                 `json:"name"`          // 关联名称
	FromResource string                 `json:"from_resource"` // 源资源类型
	ToResource   string                 `json:"to_resource"`   // 目标资源类型
	Category     string                 `json:"category"`      // 关联类别: static/dynamic
	IsBelongsTo  bool                   `json:"is_belongs_to"` // 是否为从属关系
	Labels       map[string]string      `json:"labels"`        // 标签
	Spec         map[string]interface{} `json:"spec"`          // 原始 spec 数据
}

var (
	ErrResourceDefinitionNotFound = errors.New("resource definition not found")
	ErrRelationDefinitionNotFound = errors.New("relation definition not found")
)
