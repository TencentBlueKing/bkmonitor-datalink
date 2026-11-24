package datasources

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"linkd/internal/enrich/description"
)

func TestCWStrategyBindsDatasetPhysicalLocation(t *testing.T) {
	t.Parallel()
	payload := datasetBindingSnapshot(t)
	before, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	strategy, err := readMetricBindingSnapshot(t, payload)
	if err != nil {
		t.Fatal(err)
	}
	projection, err := strategy.StrategyItemProjection()
	if err != nil {
		t.Fatal(err)
	}
	if strategy.Spec.FieldName != "cpu_usage" || projection.Expression != "a / b * 100" || string(projection.Functions) != `[{"id":"abs"}]` {
		t.Fatalf("declaration semantics changed: %+v", strategy.Spec)
	}
	for i, query := range projection.QueryConfigs {
		wantField := []string{"usage", "total"}[i]
		if query.MetricField != wantField || query.ResultTableID != "system.cpu_summary" || query.MetricRefID != 0 || query.DataSourceLabel != "bk_monitor" || query.DataTypeLabel != "time_series" {
			t.Fatalf("dataset physical binding missing: %+v", query)
		}
		if query.Alias != []string{"a", "b"}[i] || query.AggregateMethod != "AVG" || query.AggregatePeriod != 60 || !reflect.DeepEqual(query.AggregateBy, []string{"bk_target_ip"}) || string(query.AggregateFilter) != `[{"key":"hostname","method":"eq","value":["host"]}]` || string(query.Functions) != `[{"id":"rate"}]` || query.Raw["metric_field"] == nil {
			t.Fatalf("query semantics changed: %+v", query)
		}
		var semanticField string
		if json.Unmarshal(query.Raw["metric_field"], &semanticField) != nil || semanticField != []string{"cpu_usage", "cpu_total"}[i] {
			t.Fatal("raw declaration overwritten")
		}
	}
	after, err := json.Marshal(payload)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("historical snapshot mutated")
	}
}

func TestCWStrategyRejectsUnprovenDatasetBinding(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing items", func(p map[string]any) { revisionStrategy(p)["items"] = []any{} }},
		{"multiple items", func(p map[string]any) {
			revisionStrategy(p)["items"] = append(revisionStrategy(p)["items"].([]any), revisionItem(p))
		}},
		{"compile failure", func(p map[string]any) { revisionItem(p)["error"] = "compile failed" }},
		{"missing query", func(p map[string]any) { revisionItem(p)["query_configs"] = []any{} }},
		{"duplicate runtime alias", func(p map[string]any) {
			revisionItem(p)["query_configs"].([]any)[1] = revisionItem(p)["query_configs"].([]any)[0]
		}},
		{"duplicate declared alias", func(p map[string]any) { datasetDeclaredQuery(p)["alias"] = "b" }},
		{"missing dataset code", func(p map[string]any) { delete(datasetDeclaredQuery(p), "dataset_code") }},
		{"different dataset", func(p map[string]any) { datasetRuntimeQuery(p)["dataset_code"] = "other" }},
		{"different semantic field", func(p map[string]any) {
			datasetRuntimeQuery(p)["dataset_semantic"].(map[string]any)["metric_field"] = "other"
		}},
		{"missing semantic binding", func(p map[string]any) { delete(datasetRuntimeQuery(p), "dataset_semantic") }},
		{"missing physical table", func(p map[string]any) { delete(datasetRuntimeQuery(p), "result_table_id") }},
		{"missing physical field", func(p map[string]any) { datasetRuntimeQuery(p)["metric_field"] = " " }},
		{"malformed physical field", func(p map[string]any) { datasetRuntimeQuery(p)["metric_field"] = 199 }},
		{"unbound source", func(p map[string]any) { datasetRuntimeQuery(p)["unbound"] = true }},
		{"invalid source", func(p map[string]any) { datasetRuntimeQuery(p)["invalid_source"] = true }},
		{"different dimensions", func(p map[string]any) { datasetRuntimeQuery(p)["agg_dimension"] = []any{"physical_ip"} }},
		{"different semantic dimensions", func(p map[string]any) {
			datasetRuntimeQuery(p)["dataset_semantic"].(map[string]any)["agg_dimension"] = []any{"other"}
		}},
		{"different filter keys", func(p map[string]any) {
			datasetRuntimeQuery(p)["dataset_semantic"].(map[string]any)["agg_condition_keys"] = []any{}
		}},
		{"different filter value", func(p map[string]any) { datasetRuntimeQuery(p)["agg_condition"] = []any{} }},
		{"large numeric filter values differ", func(p map[string]any) {
			datasetDeclaredQuery(p)["agg_condition"] = []any{map[string]any{"key": "hostname", "value": []any{int64(9007199254740992)}}}
			datasetRuntimeQuery(p)["agg_condition"] = []any{map[string]any{"key": "hostname", "value": []any{int64(9007199254740993)}}}
		}},
		{"additional source filter", func(p map[string]any) { datasetRuntimeQuery(p)["filter_dict"] = map[string]any{"device": "cpu"} }},
		{"conflicting catalog reference", func(p map[string]any) { datasetDeclaredQuery(p)["metric_ref_id"] = 199 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := datasetBindingSnapshot(t)
			tc.change(payload)
			_, err := readMetricBindingSnapshot(t, payload)
			var failure *description.Error
			wantCode := "strategy_revision_dataset_binding_invalid"
			if tc.name == "missing items" {
				wantCode = "strategy_revision_items_invalid"
			}
			if !errors.As(err, &failure) || failure.Code != wantCode {
				t.Fatalf("unproven dataset binding accepted: %v", err)
			}
		})
	}
}

