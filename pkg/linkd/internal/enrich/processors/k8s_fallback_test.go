package processors

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/rules"
	"linkd/internal/enrich/view"
)

func TestDataK8sMissingInstanceFallback(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, model, id string
		dimensions      map[string]string
	}{
		{"cluster", "cw-K8s_Cluster", "cluster-1", map[string]string{}},
		{"namespace", "cw-K8s_Namespace", "cluster-1|default", map[string]string{"namespace": "default"}},
		{"node", "cw-K8s_Node", "cluster-1|node-1", map[string]string{"node": "node-1"}},
		{"service", "cw-K8s_Service", "cluster-1|default|web", map[string]string{"namespace": "default", "service": "web"}},
		{"workload", "cw-K8s_Workload", "cluster-1|default|Deployment|web", map[string]string{"namespace": "default", "workload_kind": "Deployment", "workload_name": "web"}},
		{"pod", "cw-K8s_Pod", "cluster-1|default|web-1", map[string]string{"namespace": "default", "pod_name": "web-1"}},
		{"standalone pod", "cw-K8s_Pod", "cluster-1|default|web-1", map[string]string{"namespace": "default", "pod_name": "web-1", "workload_kind": "Pod", "workload_name": "web-1"}},
		{"container", "cw-K8s_Container", "cluster-1|default|web-1|app", map[string]string{"namespace": "default", "pod_name": "web-1", "container_name": "app"}},
		{"PV ignores collector", "cw-K8s_PersistentVolume", "cluster-1|data", map[string]string{"persistentvolume": "data", "namespace": "collector", "pod_name": "collector-1"}},
		{"PVC", "cw-K8s_PersistentVolumeClaim", "cluster-1|default|data", map[string]string{"namespace": "default", "persistentvolumeclaim": "data"}},
	} {
		for _, mode := range []string{"monitor-resource", "k8s", "both"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				dims := domain.DimensionMap{"bcs_cluster_id": domain.NewStringScalar("cluster-1")}
				for key, value := range tc.dimensions {
					dims[key] = domain.NewStringScalar(value)
				}
				reader := &dataSliceReader{model: tc.model}
				event := processorBaseTargetAlert(t, dims)
				result, effective := runK8sFallbackTest(t, mode, event, reader, &k8sTestReader{})
				wantStatus := domain.EnrichStatusPartial
				if mode == "monitor-resource" || tc.model == "cw-K8s_Cluster" {
					wantStatus = domain.EnrichStatusSucceeded
				}
				if result.Status != wantStatus {
					t.Fatalf("status=%s want=%s", result.Status, wantStatus)
				}
				if dimensionText(effective.Labels, rules.FieldModelID) != tc.model || dimensionText(effective.Labels, rules.FieldModelInstID) != tc.id {
					t.Fatalf("identity=%s/%s want=%s/%s", dimensionText(effective.Labels, rules.FieldModelID), dimensionText(effective.Labels, rules.FieldModelInstID), tc.model, tc.id)
				}
				assertK8sFallbackDiagnostics(t, result, mode, enrich.DiagnosticCodeFallbackSucceeded)
			})
		}
	}
}

