// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
)

func expressionJSON(operator, field string, value any) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"expression": "A", "A": map[string]any{"condition": operator, "target_key": field, "target_value": value}})
	return b
}

func TestExpressionLogicAndUnknownFields(t *testing.T) {
	raw := json.RawMessage(`{"expression":"A or B and (C or D)","A":{"condition":"term","target_key":"name","target_value":"a"},"B":{"condition":"term","target_key":"source_id","target_value":"s"},"C":{"condition":"term","target_key":"level","target_value":"fatal"},"D":{"condition":"must_not_term","target_key":"model_id","target_value":"host"}}`)
	compiled, err := CompileExpression(raw, KACFields())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		fields map[string]any
		want   bool
	}{
		{map[string]any{"name": "a"}, true},
		{map[string]any{"name": "b", "source_id": "s", "level": "fatal", "model_id": "host"}, true},
		{map[string]any{"name": "b", "source_id": "s", "level": "warning", "model_id": "host"}, false},
		{map[string]any{"source_id": "s"}, true},
	} {
		result, err := compiled.Match(t.Context(), Fields{Values: tc.fields})
		if err != nil || !result.Evaluated || result.Matched != tc.want || len(result.Conditions) != 4 {
			t.Fatalf("match=%+v err=%v", result, err)
		}
	}
	result, err := compiled.Match(t.Context(), Fields{Values: map[string]any{"name": "a"}, Unavailable: map[string]bool{"model_id": true}})
	if !errors.Is(err, ErrUnavailable) || result.Evaluated || result.Matched {
		t.Fatalf("unknown became positive: %+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := compiled.Match(ctx, Fields{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				result, err := compiled.Match(t.Context(), Fields{Values: map[string]any{"name": "a"}})
				if err != nil || !result.Matched {
					t.Error("shared expression changed")
				}
			}
		})
	}
	wg.Wait()
}

func TestExpressionValidation(t *testing.T) {
	cases := []string{
		`{}`, `null`,
		`{"expression":"A","A":{"condition":"term","target_key":"conductor","target_value":"admin"}}`,
		`{"expression":"A","A":{"condition":"term","target_key":"name"}}`,
		`{"expression":"A","A":{"condition":"term","target_key":"name","target_value":null}}`,
		`{"expression":"A","A":{"condition":"terms","target_key":"name","target_value":"a"}}`,
		`{"expression":"A XOR A","A":{"condition":"term","target_key":"name","target_value":"a"}}`,
		`{"expression":"A and","A":{"condition":"term","target_key":"name","target_value":"a"}}`,
		`{"expression":"(A","A":{"condition":"term","target_key":"name","target_value":"a"}}`,
		`{"expression":"B","A":{"condition":"term","target_key":"name","target_value":"a"}}`,
		`{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"a","target_value":"b"}}`,
		`{"expression":"A","expression":"B","A":{"condition":"term","target_key":"name","target_value":"a"}}`,
		`{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"a","extra":true}}`,
	}
	for _, raw := range cases {
		if _, err := CompileExpression(json.RawMessage(raw), KACFields()); err == nil {
			t.Errorf("invalid config accepted: %s", raw)
		}
	}
	for _, pattern := range []string{`^a$`, `\d+`, `(?i)a`, `a&b`, `~a`, `@`, `#`, `<1-9>`, `[[:digit:]]`, strings.Repeat("a", 257)} {
		if _, err := CompileExpression(expressionJSON("regexp", "name", pattern), KACFields()); err == nil {
			t.Errorf("unsupported regex accepted: %s", pattern)
		}
	}
	raw := strings.Replace(string(expressionJSON("term", "name", "a")), `"A"`, `"`+strings.Repeat("(", 33)+"A"+strings.Repeat(")", 33)+`"`, 1)
	if _, err := CompileExpression(json.RawMessage(raw), KACFields()); err == nil {
		t.Fatal("deep expression accepted")
	}
}

func TestKACTermTermsAndPhraseRemainDistinct(t *testing.T) {
	cases := []struct {
		operator, field string
		query, value    any
		want            bool
	}{
		{"term", "name", "CPU", "CPU", true},
		{"term", "name", "cpu", "CPU", false},
		{"terms", "name", []any{"CPU"}, "CPU", false},
		{"terms", "name", []any{"c"}, "CPU", true},
		{"wildcard", "name", "cpu", "C P\tU", true},
		{"wildcard", "name", "数据库", "数据 库连接失败", true},
		{"wildcard", "source_name", "cpu", "CPU", false},
		{"wildcard", "name", "a*b", "axb", false},
		{"regexp", "name", "a.*b", "a\nb", true},
		{"regexp", "name", "cpu", "xxcpuxx", false},
		{"term", "name", "b", []any{"a", "b"}, true},
		{"wildcard", "name", "ab", []any{"a", "b"}, false},
		{"must_not_term", "name", "a", nil, true},
		{"term", "bk_biz_id", "0", 0, true},
		{"term", "source_id", false, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.operator+"/"+tc.field, func(t *testing.T) {
			expression, err := CompileExpression(expressionJSON(tc.operator, tc.field, tc.query), KACFields())
			if err != nil {
				t.Fatal(err)
			}
			result, err := expression.Match(t.Context(), Fields{Values: map[string]any{tc.field: tc.value}})
			if err != nil || result.Matched != tc.want {
				t.Fatalf("result=%+v error=%v value=%#v query=%#v", result, err, tc.value, tc.query)
			}
		})
	}
}
