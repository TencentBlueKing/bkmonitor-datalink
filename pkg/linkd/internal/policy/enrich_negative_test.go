// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package policy

import (
	"encoding/json"
	"testing"

	"linkd/internal/domain"
)

func TestEnrichFailureNegativeConditionsUseActualPresence(t *testing.T) {
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusFailed, domain.EnrichStatusPartial} {
		for _, operator := range []string{"must_not_term", "must_not_terms", "must_not_wildcard", "must_not_regexp"} {
			for _, tc := range []struct {
				name, raw string
				matched   bool
			}{
				{"missing", "", true}, {"null", `null`, true}, {"empty", `""`, true}, {"matching", `"h"`, false}, {"different", `"z"`, true},
			} {
				t.Run(string(status)+"/"+operator+"/"+tc.name, func(t *testing.T) {
					event := policyEvent(`[{"resource":{"status":"`+string(status)+`","patches":[]}}]`, status)
					if tc.raw != "" {
						event.ExtraData["model_id"] = json.RawMessage(tc.raw)
					}
					var wanted any = "h"
					if operator == "must_not_terms" {
						wanted = []string{"h"}
					}
					raw := encodeSpec(t, map[string]any{"expression": "A", "A": map[string]any{"condition": operator, "target_key": "model_id", "target_value": wanted}})
					expression, err := CompileExpression(raw, KACFields())
					if err != nil {
						t.Fatal(err)
					}
					for name, view := range currentOutputViews(t, event, nil) {
						result, err := expression.Match(t.Context(), view)
						if err != nil || !result.Evaluated || result.Matched != tc.matched {
							t.Fatalf("%s result=%+v err=%v want=%v", name, result, err, tc.matched)
						}
					}
				})
			}
		}
	}
}

func TestSummaryEnrichStatusWithoutProcessorsDoesNotBlockFields(t *testing.T) {
	for _, status := range []domain.EnrichStatus{domain.EnrichStatusFailed, domain.EnrichStatusPartial} {
		event := policyEvent(`[]`, status)
		for name, view := range currentOutputViews(t, event, nil) {
			got, err := view.Field(t.Context(), "name")
			if err != nil || !got.Present || got.Data != "original" {
				t.Fatalf("%s/%s: %+v %v", status, name, got, err)
			}
			got, err = view.Field(t.Context(), "bk_biz_id")
			if err != nil || got.Data != "2" {
				t.Fatalf("%s/%s dimensions fallback: %+v %v", status, name, got, err)
			}
		}
	}
}
