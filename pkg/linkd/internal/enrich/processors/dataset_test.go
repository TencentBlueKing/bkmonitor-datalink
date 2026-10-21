package processors

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func TestMetricBoundDatasetPreservesQuerySemantics(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ expression, want string }{{"a", ""}, {"a * 100", "a * 100"}} {
		t.Run(tc.expression, func(t *testing.T) {
			reader := &metricLocationReader{metadata: models.MetricMetadata{Kind: "native", FieldName: "usage", PhysicalField: "usage", TableID: "system.cpu_summary", Unit: "percent"}}
			reader.strategy = reader.strategyForTest()
			reader.strategy.Spec.FieldName, reader.strategy.Spec.TableID = "cpu_usage", ""
			reader.strategy.Spec.Name = "CPU使用率"
			reader.strategy.Spec.StrategyItem.Expression = tc.expression
			// Reader 已证明语义字段到物理位置的绑定；Raw 仍是原始声明。
			reader.strategy.Spec.StrategyItem.QueryConfigs = []models.StrategyQueryConfig{{
				QueryConfigType: "dataset", DatasetCode: "infra.system", Alias: "a", MetricField: "usage", ResultTableID: "system.cpu_summary",
				AggregateMethod: "AVG", AggregatePeriod: 60, AggregateBy: []string{"bk_target_ip"},
				AggregateFilter: json.RawMessage(`[{"key":"hostname","method":"eq","value":["host"]}]`),
				Raw:             domain.JSONObject{"metric_field": json.RawMessage(`"cpu_usage"`)},
			}}
			event := processorBaseTargetAlert(t, domain.DimensionMap{"bk_target_ip": domain.NewStringScalar("127.0.0.1")})
			before := event.Clone()
			strategyBefore, _ := json.Marshal(reader.strategy)
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
				t.Fatal("invalid metric values")
			}
			if values.MetricName != "usage" || values.DisplayName != "CPU使用率" || values.Unit != "percent" || values.MetricUniqueID != "system.cpu_summary.usage" {
				t.Fatalf("wrong dataset output: %+v", values)
			}
			var params metricQueryParams
			raw, _ := json.Marshal(values.MetricQueryParams)
			if json.Unmarshal(raw, &params) != nil || len(params.QueryConfigs) != 1 {
				t.Fatal("invalid metric query parameters")
			}
			query := params.QueryConfigs[0]
			if params.Expression != tc.want || query.Table != "system.cpu_summary" || query.Metrics[0].Field != "usage" || query.Metrics[0].Alias != "a" || query.Metrics[0].Method != "AVG" || query.Interval != 60 || !reflect.DeepEqual(query.GroupBy, []string{"bk_target_ip"}) {
				t.Fatalf("query semantics changed: %+v", params)
			}
			if len(query.Where) != 2 || query.Where[0].Key != "hostname" || !reflect.DeepEqual(query.Where[0].Value, []any{"host"}) || query.Where[1].Key != "bk_target_ip" {
				t.Fatalf("query filters changed: %+v", query.Where)
			}
			if len(reader.queries) != 1 || reader.queries[0].TableID != "system.cpu_summary" || reader.queries[0].FieldName != "usage" || reader.queries[0].TenantID != event.BKTenantID {
				t.Fatalf("directory lookup used semantic field: %+v", reader.queries)
			}
			strategyAfter, _ := json.Marshal(reader.strategy)
			if !reflect.DeepEqual(before, event) || !reflect.DeepEqual(strategyBefore, strategyAfter) {
				t.Fatal("source event or strategy changed")
			}
		})
	}
}
