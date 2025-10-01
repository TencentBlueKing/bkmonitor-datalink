// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package function

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// TestQuery_LabelMap 测试 Query.LabelMap 函数（包含 QueryString 和 Conditions 的组合）
func TestQuery_LabelMap(t *testing.T) {
	testCases := []struct {
		name     string
		query    *metadata.Query
		expected map[string][]LabelMapValue
	}{
		{
			name: "只有 Conditions",
			query: &metadata.Query{
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"error"},
							Operator:      metadata.ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]LabelMapValue{
				"status": {{Value: "error", Operator: metadata.ConditionEqual}},
			},
		},
		{
			name: "只有 QueryString",
			query: &metadata.Query{
				QueryString: "level:warning",
			},
			expected: map[string][]LabelMapValue{
				"level": {{Value: "warning", Operator: metadata.ConditionEqual}},
			},
		},
		{
			name: "QueryString 和 Conditions 组合",
			query: &metadata.Query{
				QueryString: "service:web",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"error"},
							Operator:      metadata.ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]LabelMapValue{
				"service": {{Value: "web", Operator: metadata.ConditionEqual}},
				"status":  {{Value: "error", Operator: metadata.ConditionEqual}},
			},
		},
		{
			name: "QueryString 和 Conditions 有重复字段",
			query: &metadata.Query{
				QueryString: "level:error",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"warning"},
							Operator:      metadata.ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]LabelMapValue{
				"level": {
					{
						Value: "error", Operator: metadata.ConditionEqual,
					},
				},
				"status": {
					{
						Value: "warning", Operator: metadata.ConditionEqual,
					},
				},
			},
		},
		{
			name: "QueryString 和 Conditions 有重复字段和值（去重）",
			query: &metadata.Query{
				QueryString: "level:error",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "level",
							Value:         []string{"error"},
							Operator:      metadata.ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]LabelMapValue{
				"level": {{Value: "error", Operator: metadata.ConditionEqual}},
			},
		},
		{
			name: "复杂 QueryString 和多个 Conditions - 1",
			query: &metadata.Query{
				QueryString: "NOT service:web AND component:database",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"warning", "error"},
							Operator:      metadata.ConditionNotEqual,
						},
						{
							DimensionName: "region",
							Value:         []string{"us-east-1"},
							Operator:      metadata.ConditionEqual,
						},
						{
							DimensionName: "region",
							Value:         []string{"us-east-2"},
							Operator:      metadata.ConditionEqual,
							IsWildcard:    true,
						},
					},
				},
			},
			expected: map[string][]LabelMapValue{
				"component": {
					{Value: "database", Operator: metadata.ConditionEqual},
				},
				"region": {
					{Value: "us-east-1", Operator: metadata.ConditionEqual},
					{Value: "us-east-2", Operator: metadata.ConditionContains},
				},
				"service": {
					{Value: "web", Operator: metadata.ConditionNotEqual},
				},
			},
		},
		{
			name: "复杂 QueryString 和多个 Conditions",
			query: &metadata.Query{
				QueryString: "service:web AND component:database",
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "status",
							Value:         []string{"warning", "error"},
							Operator:      metadata.ConditionEqual,
						},
						{
							DimensionName: "region",
							Value:         []string{"us-east-1"},
							Operator:      metadata.ConditionEqual,
						},
					},
				},
			},
			expected: map[string][]LabelMapValue{
				"service": {
					{Value: "web", Operator: metadata.ConditionEqual},
				},
				"component": {
					{Value: "database", Operator: metadata.ConditionEqual},
				},
				"status": {
					{Value: "warning", Operator: metadata.ConditionEqual},
					{Value: "error", Operator: metadata.ConditionEqual},
				},
				"region": {
					{Value: "us-east-1", Operator: metadata.ConditionEqual},
				},
			},
		},
		{
			name:     "空 QueryString 和空 Conditions",
			query:    nil,
			expected: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := LabelMap(context.TODO(), tc.query)
			assert.Equal(t, tc.expected, result, "Query.LabelMap result should match expected")
		})
	}
}

