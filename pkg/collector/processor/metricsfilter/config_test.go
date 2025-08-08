// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metricsfilter

import (
	"fmt"
	"testing"

	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
)

func TestRelabelConfigValidate(t *testing.T) {
	t.Run("test rule validate", func(t *testing.T) {
		// 测试 Rule 的验证逻辑
		ruleTests := []struct {
			name    string
			rule    Rule
			wantErr bool
		}{
			{
				name:    "valid in operator",
				rule:    Rule{Op: OperatorIn, Values: []interface{}{"value1", "value2"}},
				wantErr: false,
			},
			{
				name:    "invalid in operator with non-string value",
				rule:    Rule{Op: OperatorIn, Values: []interface{}{123}},
				wantErr: true,
			},
			{
				name:    "valid range operator",
				rule:    Rule{Op: OperatorRange, Values: []interface{}{map[string]interface{}{"min": 10, "max": 20}}},
				wantErr: false,
			},
			{
				name:    "invalid range operator with non-map value",
				rule:    Rule{Op: OperatorRange, Values: []interface{}{"invalid_map"}},
				wantErr: true,
			},
			{
				name:    "default range value decode",
				rule:    Rule{Op: OperatorRange, Values: []interface{}{map[string]interface{}{"max": 20}}},
				wantErr: false,
			},
			{
				name:    "unsupported operator",
				rule:    Rule{Op: "invalid_operator"},
				wantErr: true,
			},
			{
				name:    "empty values for in operator",
				rule:    Rule{Op: OperatorIn, Values: []interface{}{}},
				wantErr: false,
			},
			{
				name:    "empty values for range operator",
				rule:    Rule{Op: OperatorRange, Values: []interface{}{}},
				wantErr: false,
			},
		}

		// 执行 Rule 测试
		for _, tt := range ruleTests {
			t.Run(tt.name, func(t *testing.T) {
				err := tt.rule.Validate()
				if tt.wantErr {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			})
		}
	})

	t.Run("test config validate", func(t *testing.T) {
		// 测试 Config 的验证逻辑
		configTests := []struct {
			name    string
			metric  string
			rules   Rules
			dest    Destination
			wantErr bool
		}{
			{
				name:    "valid config",
				metric:  "test_metric",
				rules:   Rules{{Label: "label1", Op: OperatorIn, Values: []interface{}{"value1", "value2"}}},
				dest:    Destination{Label: "dest_label", Value: "dest_value", Action: ActionUpsert},
				wantErr: false,
			},
			{
				name:    "invalid config - missing metric name",
				metric:  "",
				rules:   Rules{{Label: "label1", Op: OperatorIn, Values: []interface{}{"value1", "value2"}}},
				dest:    Destination{Label: "dest_label", Value: "dest_value", Action: ActionUpsert},
				wantErr: true,
			},
			{
				name:    "invalid config - missing destinations",
				metric:  "test_metric",
				rules:   Rules{{Label: "label1", Op: OperatorIn, Values: []interface{}{"value1", "value2"}}},
				wantErr: true,
			},
			{
				name:    "invalid config - missing destination value",
				metric:  "test_metric",
				rules:   Rules{{Label: "label1", Op: OperatorIn, Values: []interface{}{"value1", "value2"}}},
				dest:    Destination{Label: "dest_label"},
				wantErr: true,
			},
		}
		// 执行 Config 测试
		for _, tt := range configTests {
			t.Run(tt.name, func(t *testing.T) {
				c := Config{
					Relabel: []RelabelAction{
						{
							Metric: tt.metric,
							Rules:  tt.rules,
							Destinations: []Destination{
								tt.dest,
							},
						},
					},
				}
				assert.Equal(t, tt.wantErr, c.Validate() != nil)
			})
		}
	})
}

func TestRelabelRule_Match(t *testing.T) {

	t.Run("in operator", func(t *testing.T) {
		ruleIn := Rule{Label: "env", Op: "in", Values: []interface{}{"prod", "staging"}}
		tests := []struct {
			name  string
			rule  Rule
			input string
			want  bool
		}{
			{
				name:  "match",
				rule:  ruleIn,
				input: "prod",
				want:  true,
			},
			{
				name:  "no match",
				rule:  ruleIn,
				input: "dev",
				want:  false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_ = tt.rule.Validate()
				got := tt.rule.Match(tt.input)
				assert.Equal(t, tt.want, got)
			})
		}
	})

	t.Run("range operator", func(t *testing.T) {
		ruleRange := Rule{Label: "code", Op: "range", Values: []interface{}{map[string]interface{}{"min": 200, "max": 299}}}
		tests := []struct {
			name  string
			rule  Rule
			input string
			want  bool
		}{
			{
				name:  "match",
				rule:  ruleRange,
				input: "204",
				want:  true,
			},
			{
				name:  "no match",
				rule:  ruleRange,
				input: "300",
				want:  false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_ = tt.rule.Validate()
				got := tt.rule.Match(tt.input)
				assert.Equal(t, tt.want, got)
			})
		}
	})

	t.Run("range operator with prefix", func(t *testing.T) {
		ruleRangePrefix := Rule{Label: "code", Op: "range", Values: []interface{}{map[string]interface{}{"prefix": "ret_", "min": 200, "max": 299}}}
		tests := []struct {
			name  string
			rule  Rule
			input string
			want  bool
		}{
			{
				name:  "match",
				rule:  ruleRangePrefix,
				input: "ret_204",
				want:  true,
			},
			{
				name:  "value not match",
				rule:  ruleRangePrefix,
				input: "ret_300",
				want:  false,
			},
			{
				name:  "prefix not match",
				rule:  ruleRangePrefix,
				input: "200",
				want:  false,
			},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				_ = tt.rule.Validate()
				got := tt.rule.Match(tt.input)
				assert.Equal(t, tt.want, got)
			})
		}
	})
}

