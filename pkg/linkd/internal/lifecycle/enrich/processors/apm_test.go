package processors

import (
	"context"
	"errors"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

func TestAPMProjectsDimensionsAndIdentity(t *testing.T) {
	t.Parallel()
	model, instanceID := apmModelIdentity("17", "checkout", "instance-a")
	if model != rules.APMServiceInstanceModelCode || instanceID != "17|checkout|instance-a" {
		t.Fatalf("identity=%s/%s", model, instanceID)
	}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{"apm_app_id": domain.NewStringScalar("17"), rules.FieldServiceName: domain.NewStringScalar("checkout"), rules.FieldAPMInstanceID: domain.NewStringScalar("instance-a"), rules.FieldAPMSpanName: domain.NewStringScalar("GET /pay"), rules.FieldAPMNetPeerName: domain.NewStringScalar("db")})
	reader := apmTestReader{}
	chain, err := enrich.NewChain([]enrich.Processor{APM{}}, enrich.Sources{CWStrategy: reader, APMApplication: reader})
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
	if result.Status != domain.EnrichStatusSucceeded || payload.Processors[0][rules.APMProcessor].Status != domain.EnrichStatusSucceeded {
		t.Fatalf("status=%q", result.Status)
	}
}

func TestAPMApplicationReaderProjectsAliasAndIdentity(t *testing.T) {
	t.Parallel()
	reader := apmTestReader{}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldServiceName: domain.NewStringScalar("checkout")})
	chain, err := enrich.NewChain([]enrich.Processor{APM{}}, enrich.Sources{CWStrategy: reader, APMApplication: reader})
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
	value := payload.Processors[0][rules.APMProcessor].Value
	if string(value["apm_app_alias"]) != `"Demo"` || string(value["model_id"]) != `"cw-service"` || string(value["model_inst_id"]) != `"17|checkout"` {
		t.Fatalf("value=%#v", value)
	}
}

func TestAPMApplicationReaderTenantMismatchReturnsPartial(t *testing.T) {
	t.Parallel()
	reader := apmTestReader{apps: []models.APMApplication{{TenantID: "tenant-b", ID: 17, Name: "demo", Alias: "Other"}}}
	chain, err := enrich.NewChain([]enrich.Processor{APM{}}, enrich.Sources{CWStrategy: reader, APMApplication: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: processorBaseTargetAlert(t, domain.DimensionMap{})})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.EnrichStatusPartial {
		t.Fatalf("status=%q", result.Status)
	}
}

func TestAPMApplicationReaderErrorReturnsPartial(t *testing.T) {
	t.Parallel()
	reader := apmTestReader{appErr: errors.New("apm unavailable")}
	chain, err := enrich.NewChain([]enrich.Processor{APM{}}, enrich.Sources{CWStrategy: reader, APMApplication: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: processorBaseTargetAlert(t, domain.DimensionMap{})})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	envelope := payload.Processors[0][rules.APMProcessor]
	if result.Status != domain.EnrichStatusPartial || !hasDiagnosticDependency(envelope.Diagnostics, rules.DependencyAPMApplication) {
		t.Fatalf("status=%q envelope=%#v", result.Status, envelope)
	}
}

func TestAPMApplicationReaderDuplicateExactMatchUsesFirstStableResult(t *testing.T) {
	t.Parallel()
	reader := apmTestReader{apps: []models.APMApplication{{TenantID: "tenant-a", ID: 17, Name: "demo", Alias: "First"}, {TenantID: "tenant-a", ID: 18, Name: "demo", Alias: "Second"}}}
	chain, err := enrich.NewChain([]enrich.Processor{APM{}}, enrich.Sources{CWStrategy: reader, APMApplication: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: processorBaseTargetAlert(t, domain.DimensionMap{})})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload.Processors[0][rules.APMProcessor].Value["apm_app_alias"]) != `"First"` {
		t.Fatalf("value=%#v", payload.Processors[0][rules.APMProcessor])
	}
}

func TestAPMApplicationReaderNonExactMatchReturnsPartial(t *testing.T) {
	t.Parallel()
	reader := apmTestReader{apps: []models.APMApplication{{TenantID: "tenant-a", ID: 17, Name: "other", Alias: "Other"}}}
	chain, err := enrich.NewChain([]enrich.Processor{APM{}}, enrich.Sources{CWStrategy: reader, APMApplication: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: processorBaseTargetAlert(t, domain.DimensionMap{})})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.EnrichStatusPartial {
		t.Fatalf("status=%q", result.Status)
	}
}

