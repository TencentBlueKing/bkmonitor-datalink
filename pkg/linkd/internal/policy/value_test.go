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
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestGroupValueIdentity(t *testing.T) {
	same := [][]any{{1, json.Number("1.0"), json.Number("1e0"), float64(1)}, {0, json.Number("-0"), math.Copysign(0, -1)}, {map[string]any{"b": false, "a": 0}, map[string]any{"a": json.Number("0.0"), "b": false}}}
	for _, group := range same {
		want := ""
		for _, v := range group {
			key, err := GroupKey([]any{v})
			if err != nil {
				t.Fatal(err)
			}
			if want == "" {
				want = key
			}
			if key != want {
				t.Fatalf("numeric/object canonicalization differs for %#v", v)
			}
		}
	}
	unique := []any{0, false, "0", "false", []any{"a", "b"}, []any{"b", "a"}, []any{"a", "a"}, " x ", "x", json.Number("9007199254740993"), json.Number("9007199254740992")}
	seen := map[string]bool{}
	for _, v := range unique {
		key, err := GroupKey([]any{v})
		if err != nil {
			t.Fatal(err)
		}
		if seen[key] {
			t.Fatalf("type or value collision: %#v", v)
		}
		seen[key] = true
	}
	first, _ := GroupKey([]any{"ab", "c"})
	second, _ := GroupKey([]any{"a", "bc"})
	if first == second {
		t.Fatal("field concatenation collision")
	}
	if _, err := GroupKey(nil); err != nil {
		t.Fatal("empty merge aggregate_fields must produce one group")
	}
}

func TestGroupValueRejectsIncompleteAndOversizedValues(t *testing.T) {
	values := []any{nil, "", " \t\u3000", []any{}, map[string]any{}, []any{"ok", nil}, map[string]any{"x": ""}, math.Inf(1), math.NaN(), json.Number("1e999999"), json.Number("1/2"), json.Number("true"), strings.Repeat("x", 65537), struct{}{}, json.RawMessage(`{"x":1,"x":2}`)}
	for _, value := range values {
		if _, err := GroupKey([]any{value}); err == nil {
			t.Errorf("invalid group accepted: %#v", value)
		}
	}
	var deep any = "x"
	for range 18 {
		deep = []any{deep}
	}
	if _, err := GroupKey([]any{deep}); err == nil {
		t.Fatal("nesting budget ignored")
	}
}
