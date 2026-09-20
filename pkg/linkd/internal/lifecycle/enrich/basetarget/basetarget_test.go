// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package basetarget

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

func TestBaseTargetBranches(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		branch     rules.BaseTargetBranch
		modelCode  string
		dimensions domain.DimensionMap
		reader     *baseTargetReader
		wantModel  string
		wantInst   string
		wantObject string
		wantStatus domain.EnrichStatus
		wantDep    string
		wantLabels []string
	}{
		{
			name: "monitor source", branch: rules.BaseTargetMonitorSource, modelCode: rules.HostModelCode,
			dimensions: domain.DimensionMap{rules.FieldBKInstID: number(t, 101)},
			reader:     hostReader(), wantModel: rules.HostModelCode, wantInst: "101", wantObject: "主机 101",
			wantStatus: domain.EnrichStatusSucceeded,
			wantLabels: []string{"bk_biz_id", "bk_biz_id|2", "bk_set_id", "bk_set_id|3", "cw-Host", "cw-Host|101"},
		},
		{
			name: "no data", branch: rules.BaseTargetNoData, modelCode: "cw-Service",
			dimensions: domain.DimensionMap{rules.FieldNoDataDimension: domain.NewBoolScalar(true), rules.FieldModelID: domain.NewStringScalar("cw-Service"), rules.FieldModelInstID: number(t, 2)},
			reader:     serviceReader(), wantModel: "cw-Service", wantInst: "2", wantObject: "订单服务",
			wantStatus: domain.EnrichStatusSucceeded,
			wantLabels: []string{"bk_biz_id", "bk_biz_id|2", "cw-Service", "cw-Service|2"},
		},
		{
			name: "system metric address fallback", branch: rules.BaseTargetSystemMetric, modelCode: rules.HostModelCode,
			dimensions: domain.DimensionMap{rules.FieldBKTargetIP: domain.NewStringScalar("10.0.0.1"), rules.FieldBKTargetCloudID: number(t, 0)},
			reader:     hostReader(), wantModel: rules.HostModelCode, wantInst: "101", wantObject: "主机 101",
			wantStatus: domain.EnrichStatusPartial,
			wantLabels: []string{"bk_biz_id", "bk_biz_id|2", "bk_set_id", "bk_set_id|3", "cw-Host", "cw-Host|101"},
		},
		{
			name: "basic", branch: rules.BaseTargetBasic, modelCode: "cw-Disk",
			dimensions: domain.DimensionMap{}, reader: &baseTargetReader{model: model("tenant-a", "cw-Disk", "磁盘", "disk"), modelFound: true},
			wantModel: "cw-Disk", wantObject: "source subject", wantStatus: domain.EnrichStatusSucceeded,
			wantLabels: []string{"bk_biz_id", "bk_biz_id|2"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			alert := baseTargetAlert(t, test.dimensions)
			original := alert.Clone()
			scope, err := enrich.NewScope(alert, enrich.Sources{
				Model: test.reader, OneModel: test.reader, CollectTopology: test.reader,
			})
			if err != nil {
				t.Fatal(err)
			}
			strategy := models.CWStrategy{ObjectModelCode: &test.modelCode}
			classification := rules.Classification{Main: rules.MainTarget, BaseTarget: test.branch}
			result, err := Enrich(context.Background(), scope, classification, strategy, test.dimensions, 2, alert.SubjectName)
			if err != nil {
				t.Fatal(err)
			}
			hasCore := result.Resource.ModelInstID != "" || test.branch == rules.BaseTargetBasic
			if result.Resource.ModelID != test.wantModel || result.Resource.ModelInstID != test.wantInst || result.Object != test.wantObject ||
				statusForDiagnostics(result.ResourceDiagnostics, hasCore) != test.wantStatus || dependency(result.ResourceDiagnostics) != test.wantDep ||
				!slices.Equal(result.Resource.CWLabels, test.wantLabels) {
				t.Fatalf("result=%#v", result)
			}
			if !reflect.DeepEqual(alert, original) {
				t.Fatalf("Alert changed: before=%#v after=%#v", original, alert)
			}
			for range 2 {
				if _, err := Enrich(context.Background(), scope, classification, strategy, test.dimensions, 2, alert.SubjectName); err != nil {
					t.Fatal(err)
				}
			}
			if test.reader.modelCalls != 1 || test.reader.instanceCalls > 1 || test.reader.topologyCalls > 1 {
				t.Fatalf("calls model=%d instance=%d topology=%d", test.reader.modelCalls, test.reader.instanceCalls, test.reader.topologyCalls)
			}
		})
	}
}

