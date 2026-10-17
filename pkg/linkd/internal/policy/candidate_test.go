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
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strconv"
	"sync"
	"testing"
)

func candidateExpression(t testing.TB, raw json.RawMessage, fields FieldCatalog) *Expression {
	t.Helper()
	e, err := CompileExpression(raw, fields)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func candidateRule(id string, expressions ...*Expression) FrozenPolicy {
	rule := FrozenPolicy{Release: Release{Scope: Scope{TenantID: "tenant", Kind: Suppression}, ID: id, Version: 1}, Compiled: &Compiled{Conditions: expressions}}
	rule.Compiled.Common.Enabled = true
	return rule
}

func TestDisabledFallbackRulesDoNotReadEventFields(t *testing.T) {
	rules := make([]FrozenPolicy, 10000)
	for i := range rules {
		rules[i] = candidateRule(strconv.Itoa(i))
		rules[i].Compiled.Common.Enabled = false
	}
	index := newCandidateIndex(rules)
	got, err := index.selectPolicies(t.Context(), func(context.Context, string, FieldMapping) (Value, error) {
		t.Fatal("disabled rules read event fields")
		return Value{}, nil
	})
	if err != nil || len(got) != 0 {
		t.Fatalf("candidates=%d error=%v", len(got), err)
	}
}

func candidateFields(fields Fields) CandidateReader {
	return func(ctx context.Context, field string, _ FieldMapping) (Value, error) {
		return fields.Field(ctx, field)
	}
}

func candidateIDs(items []FrozenPolicy) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.Release.ID)
	}
	return ids
}

func TestCandidateBooleanCoverAndFallback(t *testing.T) {
	fields := KACFields()
	a := `"A":{"condition":"term","target_key":"source_id","target_value":"a"}`
	b := `"B":{"condition":"term","target_key":"level","target_value":"warning"}`
	r := `"R":{"condition":"regexp","target_key":"name","target_value":"cpu.*"}`
	tests := []struct {
		name, expression, clauses string
		facts                     map[string]any
		want                      bool
	}{
		{"and rejects unrelated", "A AND B", a + "," + b, map[string]any{"source_id": "other", "level": "warning"}, false},
		{"and retains necessary anchor", "A AND B", a + "," + b, map[string]any{"source_id": "a", "level": "other"}, true},
		{"or second branch", "A OR B", a + "," + b, map[string]any{"source_id": "other", "level": "warning"}, true},
		{"or neither branch", "A OR B", a + "," + b, map[string]any{"source_id": "other", "level": "other"}, false},
		{"regex or falls back", "A OR R", a + "," + r, map[string]any{"source_id": "other", "name": "cpu high"}, true},
		{"regex and uses equality", "A AND R", a + "," + r, map[string]any{"source_id": "other", "name": "cpu high"}, false},
		{"nested and", "A AND (B OR R)", a + "," + b + "," + r, map[string]any{"source_id": "other"}, false},
		{"nested union", "(A AND R) OR B", a + "," + b + "," + r, map[string]any{"source_id": "other", "level": "warning"}, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := candidateExpression(t, json.RawMessage(fmt.Sprintf(`{"expression":%q,%s}`, test.expression, test.clauses)), fields)
			got, err := newCandidateIndex([]FrozenPolicy{candidateRule("p", e)}).selectPolicies(t.Context(), candidateFields(Fields{Values: test.facts}))
			if err != nil || (len(got) > 0) != test.want {
				t.Fatalf("candidates=%v err=%v", candidateIDs(got), err)
			}
		})
	}
	for _, operator := range []string{"must_not_term", "must_not_terms", "must_not_wildcard", "must_not_regexp", "regexp", "wildcard"} {
		var value any = "a"
		if operator == "must_not_terms" {
			value = []string{"a"}
		}
		e := candidateExpression(t, expressionJSON(operator, "source_id", value), fields)
		got, err := newCandidateIndex([]FrozenPolicy{candidateRule(operator, e)}).selectPolicies(t.Context(), candidateFields(Fields{}))
		if err != nil || len(got) != 1 {
			t.Fatalf("fallback %s: %v %v", operator, got, err)
		}
	}
}

