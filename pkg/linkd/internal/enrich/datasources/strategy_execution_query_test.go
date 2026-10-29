package datasources

import (
	"encoding/json"
	"testing"
)

func TestCWStrategyKeepsFirstExecutionQuerySeparateFromDeclaration(t *testing.T) {
	t.Parallel()
	for _, reverse := range []bool{false, true} {
		name := "first input"
		if reverse {
			name = "reversed input order"
		}
		t.Run(name, func(t *testing.T) {
			payload := revisionTestSnapshot(t)
			spec := revisionSpec(payload)
			spec["field_tag"], spec["field_name"] = "derived_metric", "ratio"
			queries := []any{
				map[string]any{"alias": "z", "result_table_id": "k8s", "metric_field": "rss", "index_set_id": ""},
				map[string]any{"alias": "a", "result_table_id": "other", "metric_field": "limit"},
			}
			wantTable, wantField := "k8s", "rss"
			if reverse {
				queries[0], queries[1] = queries[1], queries[0]
				wantTable, wantField = "other", "limit"
			}
			revisionItem(payload)["query_configs"] = queries
			before, _ := json.Marshal(payload)
			strategy, err := readMetricBindingSnapshot(t, payload)
			if err != nil {
				t.Fatal(err)
			}
			first := strategy.FirstExecutionQuery
			if first == nil || first.ResultTableID != wantTable || first.MetricField != wantField {
				t.Fatalf("first execution query=%+v", first)
			}
			if strategy.Spec.FieldName != "ratio" || strategy.Spec.StrategyItem.QueryConfigs[0].MetricField != "usage" {
				t.Fatal("execution member replaced declaration")
			}
			after, _ := json.Marshal(payload)
			if string(before) != string(after) {
				t.Fatal("snapshot changed")
			}
		})
	}
}

func TestCWStrategyDoesNotReplaceInvalidFirstExecutionQuery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		queries any
	}{
		{name: "missing"},
		{name: "empty", queries: []any{}},
		{name: "invalid list", queries: "invalid"},
		{name: "invalid first with valid second", queries: []any{map[string]any{"metric_field": 123}, map[string]any{"metric_field": "later"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := revisionTestSnapshot(t)
			revisionSpec(payload)["field_tag"] = "derived_metric"
			revisionItem(payload)["query_configs"] = tc.queries
			strategy, err := readMetricBindingSnapshot(t, payload)
			if err != nil {
				t.Fatal(err)
			}
			if strategy.FirstExecutionQuery != nil {
				t.Fatalf("invalid first query replaced: %+v", strategy.FirstExecutionQuery)
			}
		})
	}
}
