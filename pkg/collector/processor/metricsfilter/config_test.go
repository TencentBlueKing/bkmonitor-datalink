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
	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"testing"
)

func TestRuleMatch(t *testing.T) {
	ruleIn := Rule{
		Label:  "callee_method",
		Op:     "in",
		Values: []interface{}{"hello"},
	}
	_ = ruleIn.Validate()
	t.Run("hit in", func(t *testing.T) {
		assert.True(t, ruleIn.Match("hello"))
		assert.False(t, ruleIn.Match("world"))
	})

}

func TestConfigValidate(t *testing.T) {
	t.Run("valid config", func(t *testing.T) {
		c := Config{
			Relabel: []RelabelAction{
				{
					Metric: "test_metric",
					Rules: Rules{
						{
							Label:  "label1",
							Op:     OperatorIn,
							Values: []interface{}{"value1", "value2"},
						},
						{
							Label:  "label2",
							Op:     OperatorRange,
							Values: []interface{}{map[string]interface{}{"min": 10, "max": 20}},
						},
					},
					Destinations: []Destination{
						{
							Label: "dest_label",
							Value: "dest_value",
						},
					},
				},
			},
		}

		assert.NoError(t, c.Validate())
		assert.Len(t, c.Relabel[0].Rules[0].InValues, 2)
		assert.Len(t, c.Relabel[0].Rules[1].RangeValues, 1)
	})
	t.Run("invalid config - missing metric name", func(t *testing.T) {
		c := Config{
			Relabel: []RelabelAction{
				{
					Rules: Rules{
						{
							Label:  "label1",
							Op:     OperatorIn,
							Values: []interface{}{"value1", "value2"},
						},
						{
							Label:  "label2",
							Op:     OperatorRange,
							Values: []interface{}{map[string]interface{}{"min": 10, "max": 20}},
						},
					},
					Destinations: []Destination{
						{
							Label: "dest_label",
							Value: "dest_value",
						},
					},
				},
			},
		}

		assert.Error(t, c.Validate())
	})
	t.Run("invalid config - invalid rules", func(t *testing.T) {
		c := Config{
			Relabel: []RelabelAction{
				{
					Metric: "test_metric",
					Rules: Rules{
						{
							Label:  "label1",
							Op:     OperatorIn,
							Values: []interface{}{map[string]interface{}{"min": 10, "max": 20}},
						},
					},
					Destinations: []Destination{
						{
							Label: "dest_label",
							Value: "dest_value",
						},
					},
				},
			},
		}
		assert.Error(t, c.Validate())
	})
	t.Run("invalid config - invalid destinations", func(t *testing.T) {
		c := Config{
			Relabel: []RelabelAction{
				{
					Metric: "test_metric",
					Rules: Rules{
						{
							Label:  "label1",
							Op:     OperatorIn,
							Values: []interface{}{"value1", "value2"},
						},
					},
					Destinations: []Destination{
						{
							Value: "dest_value",
						},
					},
				},
			},
		}
		assert.Error(t, c.Validate())
	})
	t.Run("invalid config - min>max", func(t *testing.T) {
		c := Config{
			Relabel: []RelabelAction{
				{
					Metric: "test_metric",
					Rules: Rules{
						{
							Label:  "label1",
							Op:     OperatorIn,
							Values: []interface{}{"value1", "value2"},
						},
						{
							Label:  "label2",
							Op:     OperatorRange,
							Values: []interface{}{map[string]interface{}{"min": 20, "max": 10}},
						},
					},
					Destinations: []Destination{
						{
							Label: "dest_label",
							Value: "dest_value",
						},
					},
				},
			},
		}

		assert.Error(t, c.Validate())
	})

	t.Run("invalid destination", func(t *testing.T) {
		c := Config{
			Relabel: []RelabelAction{
				{
					Metric: "test_metric",
					Rules: Rules{
						{
							Label:  "label1",
							Op:     "in",
							Values: []interface{}{"value1"},
						},
					},
					Destinations: []Destination{
						{Value: "dest_value"},
					},
				},
			},
		}
		assert.Error(t, c.Validate())
	})
}

// 由CodeBuddy（内网版） Deepseek R1生成于2025.08.06 16:19:34
// createTestMap 辅助函数用于快速创建测试用属性集合
// 由CodeBuddy（内网版） Deepseek R1生成于2025.08.06 16:19:35

// 定义通用测试规则

// 测试规则验证逻辑
// 由CodeBuddy（内网版） Deepseek R1生成于2025.08.06 16:19:35

