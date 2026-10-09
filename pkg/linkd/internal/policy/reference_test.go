// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package policy

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
)

func referenceExpression(t *testing.T, operator, value string, fields FieldCatalog) *Expression {
	t.Helper()
	raw := encodeSpec(t, map[string]any{"expression": "A", "A": map[string]any{"condition": operator, "target_key": "source_name", "target_value": value, "is_alarm_field_referenced": true}})
	expression, err := compileExpression(raw, fields, true)
	if err != nil {
		t.Fatal(err)
	}
	return expression
}

func TestDependencyFieldReferencesUseIndependentMainValues(t *testing.T) {
	fields := KACFields()
	fields["flag"] = FieldBoolean
	fields["missing"] = FieldText
	for _, tc := range []struct {
		name, operator, template, want string
		value                          any
		matched                        bool
	}{
		{"string", "term", "${object}", "host", "host", true},
		{"number", "term", "${object}", "7", json.Number("7"), true},
		{"false", "term", "${flag}", "False", false, true},
		{"missing", "term", "${missing}", "", "unused", true},
		{"multiple", "term", "${object}-${missing}", "host-", "host", true},
		{"null", "term", "${object}", "None", nil, true},
		{"array", "term", "${object}", "['a', False, 7]", []any{"a", false, json.Number("7")}, true},
		{"terms single value", "terms", "${object}", "a,b", "a,b", true},
		{"terms no CSV split", "terms", "${object}", "a", "a,b", false},
		{"negative terms", "must_not_terms", "${object}", "other", "a,b", true},
		{"wildcard", "wildcard", "${object}", "host", "host", true},
		{"regexp", "regexp", "${object}", "host", "host", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			expression := referenceExpression(t, tc.operator, tc.template, fields)
			for range 4 {
				t.Run("concurrent", func(t *testing.T) {
					t.Parallel()
					origin := Fields{Values: map[string]any{"object": tc.value, "flag": tc.value}}
					candidate := Fields{Values: map[string]any{"source_name": tc.want}}
					result, err := expression.match(t.Context(), candidate, origin)
					if err != nil || !result.Evaluated || result.Matched != tc.matched {
						t.Fatalf("reference result %+v %v", result, err)
					}
				})
			}
		})
	}
	expression := referenceExpression(t, "term", "${object}", fields)
	for _, object := range []string{"first", "second", "${object}"} {
		result, err := expression.match(t.Context(), Fields{Values: map[string]any{"source_name": object}}, Fields{Values: map[string]any{"object": object}})
		if err != nil || !result.Matched {
			t.Fatal("reused resolved query or recursively expanded value", result, err)
		}
	}
	for i := range 16 {
		t.Run("shared template "+strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			object := "host-" + strconv.Itoa(i)
			result, err := expression.match(t.Context(), Fields{Values: map[string]any{"source_name": object}}, Fields{Values: map[string]any{"object": object}})
			if err != nil || !result.Matched {
				t.Fatal("concurrent origin values crossed", result, err)
			}
		})
	}
}

func TestDependencyReferencesRejectInvalidConfigurationAndUnavailableOrigin(t *testing.T) {
	for _, condition := range []map[string]any{
		{"target_value": []string{"${object}"}},
		{"target_value": "fixed"},
		{"target_value": "${unknown}"},
		{"target_value": "${object"},
		{"target_value": "${object}", "bk_obj_asst_id": "relation"},
		{"target_value": "${object}", "condition": "unknown"},
	} {
		row := map[string]any{"target_key": "name", "condition": "term", "is_alarm_field_referenced": true}
		for key, value := range condition {
			row[key] = value
		}
		raw := encodeSpec(t, map[string]any{"expression": "A", "A": row})
		if _, err := compileExpression(raw, KACFields(), true); err == nil {
			t.Fatalf("accepted invalid reference %+v", condition)
		}
	}
	raw := json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"${object}","is_alarm_field_referenced":true}}`)
	if _, err := CompileExpression(raw, KACFields()); err == nil {
		t.Fatal("reference allowed outside rely_policy")
	}
	literal := json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"name","target_value":"${object}","is_alarm_field_referenced":false}}`)
	expr, err := CompileExpression(literal, KACFields())
	if err != nil {
		t.Fatal(err)
	}
	if result, err := expr.Match(t.Context(), Fields{Values: map[string]any{"name": "${object}"}}); err != nil || !result.Matched {
		t.Fatal("literal interpolated", result, err)
	}
	expr = referenceExpression(t, "must_not_term", "${object}", KACFields())
	for _, origin := range []Reader{nil, Fields{Unavailable: map[string]bool{"object": true}}, Fields{Values: map[string]any{"object": strings.Repeat("x", 65537)}}} {
		result, err := expr.match(t.Context(), Fields{Values: map[string]any{"source_name": "other"}}, origin)
		if !errors.Is(err, ErrUnavailable) || result.Evaluated || result.Matched {
			t.Fatal("unavailable reference became negative match", result, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := expr.match(ctx, Fields{}, Fields{}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	child := mustEventView(t, policyEvent(`[]`, "succeeded"), nil)
	origin := *child
	origin.TenantID = "other"
	if _, err := child.WithOrigin(&origin); !errors.Is(err, ErrAccess) {
		t.Fatal("cross tenant origin accepted", err)
	}
	expr = referenceExpression(t, "must_not_regexp", "${object}", KACFields())
	if result, err := expr.match(t.Context(), Fields{}, Fields{Values: map[string]any{"object": "["}}); !errors.Is(err, ErrUnavailable) || result.Matched || result.Evaluated {
		t.Fatal("invalid resolved regexp became a negative match", result, err)
	}
}

func TestDependencyReferenceConvertsBooleanForTypedQuery(t *testing.T) {
	fields := KACFields()
	fields["flag"] = FieldBoolean
	raw := json.RawMessage(`{"expression":"A","A":{"condition":"term","target_key":"flag","target_value":"${flag}","is_alarm_field_referenced":true}}`)
	expr, err := compileExpression(raw, fields, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []bool{true, false} {
		reader := Fields{Values: map[string]any{"flag": flag}}
		if result, err := expr.match(t.Context(), reader, reader); err != nil || !result.Matched {
			t.Fatal("typed boolean reference failed", result, err)
		}
	}
}
