package processors

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func TestMultiQueryMetricNameKeepsLegacyFirstField(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, want                string
		reverse                   bool
		secondFunction            bool
		functions, queryFunctions json.RawMessage
	}{
		{name: "multiple queries in one item", want: "usage"},
		{name: "first declared query remains the compatibility field", reverse: true, want: "idle"},
		{name: "item function has no plain metric field", functions: json.RawMessage(`[{"id":"rate"}]`)},
		{name: "query function has no plain metric field", queryFunctions: json.RawMessage(`[{"id":"rate"}]`)},
		{name: "second query function has no plain metric field", secondFunction: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &dataSliceReader{metricUnit: "percent"}
			reader.strategy = reader.strategyForTest()
			reader.strategy.Spec.Name = "CPU使用率+CPU空闲率"
			reader.strategy.Spec.StrategyItem.Expression = "A+B"
			reader.strategy.Spec.StrategyItem.Functions = tc.functions
			queries := []models.StrategyQueryConfig{
				{Alias: "A", MetricField: "usage", ResultTableID: "system.cpu_summary", AggregateMethod: "AVG", AggregatePeriod: 60},
				{Alias: "B", MetricField: "idle", ResultTableID: "system.cpu_summary", AggregateMethod: "AVG", AggregatePeriod: 60},
			}
			if tc.reverse {
				queries[0], queries[1] = queries[1], queries[0]
			}
			queries[0].Functions = tc.queryFunctions
			if tc.secondFunction {
				queries[1].Functions = json.RawMessage(`[{"id":"rate"}]`)
			}
			reader.strategy.Spec.StrategyItem.QueryConfigs = queries
			before, _ := json.Marshal(reader.strategy)
			event := processorBaseTargetAlert(t, domain.DimensionMap{})
			original := event.Clone()
			scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader, Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (Metric{}).Process(t.Context(), scope)
			if err != nil || result.Status != domain.EnrichStatusSucceeded {
				t.Fatalf("status=%s diagnostics=%v error=%v", result.Status, result.Diagnostics, err)
			}
			var values models.MetricValues
			if json.Unmarshal(mustRawObject(t, result.Value), &values) != nil {
				t.Fatal("invalid metric output")
			}
			if values.MetricName != tc.want || values.DisplayName != "CPU使用率+CPU空闲率" {
				t.Fatalf("wrong compatibility metric field or display name: %+v", values)
			}
			var params metricQueryParams
			raw, _ := json.Marshal(values.MetricQueryParams)
			if json.Unmarshal(raw, &params) != nil || len(params.QueryConfigs) != 2 || params.Expression != "A+B" {
				t.Fatalf("multi-query semantics lost: %s", raw)
			}
			for i, query := range queries {
				got := params.QueryConfigs[i]
				functions := query.Functions
				if len(functions) == 0 {
					functions = tc.functions
				}
				if got.Metrics[0].Alias != query.Alias || got.Metrics[0].Field != query.MetricField || !reflect.DeepEqual(got.Functions, rawJSONValue(functions, []any{})) {
					t.Fatalf("query member changed: %+v", got)
				}
			}
			if !reflect.DeepEqual(params.Functions, rawJSONValue(tc.functions, []any{})) {
				t.Fatalf("item functions changed: %+v", params)
			}
			after, _ := json.Marshal(reader.strategy)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(original, event) {
				t.Fatal("source event or strategy mutated")
			}
		})
	}
}