func TestRule_Validate(t *testing.T) {
	type fields struct {
		Label       string
		Op          Operator
		Values      []interface{}
		InValues    []string
		RangeValues []RangeValue
	}

	tests := []struct {
		name    string
		fields  fields
		wantErr bool
	}{
		{
			name: "valid in operator",
			fields: fields{
				Op:     OperatorIn,
				Values: []interface{}{"value1", "value2"},
			},
			wantErr: false,
		},
		{
			name: "invalid in operator with non-string value",
			fields: fields{
				Op:     OperatorIn,
				Values: []interface{}{123},
			},
			wantErr: true,
		},
		{
			name: "valid range operator",
			fields: fields{
				Op: OperatorRange,
				Values: []interface{}{
					map[string]interface{}{"min": 10.0, "max": 20.0},
					map[string]interface{}{"min": 30.5, "max": 40.5},
				},
			},
			wantErr: false,
		},
		{
			name: "invalid range operator with non-map value",
			fields: fields{
				Op:     OperatorRange,
				Values: []interface{}{"invalid_map"},
			},
			wantErr: true,
		},
		{
			name: "invalid range value decode",
			fields: fields{
				Op: OperatorRange,
				Values: []interface{}{
					map[string]interface{}{"max": 20.0},
				},
			},
			wantErr: true,
		},
		{
			name: "unsupported operator",
			fields: fields{
				Op: "invalid_operator",
			},
			wantErr: true,
		},
		{
			name: "empty values for in operator",
			fields: fields{
				Op:     OperatorIn,
				Values: []interface{}{},
			},
			wantErr: false,
		},
		{
			name: "empty values for range operator",
			fields: fields{
				Op:     OperatorRange,
				Values: []interface{}{},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &Rule{
				Label:  tt.fields.Label,
				Op:     tt.fields.Op,
				Values: tt.fields.Values,
			}

			err := r.Validate()
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)

				switch r.Op {
				case OperatorIn, OperatorNotIn:
					assert.Len(t, r.InValues, len(tt.fields.Values))
				case OperatorRange:
					assert.Len(t, r.RangeValues, len(tt.fields.Values))
				}
			}
		})
	}
}

func createTestMap(pairs ...string) pcommon.Map {
	m := pcommon.NewMap()
	for i := 0; i < len(pairs); i += 2 {
		m.UpsertString(pairs[i], pairs[i+1])
	}
	return m
}

func TestRules_MatchMetricAttrs(t *testing.T) {

	ruleInMatch := &Rule{Label: "service", Op: "in", Values: []interface{}{"auth-service"}}
	ruleRangeMatch := &Rule{Label: "status", Op: "range", Values: []interface{}{map[string]interface{}{"min": 0, "max": 200}}}

	tests := []struct {
		name  string
		rs    *Rules
		attrs pcommon.Map
		want  bool
	}{
		{
			name:  "empty rules should match",
			rs:    &Rules{},
			attrs: createTestMap("service", "auth-service"),
			want:  true,
		},
		{
			name:  "single matching rule",
			rs:    &Rules{ruleInMatch},
			attrs: createTestMap("service", "auth-service"),
			want:  true,
		},
		{
			name:  "single non-existing label",
			rs:    &Rules{ruleInMatch},
			attrs: createTestMap("app", "payment-service"),
			want:  false,
		},
		{
			name:  "multiple rules all match",
			rs:    &Rules{ruleInMatch, ruleRangeMatch},
			attrs: createTestMap("service", "auth-service", "status", "200"),
			want:  true,
		},
		{
			name:  "range rule mismatch",
			rs:    &Rules{ruleRangeMatch},
			attrs: createTestMap("status", "500"),
			want:  false,
		},
		{
			name:  "mixed rules partial match",
			rs:    &Rules{ruleInMatch, ruleRangeMatch},
			attrs: createTestMap("service", "auth-service", "status", "404"),
			want:  false,
		},
		{
			name:  "equal boundary check",
			rs:    &Rules{{Label: "count", Op: "range", Values: []interface{}{map[string]interface{}{"min": 0, "max": 200}}}},
			attrs: createTestMap("count", "0"),
			want:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.rs.Validate()
			assert.NoError(t, err)
			got := tt.rs.MatchMetricAttrs(tt.attrs)
			assert.Equal(t, tt.want, got, "Test case [%s] failed", tt.name)
		})
	}
}

func TestRule_Match(t *testing.T) {
	t.Run("in operator match", func(t *testing.T) {
		r := Rule{Label: "env", Op: "in", Values: []interface{}{"prod", "staging"}}
		_ = r.Validate()
		assert.True(t, r.Match("prod"))
		assert.False(t, r.Match("dev"))
	})

	t.Run("range operator match", func(t *testing.T) {
		r := Rule{Label: "code", Op: "range", Values: []interface{}{map[string]interface{}{"min": 200, "max": 299}}}
		_ = r.Validate()
		assert.True(t, r.Match("204"))
		assert.False(t, r.Match("300"))
	})

	t.Run("range operator prefix match", func(t *testing.T) {
		r := Rule{Label: "code", Op: "range", Values: []interface{}{map[string]interface{}{"prefix": "ret_", "min": 200, "max": 299}}}
		_ = r.Validate()
		assert.True(t, r.Match("ret_204"))
		assert.False(t, r.Match("ret_300"))
		assert.False(t, r.Match("200"))
	})
}

func TestRules_MatchRWLabels(t *testing.T) {
	type args struct {
		labels map[string]*prompb.Label
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
				labels: map[string]*prompb.Label{
					"service": {Value: "auth-service"},
					"status":  {Value: "200"},
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
				labels: map[string]*prompb.Label{
					"status": {Value: "500"},
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
				labels: map[string]*prompb.Label{
					"status": {Value: "503"},
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
				labels: map[string]*prompb.Label{
					"status": {Value: "503"},
				},
			},
			want: false,
		},
		{
			name: "empty rules should always match",
			rs:   &Rules{},
			args: args{
				labels: map[string]*prompb.Label{
					"service": {Value: "any-service"},
				},
			},
			want: true,
		},
		{
			name: "empty labels with rules",
			rs: &Rules{
				{Label: "service", Op: "in", Values: []interface{}{"auth-service"}},
			},
			args: args{
				labels: map[string]*prompb.Label{},
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
				labels: map[string]*prompb.Label{
					"service": {Value: "auth-service"},
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
