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
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

// SchemaProvider v1beta3 扩展的 Schema 提供器接口
// 嵌入 cmdb.SchemaProvider 基础接口，并添加 v1beta3 特有的方法
type SchemaProvider interface {
	cmdb.SchemaProvider
	// GetResourcePrimaryKeys 获取资源类型的主键字段列表
	GetResourcePrimaryKeys(resourceType ResourceType) []string
	// GetRelationSchema 获取关联关系的 Schema
	GetRelationSchema(relationType RelationType) (*RelationSchema, error)
	// ListRelationSchemas 列出所有关联 Schema
	ListRelationSchemas() []RelationSchema
}

// ToResourceType 将 ResourceDefinition 转换为 v1beta3 ResourceType
func ToResourceType(rd *cmdb.ResourceDefinition) ResourceType {
	return ResourceType(rd.Name)
}

// ToRelationType 将 RelationDefinition 转换为 v1beta3 RelationType
func ToRelationType(rd *cmdb.RelationDefinition) RelationType {
	// 如果有 namespace,格式为 {namespace}:{name}
	if rd.Namespace != "" {
		return RelationType(fmt.Sprintf("%s:%s", rd.Namespace, rd.Name))
	}
	return RelationType(rd.Name)
}

// ToRelationSchema 将 RelationDefinition 转换为 v1beta3 RelationSchema
func ToRelationSchema(rd *cmdb.RelationDefinition) RelationSchema {
	category := RelationCategoryStatic
	if rd.Category == "dynamic" {
		category = RelationCategoryDynamic
	}

	return RelationSchema{
		RelationType: ToRelationType(rd),
		Category:     category,
		FromType:     ResourceType(rd.FromResource),
		ToType:       ResourceType(rd.ToResource),
		IsBelongsTo:  rd.IsBelongsTo,
	}
}