func createTestMap(pairs ...string) pcommon.Map {
	m := pcommon.NewMap()
	for i := 0; i < len(pairs); i += 2 {
		m.UpsertString(pairs[i], pairs[i+1])
	}
	return m
}

func TestRelabelRules_MatchMetricAttrs(t *testing.T) {

	ruleInMatch := Rule{Label: "service", Op: "in", Values: []interface{}{"auth-service"}}
	ruleRangeMatch := Rule{Label: "status", Op: "range", Values: []interface{}{map[string]interface{}{"min": 0, "max": 200}}}

	type args struct {
		attrs pcommon.Map
	}
	tests := []struct {
		name string
		rs   *Rules
		args args
		want bool
	}{
		{
			name: "empty rules not match",
			rs:   &Rules{},
			args: args{attrs: createTestMap("service", "auth-service")},
			want: false,
		},
		{
			name: "single matching rule",
			rs:   &Rules{ruleInMatch},
			args: args{attrs: createTestMap("service", "auth-service")},
			want: true,
		},
		{
			name: "single non-existing label",
			rs:   &Rules{ruleInMatch},
			args: args{attrs: createTestMap("app", "payment-service")},
			want: false,
		},
		{
			name: "multiple rules all match",
			rs:   &Rules{ruleInMatch, ruleRangeMatch},
			args: args{attrs: createTestMap("service", "auth-service", "status", "200")},
			want: true,
		},
		{
			name: "range rule mismatch",
			rs:   &Rules{ruleRangeMatch},
			args: args{attrs: createTestMap("status", "500")},
			want: false,
		},
		{
			name: "mixed rules partial match",
			rs:   &Rules{ruleInMatch, ruleRangeMatch},
			args: args{attrs: createTestMap("service", "auth-service", "status", "404")},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rs.Validate()
			assert.NoError(t, err)
			got := tt.rs.MatchMetricAttrs(tt.args.attrs)
			assert.Equal(t, tt.want, got, "Test case [%s] failed", tt.name)
		})
	}
}

