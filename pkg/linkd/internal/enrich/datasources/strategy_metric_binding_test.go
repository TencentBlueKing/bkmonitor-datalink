package datasources

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/enrich/description"
	"linkd/internal/enrich/models"
)

func TestCWStrategyReadersKeepNonCatalogQueries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, source, alias string
		reference           any
	}{
		{name: "physical compilation without catalog reference", alias: "a"},
		{name: "physical compilation with zero reference", alias: "a", reference: 0},
		{name: "log index metric with compiled alias", source: "klc", alias: "A"},
		{name: "log index metric is not a catalog member", source: "klc", alias: "A", reference: 199},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := revisionTestSnapshot(t)
			spec := revisionSpec(payload)
			spec["metric_source"], spec["monitor_item_type"] = tc.source, "metric"
			spec["field_name"], spec["table_id"] = "cpu_usage", ""
			spec["strategy_item"] = map[string]any{"expression": tc.alias, "query_configs": []any{
				map[string]any{"alias": tc.alias, "metric_field": "cpu_usage", "unit": "declaration-only"},
			}}
			query := map[string]any{"alias": "a", "metric_field": "usage", "metric_id": "bk_monitor.system.cpu.usage", "result_table_id": "system.cpu", "unit": "percent", "metric_ref_id": tc.reference}
			if tc.source == "klc" {
				query["index_set_id"], query["result_table_id"] = 7, ""
			}
			revisionItem(payload)["query_configs"] = []any{query}
			before, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			db := openReaderTestDB(t, func(ctx context.Context, statement string, args []driver.NamedValue) (driver.Rows, error) {
				if strings.Contains(statement, "home_application_monitortemplate") {
					return &readerTestRows{columns: []string{"id", "bk_tenant_id", "name"}, values: [][]driver.Value{{int64(7), "tenant", "CPU template"}}}, nil
				}
				assertRevisionQuery(t, ctx, statement, args)
				return snapshotTestRows(t, payload), nil
			})
			client, err := NewCWStrategyClient(CWStrategyClientConfig{DB: db})
			if err != nil {
				t.Fatal(err)
			}
			strategy, found, err := client.GetByStrategyID(t.Context(), models.StrategyRevisionQuery{TenantID: "tenant", ID: 1, Version: revisionTestVersion})
			if err != nil || !found {
				t.Fatalf("non-catalog strategy rejected: found=%v error=%v", found, err)
			}
			declared := strategy.Spec.StrategyItem.QueryConfigs[0]
			if declared.MetricRefID != 0 || declared.MetricField != "cpu_usage" || declared.Alias != tc.alias || declared.Unit != "declaration-only" || strategy.Spec.StrategyItem.Expression != tc.alias {
				t.Fatalf("compiled query overwrote declaration: %+v", declared)
			}
			descriptionClient, err := NewDescriptionConfigurationClient(db)
			if err != nil {
				t.Fatal(err)
			}
			configuration, err := descriptionClient.ReadConfiguration(t.Context(), description.ConfigurationQuery{TenantID: "tenant", StrategyID: 1, StrategyVersion: revisionTestVersion, BusinessID: 524})
			if err != nil || len(configuration.Queries) != 1 || configuration.Queries[0].Unit != "percent" || len(configuration.Algorithms) != 1 {
				t.Fatalf("execution description unavailable: %+v error=%v", configuration, err)
			}
			after, err := json.Marshal(payload)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("reader mutated historical snapshot")
			}
		})
	}
}

func TestCWStrategyBindsPublishedRuntimeMetricReferences(t *testing.T) {
	t.Parallel()
	payload := metricBindingSnapshot(t)
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
	if projection.QueryConfigs[0].MetricRefID != 199 || projection.QueryConfigs[1].MetricRefID != 200 || projection.Expression != "A / B" {
		t.Fatalf("wrong runtime binding: %+v", projection)
	}
	after, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatal("reader mutated historical snapshot")
	}

}

