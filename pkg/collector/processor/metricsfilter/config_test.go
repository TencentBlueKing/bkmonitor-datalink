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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/promlabels"
)

func TestRelabelRuleValidate(t *testing.T) {
	tests := []struct {
		name    string
		rule    RelabelRule
		wantErr bool
	}{
		{
			name:    "valid in operator",
			rule:    RelabelRule{Op: OpIn, Values: []any{"value1", "value2"}},
			wantErr: false,
		},
		{
			name:    "valid range operator",
			rule:    RelabelRule{Op: OpRange, Values: []any{map[string]any{"min": 10, "max": 20}}},
			wantErr: false,
		},
		{
			name:    "invalid range operator with non-map value",
			rule:    RelabelRule{Op: OpRange, Values: []any{"invalid_map"}},
			wantErr: true,
		},
		{
			name:    "default range value decode",
			rule:    RelabelRule{Op: OpRange, Values: []any{map[string]any{"max": 20}}},
			wantErr: false,
		},
		{
			name:    "unsupported operator",
			rule:    RelabelRule{Op: "invalid_operator"},
			wantErr: true,
		},
		{
			name:    "empty values for in operator",
			rule:    RelabelRule{Op: OpIn, Values: []any{}},
			wantErr: false,
		},
		{
			name:    "empty values for range operator",
			rule:    RelabelRule{Op: OpRange, Values: []any{}},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.wantErr, tt.rule.Validate() != nil)
		})
	}
}

func TestRelabelConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		metrics []string
		rules   RelabelRules
		target  RelabelTarget
		wantErr bool
	}{
		{
			name:    "valid config",
			metrics: []string{"test_metric"},
			rules:   RelabelRules{Rules: []*RelabelRule{{Label: "label1", Op: OpIn, Values: []any{"value1", "value2"}}}},
			target:  RelabelTarget{Label: "target_label", Value: "foo", Action: relabelUpsert},
			wantErr: false,
		},
		{
			name:    "valid config - multiple metrics",
			metrics: []string{"test_metric", "test_metric_1"},
			target:  RelabelTarget{Label: "target_label", Value: "foo", Action: relabelUpsert},
			wantErr: false,
		},
		{
			name:    "invalid config - missing metric name",
			rules:   RelabelRules{Rules: []*RelabelRule{{Label: "label1", Op: OpIn, Values: []any{"value1", "value2"}}}},
			target:  RelabelTarget{Label: "target_label", Value: "foo", Action: relabelUpsert},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{
				Relabel: []RelabelAction{{
					Metrics: tt.metrics,
					Rules:   tt.rules.Rules,
					Target:  tt.target,
				}},
			}
			assert.Equal(t, tt.wantErr, c.Validate() != nil)
		})
	}
}

func TestCodeRelabelConfigValidate(t *testing.T) {
	tests := []struct {
		name     string
		metrics  []string
		source   string
		services []*CodeRelabelService
		targets  RelabelTarget
		wantErr  bool
	}{
		{
			name:    "valid config",
			metrics: []string{"test_metric"},
			source:  "test.service",
			services: []*CodeRelabelService{{
				Name: "my.server;my.service;my.method",
				Codes: []*CodeRelabelCode{
					{Rule: "err_200~300"},
					{Rule: "err_200"},
					{Rule: "200"},
					{Rule: "200~300"},
					{Rule: "100,200,300"},
				},
			}},
			targets: RelabelTarget{Label: "target_label", Value: "foo", Action: relabelUpsert},
			wantErr: false,
		},
		{
			name:    "invalid no metrics",
			metrics: []string{},
			source:  "test.service",
			wantErr: true,
		},
		{
			name:    "invalid no service",
			metrics: []string{"test_metric"},
			source:  "test.service",
			wantErr: true,
		},
		{
			name:    "invalid service name",
			metrics: []string{"test_metric"},
			source:  "test.service",
			services: []*CodeRelabelService{{
				Name: "my.server;my.service",
				Codes: []*CodeRelabelCode{{
					Rule: "err_200~300",
				}},
			}},
			wantErr: true,
		},
		{
			name:    "invalid rule",
			metrics: []string{"test_metric"},
			source:  "test.service",
			services: []*CodeRelabelService{{
				Name: "my.server;my.service;my.method",
				Codes: []*CodeRelabelCode{{
					Rule: "err_200~!300",
				}},
			}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := Config{
				CodeRelabel: []CodeRelabelAction{{
					Metrics:  tt.metrics,
					Source:   tt.source,
					Services: tt.services,
				}},
			}
			assert.Equal(t, tt.wantErr, c.Validate() != nil)
		})
	}
}

