// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql

// ConditionField 表示一个查询条件字段
// 用于构建 WHERE 子句中的单个条件
type ConditionField struct {
	DimensionName string   `json:"field_name"` // 维度/字段名称
	Value         []string `json:"value"`      // 字段值列表，支持多值匹配
	Operator      string   `json:"op"`         // 操作符，如 "=", "!=", "=~", "!~", ">", "<" 等
}
