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
	"testing"

	"github.com/stretchr/testify/assert"
)

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
