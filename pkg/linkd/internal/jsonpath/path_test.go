// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jsonpath

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestNumberFiltersAndSingularTargets(t *testing.T) {
	q, err := Compile(`$.items[?@.n >= 2].n`)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := q.Select(t.Context(), map[string]any{"items": []any{map[string]any{"n": json.Number("1")}, map[string]any{"n": json.Number("2")}}})
	if err != nil || len(nodes) != 1 || nodes[0] != json.Number("2") {
		t.Fatalf("nodes=%v err=%v", nodes, err)
	}
	for _, path := range []string{"$", "$.x[*]", "$..x", "$.a[-1]", "$.a[?@]"} {
		if _, err := ParseTarget(path); err == nil {
			t.Errorf("accepted target %s", path)
		}
	}
	target, err := ParseTarget(`$['labels']['a.b']`)
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{}
	if err := target.Set(value, false); err != nil {
		t.Fatal(err)
	}
	if got, ok := target.Get(value); !ok || got != false {
		t.Fatalf("get=%v,%v", got, ok)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := q.Select(ctx, value); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

func TestRejectResultAmplification(t *testing.T) {
	q, err := Compile(`$['x','x','x','x']['x','x','x','x']['x','x','x','x']['x','x','x','x']['x','x','x','x']['x','x','x','x']['x','x','x','x']['x','x','x','x']`)
	if err != nil {
		t.Fatal(err)
	}
	var value any = "v"
	for range 8 {
		value = map[string]any{"x": value}
	}
	if _, err := q.Select(t.Context(), value); err == nil {
		t.Fatal("amplified selection accepted")
	}
}

func TestRejectUnboundedNestedFilterQueries(t *testing.T) {
	for _, path := range []string{`$.items[?count($.items[*]) > 0]`, `$.items[?@.x[0,0,0]]`, `$.items[?@..x]`, `$.items[?@.x[?@.y]]`} {
		if _, err := Compile(path); err == nil {
			t.Errorf("accepted nested collection query %s", path)
		}
	}
	if _, err := Compile(`$.items[?match(@.name, 'a.*')]`); err != nil {
		t.Fatalf("quoted regex is bounded: %v", err)
	}
}
