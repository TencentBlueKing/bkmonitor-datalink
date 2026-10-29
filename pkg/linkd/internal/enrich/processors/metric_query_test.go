package processors

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

type metricLocationReader struct {
	dataSliceReader
	metadata models.MetricMetadata
	queries  []models.MetricQuery
	missing  bool
}

func (r *metricLocationReader) FindMetric(_ context.Context, query models.MetricQuery) (models.MetricMetadata, bool, error) {
	r.queries = append(r.queries, query)
	return r.metadata, !r.missing, nil
}

func TestMetricCatalogQueryLocation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, table, physical, label, kind string
		missing, failed                    bool
	}{
		{name: "catalog physical location", table: "system.cpu_detail", physical: "usage", kind: "native"},
		{name: "physical field differs from definition", table: "system.cpu_detail", physical: "cpu_usage", kind: "native"},
		{name: "data label location", label: "cpu_label", physical: "usage", kind: "native"},
		{name: "missing catalog", missing: true, failed: true},
		{name: "empty physical table", physical: "usage", kind: "native", failed: true},
		{name: "empty physical field", table: "system.cpu_detail", kind: "native", failed: true},
		{name: "unknown catalog kind", table: "system.cpu_detail", physical: "usage", kind: "unknown", failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &metricLocationReader{missing: tc.missing}
			// JSON 形状来自真实目录读取；验证物理引用能穿过 Reader 公共边界。
			raw, _ := json.Marshal(map[string]any{"Kind": tc.kind, "TableID": tc.table, "PhysicalField": tc.physical, "DataLabel": tc.label, "FieldName": "usage", "Unit": "percent"})
			if err := json.Unmarshal(raw, &reader.metadata); err != nil {
				t.Fatal(err)
			}
			reader.strategy = reader.strategyForTest()
			reader.strategy.Spec.TableID = ""
			reader.strategy.Spec.StrategyItem.Expression = "a"
			reader.strategy.Spec.StrategyItem.QueryConfigs = []models.StrategyQueryConfig{{MetricRefID: 1216, MetricField: "usage", Alias: "a", AggregateMethod: "AVG", AggregatePeriod: 60}}
			before, _ := json.Marshal(reader.strategy)
			event := processorBaseTargetAlert(t, domain.DimensionMap{})
			scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader, Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (Metric{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if tc.failed {
				if got.Status != domain.EnrichStatusFailed {
					t.Fatalf("invalid location accepted: %#v", got)
				}
				return
			}
			var value models.MetricValues
			if err := json.Unmarshal(mustRawObject(t, got.Value), &value); err != nil {
				t.Fatal(err)
			}
			wantTable := tc.table
			if wantTable == "" {
				wantTable = tc.label
			}
			if got.Status != domain.EnrichStatusSucceeded || value.ResultTableID != wantTable || value.MetricName != tc.physical || value.MetricUniqueID != wantTable+"."+tc.physical {
				t.Fatalf("metric=%#v status=%s", value, got.Status)
			}
			if len(reader.queries) != 1 || reader.queries[0].TenantID != event.BKTenantID || reader.queries[0].MetricID != 1216 {
				t.Fatalf("catalog reads=%#v", reader.queries)
			}
			after, _ := json.Marshal(reader.strategy)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("published strategy mutated")
			}
		})
	}
}

func TestMetricQueryOwnershipPreservesFactsForAllSources(t *testing.T) {
	for _, source := range []string{"built_in_bk", "external"} {
		t.Run(source, func(t *testing.T) {
			reader := &metricLocationReader{metadata: models.MetricMetadata{Kind: "native", TableID: "system.cpu", PhysicalField: "usage"}}
			reader.strategy = reader.strategyForTest()
			event := processorBaseTargetAlert(t, domain.DimensionMap{})
			event.EventSourceID = source
			scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader, Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (Metric{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			var values models.MetricValues
			if err := json.Unmarshal(mustRawObject(t, got.Value), &values); err != nil {
				t.Fatal(err)
			}
			if got.Status != domain.EnrichStatusSucceeded || values.MetricUniqueID == "" {
				t.Fatalf("lost matching facts: %#v", got)
			}
			_, emitted := got.Value["metric_query_params"]
			if emitted {
				t.Fatal("all sources must leave metric query construction to consumers")
			}
		})
	}
}