func TestMonitorSourceServiceInstanceUsesBusinessContext(t *testing.T) {
	t.Parallel()
	reader := serviceReader()
	dimensions := domain.DimensionMap{rules.FieldBKInstID: number(t, 2)}
	alert := baseTargetAlert(t, dimensions)
	scope, _ := enrich.NewScope(alert, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader})
	modelCode := rules.ServiceInstanceModelCode
	_, err := Enrich(context.Background(), scope, rules.Classification{Main: rules.MainTarget, BaseTarget: rules.BaseTargetMonitorSource}, models.CWStrategy{ObjectModelCode: &modelCode}, dimensions, 2, alert.SubjectName)
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.lastQuery.AttributeFilters) != 1 || reader.lastQuery.AttributeFilters[0] != (enrich.InstanceAttributeFilter{
		Field: rules.FieldCWBIZID, Type: enrich.InstanceAttributeLong, Value: int64(2),
	}) {
		t.Fatalf("query=%#v", reader.lastQuery)
	}
}

func TestNoDataIdentityContract(t *testing.T) {
	t.Parallel()
	modelCode := "cw-Service"
	for _, test := range []struct {
		name        string
		dimensions  domain.DimensionMap
		wantCode    enrich.DiagnosticCode
		wantFields  []string
		wantQueries int
	}{
		{name: "legacy fields are ignored", dimensions: domain.DimensionMap{"cw_object_model_id": number(t, 20), "cw_object_model_inst_id": number(t, 2)}, wantCode: enrich.DiagnosticCodeMissingField, wantFields: []string{"dimensions.model_id", "dimensions.model_inst_id"}},
		{name: "missing instance", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar(modelCode)}, wantCode: enrich.DiagnosticCodeMissingField, wantFields: []string{"dimensions.model_inst_id"}},
		{name: "invalid model type", dimensions: domain.DimensionMap{rules.FieldModelID: number(t, 20), rules.FieldModelInstID: number(t, 2)}, wantCode: enrich.DiagnosticCodeInvalidField, wantFields: []string{"dimensions.model_id"}},
		{name: "model conflict", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar("cw-Other"), rules.FieldModelInstID: number(t, 2)}, wantCode: enrich.DiagnosticCodeInvalidField, wantFields: []string{"dimensions.model_id"}},
		{name: "invalid instance", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar(modelCode), rules.FieldModelInstID: domain.NewStringScalar("02")}, wantCode: enrich.DiagnosticCodeInvalidField, wantFields: []string{"dimensions.model_inst_id"}},
		{name: "valid string instance", dimensions: domain.DimensionMap{rules.FieldModelID: domain.NewStringScalar(modelCode), rules.FieldModelInstID: domain.NewStringScalar("2")}, wantQueries: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := serviceReader()
			alert := baseTargetAlert(t, test.dimensions)
			scope, err := enrich.NewScope(alert, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := Enrich(context.Background(), scope, rules.Classification{Main: rules.MainTarget, BaseTarget: rules.BaseTargetNoData}, models.CWStrategy{ObjectModelCode: &modelCode}, test.dimensions, 2, alert.SubjectName)
			if err != nil {
				t.Fatal(err)
			}
			if reader.instanceCalls != test.wantQueries {
				t.Fatalf("instance calls=%d, want %d", reader.instanceCalls, test.wantQueries)
			}
			if test.wantCode == "" {
				if result.Resource.ModelInstID != "2" || len(result.ResourceDiagnostics) != 0 {
					t.Fatalf("result=%#v", result)
				}
				return
			}
			if len(result.ResourceDiagnostics) != 1 || result.ResourceDiagnostics[0].Code != test.wantCode ||
				!slices.Equal(result.ResourceDiagnostics[0].Fields, test.wantFields) {
				t.Fatalf("diagnostics=%#v", result.ResourceDiagnostics)
			}
		})
	}
}

