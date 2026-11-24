// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
)

func TestMetricCatalogDefinitionKinds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		kind         string
		table, field string
		failed       bool
		legacyTag    bool
	}{
		{name: "bound predefined", kind: "predefined", table: "system.cpu", field: "usage"},
		{name: "unbound predefined", kind: "predefined", failed: true},
		{name: "inline derived", kind: "derived"},
		{name: "inline derived resolves existing derived tag", kind: "derived", legacyTag: true},
		{name: "derived with physical reference rejected", kind: "derived", table: "record_rule.result", field: "result_usage", failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &metricLocationReader{metadata: models.MetricMetadata{MetricsetCode: "manual.test", Kind: tc.kind, FieldName: "result_usage", FieldCNName: "使用率", Unit: "percent", TableID: tc.table, PhysicalField: tc.field}}
			r.strategy = r.strategyForTest()
			r.strategy.Spec.TableID = ""
			r.strategy.Spec.StrategyItem.QueryConfigs = []models.StrategyQueryConfig{{MetricRefID: 42, QueryConfigType: "catalog", Alias: "a"}}
			r.strategy.Spec.StrategyItem.Expression = "a"

			if tc.legacyTag {
				r.strategy.Spec.FieldTag = models.CWStrategyFieldTagDerivedMetric
				r.strategy.Spec.FieldName = "stale_name"
			}
			before, _ := json.Marshal(r.strategy)
			scope, err := enrich.NewScope(processorBaseTargetAlert(t, domain.DimensionMap{}), enrich.Sources{CWStrategy: r, Metric: r})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (Metric{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if tc.failed {
				if result.Status != domain.EnrichStatusFailed {
					t.Fatalf("invalid definition accepted: %+v", result)
				}
				return
			}
			if result.Status != domain.EnrichStatusSucceeded {
				t.Fatalf("valid definition rejected: %+v", result)
			}
			var values models.MetricValues
			if json.Unmarshal(mustRawObject(t, result.Value), &values) != nil {
				t.Fatal("invalid metric values")
			}
			if values.ResultTableID != tc.table || values.Unit != "percent" {
				t.Fatalf("values=%+v", values)
			}
			if _, exists := result.Value["metric_unique_id"]; exists {
				t.Fatal("removed metric identity was generated")
			}
			if tc.legacyTag && values.MetricName != "result_usage" {
				t.Fatalf("stale definition name used: %+v", values)
			}
			after, _ := json.Marshal(r.strategy)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("published strategy changed")
			}
		})
	}
}