func TestCWStrategyDatasetSingleQueryDefaults(t *testing.T) {
	t.Parallel()
	p := datasetBindingSnapshot(t)
	item := revisionSpec(p)["strategy_item"].(map[string]any)
	query := datasetDeclaredQuery(p)
	delete(query, "agg_dimension")
	delete(query, "agg_condition")
	delete(query, "metric_field")
	item["query_configs"], item["agg_dimension"], item["agg_condition"] = []any{query}, []any{}, []any{}
	compiled := datasetRuntimeQuery(p)
	compiled["agg_dimension"], compiled["agg_condition"] = []any{}, []any{}
	compiled["dataset_semantic"] = map[string]any{"metric_field": "cpu_usage", "agg_dimension": []any{}, "agg_condition_keys": []any{}}
	revisionItem(p)["query_configs"] = []any{compiled}
	strategy, err := readMetricBindingSnapshot(t, p)
	if err != nil || strategy.Spec.StrategyItem.QueryConfigs[0].MetricField != "usage" {
		t.Fatalf("single query with declaration defaults rejected: %v", err)
	}
}

func TestCWStrategyDatasetAndCatalogQueriesBindIndependently(t *testing.T) {
	t.Parallel()
	p := datasetBindingSnapshot(t)
	item := revisionSpec(p)["strategy_item"].(map[string]any)
	item["query_configs"] = append(item["query_configs"].([]any), map[string]any{"query_config_type": "catalog", "alias": "c", "metric_field": "load"})
	revisionItem(p)["query_configs"] = append(revisionItem(p)["query_configs"].([]any), map[string]any{"alias": "c", "metric_ref_id": 199, "metric_field": "load"})
	strategy, err := readMetricBindingSnapshot(t, p)
	if err != nil {
		t.Fatal(err)
	}
	queries := strategy.Spec.StrategyItem.QueryConfigs
	if queries[0].MetricField != "usage" || queries[1].MetricField != "total" || queries[2].MetricRefID != 199 || queries[2].ResultTableID != "" {
		t.Fatalf("dataset/catalog bindings crossed: %+v", queries)
	}
}

func datasetBindingSnapshot(t *testing.T) map[string]any {
	t.Helper()
	p := revisionTestSnapshot(t)
	spec := revisionSpec(p)
	spec["field_name"], spec["table_id"] = "cpu_usage", ""
	queries, runtime := []any{}, []any{}
	for i, field := range []string{"cpu_usage", "cpu_total"} {
		alias := []string{"a", "b"}[i]
		conditions := []any{map[string]any{"key": "hostname", "method": "eq", "value": []any{"host"}}}
		queries = append(queries, map[string]any{"query_config_type": "dataset", "dataset_code": "infra.system", "alias": alias, "metric_field": field, "agg_method": "AVG", "agg_interval": 60, "agg_dimension": []any{"bk_target_ip"}, "agg_condition": conditions, "functions": []any{map[string]any{"id": "rate"}}})
		runtime = append([]any{map[string]any{"alias": alias, "dataset_code": "infra.system", "dataset_semantic": map[string]any{"metric_field": field, "agg_dimension": []any{"bk_target_ip"}, "agg_condition_keys": []any{"hostname"}}, "metric_field": []string{"usage", "total"}[i], "result_table_id": "system.cpu_summary", "data_source_label": "bk_monitor", "data_type_label": "time_series", "agg_dimension": []any{"bk_target_ip"}, "agg_condition": conditions, "filter_dict": map[string]any{}}}, runtime...)
	}
	spec["strategy_item"] = map[string]any{"expression": "a / b * 100", "functions": []any{map[string]any{"id": "abs"}}, "query_configs": queries}
	revisionItem(p)["query_configs"] = runtime
	return p
}

func datasetDeclaredQuery(p map[string]any) map[string]any {
	return revisionSpec(p)["strategy_item"].(map[string]any)["query_configs"].([]any)[0].(map[string]any)
}

func datasetRuntimeQuery(p map[string]any) map[string]any {
	return revisionItem(p)["query_configs"].([]any)[1].(map[string]any)
}
