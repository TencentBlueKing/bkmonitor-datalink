// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sql_expr_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb/bksql/sql_expr"
)

// TestParserAllConditions 测试全条件解析的主测试函数
func TestParserAllConditions(t *testing.T) {
	d := sql_expr.NewSQLExpr("")

	doris := sql_expr.DorisSQLExpr{}
	doris.WithFieldsMap(metadata.FieldsMap{
		"text_field": {FieldType: "text", IsAnalyzed: true},
	})

	t.Run("空条件测试", func(t *testing.T) {
		conditions := metadata.AllConditions{}
		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)
		assert.Equal(t, "", result)
	})

	t.Run("单OR组单AND条件", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "host", Operator: metadata.ConditionEqual, Value: []string{"server1"}},
			},
		}
		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)
		assert.Equal(t, "`host` = 'server1'", result)
	})

	t.Run("多OR组组合", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "os", Operator: metadata.ConditionEqual, Value: []string{"linux"}},
				{DimensionName: "status", Operator: metadata.ConditionGt, Value: []string{"5"}},
			},
			{
				{DimensionName: "region", Operator: metadata.ConditionEqual, Value: []string{"north", "south"}},
			},
		}
		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)
		expected := "(`os` = 'linux' AND `status` > 5 OR `region` IN ('north', 'south'))"
		assert.Equal(t, expected, result)
	})

	t.Run("包含空AND条件过滤", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "log", Operator: metadata.ConditionRegEqual, Value: []string{}}, // 空值条件
				{DimensionName: "level", Operator: metadata.ConditionNotEqual, Value: []string{"debug"}},
			},
		}
		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)
		assert.Equal(t, "`level` != 'debug'", result)
	})

	t.Run("混合操作符测试", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "cpu", Operator: metadata.ConditionGte, Value: []string{"80"}},
				{DimensionName: "memory", Operator: metadata.ConditionLt, Value: []string{"90"}},
			},
			{
				{DimensionName: "service", Operator: metadata.ConditionContains, Value: []string{"api", "db"}},
			},
		}
		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)
		expected := "(`cpu` >= 80 AND `memory` < 90 OR `service` IN ('api', 'db'))"
		assert.Equal(t, expected, result)
	})

	t.Run("错误条件传递测试", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "error", Operator: metadata.ConditionGt, Value: []string{"1", "2"}},
			},
		}
		_, err := d.ParserAllConditions(conditions)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "operator > only support 1 value")
	})

	t.Run("多层嵌套组合测试", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
				{DimensionName: "B", Operator: metadata.ConditionNotEqual, Value: []string{"2"}},
			},
			{
				{DimensionName: "C", Operator: metadata.ConditionRegEqual, Value: []string{"test.*"}},
			},
			{
				{DimensionName: "D", Operator: metadata.ConditionLte, Value: []string{"100"}},
				{DimensionName: "E", Operator: metadata.ConditionContains, Value: []string{"x", "y"}},
			},
		}
		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)
		expected := "(`A` = '1' AND `B` != '2' OR `C` REGEXP 'test.*' OR `D` <= 100 AND `E` IN ('x', 'y'))"
		assert.Equal(t, expected, result)
	})

	t.Run("单值IN转换测试", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "version", Operator: metadata.ConditionEqual, Value: []string{"v1"}},
			},
		}
		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)
		assert.Equal(t, "`version` = 'v1'", result)
	})

	t.Run("正则表达式组合测试", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "path", Operator: metadata.ConditionRegEqual, Value: []string{"^/api", "v2$"}},
			},
		}
		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)
		assert.Equal(t, "`path` REGEXP '^/api|v2$'", result)
	})

	t.Run("字段分词", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "text_field", Operator: metadata.ConditionEqual, Value: []string{"v1"}},
			},
		}
		result, err := doris.ParserAllConditions(conditions)
		assert.NoError(t, err)
		assert.Equal(t, "`text_field` MATCH_PHRASE 'v1'", result)
	})

	t.Run("字段不分词", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "text_field_1", Operator: metadata.ConditionEqual, Value: []string{"v1"}},
			},
		}
		result, err := doris.ParserAllConditions(conditions)
		assert.NoError(t, err)
		assert.Equal(t, "`text_field_1` = 'v1'", result)
	})
}