func TestCWStrategyRejectsAmbiguousRuntimeMetricBinding(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing runtime", func(p map[string]any) { delete(revisionItem(p), "query_configs") }},
		{"ambiguous execution items", func(p map[string]any) {
			revisionStrategy(p)["items"] = append(revisionStrategy(p)["items"].([]any), revisionItem(p))
		}},
		{"runtime error", func(p map[string]any) { revisionItem(p)["error"] = "compile failed" }},
		{"duplicate alias", func(p map[string]any) { revisionItem(p)["query_configs"].([]any)[1].(map[string]any)["alias"] = "B" }},
		{"duplicate declaration alias", func(p map[string]any) {
			queries := revisionSpec(p)["strategy_item"].(map[string]any)["query_configs"].([]any)
			queries[1].(map[string]any)["alias"] = "A"
		}},
		{"field mismatch", func(p map[string]any) {
			revisionItem(p)["query_configs"].([]any)[0].(map[string]any)["metric_field"] = "other"
		}},
		{"missing reference", func(p map[string]any) {
			delete(revisionItem(p)["query_configs"].([]any)[0].(map[string]any), "metric_ref_id")
		}},
		{"negative reference", func(p map[string]any) {
			revisionItem(p)["query_configs"].([]any)[0].(map[string]any)["metric_ref_id"] = -1
		}},
		{"expanded queries cannot be paired", func(p map[string]any) {
			revisionItem(p)["query_configs"] = append(revisionItem(p)["query_configs"].([]any), map[string]any{"alias": "A_member", "metric_ref_id": 201, "metric_field": "node_load1"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := metricBindingSnapshot(t)
			tc.change(payload)
			_, err := readMetricBindingSnapshot(t, payload)
			var failure *description.Error
			if !errors.As(err, &failure) || failure.Code != "strategy_revision_metric_binding_invalid" {
				t.Fatalf("unproven runtime binding: error=%v", err)
			}
		})
	}
}

func TestCWStrategyKeepsDeclaredDerivedIdentity(t *testing.T) {
	t.Parallel()
	payload := metricBindingSnapshot(t)
	spec := revisionSpec(payload)
	spec["field_tag"] = "derived_metric"
	spec["field_name"] = "load_ratio"
	queries := spec["strategy_item"].(map[string]any)["query_configs"].([]any)
	queries[0].(map[string]any)["metric_ref_id"] = 42
	revisionItem(payload)["query_configs"] = append(revisionItem(payload)["query_configs"].([]any), map[string]any{"alias": "A_member", "metric_ref_id": 201, "metric_field": "node_load1"})
	strategy, err := readMetricBindingSnapshot(t, payload)
	if err != nil || strategy.Spec.StrategyItem.QueryConfigs[0].MetricRefID != 42 || strategy.Spec.FieldName != "load_ratio" {
		t.Fatalf("derived definition replaced by runtime member: strategy=%+v err=%v", strategy, err)
	}
}

func metricBindingSnapshot(t *testing.T) map[string]any {
	t.Helper()
	payload := revisionTestSnapshot(t)
	revisionSpec(payload)["strategy_item"] = map[string]any{"expression": "A / B", "query_configs": []any{
		map[string]any{"alias": "A", "metric_field": "node_load15", "result_table_id": "", "source_config": map[string]any{"metric_id": 196631}},
		map[string]any{"alias": "B", "metric_field": "node_load5", "result_table_id": ""},
	}}
	revisionItem(payload)["query_configs"] = []any{
		map[string]any{"alias": "B", "metric_field": "node_load5", "metric_ref_id": 200, "index_set_id": ""},
		map[string]any{"alias": "A", "metric_field": "node_load15", "metric_ref_id": 199, "data_label": "k8s"},
	}
	return payload
}
func readMetricBindingSnapshot(t *testing.T, payload map[string]any) (models.CWStrategy, error) {
	t.Helper()
	rows := snapshotTestRows(t, payload)
	row := strategyRevisionRow{TenantID: "tenant", BusinessID: 2, StrategyID: 1, Version: revisionTestVersion, Snapshot: rows.values[0][7].([]byte)}
	snapshot, err := decodeStrategyRevision(row)
	if err != nil {
		return models.CWStrategy{}, err
	}
	return cwStrategyFromRevision(row, snapshot)
}
