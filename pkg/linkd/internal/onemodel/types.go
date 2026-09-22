// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package onemodel 按显式租户查询统一实例、关联边和业务拓扑，独立于告警丰富。
package onemodel

import "errors"

// ErrInvalidDataSourceResponse 表示后端返回不完整或违反身份契约的数据。
var ErrInvalidDataSourceResponse = errors.New("invalid onemodel datasource response")

// InstanceAttributeType 是 OneModel attribute_values 使用的固定类型槽。
type InstanceAttributeType string

const (
	InstanceAttributeKeyword  InstanceAttributeType = "keyword"
	InstanceAttributeLong     InstanceAttributeType = "long"
	InstanceAttributeDouble   InstanceAttributeType = "double"
	InstanceAttributeBoolean  InstanceAttributeType = "boolean"
	InstanceAttributeDatetime InstanceAttributeType = "datetime"
	InstanceAttributeIP       InstanceAttributeType = "ip"
)

// InstanceAttributeFilter 描述一个已由调用方确定类型的 OneModel 动态属性过滤条件。
type InstanceAttributeFilter struct {
	Field string
	Type  InstanceAttributeType
	Value any
}

// InstanceQuery 描述 OneModel 实例存储的一次单实例查询。
// InstanceID 使用根字段 model_inst_id；AttributeFilters 使用 nested attribute_values。
type InstanceQuery struct {
	ModelCode        string
	InstanceID       string
	AttributeFilters []InstanceAttributeFilter
}

// Instance 是 OneModel 实例存储返回的统一实例文档。
// Fields 保存根字段，Attributes 保存来源原始属性；业务消费方按根字段优先合并读取。
type Instance struct {
	TenantID   string
	ModelCode  string
	InstanceID string
	Fields     map[string]any
	Attributes map[string]any
}

// ResourceTopology 是关联主机解析得到的业务、集群和模块投影。
type ResourceTopology struct {
	BKBizID      int64
	BKBizName    string
	BKSetID      int64
	BKSetName    string
	BKModuleID   int64
	BKModuleName string
}