func TestRelabelRules_MatchRWLabels(t *testing.T) {
	type args struct {
		labels []prompb.Label
	}
	tests := []struct {
		name string
		rs   *Rules
		args args
		want bool
	}{
		{
			name: "all rules match",
			rs: &Rules{
				{Label: "service", Op: "in", Values: []interface{}{"auth-service"}},
				{Label: "status", Op: "range", Values: []interface{}{map[string]interface{}{"min": 200, "max": 299}}},
			},
			args: args{
				labels: []prompb.Label{
					{Name: "service", Value: "auth-service"},
					{Name: "status", Value: "200"},
				},
			},
			want: true,
		},
		{
			name: "missing required label",
			rs: &Rules{
				{Label: "service", Op: "in", Values: []interface{}{"auth-service"}},
			},
			args: args{
				labels: []prompb.Label{
					{Name: "status", Value: "500"},
				},
			},
			want: false,
		},
		{
			name: "value not in range",
			rs: &Rules{
				{Label: "status", Op: "range", Values: []interface{}{map[string]interface{}{"min": 200, "max": 299}}},
			},
			args: args{
				labels: []prompb.Label{
					{Name: "status", Value: "500"},
				},
			},
			want: false,
		},
		{
			name: "value not have prefix",
			rs: &Rules{
				{Label: "status", Op: "range", Values: []interface{}{map[string]interface{}{"min": 200, "max": 299, "prefix": "ret_"}}},
			},
			args: args{
				labels: []prompb.Label{
					{Name: "status", Value: "503"},
				},
			},
			want: false,
		},
		{
			name: "empty rules not match",
			rs:   &Rules{},
			args: args{
				labels: []prompb.Label{
					{Name: "service", Value: "any-service"},
				},
			},
			want: false,
		},
		{
			name: "empty labels with rules",
			rs: &Rules{
				{Label: "service", Op: "in", Values: []interface{}{"auth-service"}},
			},
			args: args{
				labels: []prompb.Label{},
			},
			want: false,
		},
		{
			name: "partial matching labels",
			rs: &Rules{
				{Label: "service", Op: "in", Values: []interface{}{"auth-service"}},
				{Label: "env", Op: "in", Values: []interface{}{"prod"}},
			},
			args: args{
				labels: []prompb.Label{
					{Name: "service", Value: "auth-service"},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.rs.Validate()
			if got := tt.rs.MatchRWLabels(tt.args.labels); got != tt.want {
				t.Errorf("MatchRWLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func makeRWDataAndRule(numExtraLabel int) ([]prompb.Label, Rules) {
	var labels []prompb.Label
	for i := 0; i < numExtraLabel; i++ {
		labels = append(labels, prompb.Label{
			Name:  fmt.Sprintf("label_%d", i),
			Value: fmt.Sprintf("value_%d", i),
		})
	}
	labels = append(labels, prompb.Label{
		Name:  "service",
		Value: "auth-service",
	}, prompb.Label{
		Name:  "env",
		Value: "prod",
	}, prompb.Label{
		Name:  "status",
		Value: "200",
	})
	rules := Rules{
		{Label: "service", Op: "in", Values: []interface{}{"auth-service"}},
		{Label: "env", Op: "in", Values: []interface{}{"prod"}},
		{Label: "status", Op: "range", Values: []interface{}{map[string]interface{}{"min": 200, "max": 299, "prefix": "ret_"}}},
	}
	return labels, rules
}

func extractDims(labels []prompb.Label) (string, map[string]*prompb.Label) {
	m := make(map[string]*prompb.Label, len(labels))
	var name string
	for i := 0; i < len(labels); i++ {
		if labels[i].GetName() == "__name__" {
			name = labels[i].GetValue()
			continue
		}
		m[labels[i].GetName()] = &labels[i]
	}
	return name, m
}

// 直接遍历 labels，时间复杂度 o(n^2), 但是实际 n(rules) 一般比较小，性能更好
func BenchmarkMatchRWLabelsSlice(b *testing.B) {
	labels, rules := makeRWDataAndRule(10)

	for i := 0; i < b.N; i++ {
		rules.MatchRWLabels(labels)
	}
}

// 遍历一次 labels，构建 map，时间复杂度 o(n)，但申请内存造成的开销远大于遍历
func BenchmarkMatchRWLabelsMap(b *testing.B) {
	lbs, rules := makeRWDataAndRule(10)

	for i := 0; i < b.N; i++ {
		_, labels := extractDims(lbs)
		for _, rule := range rules {
			label, ok := labels[rule.Label]
			if !ok {
				continue
			}
			if matched := rule.Match(label.GetValue()); !matched {
				continue
			}
		}
	}
}
