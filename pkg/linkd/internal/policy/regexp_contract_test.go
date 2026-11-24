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
	"strings"
	"testing"
)

func TestAllPolicyKindsUseRegexp2WithoutConfiguration(t *testing.T) {
	for _, kind := range []Kind{Suppression, Shield, Merge} {
		t.Run(string(kind), func(t *testing.T) {
			spec := policySpecMap(t, kind)
			rule := expressionJSON("regexp", "name", `(?<=prefix)(?<word>主机)\k<word>`)
			if kind == Merge {
				spec["policy"] = []any{rule}
			} else {
				spec["policy"] = rule
			}
			compiled, err := Compile(kind, encodeSpec(t, spec))
			if err != nil {
				t.Fatal(err)
			}
			result, err := compiled.Conditions[0].Match(t.Context(), Fields{Values: map[string]any{"name": "prefix主机主机 suffix"}})
			if err != nil || !result.Matched {
				t.Fatalf("default regexp2 match: %+v %v", result, err)
			}
			for _, engine := range []string{"regexp2", "re2", ""} {
				spec["regex_engine"] = engine
				if _, err := Compile(kind, encodeSpec(t, spec)); err == nil {
					t.Fatalf("engine selection accepted: %q", engine)
				}
			}
		})
	}
}

func TestRegexp2SearchAnchorsAndInputBoundary(t *testing.T) {
	for _, tc := range []struct {
		pattern, text string
		want          bool
	}{
		{`cpu`, "prefix cpu suffix", true},
		{`\Acpu\z`, "prefix cpu suffix", false},
		{`\Acpu\z`, "cpu", true},
		{`a.*b`, "a\nb", false},
		{`(?s)a.*b`, "a\nb", true},
		{`x$`, strings.Repeat("x", 8192), true},
	} {
		expr, err := CompileExpression(expressionJSON("regexp", "name", tc.pattern), KACFields())
		if err != nil {
			t.Fatal(err)
		}
		result, err := expr.Match(t.Context(), Fields{Values: map[string]any{"name": tc.text}})
		if err != nil || result.Matched != tc.want {
			t.Fatalf("pattern=%q input length=%d: %+v %v", tc.pattern, len(tc.text), result, err)
		}
	}
}
