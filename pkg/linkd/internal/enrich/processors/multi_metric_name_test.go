package processors

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func TestExternalMultiQueryMetricNameKeepsLegacyFirstField(t *testing.T) {
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
			// 外部来源同样不再生成指标查询参数。
			event.EventSourceID = "external"
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
			after, _ := json.Marshal(reader.strategy)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(original, event) {
				t.Fatal("source event or strategy mutated")
			}
		})
	}
}