func TestDataK8sFallbackFailureBoundaries(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		pod           string
		missing       string
		instanceBased bool
		reader        k8sTestReader
		wantCode      enrich.DiagnosticCode
		wantID        string
	}{
		{name: "separator", pod: "web|other", wantCode: enrich.DiagnosticCodeInvalidField},
		{name: "control character", pod: "web\nother", wantCode: enrich.DiagnosticCodeInvalidField},
		{name: "identity too long", pod: strings.Repeat("x", 1024), wantCode: enrich.DiagnosticCodeInvalidField},
		{name: "missing namespace", missing: "namespace", wantCode: enrich.DiagnosticCodeMissingField},
		{name: "query failed", reader: k8sTestReader{err: errors.New("OneModel unavailable")}, wantCode: enrich.DiagnosticCodeDependencyInvalid},
		{name: "wrong tenant", reader: k8sTestReader{found: true, instance: enrich.Instance{TenantID: "other", ModelCode: "cw-K8s_Pod", InstanceID: "foreign"}}, wantCode: enrich.DiagnosticCodeDependencyInvalid},
		{name: "wrong model", reader: k8sTestReader{found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Node", InstanceID: "foreign"}}, wantCode: enrich.DiagnosticCodeDependencyInvalid},
		{name: "empty returned identity", reader: k8sTestReader{found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Pod"}}, wantCode: enrich.DiagnosticCodeDependencyInvalid},
		{name: "existing instance wins", reader: k8sTestReader{found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Pod", InstanceID: "existing-pod"}}, wantID: "existing-pod"},
		{name: "instance strategy unchanged", instanceBased: true, wantCode: enrich.DiagnosticCodeDependencyInvalid},
	} {
		for _, mode := range []string{"monitor-resource", "k8s", "both"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				t.Parallel()
				pod := tc.pod
				if pod == "" {
					pod = "web-1"
				}
				dims := domain.DimensionMap{"bcs_cluster_id": domain.NewStringScalar("cluster-1"), "namespace": domain.NewStringScalar("default"), "pod_name": domain.NewStringScalar(pod)}
				delete(dims, tc.missing)
				reader := &dataSliceReader{model: "cw-K8s_Pod"}
				if tc.instanceBased {
					reader.strategy = reader.strategyForTest()
					reader.strategy.Spec.ConfigType = ""
					dims[rules.FieldNoDataDimension] = domain.NewBoolScalar(true)
				}
				event := processorBaseTargetAlert(t, dims)
				k8s := tc.reader
				result, effective := runK8sFallbackTest(t, mode, event, reader, &k8s)
				if got := dimensionText(effective.Labels, rules.FieldModelInstID); got != tc.wantID {
					t.Fatalf("model_inst_id=%q want=%q", got, tc.wantID)
				}
				if tc.wantCode != "" {
					assertK8sFallbackDiagnostics(t, result, mode, tc.wantCode)
				}
				if tc.pod != "" {
					// 查询未命中的原诊断不能被拼接失败的诊断替换。
					assertK8sFallbackDiagnostics(t, result, mode, enrich.DiagnosticCodeDependencyInvalid)
				}
			})
		}
	}
}

func TestDataK8sFallbackDoesNotOverwriteExplicitIdentity(t *testing.T) {
	t.Parallel()
	for _, root := range []string{"labels", "dimensions", "extra_data"} {
		t.Run(root, func(t *testing.T) {
			event := processorBaseTargetAlert(t, domain.DimensionMap{"bcs_cluster_id": domain.NewStringScalar("cluster-1"), "namespace": domain.NewStringScalar("default"), "pod_name": domain.NewStringScalar("web-1")})
			switch root {
			case "labels":
				event.Labels[rules.FieldModelID] = domain.NewStringScalar("cw-K8s_Pod")
				event.Labels[rules.FieldModelInstID] = domain.NewStringScalar("explicit-pod")
			case "dimensions":
				event.Dimensions[rules.FieldModelID] = domain.NewStringScalar("cw-K8s_Pod")
				event.Dimensions[rules.FieldModelInstID] = domain.NewStringScalar("explicit-pod")
			case "extra_data":
				event.ExtraData[rules.FieldModelID] = []byte(`"cw-K8s_Pod"`)
				event.ExtraData[rules.FieldModelInstID] = []byte(`"explicit-pod"`)
			}
			result, effective := runK8sFallbackTest(t, "k8s", event, &dataSliceReader{model: "cw-K8s_Pod"}, &k8sTestReader{})
			assertK8sFallbackDiagnostics(t, result, "k8s", "identity_conflict")
			assertK8sFallbackDiagnostics(t, result, "k8s", enrich.DiagnosticCodeDependencyInvalid)
			if got := dimensionText(effective.Labels, rules.FieldModelInstID); got != "explicit-pod" {
				// 原始 dimensions/extra_data 身份可能未投影到 labels，直接读取原位置。
				if root == "labels" || got != "" {
					t.Fatalf("explicit identity overwritten: %q", got)
				}
			}
		})
	}
}

func runK8sFallbackTest(t *testing.T, mode string, event domain.Event, reader *dataSliceReader, k8s *k8sTestReader) (enrich.Result, domain.Event) {
	t.Helper()
	original := event.Clone()
	processors := []enrich.Processor{MonitorResource{}, K8s{}}
	switch mode {
	case "monitor-resource":
		processors = processors[:1]
	case "k8s":
		processors = processors[1:]
	}
	chain, err := enrich.NewChain(processors, enrich.Sources{CWStrategy: reader, Model: reader, Metric: reader, OneModel: reader, K8s: k8s})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(event, original) {
		t.Fatal("fallback modified source Event")
	}
	event.Enrich = result.Data
	effective, err := view.EnrichedEvent(event, "warning")
	if err != nil {
		t.Fatal(err)
	}
	return result, effective
}

func assertK8sFallbackDiagnostics(t *testing.T, result enrich.Result, mode string, code enrich.DiagnosticCode) {
	t.Helper()
	payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range payload.Processors {
		for name, step := range entry {
			found := false
			for _, diagnostic := range step.Diagnostics {
				if diagnostic.Code == code {
					found = code != enrich.DiagnosticCodeDependencyInvalid || diagnostic.Dependency == rules.DependencyOneModel
				}
			}
			if !found {
				t.Errorf("%s (%s) missing %s: %+v", name, mode, code, step.Diagnostics)
			}
		}
	}
}
