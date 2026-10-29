package processors

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func TestDerivedMetricUsesFirstExecutionQueryIdentity(t *testing.T) {
	t.Parallel()
	for _, source := range []string{"built_in_bk", "external"} {
		for _, tc := range []struct {
			name   string
			query  *models.StrategyQueryConfig
			want   string
			failed bool
		}{
			{name: "first input differs from parent", query: &models.StrategyQueryConfig{ResultTableID: "k8s", MetricField: "container_memory_rss"}, want: "k8s.container_memory_rss"},
			{name: "reversed inputs", query: &models.StrategyQueryConfig{ResultTableID: "k8s", MetricField: "kube_pod_container_resource_limits_memory_bytes"}, want: "k8s.kube_pod_container_resource_limits_memory_bytes"},
			{name: "empty table remains a leading dot", query: &models.StrategyQueryConfig{MetricField: "container_memory_rss"}, want: ".container_memory_rss"},
			{name: "promql identity", query: &models.StrategyQueryConfig{PromQL: "sum(bkmonitor:system:mem:used{})"}, want: "system.mem.used"},
			{name: "missing first query", failed: true},
			{name: "empty first field cannot use parent", query: &models.StrategyQueryConfig{ResultTableID: "k8s"}, failed: true},
			{name: "ambiguous promql", query: &models.StrategyQueryConfig{PromQL: "bkmonitor:system:mem:used + bkmonitor:system:mem:free"}, failed: true},
		} {
			t.Run(source+"/"+tc.name, func(t *testing.T) {
				reader := &metricLocationReader{metadata: models.MetricMetadata{Kind: "derived", FieldName: "container_memory_resource_limits_ratio", FieldCNName: "限制内存使用率", Unit: "percent"}}
				reader.strategy = reader.strategyForTest()
				reader.strategy.FirstExecutionQuery = tc.query
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
				if tc.failed {
					if err == nil && got.Status != domain.EnrichStatusFailed {
						t.Fatalf("invalid first query accepted: %+v", got)
					}
				} else {
					if err != nil || got.Status != domain.EnrichStatusSucceeded {
						t.Fatalf("status=%s error=%v", got.Status, err)
					}
					var values models.MetricValues
					if err := json.Unmarshal(mustRawObject(t, got.Value), &values); err != nil {
						t.Fatal(err)
					}
					if values.MetricUniqueID != tc.want || values.MetricName != reader.metadata.FieldName || values.ResultTableID != "" || values.Unit != "percent" || values.DisplayName != "限制内存使用率" {
						t.Fatalf("metric=%+v want_unique=%s", values, tc.want)
					}
					if values.MetricQueryParams != nil {
						t.Fatal("all derived metrics must leave query construction to consumers")
					}
				}
				after, _ := json.Marshal(reader.strategy)
				if string(before) != string(after) || !reflect.DeepEqual(original, event) {
					t.Fatal("historical strategy or event changed")
				}
			})
		}
	}
}
