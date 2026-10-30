// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

type resourceModelSetReader struct {
	dataSliceReader
	metrics map[int64]models.MetricMetadata
}

func (r *resourceModelSetReader) FindMetric(_ context.Context, query models.MetricQuery) (models.MetricMetadata, bool, error) {
	metadata, found := r.metrics[query.MetricID]
	return metadata, found, nil
}

func TestDataResourceModelSetsAndIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name           string
		models         [][]string
		strategy       string
		root           string
		conflicting    bool
		wantModel      string
		wantDiagnostic enrich.DiagnosticCode
	}{
		{name: "multiple objects selected by strategy", models: [][]string{{"cw-MySQL", "cw-Redis"}}, strategy: "cw-MySQL", root: "dimensions", wantModel: "cw-MySQL"},
		{name: "labels supply instance", models: [][]string{{"cw-MySQL"}}, root: "labels", wantModel: "cw-MySQL"},
		{name: "unassociated metric keeps independently verified event resource", models: [][]string{{}}, root: "dimensions", wantModel: "cw-MySQL"},
		{name: "extra data supply instance", models: [][]string{{"cw-MySQL"}}, root: "extra_data", wantModel: "cw-MySQL"},
		{name: "model absent from catalog set", models: [][]string{{"cw-MySQL"}}, strategy: "cw-Redis", root: "dimensions", wantDiagnostic: "identity_conflict"},
		{name: "conflicting event locations", models: [][]string{{"cw-MySQL", "cw-Redis"}}, root: "labels", conflicting: true, wantDiagnostic: "identity_conflict"},
		{name: "two queries common model", models: [][]string{{"cw-MySQL", "cw-Redis"}, {"cw-MySQL"}}, root: "dimensions", wantModel: "cw-MySQL"},
		{name: "query order reversed", models: [][]string{{"cw-MySQL"}, {"cw-MySQL", "cw-Redis"}}, root: "dimensions", wantModel: "cw-MySQL"},
		{name: "conflicting input models", models: [][]string{{"cw-Redis"}, {"cw-MySQL"}}, root: "dimensions", wantDiagnostic: "identity_conflict"},
		{name: "multiple objects without selected context", models: [][]string{{"cw-MySQL", "cw-Redis"}}, wantDiagnostic: enrich.DiagnosticCodeMissingField},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &resourceModelSetReader{dataSliceReader: dataSliceReader{model: "cw-MySQL", found: true}, metrics: map[int64]models.MetricMetadata{}}
			r.strategy = r.strategyForTest()
			r.strategy.ObjectModelCode = optionalModelCode(tc.strategy)
			r.strategy.Spec.StrategyItem.QueryConfigs = nil
			for i, ids := range tc.models {
				id := int64(i + 42)
				r.strategy.Spec.StrategyItem.QueryConfigs = append(r.strategy.Spec.StrategyItem.QueryConfigs, models.StrategyQueryConfig{MetricRefID: id, ResultTableID: "plugin.mysql", MetricField: "usage"})
				r.metrics[id] = models.MetricMetadata{MetricsetCode: "plugin.mysql", Kind: "native", ModelIDs: ids, FieldName: "usage", PhysicalField: "usage", TableID: "plugin.mysql"}
			}
			event := processorBaseTargetAlert(t, domain.DimensionMap{})
			identityModel := "cw-MySQL"
			if tc.strategy != "" {
				identityModel = tc.strategy
			}
			identity := domain.DimensionMap{"model_id": domain.NewStringScalar(identityModel), "model_inst_id": domain.NewStringScalar("service-17")}
			switch tc.root {
			case "dimensions":
				event.Dimensions = identity
			case "labels":
				for k, v := range identity {
					event.Labels[k] = v
				}
			case "extra_data":
				for k, v := range identity {
					event.ExtraData[k], _ = json.Marshal(v)
				}
			}
			if tc.conflicting {
				event.Dimensions = domain.DimensionMap{"model_id": domain.NewStringScalar("cw-Redis"), "model_inst_id": domain.NewStringScalar("redis-1")}
			}
			r.instance = enrich.Instance{TenantID: event.BKTenantID, ModelCode: "cw-MySQL", InstanceID: "service-17", Fields: map[string]any{"entity_uid": "cw-MySQL|service-17", "bk_biz_id": int64(2)}}
			before := event.Clone()
			scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: r, Metric: r, Model: r, OneModel: r})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (MonitorResource{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantDiagnostic != "" {
				found := false
				for _, d := range result.Diagnostics {
					if d.Code == tc.wantDiagnostic {
						found = true
					}
					if d.Dependency == rules.DependencyMetricCatalog {
						t.Fatalf("identity conflict reported as catalog failure: %+v", result)
					}
				}
				if !found || result.Status == domain.EnrichStatusSucceeded {
					t.Fatalf("missing conflict diagnostic: %+v", result)
				}
			} else if result.Status != domain.EnrichStatusSucceeded || string(result.Value["model_id"]) != `"`+tc.wantModel+`"` || string(result.Value["model_inst_id"]) != `"service-17"` {
				t.Fatalf("result=%+v", result)
			}
			if !reflect.DeepEqual(event, before) {
				t.Fatal("source event changed")
			}
		})
	}
}
