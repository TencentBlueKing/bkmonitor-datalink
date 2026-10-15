package datasources

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"gorm.io/datatypes"
	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
)

func TestCWStrategyBindsPublishedRuntimeMetricReferences(t *testing.T) {
	t.Parallel()
	for _, businesses := range [][]int64{{2}, {2, 4}} {
		payload := metricBindingPublication(t, businesses...)
		before, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		strategy, err := readMetricBindingPublication(t, payload)
		if err != nil {
			t.Fatal(err)
		}
		projection, err := strategy.StrategyItemProjection()
		if err != nil {
			t.Fatal(err)
		}
		if projection.QueryConfigs[0].MetricRefID != 199 || projection.QueryConfigs[1].MetricRefID != 200 || projection.Expression != "A / B" {
			t.Fatalf("wrong runtime binding: %+v", projection)
		}
		after, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(before, after) {
			t.Fatal("reader mutated publication")
		}
	}
}

func TestCWStrategyRejectsAmbiguousRuntimeMetricBinding(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing runtime", func(p map[string]any) { delete(p, "runtime_query_configs") }},
		{"business count mismatch", func(p map[string]any) { p["runtime_query_configs"] = p["runtime_query_configs"].([]any)[:1] }},
		{"runtime error", func(p map[string]any) { bindingRuntime(p, 0)["error"] = "compile failed" }},
		{"duplicate alias", func(p map[string]any) { bindingRuntimeQueries(p, 0)[1].(map[string]any)["alias"] = "B" }},
		{"duplicate declaration alias", func(p map[string]any) {
			for _, entry := range p["resolved_strategies"].([]any) {
				queries := entry.(map[string]any)["spec"].(map[string]any)["strategy_item"].(map[string]any)["query_configs"].([]any)
				queries[1].(map[string]any)["alias"] = "A"
			}
		}},
		{"field mismatch", func(p map[string]any) { bindingRuntimeQueries(p, 0)[0].(map[string]any)["metric_field"] = "other" }},
		{"missing reference", func(p map[string]any) { delete(bindingRuntimeQueries(p, 0)[0].(map[string]any), "metric_ref_id") }},
		{"negative reference", func(p map[string]any) { bindingRuntimeQueries(p, 0)[0].(map[string]any)["metric_ref_id"] = -1 }},
		{"different business reference", func(p map[string]any) { bindingRuntimeQueries(p, 1)[0].(map[string]any)["metric_ref_id"] = 999 }},
		{"expanded queries cannot be paired", func(p map[string]any) {
			bindingRuntime(p, 0)["query_configs"] = append(bindingRuntimeQueries(p, 0), map[string]any{"alias": "A_member", "metric_ref_id": 201, "metric_field": "node_load1"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := metricBindingPublication(t, 2, 4)
			tc.change(payload)
			_, err := readMetricBindingPublication(t, payload)
			var failure *description.Error
			if !errors.As(err, &failure) || failure.Code != "publication_metric_binding_invalid" {
				t.Fatalf("unproven runtime binding: error=%v", err)
			}
		})
	}
}

func TestCWStrategyKeepsDeclaredDerivedIdentity(t *testing.T) {
	t.Parallel()
	payload := metricBindingPublication(t, 2)
	spec := payload["resolved_strategies"].([]any)[0].(map[string]any)["spec"].(map[string]any)
	spec["field_tag"] = "derived_metric"
	spec["field_name"] = "load_ratio"
	queries := spec["strategy_item"].(map[string]any)["query_configs"].([]any)
	queries[0].(map[string]any)["metric_ref_id"] = 42
	bindingRuntime(payload, 0)["query_configs"] = append(bindingRuntimeQueries(payload, 0), map[string]any{"alias": "A_member", "metric_ref_id": 201, "metric_field": "node_load1"})
	strategy, err := readMetricBindingPublication(t, payload)
	if err != nil || strategy.Spec.StrategyItem.QueryConfigs[0].MetricRefID != 42 || strategy.Spec.FieldName != "load_ratio" {
		t.Fatalf("derived definition replaced by runtime member: strategy=%+v err=%v", strategy, err)
	}
}

func metricBindingPublication(t *testing.T, businesses ...int64) map[string]any {
	t.Helper()
	payload := splitTestPublication(t, businesses...)
	for i, entry := range payload["resolved_strategies"].([]any) {
		spec := entry.(map[string]any)["spec"].(map[string]any)
		spec["strategy_item"] = map[string]any{"expression": "A / B", "query_configs": []any{
			map[string]any{"alias": "A", "metric_field": "node_load15", "result_table_id": "", "source_config": map[string]any{"metric_id": 196631}},
			map[string]any{"alias": "B", "metric_field": "node_load5", "result_table_id": ""},
		}}
		bindingRuntime(payload, i)["query_configs"] = []any{
			map[string]any{"alias": "B", "metric_field": "node_load5", "metric_ref_id": 200, "index_set_id": ""},
			map[string]any{"alias": "A", "metric_field": "node_load15", "metric_ref_id": 199, "data_label": "k8s"},
		}
	}
	return payload
}

func bindingRuntime(payload map[string]any, i int) map[string]any {
	return payload["runtime_query_configs"].([]any)[i].(map[string]any)
}

func bindingRuntimeQueries(payload map[string]any, i int) []any {
	return bindingRuntime(payload, i)["query_configs"].([]any)
}

func readMetricBindingPublication(t *testing.T, payload map[string]any) (models.CWStrategy, error) {
	t.Helper()
	rows := splitTestRows(t, payload)
	row := strategyPublicationRow{ID: 1, TenantID: "tenant", SetUID: splitTestSetUID, TemplateID: 7, ConfigUID: splitTestConfigUID, Version: splitTestVersion, PublishStatus: "published", BusinessIDs: datatypes.JSON(rows.values[0][10].(string)), Payload: datatypes.JSON(rows.values[0][11].(string))}
	publication, err := decodeStrategyPublication(row)
	if err != nil {
		return models.CWStrategy{}, err
	}
	strategy, err := cwStrategyFromPublication(row, publication)
	return strategy, err
}