func TestCandidateScalarSemanticsAndUnknowns(t *testing.T) {
	fields := KACFields()
	fields["flag"] = FieldBoolean
	tests := []struct {
		name, field, op string
		query, value    any
		present         bool
		want            bool
	}{
		{"number canonical", "bk_biz_id", "term", "2.0", json.Number("2e0"), true, true},
		{"number array", "bk_biz_id", "terms", []string{"2", "3"}, []any{1, 3}, true, true},
		{"boolean", "flag", "term", false, "false", true, true},
		{"keyword case sensitive", "source_id", "term", "A", "a", true, false},
		{"keyword array", "source_id", "terms", []string{"a", "b"}, []any{"z", []any{"b"}}, true, true},
		{"missing", "source_id", "term", "a", nil, false, false},
		{"null", "source_id", "term", "a", nil, true, false},
		{"empty array", "source_id", "term", "a", []any{}, true, false},
		{"text term exact", "name", "term", "CPU", "CPU", true, true},
		{"text terms fallback", "name", "terms", []string{"c"}, "CPU", true, true},
		{"date canonical", "alarm_time", "term", "2026-01-01T00:00:00+08:00", "2025-12-31T16:00:00Z", true, true},
		{"invalid number conservative", "bk_biz_id", "term", 2, "bad", true, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			e := candidateExpression(t, expressionJSON(test.op, test.field, test.query), fields)
			got, err := newCandidateIndex([]FrozenPolicy{candidateRule("p", e)}).selectPolicies(t.Context(), func(context.Context, string, FieldMapping) (Value, error) {
				return Value{Data: test.value, Present: test.present}, nil
			})
			if err != nil || (len(got) > 0) != test.want {
				t.Fatalf("candidates=%v err=%v", got, err)
			}
		})
	}
	e := candidateExpression(t, expressionJSON("term", "source_id", "a"), fields)
	index := newCandidateIndex([]FrozenPolicy{candidateRule("p", e)})
	got, err := index.selectPolicies(t.Context(), func(context.Context, string, FieldMapping) (Value, error) { return Value{}, ErrUnavailable })
	if err != nil || len(got) != 1 {
		t.Fatal("unknown lost candidate", got, err)
	}
	if _, err = index.selectPolicies(t.Context(), func(context.Context, string, FieldMapping) (Value, error) { return Value{}, ErrAccess }); !errors.Is(err, ErrAccess) {
		t.Fatal("access swallowed", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = index.selectPolicies(ctx, candidateFields(Fields{})); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCandidateMappingsGroupsAndDependencyRoles(t *testing.T) {
	fields := FieldCatalog{"owner": FieldKeyword}
	expression := candidateExpression(t, expressionJSON("term", "owner", "alice"), fields)
	first, second := candidateRule("first", expression), candidateRule("second", expression)
	first.Compiled.Common.FieldMappings = map[string]FieldMapping{"owner": {Path: "$.labels.first", Kind: FieldKeyword}}
	second.Compiled.Common.FieldMappings = map[string]FieldMapping{"owner": {Path: "$.extra_data.second", Kind: FieldKeyword}}
	calls := map[string]int{}
	got, err := newCandidateIndex([]FrozenPolicy{first, second}).selectPolicies(t.Context(), func(_ context.Context, field string, mapping FieldMapping) (Value, error) {
		if field != "owner" || mapping.Kind != FieldKeyword {
			t.Fatal("mapping semantic lost")
		}
		calls[mapping.Path]++
		return Value{Present: true, Data: map[string]string{"$.labels.first": "bob", "$.extra_data.second": "alice"}[mapping.Path]}, nil
	})
	if err != nil || !reflect.DeepEqual(candidateIDs(got), []string{"second"}) || len(calls) != 2 {
		t.Fatal(candidateIDs(got), calls, err)
	}
	main := candidateExpression(t, expressionJSON("term", "source_id", "main"), KACFields())
	child := candidateExpression(t, expressionJSON("term", "source_id", "child"), KACFields())
	dependency := candidateRule("dependency", main)
	dependency.Compiled.Rely = child
	grouped := candidateRule("groups", main, child)
	for _, source := range []string{"main", "child"} {
		got, err = newCandidateIndex([]FrozenPolicy{dependency, grouped}).selectPolicies(t.Context(), candidateFields(Fields{Values: map[string]any{"source_id": source}}))
		if err != nil || len(got) != 2 {
			t.Fatal("role/group lost", source, got, err)
		}
	}
	reference, err := compileExpression(json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"source_id","target_value":"${source_id}","is_alarm_field_referenced":true}}`), KACFields(), true)
	if err != nil {
		t.Fatal(err)
	}
	dependency.Compiled.Rely = reference
	got, err = newCandidateIndex([]FrozenPolicy{dependency}).selectPolicies(t.Context(), candidateFields(Fields{Values: map[string]any{"source_id": "unrelated"}}))
	if err != nil || len(got) != 1 {
		t.Fatal("reference lost", got, err)
	}
	relation := candidateExpression(t, json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"model_id","target_value":"cw-Host","bk_obj_asst_id":"host_belongs"}}`), KACFields())
	got, err = newCandidateIndex([]FrozenPolicy{candidateRule("related", relation)}).selectPolicies(t.Context(), candidateFields(Fields{}))
	if err != nil || len(got) != 1 {
		t.Fatal("relation lost", got, err)
	}
}