func TestRelabelRuleMatch(t *testing.T) {
	t.Run("opIn", func(t *testing.T) {
		rule := RelabelRule{
			Label:  "env",
			Op:     OpIn,
			Values: []any{"prod", "staging"},
		}
		tests := []struct {
			name  string
			rule  RelabelRule
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

	t.Run("opRange", func(t *testing.T) {
		rule := RelabelRule{
			Label:  "code",
			Op:     OpRange,
			Values: []any{map[string]any{"min": 200, "max": 299}},
		}
		tests := []struct {
			name  string
			rule  RelabelRule
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

	t.Run("opRange with prefix", func(t *testing.T) {
		rule := RelabelRule{
			Label:  "code",
			Op:     OpRange,
			Values: []any{map[string]any{"prefix": "ret_", "min": 200, "max": 299}},
		}
		tests := []struct {
			name  string
			rule  RelabelRule
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

func TestRelabelActionRuleMatch(t *testing.T) {
	ruleOpIn := &RelabelRule{
		Label:  "service",
		Op:     "in",
		Values: []any{"auth-service"},
	}
	ruleOpRange := &RelabelRule{
		Label:  "status",
		Op:     "range",
		Values: []any{map[string]any{"min": 0, "max": 200}},
	}

	tests := []struct {
		name  string
		rules RelabelRules
		attrs pcommon.Map
		want  bool
	}{
		{
			name:  "empty rules not match",
			rules: RelabelRules{},
			attrs: createTestMap("service", "auth-service"),
			want:  false,
		},
		{
			name:  "single matching rule",
			rules: RelabelRules{Rules: []*RelabelRule{ruleOpIn}},
			attrs: createTestMap("service", "auth-service"),
			want:  true,
		},
		{
			name:  "single non-existing label",
			rules: RelabelRules{Rules: []*RelabelRule{ruleOpIn}},
			attrs: createTestMap("app", "payment-service"),
			want:  false,
		},
		{
			name:  "multiple rules all match",
			rules: RelabelRules{Rules: []*RelabelRule{ruleOpIn, ruleOpRange}},
			attrs: createTestMap("service", "auth-service", "status", "200"),
			want:  true,
		},
		{
			name:  "range rule mismatch",
			rules: RelabelRules{Rules: []*RelabelRule{ruleOpRange}},
			attrs: createTestMap("status", "500"),
			want:  false,
		},
		{
			name:  "mixed rules partial match",
			rules: RelabelRules{Rules: []*RelabelRule{ruleOpIn, ruleOpRange}},
			attrs: createTestMap("service", "auth-service", "status", "404"),
			want:  false,
		},
	}

	attrsToLabels := func(attrs pcommon.Map) promlabels.Labels {
		labels := make([]prompb.Label, 0)
		attrs.Range(func(k string, v pcommon.Value) bool {
			labels = append(labels, prompb.Label{Name: k, Value: v.AsString()})
			return true
		})
		return labels
	}

	for _, tt := range tests {
		assert.NoError(t, tt.rules.Validate())
		t.Run("OT:"+tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.rules.MatchMap(tt.attrs))
		})

		t.Run("RW:"+tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.rules.MatchLabels(attrsToLabels(tt.attrs)))
		})
	}
}

func makeRWDataAndRule(numExtraLabel int) ([]prompb.Label, RelabelRules) {
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
	rules := RelabelRules{
		Rules: []*RelabelRule{
			{Label: "service", Op: "in", Values: []any{"auth-service"}},
			{Label: "env", Op: "in", Values: []any{"prod"}},
			{Label: "status", Op: "range", Values: []any{map[string]any{"min": 200, "max": 299, "prefix": "ret_"}}},
		},
	}
	return labels, rules
}

func BenchmarkMatchRWLabelsSlice(b *testing.B) {
	labels, rules := makeRWDataAndRule(10)
	for i := 0; i < b.N; i++ {
		rules.MatchLabels(labels)
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

func BenchmarkMatchRWLabelsMap(b *testing.B) {
	lbs, rules := makeRWDataAndRule(10)
	for i := 0; i < b.N; i++ {
		labels := makeLabelMap(lbs)
		for _, rule := range rules.Rules {
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
