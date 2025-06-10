// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLabelMapEntry_Basic(t *testing.T) {
	testCases := []struct {
		name     string
		queryTs  *QueryTs
		expected map[string]*LabelMapEntry
	}{
		{
			name: "ConditionEqual - positive操作符",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "status",
									Value:         []string{"error"},
									Operator:      ConditionEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string]*LabelMapEntry{
				"es_inc:status:ae41e896": {
					Values: []string{"error"},
				},
				"hl:status": {
					Values: []string{"error"},
				},
			},
		},
		{
			name: "ConditionNotEqual - negative操作符",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "status",
									Value:         []string{"success"},
									Operator:      ConditionNotEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string]*LabelMapEntry{
				"es_exc:status:e3ef506e": {
					Values: []string{"success"},
				},
				"hl:status": {
					Values: []string{"success"},
				},
			},
		},
		{
			name: "混合操作符 - 同一字段的positive和negative",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "level",
									Value:         []string{"error"},
									Operator:      ConditionEqual,
								},
								{
									DimensionName: "level",
									Value:         []string{"debug"},
									Operator:      ConditionNotEqual,
								},
							},
						},
					},
				},
			},
			expected: map[string]*LabelMapEntry{
				"es_inc:level:9dc94448": {
					Values: []string{"error"},
				},
				"es_exc:level:8184a51c": {
					Values: []string{"debug"},
				},
				"hl:level": {
					Values: []string{"debug", "error"}, // 按字母顺序排序
				},
			},
		},
		{
			name: "QueryString - 默认为positive",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: "service:web",
					},
				},
			},
			expected: map[string]*LabelMapEntry{
				"es_inc:service:5577a3d3": {
					Values: []string{"web"},
				},
				"hl:service": {
					Values: []string{"web"},
				},
			},
		},
		{
			name: "QueryString和Conditions组合",
			queryTs: &QueryTs{
				QueryList: []*Query{
					{
						QueryString: "service:web",
						Conditions: Conditions{
							FieldList: []ConditionField{
								{
									DimensionName: "status",
									Value:         []string{"error"},
									Operator:      ConditionEqual,
								},
								{
									DimensionName: "level",
									Value:         []string{"debug"},
									Operator:      ConditionNotContains,
								},
							},
						},
					},
				},
			},
			expected: map[string]*LabelMapEntry{
				// ES聚合key
				"es_inc:service:5577a3d3": {
					Values: []string{"web"},
				},
				"es_inc:status:ae41e896": {
					Values: []string{"error"},
				},
				"es_exc:level:23e1b083": {
					Values: []string{"debug"},
				},
				// Highlight key
				"hl:service": {
					Values: []string{"web"},
				},
				"hl:status": {
					Values: []string{"error"},
				},
				"hl:level": {
					Values: []string{"debug"},
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tc.queryTs.LabelMap()
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result, "LabelMap result should match expected")
		})
	}
}

// TestToHighlightMap 测试ToHighlightMap功能
func TestToHighlightMap(t *testing.T) {
	queryTs := &QueryTs{
		QueryList: []*Query{
			{
				Conditions: Conditions{
					FieldList: []ConditionField{
						{
							DimensionName: "status",
							Value:         []string{"error"},
							Operator:      ConditionEqual,
						},
						{
							DimensionName: "level",
							Value:         []string{"debug"},
							Operator:      ConditionNotEqual,
						},
					},
				},
			},
		},
	}

	expected := map[string][]string{
		"status": {"error"},
		"level":  {"debug"},
	}

	result, err := queryTs.ToHighlightMap()
	assert.NoError(t, err)
	assert.Equal(t, expected, result, "ToHighlightMap result should match expected")
}

func TestIsPositiveOperator(t *testing.T) {
	testCases := []struct {
		operator string
		expected bool
	}{
		{ConditionEqual, true},
		{ConditionExact, true},
		{ConditionContains, true},
		{ConditionRegEqual, true},
		{ConditionNotEqual, false},
		{ConditionNotContains, false},
		{ConditionNotRegEqual, false},
		{ConditionGt, true}, // 其他操作符默认为positive
		{ConditionLt, true}, // 其他操作符默认为positive
		{"unknown", true},   // 未知操作符默认为positive
	}

	for _, tc := range testCases {
		t.Run(tc.operator, func(t *testing.T) {
			result := isPositiveOperator(tc.operator)
			assert.Equal(t, tc.expected, result, "isPositiveOperator result should match expected")
		})
	}
}