func TestAPMResourceProjectionFromDimensions(t *testing.T) {
	t.Parallel()
	reader := apmTestReader{}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{"apm_app_id": domain.NewStringScalar("17"), rules.FieldServiceName: domain.NewStringScalar("checkout"), rules.FieldAPMInstanceID: domain.NewStringScalar("instance-a")})
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, APM{}}, enrich.Sources{CWStrategy: reader, OneModel: reader, APMApplication: reader})
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
	resource := payload.Processors[1][rules.ResourceProcessor]
	if string(resource.Value["model_id"]) != `"cw-service_instance"` || string(resource.Value["model_inst_id"]) != `"17|checkout|instance-a"` {
		t.Fatalf("resource=%#v", resource)
	}
}

func TestAPMCompletePayloadCoversAllIdentityLevels(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, service, instance, model, id string }{
		{name: "application", model: rules.APMApplicationModelCode, id: "17"},
		{name: "service", service: "checkout", model: rules.APMServiceModelCode, id: "17|checkout"},
		{name: "service instance", service: "checkout", instance: "instance-a", model: rules.APMServiceInstanceModelCode, id: "17|checkout|instance-a"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := apmTestReader{}
			dimensions := domain.DimensionMap{"apm_app_id": domain.NewStringScalar("17")}
			if tc.service != "" {
				dimensions[rules.FieldServiceName] = domain.NewStringScalar(tc.service)
			}
			if tc.instance != "" {
				dimensions[rules.FieldAPMInstanceID] = domain.NewStringScalar(tc.instance)
			}
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, APM{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, APMApplication: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: processorBaseTargetAlert(t, dimensions)})
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
			resource := payload.Processors[1][rules.ResourceProcessor]
			if string(resource.Value["model_id"]) != `"`+tc.model+`"` || string(resource.Value["model_inst_id"]) != `"`+tc.id+`"` {
				t.Fatalf("resource=%#v", resource)
			}
		})
	}
}

func TestAPMCompleteProcessorChainPreservesAPMContext(t *testing.T) {
	t.Parallel()
	reader := apmTestReader{apps: []models.APMApplication{{TenantID: "tenant-a", ID: 17, Name: "demo", Alias: "Demo"}}}
	alert := processorBaseTargetAlert(t, domain.DimensionMap{rules.FieldServiceName: domain.NewStringScalar("checkout"), rules.FieldAPMInstanceID: domain.NewStringScalar("instance-a")})
	chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, APM{}, Metric{}, EventSource{}}, enrich.Sources{CWStrategy: reader, APMApplication: reader, Metric: reader, Model: reader, OneModel: reader, AlarmSource: reader})
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
	if len(payload.Processors) != 6 || payload.Processors[3][rules.APMProcessor].Status != domain.EnrichStatusSucceeded {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestAPMMissingApplicationIdentityReturnsPartial(t *testing.T) {
	t.Parallel()
	chain, err := enrich.NewChain([]enrich.Processor{APM{}}, enrich.Sources{CWStrategy: apmTestReader{}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), lifecycle.EnrichInput{Alert: processorBaseTargetAlert(t, domain.DimensionMap{})})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != domain.EnrichStatusPartial {
		t.Fatalf("status=%q", result.Status)
	}
}

func hasDiagnosticDependency(diagnostics []enrich.Diagnostic, dependency string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Dependency == dependency {
			return true
		}
	}
	return false
}

type apmTestReader struct {
	apps   []models.APMApplication
	appErr error
}

func (r apmTestReader) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	return models.MetricMetadata{}, false, nil
}
func (r apmTestReader) GetModelByCode(context.Context, string, string) (enrich.Model, bool, error) {
	return enrich.Model{}, false, nil
}
func (r apmTestReader) FindInstance(context.Context, string, enrich.InstanceQuery) (enrich.Instance, bool, error) {
	return enrich.Instance{}, false, nil
}
func (r apmTestReader) GetAlarmSourceName(context.Context, string, string) (string, bool, error) {
	return "source", true, nil
}
func (r apmTestReader) FindAPMApplications(context.Context, string, string) ([]models.APMApplication, error) {
	if r.appErr != nil {
		return nil, r.appErr
	}
	if r.apps != nil {
		return r.apps, nil
	}
	return []models.APMApplication{{TenantID: "tenant-a", ID: 17, Name: "demo", Alias: "Demo"}}, nil
}

func (apmTestReader) GetByBKStrategyID(context.Context, string, int64) (models.CWStrategy, bool, error) {
	biz := int64(2)
	return models.CWStrategy{BKBizID: &biz, Spec: models.CWStrategySpec{ConfigType: models.CWStrategyConfigTypeData, DataSource: "应用-demo", StrategyItem: &models.CWStrategyItem{QueryConfigs: []models.StrategyQueryConfig{{ResultTableID: "2_bkapm_metric_demo.__default__", MetricField: "duration"}}}}}, true, nil
}
