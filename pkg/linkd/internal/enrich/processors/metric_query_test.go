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

func TestMetricQueryIdentityExpression(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, expression, alias, want string
		functions, queryFunctions     json.RawMessage
		multi, derived, promql        bool
	}{
		{name: "promql default alias", expression: "A", promql: true},
		{name: "promql unknown alias", expression: "B", promql: true, want: "B"},
		{name: "promql calculation", expression: "A*100", promql: true, want: "A*100"},
		{name: "promql multiple", expression: "A", promql: true, multi: true, want: "A"},
		{name: "promql query function", expression: "A", promql: true, queryFunctions: json.RawMessage(`[{"id":"rate"}]`), want: "A"},
		{name: "promql derived", expression: "A", promql: true, derived: true, want: "A"},
		{name: "single alias", expression: "a", alias: "a"},
		{name: "uppercase alias", expression: "A", alias: "A"},
		{name: "whitespace", expression: " a ", alias: "a"},
		{name: "empty expression", alias: "a"},
		{name: "different alias", expression: "b", alias: "a", want: "b"},
		{name: "missing alias", expression: "a", want: "a"},
		{name: "calculation", expression: "a*100", alias: "a", want: "a*100"},
		{name: "multiple queries", expression: "a", alias: "a", multi: true, want: "a"},
		{name: "global function", expression: "a", alias: "a", functions: json.RawMessage(`[{"id":"rate"}]`), want: "a"},
		{name: "query function", expression: "a", alias: "a", queryFunctions: json.RawMessage(`[{"id":"rate"}]`), want: "a"},
		{name: "derived definition", expression: "a", alias: "a", derived: true, want: "a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			queries := []models.StrategyQueryConfig{{Alias: tc.alias, MetricField: "usage", ResultTableID: "system.cpu_detail", Functions: tc.queryFunctions}}
			if tc.promql {
				queries[0].QueryConfigType = "prometheus"
				queries[0].PromQL = "up"
			}
			if tc.multi {
				queries = append(queries, models.StrategyQueryConfig{Alias: "b"})
			}
			strategy := models.CWStrategy{}
			if tc.derived {
				strategy.Spec.FieldTag = models.CWStrategyFieldTagDerivedMetric
			}
			projection := models.StrategyItemProjection{Expression: tc.expression, QueryConfigs: queries, Functions: tc.functions}
			got := buildMetricQueryParams(projection, strategy, domain.DimensionMap{}, 2)
			if got.Expression != tc.want {
				t.Fatalf("expression=%q want=%q", got.Expression, tc.want)
			}
		})
	}
}

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
			if value.MetricQueryParams == nil {
				t.Fatal("builtin compatibility query parameters are missing")
			}
			var params metricQueryParams
			encoded, _ := json.Marshal(value.MetricQueryParams)
			if err := json.Unmarshal(encoded, &params); err != nil {
				t.Fatal(err)
			}
			if params.Expression != "" || len(params.QueryConfigs) != 1 || params.QueryConfigs[0].Table != wantTable || params.QueryConfigs[0].Metrics[0].Field != tc.physical {
				t.Fatalf("query=%#v", params)
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

func TestMetricCompatibilityParametersPreserveFactsForAllSources(t *testing.T) {
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
			if values.MetricQueryParams == nil {
				t.Fatal("compatibility query parameters are missing")
			}
		})
	}
}