func TestBaseTargetIdentityMismatchAndTopologyMiss(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		branch     rules.BaseTargetBranch
		modelCode  string
		reader     *baseTargetReader
		dimensions domain.DimensionMap
		wantDep    string
		wantInst   string
		wantStatus domain.EnrichStatus
	}{
		{name: "model metadata mismatch", branch: rules.BaseTargetMonitorSource, modelCode: "cw-Service", dimensions: domain.DimensionMap{rules.FieldBKInstID: number(t, 2)}, reader: func() *baseTargetReader { r := serviceReader(); r.model.ModelCode = "cw-Other"; return r }(), wantDep: rules.DependencyOneModel, wantInst: "2", wantStatus: domain.EnrichStatusPartial},
		{name: "foreign tenant", branch: rules.BaseTargetMonitorSource, modelCode: rules.HostModelCode, dimensions: domain.DimensionMap{rules.FieldBKInstID: number(t, 101)}, reader: func() *baseTargetReader { r := hostReader(); r.instance.TenantID = "tenant-b"; return r }(), wantDep: rules.DependencyOneModel, wantStatus: domain.EnrichStatusFailed},
		{name: "model mismatch", branch: rules.BaseTargetMonitorSource, modelCode: rules.HostModelCode, dimensions: domain.DimensionMap{rules.FieldBKInstID: number(t, 101)}, reader: func() *baseTargetReader { r := hostReader(); r.instance.ModelCode = "cw-Other"; return r }(), wantDep: rules.DependencyOneModel, wantStatus: domain.EnrichStatusFailed},
		{name: "instance mismatch", branch: rules.BaseTargetMonitorSource, modelCode: rules.HostModelCode, dimensions: domain.DimensionMap{rules.FieldBKInstID: number(t, 101)}, reader: func() *baseTargetReader { r := hostReader(); r.instance.InstanceID = "999"; return r }(), wantDep: rules.DependencyOneModel, wantStatus: domain.EnrichStatusFailed},
		{name: "related host missing", branch: rules.BaseTargetMonitorSource, modelCode: "cw-Service", dimensions: domain.DimensionMap{rules.FieldBKInstID: number(t, 2)}, reader: func() *baseTargetReader {
			r := serviceReader()
			r.model.Fields[rules.FieldHostRelatedField] = "service_run_host"
			delete(r.instance.Attributes, rules.FieldBKBizID)
			return r
		}(), wantDep: rules.DependencyCollectTopology, wantInst: "2", wantStatus: domain.EnrichStatusPartial},
		{name: "topology missing", branch: rules.BaseTargetSystemMetric, modelCode: rules.HostModelCode, dimensions: domain.DimensionMap{rules.FieldBKHostID: number(t, 101)}, reader: func() *baseTargetReader { r := hostReader(); r.topologyFound = false; return r }(), wantDep: rules.DependencyCollectTopology, wantInst: "101", wantStatus: domain.EnrichStatusPartial},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			alert := baseTargetAlert(t, test.dimensions)
			scope, err := enrich.NewScope(alert, enrich.Sources{Model: test.reader, OneModel: test.reader, CollectTopology: test.reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := Enrich(context.Background(), scope, rules.Classification{Main: rules.MainTarget, BaseTarget: test.branch}, models.CWStrategy{ObjectModelCode: &test.modelCode}, test.dimensions, 2, alert.SubjectName)
			if err != nil {
				t.Fatal(err)
			}
			if dependency(result.ResourceDiagnostics) != test.wantDep || result.Resource.ModelInstID != test.wantInst || statusForDiagnostics(result.ResourceDiagnostics, result.Resource.ModelInstID != "") != test.wantStatus {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestBaseTargetCanceledLookupDoesNotReuseScenario(t *testing.T) {
	t.Parallel()
	reader := &cancelOnceModelReader{baseTargetReader: hostReader()}
	dimensions := domain.DimensionMap{rules.FieldBKInstID: number(t, 101)}
	alert := baseTargetAlert(t, dimensions)
	scope, err := enrich.NewScope(alert, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reader.cancel = cancel
	code := rules.HostModelCode
	classification := rules.Classification{Main: rules.MainTarget, BaseTarget: rules.BaseTargetMonitorSource}
	if _, err := Enrich(ctx, scope, classification, models.CWStrategy{ObjectModelCode: &code}, dimensions, 2, alert.SubjectName); !errors.Is(err, context.Canceled) {
		t.Fatalf("first error=%v, want canceled", err)
	}
	result, err := Enrich(context.Background(), scope, classification, models.CWStrategy{ObjectModelCode: &code}, dimensions, 2, alert.SubjectName)
	if err != nil || result.Resource.ModelInstID != "101" || len(result.ResourceDiagnostics) != 0 || reader.modelCalls != 1 {
		t.Fatalf("retry result=%#v err=%v modelCalls=%d", result, err, reader.modelCalls)
	}
}

type cancelOnceModelReader struct {
	*baseTargetReader
	cancel context.CancelFunc
}

func (r *cancelOnceModelReader) GetModelByCode(ctx context.Context, tenant, code string) (enrich.Model, bool, error) {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
		return enrich.Model{}, false, context.Canceled
	}
	return r.baseTargetReader.GetModelByCode(ctx, tenant, code)
}

func TestBaseTargetRejectsForeignRelatedHost(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		host enrich.Instance
	}{
		{name: "wrong tenant", host: enrich.Instance{TenantID: "tenant-b", ModelCode: rules.HostModelCode, InstanceID: "101"}},
		{name: "wrong model", host: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-Other", InstanceID: "101"}},
		{name: "missing identity", host: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := serviceReader()
			reader.model.Fields[rules.FieldHostRelatedField] = "service_run_host"
			delete(reader.instance.Attributes, rules.FieldBKBizID)
			reader.relatedHost, reader.relatedFound = test.host, true
			dimensions := domain.DimensionMap{rules.FieldBKInstID: number(t, 2)}
			alert := baseTargetAlert(t, dimensions)
			scope, err := enrich.NewScope(alert, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader})
			if err != nil {
				t.Fatal(err)
			}
			code := "cw-Service"
			result, err := Enrich(context.Background(), scope, rules.Classification{Main: rules.MainTarget, BaseTarget: rules.BaseTargetMonitorSource}, models.CWStrategy{ObjectModelCode: &code}, dimensions, 2, alert.SubjectName)
			if err != nil {
				t.Fatal(err)
			}
			if result.Resource.ModelInstID != "2" || dependency(result.ResourceDiagnostics) != rules.DependencyCollectTopology || reader.topologyCalls != 0 {
				t.Fatalf("result=%#v topologyCalls=%d", result, reader.topologyCalls)
			}
		})
	}
}

func TestBaseTargetCancelDuringTopologyLookup(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	reader := &cancelingTopologyReader{baseTargetReader: hostReader(), cancel: cancel}
	dimensions := domain.DimensionMap{rules.FieldBKInstID: number(t, 101)}
	alert := baseTargetAlert(t, dimensions)
	scope, err := enrich.NewScope(alert, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader})
	if err != nil {
		t.Fatal(err)
	}
	code := rules.HostModelCode
	if _, err := Enrich(ctx, scope, rules.Classification{Main: rules.MainTarget, BaseTarget: rules.BaseTargetMonitorSource}, models.CWStrategy{ObjectModelCode: &code}, dimensions, 2, alert.SubjectName); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context canceled", err)
	}
}

