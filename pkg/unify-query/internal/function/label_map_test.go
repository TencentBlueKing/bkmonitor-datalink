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
	"reflect"
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
			h := &LabelMapFactory{
				highlightMaxAnalyzedOffset: tt.fields.maxAnalyzedOffset,
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

func TestNewHighLightFactory(t *testing.T) {
	type args struct {
		labelMap          map[string][]string
		maxAnalyzedOffset int
	}
	tests := []struct {
		name string
		args args
		want *LabelMapFactory
	}{
		{
			name: "normal initialization",
			args: args{
				labelMap: map[string][]string{
					"service":  {"api", "backend"},
					"env":      {"prod"},
					"response": {"2xx", "5xx"},
				},
				maxAnalyzedOffset: 1024,
			},
			want: &LabelMapFactory{
				labelMap: map[string][]string{
					"service":  {"api", "backend"},
					"env":      {"prod"},
					"response": {"2xx", "5xx"},
				},
				highlightMaxAnalyzedOffset: 1024,
			},
		},
		{
			name: "zero value offset",
			args: args{
				labelMap:          map[string][]string{"status": {"active"}},
				maxAnalyzedOffset: 0,
			},
			want: &LabelMapFactory{
				labelMap:                   map[string][]string{"status": {"active"}},
				highlightMaxAnalyzedOffset: 0,
			},
		},
		{
			name: "negative offset",
			args: args{
				labelMap:          map[string][]string{"error": {"timeout"}},
				maxAnalyzedOffset: -1,
			},
			want: &LabelMapFactory{
				labelMap:                   map[string][]string{"error": {"timeout"}},
				highlightMaxAnalyzedOffset: -1,
			},
		},
		{
			name: "empty labelMap",
			args: args{
				labelMap:          nil,
				maxAnalyzedOffset: 2048,
			},
			want: &LabelMapFactory{
				labelMap:                   nil,
				highlightMaxAnalyzedOffset: 2048,
			},
		},
		{
			name: "empty slice value",
			args: args{
				labelMap:          map[string][]string{"tags": {}},
				maxAnalyzedOffset: 512,
			},
			want: &LabelMapFactory{
				labelMap:                   map[string][]string{"tags": {}},
				highlightMaxAnalyzedOffset: 512,
			},
		},
		{
			name: "complex multi-value map",
			args: args{
				labelMap: map[string][]string{
					"metrics": {"cpu", "mem", "disk"},
					"alerts":  {"critical"},
				},
				maxAnalyzedOffset: 4096,
			},
			want: &LabelMapFactory{
				labelMap: map[string][]string{
					"metrics": {"cpu", "mem", "disk"},
					"alerts":  {"critical"},
				},
				highlightMaxAnalyzedOffset: 4096,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NewLabelMapFactory(tt.args.labelMap, tt.args.maxAnalyzedOffset)

			if tt.want.labelMap == nil && got.labelMap != nil {
				t.Errorf("labelMap should be nil, got %v", got.labelMap)
			} else if !reflect.DeepEqual(got.labelMap, tt.want.labelMap) {
				t.Errorf("labelMap mismatch\ngot:  %v\nwant: %v", got.labelMap, tt.want.labelMap)
			}

			if got.highlightMaxAnalyzedOffset != tt.want.highlightMaxAnalyzedOffset {
				t.Errorf("maxAnalyzedOffset = %v, want %v", got.highlightMaxAnalyzedOffset, tt.want.highlightMaxAnalyzedOffset)
			}
		})
	}
}

func TestHighLightFactory_processField(t *testing.T) {
	type fields struct {
		maxAnalyzedOffset int
	}
	type args struct {
		fieldValue any
		keywords   []string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   any
	}{
		{
			name: "string type with highlight",
			fields: fields{
				maxAnalyzedOffset: 100,
			},
			args: args{
				fieldValue: "hello world",
				keywords:   []string{"hello"},
			},
			want: []string{"<mark>hello</mark> world"},
		},
		{
			name: "int type converted to string and highlighted",
			fields: fields{
				maxAnalyzedOffset: 100,
			},
			args: args{
				fieldValue: 123,
				keywords:   []string{"123"},
			},
			want: []string{"<mark>123</mark>"},
		},
		{
			name: "muti contains string type with highlight",
			fields: fields{
				maxAnalyzedOffset: 100,
			},
			args: args{
				fieldValue: "hello world",
				keywords:   []string{"he", "hello", "hello", "hel"},
			},
			want: []string{"<mark>hello</mark> world"},
		},
		{
			name: "unsupported type returns nil",
			args: args{
				fieldValue: true,
				keywords:   []string{"true"},
			},
			want: nil,
		},
		{
			name: "no highlight when keywords not found",
			fields: fields{
				maxAnalyzedOffset: 100,
			},
			args: args{
				fieldValue: "no match",
				keywords:   []string{"xyz"},
			},
			want: nil,
		},
		{
			name: "empty string with empty keywords",
			fields: fields{
				maxAnalyzedOffset: 100,
			},
			args: args{
				fieldValue: "",
				keywords:   []string{""},
			},
			want: nil,
		},
		{
			name: "maxAnalyzedOffset truncates string",
			fields: fields{
				maxAnalyzedOffset: 5,
			},
			args: args{
				fieldValue: "longstring",
				keywords:   []string{},
			},
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &LabelMapFactory{
				highlightMaxAnalyzedOffset: tt.fields.maxAnalyzedOffset,
			}
			if got := h.processHighlightField(tt.args.fieldValue, tt.args.keywords); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("HighLightFactory.processField() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestLabelMapFactory_FetchIncludeFieldValues(t *testing.T) {
	type fields struct {
		labelMap                   map[string][]string
		highlightMaxAnalyzedOffset int
	}
	type args struct {
		fieldName string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   []string
		want1  bool
	}{
		{
			name: "should include field values",
			fields: fields{
				labelMap: map[string][]string{
					"service":  {"api", "backend"},
					"env":      {"prod"},
					"response": {"2xx", "5xx"},
				},
				highlightMaxAnalyzedOffset: 1024,
			},
			args: args{
				fieldName: "service",
			},
			want:  []string{"api", "backend"},
			want1: true,
		},
		{
			name: "should not include empty_string indexed values",
			fields: fields{
				labelMap: map[string][]string{
					"service":  {"api", "backend"},
					"env":      {"prod"},
					"response": {"2xx", "5xx"},
					"":         {"empty_string"}, // 空串用来作为高亮时会单独返回，在include时不需要返回
				},
				highlightMaxAnalyzedOffset: 1024,
			},
			args: args{
				fieldName: "service",
			},
			want:  []string{"api", "backend"},
			want1: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &LabelMapFactory{
				labelMap:                   tt.fields.labelMap,
				highlightMaxAnalyzedOffset: tt.fields.highlightMaxAnalyzedOffset,
			}
			got, got1 := h.FetchIncludeFieldValues(tt.args.fieldName)
			assert.Equalf(t, tt.want, got, "FetchIncludeFieldValues(%v)", tt.args.fieldName)
			assert.Equalf(t, tt.want1, got1, "FetchIncludeFieldValues(%v)", tt.args.fieldName)
		})
	}
}
