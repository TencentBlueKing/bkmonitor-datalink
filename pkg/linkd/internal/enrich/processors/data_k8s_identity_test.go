package processors

import (
	"errors"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
	"linkd/internal/enrich/view"
)

func TestDataK8sNamespaceAggregationUsesEventIdentity(t *testing.T) {
	t.Parallel()
	reader := &resourceModelSetReader{
		dataSliceReader: dataSliceReader{model: "cw-K8s_Container"},
		metrics: map[int64]models.MetricMetadata{121: {
			MetricsetCode: "k8s", Kind: "native", ModelIDs: []string{"cw-K8s_Container"},
			FieldName: "container_cpu_usage_seconds_total", PhysicalField: "container_cpu_usage_seconds_total", DataLabel: "k8s",
		}},
	}
	reader.strategy = reader.strategyForTest()
	reader.strategy.ObjectModelCode = nil
	reader.strategy.Spec.StrategyItem.QueryConfigs = []models.StrategyQueryConfig{{
		MetricRefID: 121, AggregateMethod: "SUM", AggregateBy: []string{"bcs_cluster_id", "namespace"},
	}}
	event := processorBaseTargetAlert(t, domain.DimensionMap{
		"bcs_cluster_id": domain.NewStringScalar("cluster-1"), "namespace": domain.NewStringScalar("default"),
	})
	original := event.Clone()
	k8s := &k8sTestReader{contexts: k8sTestContexts()}
	chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, K8s{}, Metric{}}, enrich.Sources{
		CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, K8s: k8s,
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
	for _, entry := range payload.Processors {
		for name, step := range entry {
			if step.Status != domain.EnrichStatusSucceeded || len(step.Diagnostics) != 0 {
				t.Errorf("%s status=%s diagnostics=%+v", name, step.Status, step.Diagnostics)
			}
		}
	}
	if !reflect.DeepEqual(event, original) {
		t.Fatal("enrichment changed source Event")
	}
	event.Enrich = result.Data
	effective, err := view.EnrichedEvent(event, "warning")
	if err != nil {
		t.Fatal(err)
	}
	if got := dimensionText(effective.Labels, rules.FieldModelID); got != "cw-K8s_Namespace" {
		t.Errorf("model_id=%q want=cw-K8s_Namespace", got)
	}
	if got := dimensionText(effective.Labels, rules.FieldModelInstID); got != "cluster-1|default" {
		t.Errorf("model_inst_id=%q want=cluster-1|default", got)
	}
}

