package datasources

import (
	"encoding/json"
	"testing"
)

func TestCWStrategyDoesNotReadExecutionQueryPosition(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		queries any
	}{
		{name: "missing"},
		{name: "empty", queries: []any{}},
		{name: "invalid list", queries: "invalid"},
		{name: "invalid first position", queries: []any{map[string]any{"metric_field": 123}, map[string]any{"metric_field": "later"}}},
		{name: "different member position", queries: []any{map[string]any{"result_table_id": "other", "metric_field": "member"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := revisionTestSnapshot(t)
			spec := revisionSpec(payload)
			spec["field_tag"], spec["field_name"] = "derived_metric", "ratio"
			revisionItem(payload)["query_configs"] = tc.queries
			before, _ := json.Marshal(payload)
			strategy, err := readMetricBindingSnapshot(t, payload)
			if err != nil {
				t.Fatal(err)
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
