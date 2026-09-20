package processors

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

func TestK8sWorkloadPodUsesPodModel(t *testing.T) {
	t.Parallel()
	dimensions := domain.DimensionMap{rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"), rules.FieldNamespace: domain.NewStringScalar("default"), rules.FieldWorkloadKind: domain.NewStringScalar("Pod"), rules.FieldWorkloadName: domain.NewStringScalar("pod-1"), rules.FieldPodName: domain.NewStringScalar("pod-1")}
	reader := &k8sTestReader{instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-K8s_Pod", InstanceID: "pod-instance"}, found: true}
	chain, err := enrich.NewChain([]enrich.Processor{K8s{}}, enrich.Sources{K8s: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: processorBaseTargetAlert(t, dimensions)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.EnrichStatusSucceeded || !containsString(reader.models, "cw-K8s_Pod") {
		t.Fatalf("status=%q model=%q", result.Status, reader.modelCode)
	}
}

func TestK8sReaderUnavailableKeepsProjection(t *testing.T) {
	t.Parallel()
	alert := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"), rules.FieldNamespace: domain.NewStringScalar("default"), rules.FieldPodName: domain.NewStringScalar("web-1")})
	chain, err := enrich.NewChain([]enrich.Processor{K8s{}}, enrich.Sources{K8s: &k8sTestReader{err: errors.New("k8s reader is unavailable")}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.EnrichStatusSucceeded {
		t.Fatalf("status=%q", result.Status)
	}
}

func TestK8sMissingIdentityReturnsFailedWithProjection(t *testing.T) {
	t.Parallel()
	alert := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"), rules.FieldContainerName: domain.NewStringScalar("app")})
	chain, err := enrich.NewChain([]enrich.Processor{K8s{}}, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	envelope := payload.Processors[0][rules.K8sProcessor]
	if result.Status != domain.EnrichStatusFailed || envelope.Status != domain.EnrichStatusFailed || len(envelope.Diagnostics) != 1 || len(envelope.Diagnostics[0].Fields) != 2 {
		t.Fatalf("status=%q envelope=%#v", result.Status, envelope)
	}
}

func TestK8sIdentityResponseMismatchReturnsPartial(t *testing.T) {
	t.Parallel()
	alert := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"), rules.FieldNamespace: domain.NewStringScalar("default"), rules.FieldPodName: domain.NewStringScalar("web-1")})
	reader := &k8sTestReader{instance: enrich.Instance{TenantID: "tenant-b", ModelCode: "cw-K8s_Pod", InstanceID: "pod-1"}, found: true}
	chain, err := enrich.NewChain([]enrich.Processor{K8s{}}, enrich.Sources{K8s: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.EnrichStatusPartial {
		t.Fatalf("status=%q", result.Status)
	}
}

type k8sTestReader struct {
	instance  enrich.Instance
	found     bool
	err       error
	modelCode string
	models    []string
}

func (r *k8sTestReader) FindK8sInstance(_ context.Context, _ string, modelCode string, _ domain.DimensionMap) (enrich.Instance, bool, error) {
	r.modelCode = modelCode
	r.models = append(r.models, modelCode)
	return r.instance, r.found, r.err
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestK8sFullProcessorPayload(t *testing.T) {
	t.Parallel()
	alert := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"), rules.FieldNamespace: domain.NewStringScalar("default"), rules.FieldService: domain.NewStringScalar("web")})
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, K8s{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: k8sStrategyReader{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.Processors) != 6 {
		t.Fatalf("processors=%d", len(payload.Processors))
	}
}

type k8sStrategyReader struct{}

func (k8sStrategyReader) GetByBKStrategyID(context.Context, string, int64) (models.CWStrategy, bool, error) {
	biz := int64(2)
	return models.CWStrategy{BKBizID: &biz, ObjectModelCode: optionalModelCode(rules.K8sNodeModelCode), Spec: models.CWStrategySpec{ConfigType: models.CWStrategyConfigTypeData, Name: "K8s", StrategyItem: &models.CWStrategyItem{QueryConfigs: []models.StrategyQueryConfig{{ResultTableID: "k8s.metric", MetricField: "usage"}}}}}, true, nil
}

func TestK8sProjectsExplicitDimensions(t *testing.T) {
	t.Parallel()
	alert := processorBaseTargetAlert(t, domain.DimensionMap{
		rules.FieldBCSClusterID: domain.NewStringScalar("cluster-1"),
		"cluster_name":          domain.NewStringScalar("集群一"), rules.FieldNamespace: domain.NewStringScalar("default"),
		rules.FieldService: domain.NewStringScalar("web"), rules.FieldWorkloadKind: domain.NewStringScalar("Deployment"),
		rules.FieldWorkloadName: domain.NewStringScalar("web"), rules.FieldPodName: domain.NewStringScalar("web-1"), rules.FieldContainerName: domain.NewStringScalar("app"),
	})
	chain, err := enrich.NewChain([]enrich.Processor{K8s{}}, enrich.Sources{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := json.Unmarshal(mustRawObject(t, payload.Processors[0]["k8s"].Value), &values); err != nil {
		t.Fatal(err)
	}
	if values["bcs_cluster_id"] != "cluster-1" || values["namespace"] != "default" || values["pod_name"] != "web-1" || values["container_name"] != "app" {
		t.Fatalf("values=%#v", values)
	}
}
