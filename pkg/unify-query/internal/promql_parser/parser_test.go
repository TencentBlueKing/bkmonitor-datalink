// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package promql_parser

import (
	"testing"

	"github.com/prometheus/prometheus/model/labels"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestParseMetricSelector(t *testing.T) {
	tests := []struct {
		name     string
		selector string
		want     []*labels.Matcher
		wantErr  bool
	}{
		{
			name:     "simple metric name",
			selector: "http_requests_total",
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
			},
			wantErr: false,
		},
		{
			name:     "metric with single label",
			selector: `http_requests_total{method="GET"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				{Type: labels.MatchEqual, Name: "method", Value: "GET"},
			},
			wantErr: false,
		},
		{
			name:     "metric with multiple labels",
			selector: `http_requests_total{method="GET",status="200"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				{Type: labels.MatchEqual, Name: "method", Value: "GET"},
				{Type: labels.MatchEqual, Name: "status", Value: "200"},
			},
			wantErr: false,
		},
		{
			name:     "metric with regex matcher",
			selector: `http_requests_total{method=~"GET|POST"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				// 需要执行内部的FastRegexMatcher编译正则表达式
				labels.MustNewMatcher(labels.MatchRegexp, "method", "GET|POST"),
			},
			wantErr: false,
		},
		{
			name:     "metric with not equal matcher",
			selector: `http_requests_total{method!="GET"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				{Type: labels.MatchNotEqual, Name: "method", Value: "GET"},
			},
			wantErr: false,
		},
		{
			name:     "metric with not regex matcher",
			selector: `http_requests_total{method!~"GET|POST"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				labels.MustNewMatcher(labels.MatchNotRegexp, "method", "GET|POST"),
			},
			wantErr: false,
		},
		{
			name:     "labels only",
			selector: `{__name__="http_requests_total",method="GET"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				{Type: labels.MatchEqual, Name: "method", Value: "GET"},
			},
			wantErr: false,
		},
		{
			name:     "empty selector",
			selector: "",
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid brackets - missing closing brace",
			selector: `http_requests_total{method="GET"`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid brackets - missing opening brace",
			selector: `http_requests_totalmethod="GET"}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid brackets - only opening brace",
			selector: `{`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid brackets - only closing brace",
			selector: `}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "mismatched quotes - unclosed double quote",
			selector: `http_requests_total{method="GET}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "mismatched quotes - unclosed single quote",
			selector: `http_requests_total{method='GET}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid label name - starts with number",
			selector: `http_requests_total{2method="GET"}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid operator - double equal without regex",
			selector: `http_requests_total{method=="GET"}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "missing operator",
			selector: `http_requests_total{method"GET"}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "missing value",
			selector: `http_requests_total{method=}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "missing label name",
			selector: `http_requests_total{="GET"}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid comma placement - leading comma",
			selector: `http_requests_total{,method="GET"}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid comma placement - double comma",
			selector: `http_requests_total{method="GET",,status="200"}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid metric name - contains spaces",
			selector: `http requests total{method="GET"}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "invalid characters in metric name",
			selector: `http-requests@total{method="GET"}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "nested braces",
			selector: `http_requests_total{method="{GET}"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				{Type: labels.MatchEqual, Name: "method", Value: "{GET}"},
			},
			wantErr: false,
		},
		{
			name:     "empty label set",
			selector: `{}`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "only whitespace",
			selector: "   ",
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "escape sequence in string (actually valid)",
			selector: `http_requests_total{method="GET\x"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				{Type: labels.MatchEqual, Name: "method", Value: "GET\\x"},
			},
			wantErr: false,
		},
		// 边界条件测试
		{
			name:     "trailing comma (should be valid)",
			selector: `http_requests_total{method="GET",}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				{Type: labels.MatchEqual, Name: "method", Value: "GET"},
			},
			wantErr: false,
		},
		{
			name:     "numeric value in string",
			selector: `http_requests_total{port="8080"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				{Type: labels.MatchEqual, Name: "port", Value: "8080"},
			},
			wantErr: false,
		},
		{
			name:     "special characters in string value",
			selector: `http_requests_total{path="/api/v1/test-endpoint"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "http_requests_total"},
				{Type: labels.MatchEqual, Name: "path", Value: "/api/v1/test-endpoint"},
			},
			wantErr: false,
		},
		{
			name:     "unclosed string quote at end",
			selector: `http_requests_total{method="GET`,
			want:     nil,
			wantErr:  true,
		},
		{
			name:     "seprate by dot",
			selector: `metric.name{label="value"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "metric.name"},
				{Type: labels.MatchEqual, Name: "label", Value: "value"},
			},
			wantErr: false,
		},
		{
			name:     "complex metric name with colons",
			selector: `bklog:log_index_set_16750_clustered:_index{severity_text="error",resource__bk_46__cluster_id="BCS-K8S-41630"}`,
			want: []*labels.Matcher{
				{Type: labels.MatchEqual, Name: "__name__", Value: "bklog:log_index_set_16750_clustered:_index"},
				{Type: labels.MatchEqual, Name: "severity_text", Value: "error"},
				{Type: labels.MatchEqual, Name: "resource__bk_46__cluster_id", Value: "BCS-K8S-41630"},
			},
			wantErr: false,
		},
	}

	mock.Init()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMetricSelector(tt.selector)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMetricSelector() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !assert.Equal(t, got, tt.want) {
				t.Errorf("ParseMetricSelector() = %v, want %v", got, tt.want)
			}
		})
	}
}
