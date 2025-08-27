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
	t.Run("rule validate", func(t *testing.T) {
		tests := []struct {
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

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				assert.Equal(t, tt.wantErr, tt.rule.Validate() != nil)
			})
		}
	})

	t.Run("config validate", func(t *testing.T) {
		tests := []struct {
			name    string
			metrics []string
			rules   Rules
			dest    []Destination
			wantErr bool
		}{
			{
				name:    "valid config",
				metrics: []string{"test_metric"},
				rules:   Rules{{Label: "label1", Op: OperatorIn, Values: []interface{}{"value1", "value2"}}},
				dest:    []Destination{{Label: "dest_label", Value: "dest_value", Action: ActionUpsert}},
				wantErr: false,
			},
			{
				name:    "valid config - multiple metrics",
				metrics: []string{"test_metric", "test_metric_1"},
				dest:    []Destination{{Label: "dest_label", Value: "dest_value", Action: ActionUpsert}},
				wantErr: false,
			},
			{
				name:    "invalid config - missing metric name",
				rules:   Rules{{Label: "label1", Op: OperatorIn, Values: []interface{}{"value1", "value2"}}},
				dest:    []Destination{{Label: "dest_label", Value: "dest_value", Action: ActionUpsert}},
				wantErr: true,
			},
			{
				name:    "invalid config - missing destinations",
				metrics: []string{"test_metric"},
				rules:   Rules{{Label: "label1", Op: OperatorIn, Values: []interface{}{"value1", "value2"}}},
				wantErr: true,
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				c := Config{
					Relabel: []RelabelAction{{
						Metrics:      tt.metrics,
						Rules:        tt.rules,
						Destinations: tt.dest,
					}},
				}
				assert.Equal(t, tt.wantErr, c.Validate() != nil)
			})
		}
	})
}

