// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestAPMCatalogResourceAndDisplay(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, service, instance, model, id string }{
		{name: "application", model: "cw-application", id: "17"},
		{name: "service", service: "checkout", model: "cw-service", id: "17|checkout"},
		{name: "instance", service: "checkout", instance: "instance-a", model: "cw-service_instance", id: "17|checkout|instance-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := apmCatalogTestReader{strategy: apmCatalogStrategy(t)}
			event := processorBaseTargetAlert(t, domain.DimensionMap{
				"app_name":               domain.NewStringScalar("demo"),
				rules.FieldServiceName:   domain.NewStringScalar(tc.service),
				rules.FieldAPMInstanceID: domain.NewStringScalar(tc.instance),
			})
			event.SubjectSystem, event.SubjectType, event.SubjectID = "apm", "service", "demo:checkout"
			before := event.Clone()
			chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, APM{}, Display{}}, enrich.Sources{
				CWStrategy: reader, Model: reader, OneModel: reader, APMApplication: reader, Business: reader, Metric: reader,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			for i, name := range []string{rules.MonitorResourceProcessor, rules.APMProcessor, rules.DisplayProcessor} {
				output := payload.Processors[i][name]
				if output.Status != domain.EnrichStatusSucceeded {
					t.Fatalf("%s: %+v", name, output)
				}
				if i < 2 && (string(output.Value["model_id"]) != `"`+tc.model+`"` || string(output.Value["model_inst_id"]) != `"`+tc.id+`"`) {
					t.Fatalf("%s identity: %+v", name, output)
				}
			}
			if got := string(payload.Processors[2][rules.DisplayProcessor].Value["object"]); got != `"`+tc.service+`"` {
				t.Fatalf("object=%s", got)
			}
			for _, value := range []string{tc.service, tc.instance} {
				if value != "" && strings.Contains(string(payload.Processors[2][rules.DisplayProcessor].Value["dimension_text"]), value) {
					t.Fatal("APM dimensions duplicated outside APM output")
				}
			}
			if !reflect.DeepEqual(event, before) {
				t.Fatal("enrichment changed source Event")
			}
		})
	}
}

func TestAPMCatalogDependencyFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reader     apmTestReader
		dimensions domain.DimensionMap
	}{
		{name: "application read failure", reader: apmTestReader{appErr: errors.New("application unavailable")}, dimensions: domain.DimensionMap{"app_name": domain.NewStringScalar("demo")}},
		{name: "other tenant", reader: apmTestReader{apps: []models.APMApplication{{TenantID: "other", ID: 17, Name: "demo", BKBizID: 2}}}, dimensions: domain.DimensionMap{"app_name": domain.NewStringScalar("demo")}},
		{name: "missing application name"},
		{name: "other business", reader: apmTestReader{apps: []models.APMApplication{{TenantID: "tenant-a", ID: 17, Name: "demo", BKBizID: 3}}}, dimensions: domain.DimensionMap{"app_name": domain.NewStringScalar("demo")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := apmCatalogTestReader{apmTestReader: tc.reader, strategy: apmCatalogStrategy(t)}
			chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, APM{}}, enrich.Sources{CWStrategy: reader, Model: reader, OneModel: reader, APMApplication: reader, Business: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: processorBaseTargetAlert(t, tc.dimensions)})
			if err != nil {
				t.Fatal(err)
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			for i, name := range []string{rules.MonitorResourceProcessor, rules.APMProcessor} {
				output := payload.Processors[i][name]
				if output.Status == domain.EnrichStatusSucceeded || output.Status == domain.EnrichStatusSkipped || len(output.Diagnostics) == 0 {
					t.Fatalf("%s: %+v", name, output)
				}
				if hasDiagnosticDependency(output.Diagnostics, rules.DependencyMetricCatalog) {
					t.Fatalf("APM failure misreported as metric_catalog: %+v", output)
				}
			}
		})
	}
}

func apmCatalogStrategy(t *testing.T) models.CWStrategy {
	t.Helper()
	var strategy models.CWStrategy
	// 与历史发布材料一致：衍生指标通过目录引用，不携带物理结果表或模型。
	if err := json.Unmarshal([]byte(`{"spec":{"config_type":"data","data_source":"指标目录","field_tag":"derived_metric","field_name":"average_response_time","strategy_item":{"query_configs":[{"query_config_type":"catalog","metric_ref_id":10539,"metricset_code":"apm","metric_field":"average_response_time"}]}}}`), &strategy); err != nil {
		t.Fatal(err)
	}
	return strategy
}

type apmCatalogTestReader struct {
	apmTestReader
	strategy models.CWStrategy
}

func (r apmCatalogTestReader) GetByStrategyID(context.Context, models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
	return r.strategy, true, nil
}