type cancelingTopologyReader struct {
	*baseTargetReader
	cancel context.CancelFunc
}

func (r *cancelingTopologyReader) FindHostTopology(context.Context, string, string) (models.ResourceTopology, bool, error) {
	r.cancel()
	return models.ResourceTopology{}, false, context.Canceled
}

func TestBaseTargetContextCancellation(t *testing.T) {
	t.Parallel()
	reader := hostReader()
	alert := baseTargetAlert(t, domain.DimensionMap{rules.FieldBKInstID: number(t, 101)})
	scope, _ := enrich.NewScope(alert, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	modelCode := rules.HostModelCode
	if _, err := Enrich(ctx, scope, rules.Classification{Main: rules.MainTarget, BaseTarget: rules.BaseTargetMonitorSource}, models.CWStrategy{ObjectModelCode: &modelCode}, alert.Dimensions, 2, alert.SubjectName); !errors.Is(err, context.Canceled) {
		t.Fatalf("error=%v, want context canceled", err)
	}
}

func TestBaseTargetDependencyFailurePreservesInstance(t *testing.T) {
	t.Parallel()
	reader := hostReader()
	reader.topologyErr = errors.New("unavailable")
	dimensions := domain.DimensionMap{rules.FieldBKInstID: number(t, 101)}
	alert := baseTargetAlert(t, dimensions)
	scope, _ := enrich.NewScope(alert, enrich.Sources{Model: reader, OneModel: reader, CollectTopology: reader})
	modelCode := rules.HostModelCode
	result, err := Enrich(context.Background(), scope, rules.Classification{Main: rules.MainTarget, BaseTarget: rules.BaseTargetMonitorSource}, models.CWStrategy{ObjectModelCode: &modelCode}, dimensions, 2, alert.SubjectName)
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource.ModelInstID != "101" || dependency(result.ResourceDiagnostics) != rules.DependencyCollectTopology ||
		statusForDiagnostics(result.ResourceDiagnostics, true) != domain.EnrichStatusPartial {
		t.Fatalf("result=%#v", result)
	}
}

func hostReader() *baseTargetReader {
	return &baseTargetReader{
		model: model("tenant-a", rules.HostModelCode, "主机", "host"), modelFound: true,
		instance: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "101", Fields: map[string]any{
			"model_id": rules.HostModelCode, "model_inst_id": "101", "entity_uid": "cw-Host|101", "bk_biz_ids": []int64{2},
		}, Attributes: map[string]any{rules.FieldBKHostID: int64(101), rules.FieldBKInstDisplayName: "主机 101"}}, instanceFound: true,
		topology: models.ResourceTopology{BKBizID: 2, BKBizName: "业务", BKSetID: 3, BKSetName: "集群", BKModuleID: 4, BKModuleName: "模块"}, topologyFound: true,
	}
}

