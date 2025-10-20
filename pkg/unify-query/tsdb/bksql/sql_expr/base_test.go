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
	t.Run("无公共条件", func(t *testing.T) {
		// 所有OR分支完全不同
		conditions := metadata.AllConditions{
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"2"}},
			},
			{
				{DimensionName: "C", Operator: metadata.ConditionEqual, Value: []string{"3"}},
				{DimensionName: "D", Operator: metadata.ConditionEqual, Value: []string{"4"}},
			},
			{
				{DimensionName: "E", Operator: metadata.ConditionEqual, Value: []string{"5"}},
				{DimensionName: "F", Operator: metadata.ConditionEqual, Value: []string{"6"}},
			},
		}

		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)

		expected := "(`A` = '1' AND `B` = '2' OR `C` = '3' AND `D` = '4' OR `E` = '5' AND `F` = '6')"
		assert.Equal(t, expected, result)
		t.Logf("无公共条件结果：%s", result)
	})

	t.Run("条件位置不同", func(t *testing.T) {
		// 公共条件在不同位置，应该能被正确识别并提取
		conditions := metadata.AllConditions{
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"2"}},
				{DimensionName: "C", Operator: metadata.ConditionEqual, Value: []string{"3"}},
			},
			{
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"2"}},
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
				{DimensionName: "D", Operator: metadata.ConditionEqual, Value: []string{"4"}},
			},
			{
				{DimensionName: "C", Operator: metadata.ConditionEqual, Value: []string{"3"}},
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"2"}},
			},
		}

		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)

		expected := "`A` = '1' AND `B` = '2' AND (`C` = '3' OR `D` = '4' OR `C` = '3')"
		assert.Equal(t, expected, result)
		t.Logf("条件位置不同结果：%s", result)
	})

	t.Run("字段相同操作符不同", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"2"}},
			},
			{
				{DimensionName: "A", Operator: metadata.ConditionGt, Value: []string{"1"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"2"}},
			},
		}

		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)

		expected := "`B` = '2' AND (`A` = '1' OR `A` > 1)"
		assert.Equal(t, expected, result)
		t.Logf("字段相同操作符不同结果：%s", result)
	})

	t.Run("字段操作符相同值不同", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"2"}},
			},
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"3"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"2"}},
			},
		}

		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)

		expected := "`B` = '2' AND (`A` = '1' OR `A` = '3')"
		assert.Equal(t, expected, result)
		t.Logf("字段操作符相同值不同结果：%s", result)
	})

	t.Run("IN条件值顺序", func(t *testing.T) {
		// IN条件的值顺序不同，但应该识别为相同条件
		conditions := metadata.AllConditions{
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1", "2", "3"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"x"}},
			},
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"3", "1", "2"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"y"}},
			},
		}

		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)

		// A IN (1,2,3) 和 A IN (3,1,2) 应该被识别为相同条件（值排序后签名相同）
		expected := "`A` IN ('3', '1', '2') AND (`B` = 'x' OR `B` = 'y')"
		assert.Equal(t, expected, result)
		t.Logf("IN条件值顺序结果：%s", result)
	})

	t.Run("单个分支", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
			},
		}
		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)
		assert.Equal(t, "`A` = '1'", result)
	})

	t.Run("场景14-全部条件提取后为空", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
			},
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
			},
		}
		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)

		expected := "`A` = '1'"
		assert.Equal(t, expected, result)
		t.Logf("全部条件提取后结果：%s", result)
	})

	t.Run("场景3-部分分支有公共条件", func(t *testing.T) {
		conditions := metadata.AllConditions{
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"2"}},
				{DimensionName: "C", Operator: metadata.ConditionEqual, Value: []string{"3"}},
			},
			{
				{DimensionName: "A", Operator: metadata.ConditionEqual, Value: []string{"1"}},
				{DimensionName: "B", Operator: metadata.ConditionEqual, Value: []string{"2"}},
				{DimensionName: "D", Operator: metadata.ConditionEqual, Value: []string{"4"}},
			},
			{
				{DimensionName: "E", Operator: metadata.ConditionEqual, Value: []string{"5"}},
				{DimensionName: "F", Operator: metadata.ConditionEqual, Value: []string{"6"}},
				{DimensionName: "G", Operator: metadata.ConditionEqual, Value: []string{"7"}},
			},
		}

		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)

		// 当前策略：不优化部分公共条件，保持原样
		expected := "(`A` = '1' AND `B` = '2' AND `C` = '3' OR `A` = '1' AND `B` = '2' AND `D` = '4' OR `E` = '5' AND `F` = '6' AND `G` = '7')"
		assert.Equal(t, expected, result)
		t.Logf("部分分支有公共条件结果：%s", result)
	})

	t.Run("场景15-特殊字符转义", func(t *testing.T) {
		// 测试包含特殊字符的条件是否能正确优化
		conditions := metadata.AllConditions{
			{
				{DimensionName: "path", Operator: metadata.ConditionEqual, Value: []string{"/data/log's/file.log"}},
				{DimensionName: "common", Operator: metadata.ConditionEqual, Value: []string{"value"}},
			},
			{
				{DimensionName: "text", Operator: metadata.ConditionContains, Value: []string{"test"}},
				{DimensionName: "common", Operator: metadata.ConditionEqual, Value: []string{"value"}},
			},
		}

		result, err := d.ParserAllConditions(conditions)
		assert.NoError(t, err)

		// 优化后：应该提取 common='value' 为公共条件
		// 确保单引号被正确转义
		expected := "`common` = 'value' AND (`path` = '/data/log''s/file.log' OR `text` = 'test')"
		assert.Equal(t, expected, result)
		assert.Contains(t, result, "''") // 单引号应该被转义为两个单引号
		t.Logf("特殊字符转义结果：%s", result)
	})
}
