// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

type FieldOption struct {
	// 字段数据类型: 定义了该字段在查询时被如何处理和校验。
	// date, text, keyword, long, integer, boolean等
	FieldName string `json:"field_name"`
	// 字段的原始名称: 用于构建最终发送到 存储 的查询语句。
	// time, @timestamp, message
	FieldType string `json:"field_type"`
	// 原始字段名: 可能在复杂的 Mapping（如多字段）中用于记录最原始的字段名。
	// 对于 message.keyword字段，origin_field为 "message"
	OriginField string `json:"origin_field"`
	// 是否可聚合: 对应{field}.doc_values，这影响字段能否用于排序、聚合和脚本计算。
	IsAgg bool `json:"is_agg"`
	// 字段是否被分析（分词）: 对于 text类型为 true，对于 keyword/date等类型为 false。
	IsAnalyzed bool `json:"is_analyzed"`
	// 是否区分大小写: 针对 keyword类字符串查询，由 {field}.normalizer推导。
	IsCaseSensitive bool `json:"is_case_sensitive"`
	// 自定义分词字符: 指定除空格外的其他分词符号（较少使用）。
	// 例如 -表示按连字符分词
	TokenizeOnChars string `json:"tokenize_on_chars"`
}

type FieldMap map[string]FieldOption
