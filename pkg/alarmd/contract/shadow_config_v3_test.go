package contract

import (
	"encoding/json"
	"os"
	"testing"
)

func TestComparisonConfigV3StrictCheckpoint(t *testing.T) {
	original, err := os.ReadFile("testdata/query-v3/config.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := DecodeComparisonConfigV3(original, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := CanonicalComparisonConfigV3(*c)
	if err != nil || string(b)+"\n" != string(original) {
		t.Fatal("checkpoint bytes changed", err)
	}
	for _, kind := range []string{"missing_time_aggregation", "null_dimensions_item", "unknown_function", "number_wrong_type", "unknown_query"} {
		t.Run(kind, func(t *testing.T) {
			var raw map[string]any
			if err := json.Unmarshal(original, &raw); err != nil {
				t.Fatal(err)
			}
			query := raw["query"].(map[string]any)
			selector := query["selectors"].([]any)[0].(map[string]any)
			switch kind {
			case "missing_time_aggregation":
				delete(selector, "time_aggregation")
			case "null_dimensions_item":
				selector["dimensions"] = []any{nil}
			case "unknown_function":
				selector["functions"].([]any)[0].(map[string]any)["new_semantics"] = true
			case "unknown_query":
				query["output_list"] = []any{}
			case "number_wrong_type":
				selector["functions"].([]any)[0].(map[string]any)["arguments"] = []any{map[string]any{"kind": "NUMBER", "value": 1}}
			}
			wire, _ := json.Marshal(raw)
			if _, err := DecodeComparisonConfigV3(wire, 1<<20); err == nil {
				t.Fatal("invalid V3 accepted")
			}
		})
	}
}

func TestComparisonConfigV3OrderedQueryAndScalarKinds(t *testing.T) {
	b, err := os.ReadFile("testdata/query-v3/config.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := DecodeComparisonConfigV3(b, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	_, baseline, err := CanonicalComparisonConfigV3(*c)
	if err != nil {
		t.Fatal(err)
	}
	c.Query.Selectors[0], c.Query.Selectors[1] = c.Query.Selectors[1], c.Query.Selectors[0]
	_, changed, err := CanonicalComparisonConfigV3(*c)
	if err != nil || baseline == changed {
		t.Fatal("ordered selectors collapsed", err)
	}
	for _, kind := range []string{"STRING", "NUMBER", "BOOLEAN"} {
		raw := json.RawMessage(`"1"`)
		if kind == "BOOLEAN" {
			raw = json.RawMessage(`true`)
		}
		c.Query.Selectors[0].Functions[0].Arguments = []ShadowQueryScalarV3{{Kind: kind, Value: raw}}
		_, digest, err := CanonicalComparisonConfigV3(*c)
		if err != nil || digest == changed {
			t.Fatal("scalar kind collapsed", err)
		}
		changed = digest
	}
}
