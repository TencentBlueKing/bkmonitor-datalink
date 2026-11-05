package processors

import (
	"encoding/json"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"reflect"
	"testing"
)

func TestDerivedMetricDoesNotRequireExecutionQueryPosition(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"built_in_bk", "external"} {
		t.Run(source, func(t *testing.T) {
			reader := &metricLocationReader{metadata: models.MetricMetadata{Kind: "derived", FieldName: "container_memory_resource_limits_ratio", FieldCNName: "限制内存使用率", Unit: "percent"}}
			reader.strategy = reader.strategyForTest()
			reader.strategy.Spec.TableID = ""
			reader.strategy.Spec.Name = "限制内存使用率"
			reader.strategy.Spec.FieldName = reader.metadata.FieldName
			reader.strategy.Spec.StrategyItem.Expression = "a"
			reader.strategy.Spec.StrategyItem.QueryConfigs = []models.StrategyQueryConfig{{QueryConfigType: "catalog", MetricRefID: 219, MetricField: reader.metadata.FieldName, Alias: "a", AggregatePeriod: 60}}
			before, _ := json.Marshal(reader.strategy)
			event := processorBaseTargetAlert(t, domain.DimensionMap{})
			event.EventSourceID = source
			original := event.Clone()
			scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: reader, Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			got, err := (Metric{}).Process(t.Context(), scope)
			if err != nil || got.Status != domain.EnrichStatusSucceeded {
				t.Fatalf("status=%s error=%v", got.Status, err)
			}
			if _, exists := got.Value["metric_unique_id"]; exists {
				t.Fatal("removed identity was generated")
			}
			var values models.MetricValues
			if err = json.Unmarshal(mustRawObject(t, got.Value), &values); err != nil {
				t.Fatal(err)
			}
			if values.MetricName != reader.metadata.FieldName || values.ResultTableID != "" || values.Unit != "percent" || values.DisplayName != "限制内存使用率" {
				t.Fatalf("metric=%+v", values)
			}
			after, _ := json.Marshal(reader.strategy)
			if string(before) != string(after) || !reflect.DeepEqual(original, event) {
				t.Fatal("strategy or event changed")
			}
		})
	}
}
