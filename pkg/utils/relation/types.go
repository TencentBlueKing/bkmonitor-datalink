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
	"errors"
	"fmt"
	"sort"
)

const (
	RedisKeyPrefix         = "bkmonitorv3:entity"
	KindResourceDefinition = "ResourceDefinition"
	KindRelationDefinition = "RelationDefinition"
	NamespaceAll           = "__all__"
)

type DirectionType int

const (
	DirectionTypeDirectional   DirectionType = 0
	DirectionTypeBidirectional DirectionType = 1
)

type FieldDefinition struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Required  bool   `json:"required"`
}

type ResourceDefinition struct {
	Namespace string                 `json:"namespace"`
	Name      string                 `json:"name"`
	Fields    []FieldDefinition      `json:"fields"`
	Labels    map[string]string      `json:"labels"`
	Spec      map[string]interface{} `json:"spec"`
}

func (rd *ResourceDefinition) GetPrimaryKeys() []string {
	keys := make([]string, 0)
	for _, field := range rd.Fields {
		if field.Required {
			keys = append(keys, field.Name)
		}
	}
	return keys
}

type RelationDefinition struct {
	Namespace     string                 `json:"namespace"`
	Name          string                 `json:"name"`
	FromResource  string                 `json:"from_resource"`
	ToResource    string                 `json:"to_resource"`
	Category      string                 `json:"category"`
	IsDirectional bool                   `json:"is_directional"`
	IsBelongsTo   bool                   `json:"is_belongs_to"`
	Labels        map[string]string      `json:"labels"`
	Spec          map[string]interface{} `json:"spec"`
}

func (rd *RelationDefinition) GetRelationName() string {
	if rd.IsDirectional {
		return fmt.Sprintf("%s_to_%s_flow", rd.FromResource, rd.ToResource)
	}
	resources := []string{rd.FromResource, rd.ToResource}
	sort.Strings(resources)
	return fmt.Sprintf("%s_with_%s_relation", resources[0], resources[1])
}

func (rd *RelationDefinition) GetRequiredFields(fromResourceDef, toResourceDef *ResourceDefinition) []string {
	fields := make([]string, 0)
	if fromResourceDef != nil {
		fields = append(fields, fromResourceDef.GetPrimaryKeys()...)
	}
	if toResourceDef != nil {
		fields = append(fields, toResourceDef.GetPrimaryKeys()...)
	}
	return fields
}

var (
	ErrResourceDefinitionNotFound = errors.New("resource definition not found")
	ErrRelationDefinitionNotFound = errors.New("relation definition not found")
)

// SchemaChangeCallback is called when schema (resource or relation definitions) changes
// kind: "ResourceDefinition" or "RelationDefinition"
// namespace: the namespace that was changed
type SchemaChangeCallback func(kind, namespace string)

type SchemaProvider interface {
	GetResourceDefinition(namespace, name string) (*ResourceDefinition, error)
	ListResourceDefinitions(namespace string) ([]*ResourceDefinition, error)
	GetRelationDefinition(namespace, name string) (*RelationDefinition, error)
	ListRelationDefinitions(namespace string) ([]*RelationDefinition, error)
	GetResourcePrimaryKeys(resourceType ResourceType) []string
	GetRelationSchema(relationType RelationName) (*RelationSchema, error)
	ListRelationSchemas() []RelationSchema
	FindRelationByResourceTypes(namespace, fromResource, toResource string, directionType DirectionType) (*RelationDefinition, bool)
	
	// Subscribe registers a callback for schema change notifications
	// The callback will be invoked when resource or relation definitions are reloaded
	Subscribe(callback SchemaChangeCallback) error
}

type ResourceType string

type RelationName string

type RelationCategory string

const (
	RelationCategoryStatic  RelationCategory = "static"
	RelationCategoryDynamic RelationCategory = "dynamic"
)

type TraversalDirection string

const (
	DirectionOutbound TraversalDirection = "outbound"
	DirectionInbound  TraversalDirection = "inbound"
	DirectionBoth     TraversalDirection = "both"
)

type RelationSchema struct {
	RelationName RelationName
	Category     RelationCategory
	FromType     ResourceType
	ToType       ResourceType
	IsBelongsTo  bool
}

func ToResourceType(rd *ResourceDefinition) ResourceType {
	return ResourceType(rd.Name)
}

func ToRelationName(rd *RelationDefinition) RelationName {
	if rd.Namespace != "" {
		return RelationName(fmt.Sprintf("%s:%s", rd.Namespace, rd.Name))
	}
	return RelationName(rd.Name)
}

func ToRelationCategory(category string) RelationCategory {
	if category == "dynamic" {
		return RelationCategoryDynamic
	}
	return RelationCategoryStatic
}

func ToRelationSchema(rd *RelationDefinition) RelationSchema {
	return RelationSchema{
		RelationName: ToRelationName(rd),
		Category:     ToRelationCategory(rd.Category),
		FromType:     ResourceType(rd.FromResource),
		ToType:       ResourceType(rd.ToResource),
		IsBelongsTo:  rd.IsBelongsTo,
	}
}