func serviceReader() *baseTargetReader {
	return &baseTargetReader{
		model: model("tenant-a", "cw-Service", "服务", "service"), modelFound: true,
		instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2", Fields: map[string]any{
			"model_id": "cw-Service", "model_inst_id": "2", "entity_uid": "cw-Service|2",
		}, Attributes: map[string]any{rules.FieldBKInstID: int64(2), rules.FieldBKBizID: int64(2), rules.FieldBKInstDisplayName: "订单服务"}}, instanceFound: true,
	}
}

func model(tenant, code, name, objectID string) enrich.Model {
	return enrich.Model{TenantID: tenant, ModelID: "20", ModelCode: code, Fields: map[string]any{
		rules.FieldObjectModelName: name, rules.FieldBKCMDBObjectID: objectID,
	}}
}

type baseTargetReader struct {
	model         enrich.Model
	modelFound    bool
	modelErr      error
	modelCalls    int
	instance      enrich.Instance
	instanceFound bool
	instanceErr   error
	instanceCalls int
	lastQuery     enrich.InstanceQuery
	relatedHost   enrich.Instance
	relatedFound  bool
	relatedErr    error
	topology      models.ResourceTopology
	topologyFound bool
	topologyErr   error
	topologyCalls int
}

func (r *baseTargetReader) GetModelByCode(context.Context, string, string) (enrich.Model, bool, error) {
	r.modelCalls++
	return r.model, r.modelFound, r.modelErr
}

func (r *baseTargetReader) FindInstance(_ context.Context, _ string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.instanceCalls++
	r.lastQuery = query
	return r.instance, r.instanceFound, r.instanceErr
}

func (r *baseTargetReader) FindRelatedHost(context.Context, string, string, string, string) (enrich.Instance, bool, error) {
	return r.relatedHost, r.relatedFound, r.relatedErr
}

func (r *baseTargetReader) FindHostTopology(context.Context, string, string) (models.ResourceTopology, bool, error) {
	r.topologyCalls++
	return r.topology, r.topologyFound, r.topologyErr
}

func baseTargetAlert(t *testing.T, dimensions domain.DimensionMap) domain.Alert {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Alert{
		EventSourceVersion: 1, AlertID: "alert-basetarget", BKTenantID: "tenant-a", EventSourceID: "built_in_bk",
		Fingerprint: "base-target", Title: "source title", Content: "source content", Severity: "warning",
		SubjectName: "source subject", Dimensions: dimensions, Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{},
		Status: domain.AlertStatusActive, LatestEventID: "event-1", TriggerEventID: "event-1",
		LastOccurredAt: now, UpdateAt: now, BeginAt: now, CreateAt: now,
		EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
}

func number(t *testing.T, value float64) domain.Scalar {
	t.Helper()
	result, err := domain.NewNumberScalar(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func dependency(diagnostics []enrich.Diagnostic) string {
	for _, diagnostic := range diagnostics {
		if diagnostic.Dependency != "" {
			return diagnostic.Dependency
		}
	}
	return ""
}
