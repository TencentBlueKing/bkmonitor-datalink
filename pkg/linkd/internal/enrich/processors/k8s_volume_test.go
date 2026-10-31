package processors

import (
	"reflect"
	"strconv"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/rules"
)

func TestK8sVolumeIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, model, field string }{
		{"PV", "cw-K8s_PersistentVolume", "persistentvolume"},
		{"PVC", "cw-K8s_PersistentVolumeClaim", "persistentvolumeclaim"},
	} {
		for _, explicitModel := range []bool{false, true} {
			t.Run(tc.name+"/explicit="+strconv.FormatBool(explicitModel), func(t *testing.T) {
				dimensions := domain.DimensionMap{
					"bcs_cluster_id": domain.NewStringScalar("cluster-1"), "namespace": domain.NewStringScalar("default"),
					tc.field: domain.NewStringScalar("data-volume"), "pod_name": domain.NewStringScalar("collector"),
					"workload_kind": domain.NewStringScalar("Pod"), "workload_name": domain.NewStringScalar("collector"),
				}
				event := processorBaseTargetAlert(t, dimensions)
				if explicitModel {
					event.Labels[rules.FieldModelID] = domain.NewStringScalar(tc.model)
				}
				original := event.Clone()
				reader := &k8sTestReader{contexts: k8sTestContexts(), instance: enrich.Instance{TenantID: "tenant-a", ModelCode: tc.model, InstanceID: "volume-instance"}, found: true}
				reader.contexts["cw-K8s_Namespace"] = enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Namespace", InstanceID: "cluster-1|default", Attributes: map[string]any{"bk_biz_id": 99}}
				reader.contexts["cw-K8s_Cluster"] = enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Cluster", InstanceID: "cluster-1", Attributes: map[string]any{"bk_biz_id": 2}}
				chain, err := enrich.NewChain([]enrich.Processor{K8s{}}, enrich.Sources{K8s: reader})
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
				output := payload.Processors[0][rules.K8sProcessor]
				if output.Status != domain.EnrichStatusSucceeded || string(output.Value["model_id"]) != `"`+tc.model+`"` || string(output.Value["model_inst_id"]) != `"volume-instance"` {
					t.Fatalf("status=%s value=%s diagnostics=%v", output.Status, output.Value, output.Diagnostics)
				}
				if !reflect.DeepEqual(original, event) {
					t.Fatal("source Event changed")
				}
				if tc.name == "PV" && (containsString(reader.models, "cw-K8s_Namespace") || string(output.Value["namespace"]) != `""` || string(output.Value["bk_biz_id"]) != "2") {
					t.Fatalf("PV used collector namespace: models=%v value=%s", reader.models, output.Value)
				}
				if tc.name == "PVC" && string(output.Value["bk_biz_id"]) != "99" {
					t.Fatalf("PVC lost namespace business: value=%s", output.Value)
				}
			})
		}
	}
}

func TestK8sVolumeInvalidIdentity(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, model, field string }{
		{"PV", "cw-K8s_PersistentVolume", "persistentvolume"},
		{"PVC", "cw-K8s_PersistentVolumeClaim", "persistentvolumeclaim"},
	} {
		failures := []string{"missing volume", "missing cluster", "not found", "other tenant"}
		if tc.name == "PVC" {
			failures = append(failures, "missing namespace")
		}
		for _, failure := range failures {
			t.Run(tc.name+"/"+failure, func(t *testing.T) {
				event := processorBaseTargetAlert(t, domain.DimensionMap{"bcs_cluster_id": domain.NewStringScalar("cluster-1"), "namespace": domain.NewStringScalar("default"), tc.field: domain.NewStringScalar("data-volume")})
				event.Labels[rules.FieldModelID] = domain.NewStringScalar(tc.model)
				reader := &k8sTestReader{contexts: k8sTestContexts(), instance: enrich.Instance{TenantID: "tenant-a", ModelCode: tc.model, InstanceID: "volume-instance"}, found: true}
				wantCode := enrich.DiagnosticCodeDependencyInvalid
				switch failure {
				case "missing volume":
					delete(event.Dimensions, tc.field)
					wantCode = enrich.DiagnosticCodeMissingField
				case "missing cluster":
					delete(event.Dimensions, "bcs_cluster_id")
					wantCode = enrich.DiagnosticCodeMissingField
				case "not found":
					reader.found = false
				case "missing namespace":
					delete(event.Dimensions, "namespace")
					wantCode = enrich.DiagnosticCodeMissingField
				case "other tenant":
					reader.instance.TenantID = "tenant-b"
				}
				scope, err := enrich.NewScope(event, enrich.Sources{K8s: reader})
				if err != nil {
					t.Fatal(err)
				}
				output, err := (K8s{}).Process(t.Context(), scope)
				if err != nil {
					t.Fatal(err)
				}
				// 模型已提供但实例缺失时发布清理补丁，因此状态为 partial。
				if output.Status != domain.EnrichStatusPartial || len(output.Diagnostics) != 1 || output.Diagnostics[0].Code != wantCode || string(output.Value["model_inst_id"]) != `""` {
					t.Fatalf("status=%s value=%s diagnostics=%v", output.Status, output.Value, output.Diagnostics)
				}
			})
		}
	}
}

func TestMonitorResourceResolvesVolumesWithoutK8sProcessor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ model, field string }{
		{"cw-K8s_PersistentVolume", "persistentvolume"},
		{"cw-K8s_PersistentVolumeClaim", "persistentvolumeclaim"},
	} {
		t.Run(tc.model, func(t *testing.T) {
			event := processorBaseTargetAlert(t, domain.DimensionMap{"bcs_cluster_id": domain.NewStringScalar("cluster-1"), "namespace": domain.NewStringScalar("default"), tc.field: domain.NewStringScalar("data-volume")})
			reader := &dataSliceReader{model: tc.model, table: "k8s"}
			k8s := &k8sTestReader{instance: enrich.Instance{TenantID: "tenant-a", ModelCode: tc.model, InstanceID: "volume-instance"}, found: true}
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, MonitorResource{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader, K8s: k8s})
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
			output := payload.Processors[1][rules.MonitorResourceProcessor]
			if output.Status != domain.EnrichStatusSucceeded || string(output.Value["model_id"]) != `"`+tc.model+`"` || string(output.Value["model_inst_id"]) != `"volume-instance"` {
				t.Fatalf("status=%s value=%s diagnostics=%v", output.Status, output.Value, output.Diagnostics)
			}
		})
	}
}