func TestHighLightFactory_splitTextForAnalysis(t *testing.T) {
	type fields struct {
		maxAnalyzedOffset int
	}
	type args struct {
		text string
	}
	tests := []struct {
		name           string
		fields         fields
		args           args
		wantAnalyzable string
		wantRemaining  string
	}{
		{
			name: "maxAnalyzedOffset zero",
			fields: fields{
				maxAnalyzedOffset: 0,
			},
			args: args{
				text: "this_is_a_long_text",
			},
			wantAnalyzable: "this_is_a_long_text",
			wantRemaining:  "",
		},
		{
			name: "text shorter than max offset",
			fields: fields{
				maxAnalyzedOffset: 20,
			},
			args: args{
				text: "short",
			},
			wantAnalyzable: "short",
			wantRemaining:  "",
		},
		{
			name: "text exactly at max offset",
			fields: fields{
				maxAnalyzedOffset: 5,
			},
			args: args{
				text: "12345",
			},
			wantAnalyzable: "12345",
			wantRemaining:  "",
		},
		{
			name: "text longer than max offset",
			fields: fields{
				maxAnalyzedOffset: 5,
			},
			args: args{
				text: "1234567890",
			},
			wantAnalyzable: "12345",
			wantRemaining:  "67890",
		},
		{
			name: "empty text input",
			fields: fields{
				maxAnalyzedOffset: 10,
			},
			args: args{
				text: "",
			},
			wantAnalyzable: "",
			wantRemaining:  "",
		},
		{
			name: "maxAnalyzedOffset negative (treated as no limit)",
			fields: fields{
				maxAnalyzedOffset: -1,
			},
			args: args{
				text: "should_return_full_text",
			},
			wantAnalyzable: "should_return_full_text",
			wantRemaining:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &HighLightFactory{
				maxAnalyzedOffset: tt.fields.maxAnalyzedOffset,
			}
			gotAnalyzable, gotRemaining := h.splitTextForAnalysis(tt.args.text)
			if gotAnalyzable != tt.wantAnalyzable {
				t.Errorf("splitTextForAnalysis() gotAnalyzable = %v, want %v", gotAnalyzable, tt.wantAnalyzable)
			}
			if gotRemaining != tt.wantRemaining {
				t.Errorf("splitTextForAnalysis() gotRemaining = %v, want %v", gotRemaining, tt.wantRemaining)
			}
		})
	}
}

func TestHighLightFactory_process(t *testing.T) {
	data := map[string]any{
		"file":           "victoriaMetrics/instance.go:397",
		"gseIndex":       "8019256",
		"iterationIndex": 14,
		"level":          "info",
		"message":        "victoriaMetrics query and victoriaMetrics query or victoria metrics query and victoriametrics query",
	}

	// map[gseIndex:[{Value:8019256 Operator:eq}]]
	h := &HighLightFactory{
		labelMap: map[string][]LabelMapValue{
			"gseIndex": {
				{
					Value:    "8019256",
					Operator: "eq",
				},
			},
			"": {
				{
					Value:    "metrics",
					Operator: "contains",
				},
			},
			"level": {
				{
					Value:    "info",
					Operator: "eq",
				},
				{
					Value:    "In",
					Operator: "contains",
				},
			},
		},
	}

	expected := map[string]any{
		"gseIndex": []string{"<mark>8019256</mark>"},
		"file":     []string{"victoria<mark>Metrics</mark>/instance.go:397"},
		"level":    []string{"<mark>info</mark>"},
		"message":  []string{"victoria<mark>Metrics</mark> query and victoria<mark>Metrics</mark> query or victoria <mark>metrics</mark> query and victoria<mark>metrics</mark> query"},
	}

	nd := h.Process(data)
	assert.Equal(t, expected, nd)
}
