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
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			c.orders.SortSliceList(c.list)

			assert.Equal(t, c.expected, c.list)
		})
	}
}

func TestMapConditionOperator(t *testing.T) {
	testCases := []struct {
		name           string
		operator       string
		expectedResult OperatorMapping
		expectError    bool
	}{
		{
			name:           "equal operator",
			operator:       ConditionEqual,
			expectedResult: OperatorMapping{LabelOperator: "eq", ShouldSkip: false},
			expectError:    false,
		},
		{
			name:           "exact operator",
			operator:       ConditionExact,
			expectedResult: OperatorMapping{LabelOperator: "eq", ShouldSkip: false},
			expectError:    false,
		},
		{
			name:           "contains operator",
			operator:       ConditionContains,
			expectedResult: OperatorMapping{LabelOperator: "contains", ShouldSkip: false},
			expectError:    false,
		},
		{
			name:           "regex equal operator",
			operator:       ConditionRegEqual,
			expectedResult: OperatorMapping{LabelOperator: "req", ShouldSkip: false},
			expectError:    false,
		},
		{
			name:           "greater than operator",
			operator:       ConditionGt,
			expectedResult: OperatorMapping{LabelOperator: "gt", ShouldSkip: false},
			expectError:    false,
		},
		{
			name:           "greater than or equal operator",
			operator:       ConditionGte,
			expectedResult: OperatorMapping{LabelOperator: "gte", ShouldSkip: false},
			expectError:    false,
		},
		{
			name:           "less than operator",
			operator:       ConditionLt,
			expectedResult: OperatorMapping{LabelOperator: "lt", ShouldSkip: false},
			expectError:    false,
		},
		{
			name:           "less than or equal operator",
			operator:       ConditionLte,
			expectedResult: OperatorMapping{LabelOperator: "lte", ShouldSkip: false},
			expectError:    false,
		},
		{
			name:           "not equal operator should skip",
			operator:       ConditionNotEqual,
			expectedResult: OperatorMapping{ShouldSkip: true},
			expectError:    false,
		},
		{
			name:           "not contains operator should skip",
			operator:       ConditionNotContains,
			expectedResult: OperatorMapping{ShouldSkip: true},
			expectError:    false,
		},
		{
			name:           "not regex equal operator should skip",
			operator:       ConditionNotRegEqual,
			expectedResult: OperatorMapping{ShouldSkip: true},
			expectError:    false,
		},
		{
			name:           "not existed operator should skip",
			operator:       ConditionNotExisted,
			expectedResult: OperatorMapping{ShouldSkip: true},
			expectError:    false,
		},
		{
			name:           "existed operator should skip",
			operator:       ConditionExisted,
			expectedResult: OperatorMapping{ShouldSkip: true},
			expectError:    false,
		},
		{
			name:        "unknown operator should error",
			operator:    "unknown",
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := MapConditionOperator(tc.operator)

			if tc.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), "unknown operator")
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedResult, result)
			}
		})
	}
}

func TestProcessConditionForLabelMap(t *testing.T) {
	testCases := []struct {
		name          string
		dimensionName string
		values        []string
		operator      string
		expectError   bool
		expectedCalls []struct {
			key      string
			value    string
			operator string
		}
	}{
		{
			name:          "process equal condition",
			dimensionName: "level",
			values:        []string{"error", "warn"},
			operator:      ConditionEqual,
			expectError:   false,
			expectedCalls: []struct {
				key      string
				value    string
				operator string
			}{
				{key: "level", value: "error", operator: "eq"},
				{key: "level", value: "warn", operator: "eq"},
			},
		},
		{
			name:          "process contains condition",
			dimensionName: "status",
			values:        []string{"success", "failed"},
			operator:      ConditionContains,
			expectError:   false,
			expectedCalls: []struct {
				key      string
				value    string
				operator string
			}{
				{key: "status", value: "success", operator: "contains"},
				{key: "status", value: "failed", operator: "contains"},
			},
		},
		{
			name:          "skip negative condition",
			dimensionName: "level",
			values:        []string{"debug"},
			operator:      ConditionNotEqual,
			expectError:   false,
			expectedCalls: nil, // should not call addLabelFunc
		},
		{
			name:          "skip empty values",
			dimensionName: "level",
			values:        []string{},
			operator:      ConditionEqual,
			expectError:   false,
			expectedCalls: nil,
		},
		{
			name:          "skip empty string values",
			dimensionName: "level",
			values:        []string{"", "warn"},
			operator:      ConditionEqual,
			expectError:   false,
			expectedCalls: []struct {
				key      string
				value    string
				operator string
			}{
				{key: "level", value: "warn", operator: "eq"},
			},
		},
		{
			name:          "unknown operator should error",
			dimensionName: "level",
			values:        []string{"error"},
			operator:      "unknown",
			expectError:   true,
			expectedCalls: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var actualCalls []struct {
				key      string
				value    string
				operator string
			}

			addLabelFunc := func(key, value, operator string) {
				actualCalls = append(actualCalls, struct {
					key      string
					value    string
					operator string
				}{key: key, value: value, operator: operator})
			}

			err := ProcessConditionForLabelMap(tc.dimensionName, tc.values, tc.operator, addLabelFunc)

			if tc.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedCalls, actualCalls)
			}
		})
	}
}
