// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"context"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

type apmMetricSourceReader struct {
	apmCatalogTestReader
	metadata models.MetricMetadata
	err      error
}

func (r apmMetricSourceReader) FindMetric(context.Context, models.MetricQuery) (models.MetricMetadata, bool, error) {
	return r.metadata, r.err == nil, r.err
}

func TestAPMCatalogActualSourceAndModels(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, definition, source string
		models                   []string
		wantModel, wantID        string
		conflict, readFailure    bool
	}{
		{name: "single application source", definition: "apm_application.17", source: "apm_application.17", models: []string{rules.APMApplicationModelCode, rules.APMServiceModelCode, rules.APMServiceInstanceModelCode}, wantModel: rules.APMServiceModelCode, wantID: "17|checkout"},
		{name: "predefined actual APM source", definition: "manual.test", source: "apm_application.17", models: []string{rules.APMServiceModelCode}, wantModel: rules.APMServiceModelCode, wantID: "17|checkout"},
		{name: "single applicable application", definition: "apm", models: []string{rules.APMApplicationModelCode}, wantModel: rules.APMApplicationModelCode, wantID: "17"},
		{name: "wrong applicable model", definition: "apm", models: []string{"cw-Host"}, conflict: true},
		{name: "catalog unavailable", definition: "apm", readFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := apmMetricSourceReader{apmCatalogTestReader: apmCatalogTestReader{strategy: apmCatalogStrategy(t)}, metadata: models.MetricMetadata{Kind: "native", MetricsetCode: tc.definition, RefMetricsetCode: tc.source, ModelIDs: tc.models, FieldName: "duration", PhysicalField: "duration", DataLabel: tc.source}}
			r.strategy.Spec.StrategyItem.QueryConfigs[0].MetricsetCode = "manual.test"
			if tc.readFailure {
				r.err = errors.New("catalog unavailable")
			}
			event := processorBaseTargetAlert(t, domain.DimensionMap{"app_name": domain.NewStringScalar("demo"), "service_name": domain.NewStringScalar("checkout")})
			for _, processor := range []enrich.Processor{MonitorResource{}, APM{}} {
				scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: r, Metric: r, APMApplication: r, Business: r, OneModel: r, Model: r})
				if err != nil {
					t.Fatal(err)
				}
				result, err := processor.Process(t.Context(), scope)
				if err != nil {
					t.Fatal(err)
				}
				if tc.conflict || tc.readFailure {
					if result.Status == domain.EnrichStatusSucceeded || result.Status == domain.EnrichStatusSkipped || len(result.Diagnostics) == 0 {
						t.Fatalf("%s hid failure: %+v", processor.Name(), result)
					}
					if tc.conflict && result.Diagnostics[0].Code != "identity_conflict" {
						t.Fatalf("wrong conflict: %+v", result)
					}
					continue
				}
				if result.Status != domain.EnrichStatusSucceeded || string(result.Value["model_id"]) != `"`+tc.wantModel+`"` || string(result.Value["model_inst_id"]) != `"`+tc.wantID+`"` {
					t.Fatalf("%s result=%+v", processor.Name(), result)
				}
			}
		})
	}
}

func TestAPMActualNonAPMSourceOverridesSnapshot(t *testing.T) {
	r := apmMetricSourceReader{apmCatalogTestReader: apmCatalogTestReader{strategy: apmCatalogStrategy(t)}, metadata: models.MetricMetadata{MetricsetCode: "manual.test", RefMetricsetCode: "plugin.mysql", FieldName: "usage"}}
	scope, err := enrich.NewScope(processorBaseTargetAlert(t, domain.DimensionMap{}), enrich.Sources{CWStrategy: r, Metric: r})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (APM{}).Process(t.Context(), scope)
	if err != nil || result.Status != domain.EnrichStatusSkipped {
		t.Fatalf("stale snapshot selected APM: %+v err=%v", result, err)
	}
}

type apmMixedMetricReader struct {
	apmCatalogTestReader
	differentApplications bool
}

func (r apmMixedMetricReader) FindMetric(_ context.Context, q models.MetricQuery) (models.MetricMetadata, bool, error) {
	code := "apm"
	if q.MetricID == 43 {
		code = "plugin.mysql"
	}
	if r.differentApplications {
		code = "apm_application.17"
		if q.MetricID == 43 {
			code = "apm_application.18"
		}
	}
	return models.MetricMetadata{MetricsetCode: code, RefMetricsetCode: code, FieldName: "usage"}, true, nil
}