func TestDataK8sIdentityInferenceBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		dimensions    map[string]string
		catalogModel  string
		strategyModel string
		instanceBased bool
		lookupFailure string
		wantModel     string
		wantID        string
		wantCode      enrich.DiagnosticCode
	}{
		{name: "cluster", dimensions: map[string]string{"bcs_cluster_id": "cluster-1"}, wantModel: "cw-K8s_Cluster", wantID: "cluster-1"},
		{name: "pod", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "pod_name": "web-1"}, wantModel: "cw-K8s_Pod", wantID: "pod-1"},
		{name: "container despite pod catalog", catalogModel: "cw-K8s_Pod", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "pod_name": "web-1", "container_name": "app"}, wantModel: "cw-K8s_Container", wantID: "container-1"},
		{name: "container of standalone Pod", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "pod_name": "web-1", "container_name": "app", "workload_kind": "Pod", "workload_name": "web-1"}, wantModel: "cw-K8s_Container", wantID: "container-1"},
		{name: "node", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "node": "node-1"}, wantModel: "cw-K8s_Node", wantID: "node-1"},
		{name: "workload", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "workload_kind": "Deployment", "workload_name": "web"}, wantModel: "cw-K8s_Workload", wantID: "workload-1"},
		{name: "service", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "service": "web"}, wantModel: "cw-K8s_Service", wantID: "service-1"},
		{name: "PV overrides collector pod", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "persistentvolume": "data", "pod_name": "collector"}, wantModel: "cw-K8s_PersistentVolume", wantID: "volume-1"},
		{name: "PVC overrides collector pod", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "persistentvolumeclaim": "data", "pod_name": "collector"}, wantModel: "cw-K8s_PersistentVolumeClaim", wantID: "claim-1"},
		{name: "K8s strategy model is not an instance constraint in DATA", strategyModel: "cw-K8s_Container", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default"}, wantModel: "cw-K8s_Namespace", wantID: "cluster-1|default"},
		{name: "complete Event identity wins over K8s strategy and dimensions", strategyModel: "cw-K8s_Container", dimensions: map[string]string{"model_id": "cw-K8s_Namespace", "model_inst_id": "cluster-1|default", "bcs_cluster_id": "cluster-1", "namespace": "default", "pod_name": "web-1"}, wantModel: "cw-K8s_Namespace", wantID: "cluster-1|default"},
		{name: "no dimensions does not restore catalog model", wantCode: enrich.DiagnosticCodeMissingField},
		{name: "namespace without cluster is incomplete", dimensions: map[string]string{"namespace": "default"}, wantCode: enrich.DiagnosticCodeMissingField},
		{name: "container without pod does not fall back to namespace", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "container_name": "app"}, wantCode: enrich.DiagnosticCodeMissingField},
		{name: "incomplete explicit identity does not fall back", dimensions: map[string]string{"model_id": "cw-K8s_Container", "bcs_cluster_id": "cluster-1", "namespace": "default"}, wantCode: enrich.DiagnosticCodeInvalidField},
		{name: "instance strategy keeps Container constraint", strategyModel: "cw-K8s_Container", instanceBased: true, dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default"}, wantCode: enrich.DiagnosticCodeMissingField},
		{name: "other model keeps existing inference", catalogModel: "cw-MySQL", strategyModel: "cw-MySQL", dimensions: map[string]string{"model_id": "cw-MySQL", "model_inst_id": "mysql-1", "bcs_cluster_id": "cluster-1", "namespace": "default"}, wantModel: "cw-MySQL", wantID: "mysql-1"},
		{name: "pod not found uses its natural key and not its namespace", lookupFailure: "not found", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "pod_name": "web-1"}, wantModel: "cw-K8s_Pod", wantID: "cluster-1|default|web-1", wantCode: enrich.DiagnosticCodeFallbackSucceeded},
		{name: "reader error", lookupFailure: "error", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "pod_name": "web-1"}, wantCode: enrich.DiagnosticCodeDependencyInvalid},
		{name: "wrong tenant", lookupFailure: "tenant", dimensions: map[string]string{"bcs_cluster_id": "cluster-1", "namespace": "default", "pod_name": "web-1"}, wantCode: enrich.DiagnosticCodeDependencyInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			catalogModel := tc.catalogModel
			if catalogModel == "" {
				catalogModel = "cw-K8s_Container"
			}
			reader := &resourceModelSetReader{
				dataSliceReader: dataSliceReader{model: catalogModel},
				metrics: map[int64]models.MetricMetadata{121: {
					MetricsetCode: "k8s", Kind: "native", ModelIDs: []string{catalogModel},
					FieldName: "usage", PhysicalField: "usage", DataLabel: "k8s",
				}},
			}
			reader.strategy = reader.strategyForTest()
			reader.strategy.ObjectModelCode = optionalModelCode(tc.strategyModel)
			reader.strategy.Spec.StrategyItem.QueryConfigs = []models.StrategyQueryConfig{{MetricRefID: 121}}
			if tc.instanceBased {
				reader.strategy.Spec.ConfigType = ""
			}
			dimensions := domain.DimensionMap{}
			for key, value := range tc.dimensions {
				dimensions[key] = domain.NewStringScalar(value)
			}
			event := processorBaseTargetAlert(t, dimensions)
			original := event.Clone()
			instance := enrich.Instance{TenantID: event.BKTenantID, ModelCode: tc.wantModel, InstanceID: tc.wantID, Fields: map[string]any{"entity_uid": tc.wantModel + "|" + tc.wantID, "bk_biz_id": int64(2)}}
			reader.instance, reader.found = instance, tc.wantID != ""
			k8s := &k8sTestReader{contexts: k8sTestContexts(), instance: instance, found: tc.wantID != ""}
			switch tc.lookupFailure {
			case "not found":
				k8s.found = false
			case "error":
				k8s.err = errors.New("OneModel unavailable")
			case "tenant":
				k8s.instance = enrich.Instance{TenantID: "other", ModelCode: "cw-K8s_Pod", InstanceID: "foreign-pod"}
				k8s.found = true
			}
			chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, K8s{}, Metric{}}, enrich.Sources{
				CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, K8s: k8s,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
			if err != nil {
				t.Fatal(err)
			}
			if (tc.wantCode == "" || tc.wantCode == enrich.DiagnosticCodeFallbackSucceeded) && result.Status != domain.EnrichStatusSucceeded {
				t.Errorf("status=%s want=succeeded", result.Status)
			}
			if !reflect.DeepEqual(event, original) {
				t.Fatal("enrichment changed source Event")
			}
			payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
			if err != nil {
				t.Fatal(err)
			}
			foundCode := false
			for _, entry := range payload.Processors {
				for name, step := range entry {
					for _, diagnostic := range step.Diagnostics {
						foundCode = foundCode || diagnostic.Code == tc.wantCode
					}
					if name == rules.MetricProcessor && step.Status != domain.EnrichStatusSucceeded {
						t.Errorf("metric failed: %+v", step)
					}
				}
			}
			if tc.wantCode != "" && !foundCode {
				t.Errorf("missing diagnostic %q: %+v", tc.wantCode, payload)
			}
			event.Enrich = result.Data
			effective, err := view.EnrichedEvent(event, "warning")
			if err != nil {
				t.Fatal(err)
			}
			if got := dimensionText(effective.Labels, rules.FieldModelID); got != tc.wantModel {
				t.Errorf("model_id=%q want=%q", got, tc.wantModel)
			}
			if got := dimensionText(effective.Labels, rules.FieldModelInstID); got != tc.wantID {
				t.Errorf("model_inst_id=%q want=%q", got, tc.wantID)
			}
		})
	}
}
