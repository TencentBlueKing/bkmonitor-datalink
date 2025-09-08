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
				labels.MustNewMatcher(labels.MatchEqual, "__name__", "http_requests_total"),
			},
			wantErr: false,
		},
		{
			name:     "metric with single label",
			selector: `http_requests_total{method="GET"}`,
			want: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "__name__", "http_requests_total"),
				labels.MustNewMatcher(labels.MatchEqual, "method", "GET"),
			},
			wantErr: false,
		},
		{
			name:     "metric with multiple labels",
			selector: `http_requests_total{method="GET",status="200"}`,
			want: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "__name__", "http_requests_total"),
				labels.MustNewMatcher(labels.MatchEqual, "method", "GET"),
				labels.MustNewMatcher(labels.MatchEqual, "status", "200"),
			},
			wantErr: false,
		},
		{
			name:     "metric with regex matcher",
			selector: `http_requests_total{method=~"GET|POST"}`,
			want: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "__name__", "http_requests_total"),
				labels.MustNewMatcher(labels.MatchRegexp, "method", "GET|POST"),
			},
			wantErr: false,
		},
		{
			name:     "metric with not equal matcher",
			selector: `http_requests_total{method!="GET"}`,
			want: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "__name__", "http_requests_total"),
				labels.MustNewMatcher(labels.MatchNotEqual, "method", "GET"),
			},
			wantErr: false,
		},
		{
			name:     "metric with not regex matcher",
			selector: `http_requests_total{method!~"GET|POST"}`,
			want: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "__name__", "http_requests_total"),
				labels.MustNewMatcher(labels.MatchNotRegexp, "method", "GET|POST"),
			},
			wantErr: false,
		},
		{
			name:     "labels only",
			selector: `{__name__="http_requests_total",method="GET"}`,
			want: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "__name__", "http_requests_total"),
				labels.MustNewMatcher(labels.MatchEqual, "method", "GET"),
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
			name:     "seprate by dot",
			selector: "metric.name{label=\"value\"}",
			want: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "__name__", "metric.name"),
				labels.MustNewMatcher(labels.MatchEqual, "label", "value"),
			},
			wantErr: false,
		},
		{
			name:     "complex metric name with colons",
			selector: `bklog:log_index_set_16750_clustered:_index{severity_text="error",resource__bk_46__cluster_id="BCS-K8S-41630"}`,
			want: []*labels.Matcher{
				labels.MustNewMatcher(labels.MatchEqual, "__name__", "bklog:log_index_set_16750_clustered:_index"),
				labels.MustNewMatcher(labels.MatchEqual, "severity_text", "error"),
				labels.MustNewMatcher(labels.MatchEqual, "resource__bk_46__cluster_id", "BCS-K8S-41630"),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseMetricSelector(tt.selector)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseMetricSelector() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !matchersEqual(got, tt.want) {
				t.Errorf("ParseMetricSelector() = %v, want %v", got, tt.want)
			}
		})
	}
}

func matchersEqual(a, b []*labels.Matcher) bool {
	if len(a) != len(b) {
		return false
	}
	for i, ma := range a {
		mb := b[i]
		if ma.Type != mb.Type || ma.Name != mb.Name || ma.Value != mb.Value {
			return false
		}
	}
	return true
}