func TestAPMMixedSourcesDoNotSelectFirstInput(t *testing.T) {
	for _, differentApplications := range []bool{false, true} {
		for _, ids := range [][]int64{{42, 43}, {43, 42}} {
			r := apmMixedMetricReader{apmCatalogTestReader: apmCatalogTestReader{strategy: apmCatalogStrategy(t)}}
			r.differentApplications = differentApplications
			r.strategy.Spec.StrategyItem.QueryConfigs = nil
			for _, id := range ids {
				r.strategy.Spec.StrategyItem.QueryConfigs = append(r.strategy.Spec.StrategyItem.QueryConfigs, models.StrategyQueryConfig{MetricRefID: id})
			}
			for _, p := range []enrich.Processor{MonitorResource{}, APM{}} {
				scope, err := enrich.NewScope(processorBaseTargetAlert(t, domain.DimensionMap{}), enrich.Sources{CWStrategy: r, Metric: r})
				if err != nil {
					t.Fatal(err)
				}
				result, err := p.Process(t.Context(), scope)
				if err != nil || result.Status != domain.EnrichStatusFailed || len(result.Diagnostics) != 1 || result.Diagnostics[0].Code != "identity_conflict" {
					t.Fatalf("%s selected first source: %+v err=%v", p.Name(), result, err)
				}
			}
		}
	}
}

type apmInterfaceReader struct {
	apmMetricSourceReader
	appID int64
}

func (r apmInterfaceReader) GetModelByCode(ctx context.Context, tenant, code string) (enrich.Model, bool, error) {
	if code != rules.APMInterfaceModelCode {
		return r.apmTestReader.GetModelByCode(ctx, tenant, code)
	}
	return enrich.Model{TenantID: tenant, ModelCode: code, ModelID: "interface-model", Fields: map[string]any{"object_model_name": "接口"}}, true, nil
}

func (r apmInterfaceReader) FindInstance(ctx context.Context, tenant string, q enrich.InstanceQuery) (enrich.Instance, bool, error) {
	if q.ModelCode != rules.APMInterfaceModelCode {
		return r.apmTestReader.FindInstance(ctx, tenant, q)
	}
	return enrich.Instance{TenantID: tenant, ModelCode: q.ModelCode, InstanceID: q.InstanceID, Fields: map[string]any{"entity_uid": q.ModelCode + "|" + q.InstanceID}, Attributes: map[string]any{"app_id": r.appID, "service_name": "checkout"}}, true, nil
}

func TestAPMInterfaceUsesVerifiedCanonicalIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		appID    int64
		identity bool
		want     domain.EnrichStatus
	}{
		{name: "canonical interface", appID: 17, identity: true, want: domain.EnrichStatusSucceeded},
		{name: "other application", appID: 18, identity: true, want: domain.EnrichStatusFailed},
		{name: "span name cannot identify interface", appID: 17, want: domain.EnrichStatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := apmInterfaceReader{apmMetricSourceReader: apmMetricSourceReader{apmCatalogTestReader: apmCatalogTestReader{strategy: apmCatalogStrategy(t)}, metadata: models.MetricMetadata{MetricsetCode: "apm", ModelIDs: []string{rules.APMInterfaceModelCode}, FieldName: "duration"}}, appID: tc.appID}
			event := processorBaseTargetAlert(t, domain.DimensionMap{"app_name": domain.NewStringScalar("demo"), "service_name": domain.NewStringScalar("checkout"), "span_name": domain.NewStringScalar("GET /orders")})
			if tc.identity {
				event.Dimensions["model_id"] = domain.NewStringScalar(rules.APMInterfaceModelCode)
				event.Dimensions["model_inst_id"] = domain.NewStringScalar("17|checkout|GET /orders|http|1")
			}
			for _, p := range []enrich.Processor{MonitorResource{}, APM{}} {
				scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: r, Metric: r, OneModel: r, Model: r, Business: r, APMApplication: r})
				if err != nil {
					t.Fatal(err)
				}
				result, err := p.Process(t.Context(), scope)
				if err != nil || result.Status != tc.want {
					t.Fatalf("%s result=%+v err=%v", p.Name(), result, err)
				}
				if tc.want == domain.EnrichStatusSucceeded && string(result.Value["model_inst_id"]) != `"17|checkout|GET /orders|http|1"` {
					t.Fatalf("interface identity changed: %+v", result)
				}
			}
		})
	}
}
