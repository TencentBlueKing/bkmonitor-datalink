package datasources

import (
	"encoding/json"
	"linkd/internal/enrich/models"
	"testing"
)

func TestRevisionDerivedKindComesFromBoundParent(t *testing.T) {
	for _, tc := range []struct {
		name               string
		parent, definition int64
		field              string
		wantError          bool
	}{
		{name: "expanded catalog parent", parent: 219, definition: 219, field: "ratio"},
		{name: "different parent", parent: 220, definition: 219, field: "ratio", wantError: true},
		{name: "different definition", parent: 219, definition: 220, field: "ratio", wantError: true},
		{name: "different field", parent: 219, definition: 219, field: "other", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := revisionTestSnapshot(t)
			spec := revisionSpec(payload)
			spec["field_name"], spec["field_tag"], spec["table_id"] = "ratio", "", ""
			spec["strategy_item"] = map[string]any{"expression": "a", "query_configs": []any{map[string]any{"query_config_type": "catalog", "metric_ref_id": 219, "metric_field": "ratio", "alias": "a"}}}
			snapshot := map[string]any{"parent_metric_id": tc.parent, "derived": map[string]any{"metric_id": tc.definition, "metric_field": tc.field}}
			revisionItem(payload)["query_configs"] = []any{map[string]any{"alias": "a", "metric_ref_id": 145, "metric_field": "rss", "metric_snapshot": snapshot}, map[string]any{"alias": "b", "metric_ref_id": 180, "metric_field": "limit", "metric_snapshot": snapshot}}
			before, _ := json.Marshal(payload)
			strategy, err := readMetricBindingSnapshot(t, payload)
			if tc.wantError {
				if err == nil {
					t.Fatal("inconsistent parent accepted")
				}
				return
			}
			if err != nil || strategy.Spec.FieldTag != models.CWStrategyFieldTagDerivedMetric || strategy.Spec.StrategyItem.QueryConfigs[0].MetricRefID != 219 || strategy.Spec.StrategyItem.Expression != "a" {
				t.Fatalf("strategy=%+v error=%v", strategy.Spec, err)
			}
			after, _ := json.Marshal(payload)
			if string(before) != string(after) {
				t.Fatal("snapshot changed")
			}
		})
	}
}
