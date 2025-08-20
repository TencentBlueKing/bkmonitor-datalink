// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
)

func TestReplaceVmCondition(t *testing.T) {
	for name, c := range map[string]struct {
		condition     VmCondition
		replaceLabels ReplaceLabels
		expected      VmCondition
	}{
		"test_1": {
			condition: `tag_1="a"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
			},
			expected: `tag_1="b"`,
		},
		"test_2": {
			condition: `tag_1="a1"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
			},
			expected: `tag_1="a1"`,
		},
		"test_3": {
			condition: `tag_1="a"-rr`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
			},
			expected: `tag_1="a"-rr`,
		},
		"test_4": {
			condition: `tag_1="a" or tag_2="good"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
				"tag_2": ReplaceLabel{
					Source: "good",
					Target: "bad",
				},
			},
			expected: `tag_1="b" or tag_2="bad"`,
		},
		"test_5": {
			condition: `tag_1="a" or tag_2="good", tag_1="a"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
				"tag_2": ReplaceLabel{
					Source: "good",
					Target: "bad",
				},
			},
			expected: `tag_1="b" or tag_2="bad", tag_1="b"`,
		},
		"test_6": {
			condition: `tag_1="a" or tag_2="good", tag_1="cat"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
				"tag_2": ReplaceLabel{
					Source: "good",
					Target: "bad",
				},
			},
			expected: `tag_1="b" or tag_2="bad", tag_1="cat"`,
		},
		"test_7": {
			condition: `tag_1="a" or tag_1="cat", tag_3="a", tag_5="a"`,
			replaceLabels: ReplaceLabels{
				"tag_1": ReplaceLabel{
					Source: "a",
					Target: "b",
				},
				"tag_2": ReplaceLabel{
					Source: "good",
					Target: "bad",
				},
			},
			expected: `tag_1="b" or tag_1="cat", tag_3="a", tag_5="a"`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			actual := ReplaceVmCondition(c.condition, c.replaceLabels)
			assert.Equal(t, c.expected, actual)
		})
	}
}

func TestOrders_SortSliceList(t *testing.T) {
	testCases := []struct {
		name     string
		orders   Orders
		list     []map[string]any
		expected []map[string]any
	}{
		{
			name: "test - 1",
			orders: Orders{
				{
					Name: "a",
					Ast:  false,
				},
				{
					Name: "b",
					Ast:  true,
				},
			},
			list: []map[string]any{
				{
					"a": "123456",
					"b": "abc",
				},
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abd",
				},
			},
			expected: []map[string]any{
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abc",
				},
				{
					"a": "123456",
					"b": "abd",
				},
			},
		},
		{
			name: "test - 2",
			orders: Orders{
				{
					Name: "a",
					Ast:  false,
				},
				{
					Name: "b",
					Ast:  false,
				},
			},
			list: []map[string]any{
				{
					"a": "123456",
					"b": "abc",
				},
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abd",
				},
			},
			expected: []map[string]any{
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abc",
				},
			},
		},
		{
			name: "test - 3",
			list: []map[string]any{
				{
					"a": "123456",
					"b": "abc",
				},
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abd",
				},
			},
			expected: []map[string]any{
				{
					"a": "123456",
					"b": "abc",
				},
				{
					"a": "123457",
					"b": "abd",
				},
				{
					"a": "123456",
					"b": "abd",
				},
			},
		},
		{
			name: "test - time",
			orders: Orders{
				{
					Name: "time",
					Ast:  false,
				},
			},
			list: []map[string]any{
				{
					"time": "1754466569000000002", // 2025-08-06 15:49:29
				},
				{
					"time": "2025-08-06T17:49:29.000000001Z",
				},
				{
					"time": "2025-08-06T17:49:29.000000002Z",
				},
				{
					"time": "1754466568000", // 2025-08-06 15:49:28
				},
				{
					"time": "2025-08-06T17:46:29.000000002Z",
				},
				{
					"time": "1754866568000", // 2025-08-11 06:56:08
				},
			},
			expected: []map[string]any{
				{
					"time": "1754866568000", // 2025-08-11 06:56:08
				},
				{
					"time": "2025-08-06T17:49:29.000000002Z",
				},
				{
					"time": "2025-08-06T17:49:29.000000001Z",
				},
				{
					"time": "2025-08-06T17:46:29.000000002Z",
				},
				{
					"time": "1754466569000000002", // 2025-08-06 15:49:29
				},
				{
					"time": "1754466568000", // 2025-08-06 15:49:28
				},
			},
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			c.orders.SortSliceList(c.list, map[string]string{
				"time": TypeDate,
			})

			assert.Equal(t, c.expected, c.list)
		})
	}
}

// TestQuery_LabelMap 测试 Query.LabelMap 函数（包含 QueryString 和 Conditions 的组合）
func TestQuery_LabelMap(t *testing.T) {
	testCases := []struct {
		name     string
		query    Query
		expected map[string][]function.LabelMapValue
	}{
		{
			name: "只有 Conditions",
			query: Query{
				AllConditions: AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"error"},
							Operator:      ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]function.LabelMapValue{
				"status": {{Value: "error", Operator: ConditionEqual}},
			},
		},
		{
			name: "只有 QueryString",
			query: Query{
				QueryString: "level:warning",
			},
			expected: map[string][]function.LabelMapValue{
				"level": {{Value: "warning", Operator: ConditionEqual}},
			},
		},
		{
			name: "QueryString 和 Conditions 组合",
			query: Query{
				QueryString: "service:web",
				AllConditions: AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"error"},
							Operator:      ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]function.LabelMapValue{
				"service": {{Value: "web", Operator: ConditionEqual}},
				"status":  {{Value: "error", Operator: ConditionEqual}},
			},
		},
		{
			name: "QueryString 和 Conditions 有重复字段",
			query: Query{
				QueryString: "level:error",
				AllConditions: AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"warning"},
							Operator:      ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]function.LabelMapValue{
				"level": {
					{
						Value: "error", Operator: ConditionEqual,
					},
				},
				"status": {
					{
						Value: "warning", Operator: ConditionEqual,
					},
				},
			},
		},
		{
			name: "QueryString 和 Conditions 有重复字段和值（去重）",
			query: Query{
				QueryString: "level:error",
				AllConditions: AllConditions{
					{
						{
							DimensionName: "level",
							Value:         []string{"error"},
							Operator:      ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]function.LabelMapValue{
				"level": {{Value: "error", Operator: ConditionEqual}},
			},
		},
		{
			name: "复杂 QueryString 和多个 Conditions - 1",
			query: Query{
				QueryString: "NOT service:web AND component:database",
				AllConditions: AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"warning", "error"},
							Operator:      ConditionNotEqual,
						},
						{
							DimensionName: "region",
							Value:         []string{"us-east-1"},
							Operator:      ConditionEqual,
						},
						{
							DimensionName: "region",
							Value:         []string{"us-east-2"},
							Operator:      ConditionEqual,
							IsWildcard:    true,
						},
					},
				},
			},
			expected: map[string][]function.LabelMapValue{
				"component": {
					{Value: "database", Operator: ConditionEqual},
				},
				"region": {
					{Value: "us-east-1", Operator: ConditionEqual},
					{Value: "us-east-2", Operator: ConditionContains},
				},
			},
		},
		{
			name: "复杂 QueryString 和多个 Conditions",
			query: Query{
				QueryString: "service:web AND component:database",
				AllConditions: AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"warning", "error"},
							Operator:      ConditionEqual,
						},
						{
							DimensionName: "region",
							Value:         []string{"us-east-1"},
							Operator:      ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]function.LabelMapValue{
				"service": {
					{Value: "web", Operator: ConditionEqual},
				},
				"component": {
					{Value: "database", Operator: ConditionEqual},
				},
				"status": {
					{Value: "warning", Operator: ConditionEqual},
					{Value: "error", Operator: ConditionEqual},
				},
				"region": {
					{Value: "us-east-1", Operator: ConditionEqual},
				},
			},
		},
		{
			name:     "空 QueryString 和空 Conditions",
			query:    Query{},
			expected: map[string][]function.LabelMapValue{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.query.LabelMap()
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result, "Query.LabelMap result should match expected")
		})
	}
}
