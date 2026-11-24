// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestK8sCustomCatalogWithoutModel(t *testing.T) {
	t.Parallel()
	const code = "k8s_custom"
	for _, scenario := range []string{"reported pod and container", "no resource dimensions", "OneModel unavailable"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			reader := newK8sCustomCatalogReader(code)
			// 发布快照没有来源编码时，仍须按命中的目录行识别来源。
			reader.strategy.Spec.StrategyItem.QueryConfigs[0].MetricsetCode = ""
			dimensions := domain.DimensionMap{
				rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"),
				rules.FieldNamespace:    domain.NewStringScalar("default"),
				"pod":                   domain.NewStringScalar("databus-0"),
				"container":             domain.NewStringScalar("databus"),
			}
			k8s := &k8sTestReader{contexts: k8sTestContexts()}
			wantK8s, wantOverall := domain.EnrichStatusSucceeded, domain.EnrichStatusSucceeded
			switch scenario {
			case "no resource dimensions":
				dimensions = domain.DimensionMap{}
				wantK8s = domain.EnrichStatusSkipped
			case "OneModel unavailable":
				k8s.contexts = nil
				k8s.err = errors.New("OneModel unavailable")
				wantK8s, wantOverall = domain.EnrichStatusPartial, domain.EnrichStatusPartial
			}
			event := processorBaseTargetAlert(t, dimensions)
			before := event.Clone()
			chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, K8s{}, Metric{}}, enrich.Sources{
				CWStrategy: reader, Metric: reader, K8s: k8s,
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
			resource := payload.Processors[0][rules.MonitorResourceProcessor]
			k8sOutput := payload.Processors[1][rules.K8sProcessor]
			metric := payload.Processors[2][rules.MetricProcessor]
			if resource.Status != domain.EnrichStatusSkipped || len(resource.Diagnostics) != 0 || len(resource.Patches) != 0 {
				t.Fatalf("directory model absence must not fail or publish resource identity: %+v", resource)
			}
			if result.Status != wantOverall || k8sOutput.Status != wantK8s || metric.Status != domain.EnrichStatusSucceeded {
				t.Fatalf("overall=%s k8s=%+v metric=%+v", result.Status, k8sOutput, metric)
			}
			if scenario == "reported pod and container" {
				// 本次不把 pod/container 当作 pod_name/container_name，保持 Namespace 定位。
				if string(k8sOutput.Value["model_id"]) != `"cw-K8s_Namespace"` || string(k8sOutput.Value["model_inst_id"]) != `"cluster-1|default"` {
					t.Fatalf("K8s identity changed: %+v", k8sOutput)
				}
			}
			if scenario == "OneModel unavailable" && !hasDiagnosticDependency(k8sOutput.Diagnostics, rules.DependencyOneModel) {
				t.Fatalf("OneModel failure hidden: %+v", k8sOutput)
			}
			if string(metric.Value["metric_name"]) != `"batch_num"` || string(metric.Value["result_table_id"]) != `"`+code+`"` {
				t.Fatalf("metric location changed: %+v", metric)
			}
			if len(reader.queries) != 1 || reader.queries[0].TenantID != event.BKTenantID || reader.queries[0].MetricID != 42 {
				t.Fatalf("directory identity or reuse changed: %+v", reader.queries)
			}
			if !reflect.DeepEqual(event, before) {
				t.Fatal("source Event changed")
			}
		})
	}
}

func TestK8sCustomCatalogKeepsDirectoryFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*k8sCustomCatalogReader)
	}{
		{name: "missing catalog row", change: func(r *k8sCustomCatalogReader) { r.found = false }},
		{name: "catalog read failure", change: func(r *k8sCustomCatalogReader) { r.err = errors.New("catalog unavailable") }},
		{name: "missing catalog source", change: func(r *k8sCustomCatalogReader) { r.metadata.MetricsetCode = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := newK8sCustomCatalogReader("k8s_custom")
			tc.change(reader)
			scope, err := enrich.NewScope(processorBaseTargetAlert(t, domain.DimensionMap{}), enrich.Sources{CWStrategy: reader, Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			output, err := (MonitorResource{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if output.Status != domain.EnrichStatusFailed || !hasDiagnosticDependency(output.Diagnostics, rules.DependencyMetricCatalog) {
				t.Fatalf("directory failure bypassed by snapshot source: %+v", output)
			}
		})
	}
}

type k8sCustomCatalogReader struct {
	strategy models.CWStrategy
	metadata models.MetricMetadata
	found    bool
	err      error
	queries  []models.MetricQuery
}

func newK8sCustomCatalogReader(code string) *k8sCustomCatalogReader {
	bizID := int64(2)
	return &k8sCustomCatalogReader{
		strategy: models.CWStrategy{BKBizID: &bizID, Spec: models.CWStrategySpec{
			ConfigType: models.CWStrategyConfigTypeData, Name: "batch_num",
			StrategyItem: &models.CWStrategyItem{QueryConfigs: []models.StrategyQueryConfig{{MetricRefID: 42, MetricsetCode: code, MetricField: "batch_num"}}},
		}},
		metadata: models.MetricMetadata{MetricsetCode: code, Kind: "native", FieldName: "batch_num", PhysicalField: "batch_num", DataLabel: code},
		found:    true,
	}
}

func (r *k8sCustomCatalogReader) GetByStrategyID(context.Context, models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
	return r.strategy, true, nil
}

func (r *k8sCustomCatalogReader) FindMetric(_ context.Context, query models.MetricQuery) (models.MetricMetadata, bool, error) {
	r.queries = append(r.queries, query)
	return r.metadata, r.found, r.err
}

func TestUnassociatedCatalogSourcesDoNotRequireModel(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"k8s", "k8s_custom", "k8s_cluster.bcs_k8s_00000", "source.demo", "external.metrics", "report.xx_test", "report.other"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			reader := newK8sCustomCatalogReader(code)
			scope, err := enrich.NewScope(processorBaseTargetAlert(t, domain.DimensionMap{}), enrich.Sources{CWStrategy: reader, Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (MonitorResource{}).Process(t.Context(), scope)
			if err != nil || result.Status != domain.EnrichStatusSkipped || len(result.Diagnostics) != 0 || len(result.Value) != 0 {
				t.Fatalf("valid unassociated definition failed: %+v err=%v", result, err)
			}
		})
	}
}