func TestRelabelRuleMatch(t *testing.T) {
	t.Run("in operator", func(t *testing.T) {
		rule := Rule{
			Label:  "env",
			Op:     "in",
			Values: []interface{}{"prod", "staging"},
		}
		tests := []struct {
			name  string
			rule  Rule
			input string
			want  bool
		}{
			{
				name:  "match",
				rule:  rule,
				input: "prod",
				want:  true,
			},
			{
				name:  "no match",
				rule:  rule,
				input: "dev",
				want:  false,
			},
		}

		for _, tt := range tests {
			assert.NoError(t, tt.rule.Validate())
			t.Run(tt.name, func(t *testing.T) {
				assert.Equal(t, tt.want, tt.rule.Match(tt.input))
			})
		}
	})

	t.Run("range operator", func(t *testing.T) {
		rule := Rule{
			Label:  "code",
			Op:     "range",
			Values: []interface{}{map[string]interface{}{"min": 200, "max": 299}},
		}
		tests := []struct {
			name  string
			rule  Rule
			input string
			want  bool
		}{
			{
				name:  "match",
				rule:  rule,
				input: "204",
				want:  true,
			},
			{
				name:  "no match",
				rule:  rule,
				input: "300",
				want:  false,
			},
		}

		for _, tt := range tests {
			assert.NoError(t, tt.rule.Validate())
			t.Run(tt.name, func(t *testing.T) {
				assert.Equal(t, tt.want, tt.rule.Match(tt.input))
			})
		}
	})

	t.Run("range operator with prefix", func(t *testing.T) {
		rule := Rule{
			Label:  "code",
			Op:     "range",
			Values: []interface{}{map[string]interface{}{"prefix": "ret_", "min": 200, "max": 299}},
		}
		tests := []struct {
			name  string
			rule  Rule
			input string
			want  bool
		}{
			{
				name:  "match",
				rule:  rule,
				input: "ret_204",
				want:  true,
			},
			{
				name:  "value not match",
				rule:  rule,
				input: "ret_300",
				want:  false,
			},
			{
				name:  "prefix not match",
				rule:  rule,
				input: "200",
				want:  false,
			},
		}

		for _, tt := range tests {
			assert.NoError(t, tt.rule.Validate())
			t.Run(tt.name, func(t *testing.T) {
				assert.Equal(t, tt.want, tt.rule.Match(tt.input))
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

func TestRelabelRuleMatchMetricAttrs(t *testing.T) {
	ruleOpIn := Rule{
		Label:  "service",
		Op:     "in",
		Values: []interface{}{"auth-service"},
	}
	ruleOpRange := Rule{
		Label:  "status",
		Op:     "range",
		Values: []interface{}{map[string]interface{}{"min": 0, "max": 200}},
	}

	tests := []struct {
		name  string
		rules *Rules
		attrs pcommon.Map
		want  bool
	}{
		{
			name:  "empty rules not match",
			rules: &Rules{},
			attrs: createTestMap("service", "auth-service"),
			want:  false,
		},
		{
			name:  "single matching rule",
			rules: &Rules{ruleOpIn},
			attrs: createTestMap("service", "auth-service"),
			want:  true,
		},
		{
			name:  "single non-existing label",
			rules: &Rules{ruleOpIn},
			attrs: createTestMap("app", "payment-service"),
			want:  false,
		},
		{
			name:  "multiple rules all match",
			rules: &Rules{ruleOpIn, ruleOpRange},
			attrs: createTestMap("service", "auth-service", "status", "200"),
			want:  true,
		},
		{
			name:  "range rule mismatch",
			rules: &Rules{ruleOpRange},
			attrs: createTestMap("status", "500"),
			want:  false,
		},
		{
			name:  "mixed rules partial match",
			rules: &Rules{ruleOpIn, ruleOpRange},
			attrs: createTestMap("service", "auth-service", "status", "404"),
			want:  false,
		},
	}

	attrsToLabels := func(attrs pcommon.Map) PromLabels {
		labels := make([]prompb.Label, 0)
		attrs.Range(func(k string, v pcommon.Value) bool {
			labels = append(labels, prompb.Label{Name: k, Value: v.AsString()})
			return true
		})
		return labels
	}

	for _, tt := range tests {
		assert.NoError(t, tt.rules.Validate())
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.rules.MatchMetricAttrs(tt.attrs))
		})

		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.rules.MatchRWLabels(attrsToLabels(tt.attrs)))
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
	labels = append(labels,
		prompb.Label{Name: "service", Value: "auth-service"},
		prompb.Label{Name: "env", Value: "prod"},
		prompb.Label{Name: "status", Value: "200"},
	)
	rules := Rules{
		{Label: "service", Op: "in", Values: []interface{}{"auth-service"}},
		{Label: "env", Op: "in", Values: []interface{}{"prod"}},
		{Label: "status", Op: "range", Values: []interface{}{map[string]interface{}{"min": 200, "max": 299, "prefix": "ret_"}}},
	}
	return labels, rules
}

// 直接遍历 labels，时间复杂度 o(n^2), 但是实际 n(rules) 一般比较小，性能更好
func BenchmarkMatchRWLabelsSlice(b *testing.B) {
	labels, rules := makeRWDataAndRule(10)
	for i := 0; i < b.N; i++ {
		rules.MatchRWLabels(labels)
	}
}

func makeLabelMap(labels []prompb.Label) map[string]*prompb.Label {
	m := make(map[string]*prompb.Label, len(labels))
	for i := 0; i < len(labels); i++ {
		if labels[i].GetName() == "__name__" {
			continue
		}
		m[labels[i].GetName()] = &labels[i]
	}
	return m
}

// 遍历一次 labels，构建 map，时间复杂度 o(n)，但申请内存造成的开销远大于遍历
func BenchmarkMatchRWLabelsMap(b *testing.B) {
	lbs, rules := makeRWDataAndRule(10)
	for i := 0; i < b.N; i++ {
		labels := makeLabelMap(lbs)
		for _, rule := range rules {
			if label, ok := labels[rule.Label]; ok {
				rule.Match(label.GetValue())
			}
		}
	}
}

func BenchmarkMetricNamesContains(b *testing.B) {
	const num = 10
	b.Run("iter", func(b *testing.B) {
		var metrics []string
		for i := 0; i < num; i++ {
			metrics = append(metrics, fmt.Sprintf("metric_%d", i))
		}
		contains := func(slice []string, item string) bool {
			for i := 0; i < len(slice); i++ {
				if slice[i] == item {
					return true
				}
			}
			return false
		}
		for i := 0; i < b.N; i++ {
			contains(metrics, fmt.Sprintf("metric_%d", i%(2*num)))
		}
	})

	b.Run("map", func(b *testing.B) {
		metrics := make(map[string]struct{})
		for i := 0; i < num; i++ {
			metrics[fmt.Sprintf("metric_%d", i)] = struct{}{}
		}
		contains := func(item string) bool {
			_, ok := metrics[item]
			return ok
		}
		for i := 0; i < b.N; i++ {
			contains(fmt.Sprintf("metric_%d", i%(2*num)))
		}
	})
}