func TestCandidateRandomizedMatchesFullEvaluation(t *testing.T) {
	random := rand.New(rand.NewPCG(42, 731)) //nolint:gosec // 固定种子的匹配对照数据，不用于安全身份。
	policies := make([]FrozenPolicy, 300)
	for i := range policies {
		clauses := map[string]any{}
		used := 0
		var tree func(int) string
		tree = func(depth int) string {
			if depth > 0 && random.IntN(3) != 0 {
				left, right := tree(depth-1), tree(depth-1)
				op := []string{"AND", "OR"}[random.IntN(2)]
				return "(" + left + " " + op + " " + right + ")"
			}
			id := fmt.Sprintf("A%d", used)
			used++
			field, operator := "source_id", "term"
			var value any = fmt.Sprintf("s%d", random.IntN(12))
			switch random.IntN(6) {
			case 0:
				operator = "terms"
				value = []string{fmt.Sprintf("s%d", random.IntN(12)), fmt.Sprintf("s%d", random.IntN(12))}
			case 1:
				operator = "must_not_term"
			case 2:
				field = "bk_biz_id"
				value = random.IntN(4)
			case 3:
				field = "name"
				operator = "regexp"
				value = "cpu.*"
			}
			clauses[id] = map[string]any{"condition": operator, "target_key": field, "target_value": value}
			return id
		}
		expression := tree(3)
		clauses["expression"] = expression
		raw, _ := json.Marshal(clauses)
		policies[i] = candidateRule(strconv.Itoa(i), candidateExpression(t, raw, KACFields()))
	}
	index := newCandidateIndex(policies)
	for trial := range 150 {
		facts := Fields{Values: map[string]any{"source_id": fmt.Sprintf("s%d", random.IntN(15)), "bk_biz_id": random.IntN(5), "name": []string{"cpu high", "memory"}[random.IntN(2)]}}
		selected, err := index.selectPolicies(t.Context(), candidateFields(facts))
		if err != nil {
			t.Fatal(err)
		}
		actual := []string{}
		expected := []string{}
		for _, item := range policies {
			result, err := item.Compiled.Conditions[0].Match(t.Context(), facts)
			if err != nil {
				t.Fatal(err)
			}
			if result.Matched {
				expected = append(expected, item.Release.ID)
			}
		}
		for _, item := range selected {
			result, err := item.Compiled.Conditions[0].Match(t.Context(), facts)
			if err != nil {
				t.Fatal(err)
			}
			if result.Matched {
				actual = append(actual, item.Release.ID)
			}
		}
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("trial %d lost/order changed: got %v expected %v", trial, actual, expected)
		}
	}
}

func candidateScaleIndex(t testing.TB, count int) *candidateIndex {
	t.Helper()
	policies := make([]FrozenPolicy, count)
	for i := range policies {
		policies[i] = candidateRule(strconv.Itoa(i), candidateExpression(t, expressionJSON("term", "source_id", strconv.Itoa(i)), KACFields()))
	}
	return newCandidateIndex(policies)
}

func TestCandidateTenThousandRulesReadOneFieldAndConcurrent(t *testing.T) {
	index := candidateScaleIndex(t, 10000)
	var wg sync.WaitGroup
	for worker := range 12 {
		wg.Go(func() {
			for i := range 20 {
				id := strconv.Itoa(worker*20 + i)
				calls := 0
				got, err := index.selectPolicies(t.Context(), func(context.Context, string, FieldMapping) (Value, error) {
					calls++
					return Value{Present: true, Data: id}, nil
				})
				if err != nil || calls != 1 || !slices.Equal(candidateIDs(got), []string{id}) {
					t.Errorf("calls %d candidates %v err %v", calls, candidateIDs(got), err)
				}
			}
		})
	}
	wg.Wait()
}

func BenchmarkCandidateSelection(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			index := candidateScaleIndex(b, count)
			reader := candidateFields(Fields{Values: map[string]any{"source_id": "42"}})
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				got, err := index.selectPolicies(b.Context(), reader)
				if err != nil || len(got) != 1 {
					b.Fatal(got, err)
				}
			}
		})
	}
}

func BenchmarkCandidateFullScan(b *testing.B) {
	for _, count := range []int{100, 1000, 10000} {
		b.Run(strconv.Itoa(count), func(b *testing.B) {
			index := candidateScaleIndex(b, count)
			facts := Fields{Values: map[string]any{"source_id": "42"}}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				matched := 0
				for _, item := range index.policies {
					result, err := item.Compiled.Conditions[0].Match(b.Context(), facts)
					if err != nil {
						b.Fatal(err)
					}
					if result.Matched {
						matched++
					}
				}
				if matched != 1 {
					b.Fatal(matched)
				}
			}
		})
	}
}
