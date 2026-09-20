// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package collect

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

func TestResolverAndProjectors(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		dimensions     domain.DimensionMap
		reader         *collectReader
		wantModel      string
		wantInstance   string
		wantObject     string
		wantDependency string
		wantCode       enrich.DiagnosticCode
		wantLabels     []string
		wantBizID      any
		wantSetID      any
		wantModuleID   any
		wantCloudID    any
		wantCloudName  string
	}{
		{
			name:       "config and instance found",
			dimensions: domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("collect-1")},
			reader: &collectReader{
				config: models.CollectConfig{
					UID: "uid-1", BKTenantID: "tenant-a", BKCollectTaskID: "collect-1",
					BKObjectCode: "cw-Service", BKInstID: 2,
				}, configFound: true,
				instance: enrich.Instance{
					TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2",
					Fields: map[string]any{
						rules.FieldBKInstDisplayName: "订单服务", rules.FieldBKInstID: int64(2),
						rules.FieldBKBizID: int64(3), rules.FieldBKBizName: "订单业务",
					},
				}, instanceFound: true,
			},
			wantModel: "cw-Service", wantInstance: "2", wantObject: "订单服务",
			wantBizID: int64(3), wantLabels: []string{"bk_biz_id", "bk_biz_id|3", "cw-Service", "cw-Service|2"},
		},
		{
			name:       "config missing",
			dimensions: domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("missing")},
			reader:     &collectReader{}, wantObject: "source subject",
			wantDependency: rules.DependencyCollectConfig,
		},
		{
			name:       "instance failure",
			dimensions: domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("collect-1")},
			reader: &collectReader{
				config: models.CollectConfig{
					UID: "uid-1", BKTenantID: "tenant-a", BKCollectTaskID: "collect-1",
					BKObjectCode: "cw-Service", BKInstID: 2,
				}, configFound: true, instanceErr: errors.New("unavailable"),
			},
			wantModel: "cw-Service", wantInstance: "2", wantObject: "source subject",
			wantDependency: rules.DependencyOneModel,
		},
		{
			name:       "boolean task identity",
			dimensions: domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewBoolScalar(true)},
			reader:     &collectReader{}, wantObject: "source subject", wantCode: enrich.DiagnosticCodeInvalidField,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			scope, err := enrich.NewScope(collectAlert(t, test.dimensions), enrich.Sources{
				CollectConfig: test.reader, Model: test.reader, OneModel: test.reader,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := Enrich(context.Background(), scope, test.dimensions, 2, "source subject")
			if err != nil {
				t.Fatal(err)
			}
			if result.Resource.ModelID != test.wantModel || result.Resource.ModelInstID != test.wantInstance ||
				dependency(result.ResourceDiagnostics) != test.wantDependency ||
				(test.wantCode != "" && diagnosticCode(result.ResourceDiagnostics) != test.wantCode) ||
				!slices.Equal(result.Resource.CWLabels, test.wantLabels) ||
				!reflect.DeepEqual(result.Resource.BKBizID, test.wantBizID) ||
				!reflect.DeepEqual(result.Resource.BKSetID, test.wantSetID) ||
				!reflect.DeepEqual(result.Resource.BKModuleID, test.wantModuleID) ||
				!reflect.DeepEqual(result.Resource.BKCloudID, test.wantCloudID) ||
				result.Resource.BKCloudName != test.wantCloudName {
				t.Fatalf("result=%#v", result)
			}
			if result.Object != test.wantObject {
				t.Fatalf("result=%#v", result)
			}
		})
	}
}

func TestCollectUsesOneModelHostForCloudName(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		dimensions domain.DimensionMap
		wantQuery  enrich.InstanceQuery
	}{
		{name: "host id", dimensions: domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("collect-1"), rules.FieldBKHostID: collectNumber(t, 101), rules.FieldBKTargetCloudID: collectNumber(t, 0)}, wantQuery: enrich.InstanceQuery{ModelCode: rules.HostModelCode, InstanceID: "101", AttributeFilters: []enrich.InstanceAttributeFilter{{Field: rules.FieldBKCloudID, Type: enrich.InstanceAttributeLong, Value: int64(0)}}}},
		{name: "string cloud id", dimensions: domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("collect-1"), rules.FieldBKTargetIP: domain.NewStringScalar("192.0.2.10"), rules.FieldBKTargetCloudID: domain.NewStringScalar("0")}, wantQuery: enrich.InstanceQuery{ModelCode: rules.HostModelCode, AttributeFilters: []enrich.InstanceAttributeFilter{
			{Field: rules.FieldBKHostInnerIP, Type: enrich.InstanceAttributeKeyword, Value: "192.0.2.10"},
			{Field: rules.FieldBKCloudID, Type: enrich.InstanceAttributeLong, Value: int64(0)},
		}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &collectReader{
				config: models.CollectConfig{UID: "uid-1", BKTenantID: "tenant-a", BKCollectTaskID: "collect-1", BKObjectCode: "cw-Service", BKInstID: 2}, configFound: true,
				instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2", Fields: map[string]any{rules.FieldBKBizID: int64(3)}}, instanceFound: true,
				secondaryInstance: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "101", Attributes: map[string]any{rules.FieldBKCloudID: int64(0), rules.FieldBKCloudName: "默认区域"}}, secondaryFound: true,
			}
			scope, err := enrich.NewScope(collectAlert(t, test.dimensions), enrich.Sources{CollectConfig: reader, Model: reader, OneModel: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := Enrich(context.Background(), scope, test.dimensions, 2, "source subject")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(reader.secondaryQuery, test.wantQuery) || result.Resource.BKCloudID != int64(0) || result.Resource.BKCloudName != "默认区域" || len(result.ResourceDiagnostics) != 0 {
				t.Fatalf("query=%#v resource=%#v diagnostics=%#v", reader.secondaryQuery, result.Resource, result.ResourceDiagnostics)
			}
		})
	}
}

func TestServiceInstanceUsesFinalBizIDInInstanceQuery(t *testing.T) {
	t.Parallel()
	finalBizID := int64(9)
	dimensions := domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("collect-service")}
	reader := &collectReader{
		config: models.CollectConfig{
			UID: "uid-service", BKTenantID: "tenant-a", BKCollectTaskID: "collect-service",
			BKObjectCode: rules.ServiceInstanceModelCode, BKInstID: 2, FinalBizID: &finalBizID,
		}, configFound: true,
		instance: enrich.Instance{
			TenantID: "tenant-a", ModelCode: rules.ServiceInstanceModelCode, InstanceID: "2",
			Fields: map[string]any{rules.FieldCWBIZID: int64(9)},
		}, instanceFound: true,
	}
	scope, err := enrich.NewScope(collectAlert(t, dimensions), enrich.Sources{CollectConfig: reader, Model: reader, OneModel: reader})
	if err != nil {
		t.Fatal(err)
	}
	_, err = Enrich(context.Background(), scope, dimensions, 2, "source subject")
	if err != nil {
		t.Fatal(err)
	}
	if reader.lastQuery.ModelCode != rules.ServiceInstanceModelCode ||
		reader.lastQuery.InstanceID != "2" || len(reader.lastQuery.AttributeFilters) != 1 ||
		reader.lastQuery.AttributeFilters[0] != (enrich.InstanceAttributeFilter{
			Field: rules.FieldCWBIZID, Type: enrich.InstanceAttributeLong, Value: int64(9),
		}) {
		t.Fatalf("query=%#v", reader.lastQuery)
	}
}

func TestRemoteCollectTopologyFailurePreservesResourceAndReportsDependency(t *testing.T) {
	t.Parallel()
	finalBizID := int64(9)
	dimensions := domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("collect-remote")}
	reader := &collectReader{
		config: models.CollectConfig{
			UID: "uid-remote", BKTenantID: "tenant-a", BKCollectTaskID: "collect-remote",
			BKObjectCode: "cw-Service", BKInstID: 2, FinalBizID: &finalBizID, IsRemoteCollect: true,
		}, configFound: true,
		instance: enrich.Instance{
			TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2",
			Fields: map[string]any{rules.FieldBKInstID: int64(2)},
		}, instanceFound: true,
		model: enrich.Model{
			TenantID: "tenant-a", ModelID: "20", ModelCode: "cw-Service",
			Fields: map[string]any{rules.FieldHostRelatedField: "service_run_host"},
		}, modelFound: true,
		relatedErr: errors.New("topology unavailable"),
	}
	scope, err := enrich.NewScope(collectAlert(t, dimensions), enrich.Sources{
		CollectConfig: reader, Model: reader, OneModel: reader, CollectTopology: reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Enrich(context.Background(), scope, dimensions, 2, "source subject")
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource.ModelID != "cw-Service" || result.Resource.ModelInstID != "2" ||
		dependency(result.ResourceDiagnostics) != rules.DependencyCollectTopology {
		t.Fatalf("result=%#v", result)
	}
}

func TestCollectAppliesRelatedHostCloudContext(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name          string
		instanceCloud map[string]any
		hostCloud     map[string]any
		wantID        any
		wantName      string
	}{
		{name: "fill from related host", hostCloud: map[string]any{rules.FieldBKCloudID: int64(0), rules.FieldBKCloudName: "默认区域"}, wantID: int64(0), wantName: "默认区域"},
		{name: "preserve resource cloud", instanceCloud: map[string]any{rules.FieldBKCloudID: int64(7), rules.FieldBKCloudName: "资源区域"}, hostCloud: map[string]any{rules.FieldBKCloudID: int64(0), rules.FieldBKCloudName: "默认区域"}, wantID: int64(7), wantName: "资源区域"},
		{name: "reject mismatched host name", instanceCloud: map[string]any{rules.FieldBKCloudID: int64(7)}, hostCloud: map[string]any{rules.FieldBKCloudID: int64(0), rules.FieldBKCloudName: "默认区域"}, wantID: int64(7)},
		{name: "fill matching host name", instanceCloud: map[string]any{rules.FieldBKCloudID: int64(0)}, hostCloud: map[string]any{rules.FieldBKCloudID: int64(0), rules.FieldBKCloudName: "默认区域"}, wantID: int64(0), wantName: "默认区域"},
		{name: "missing host name", hostCloud: map[string]any{rules.FieldBKCloudID: int64(0)}, wantID: int64(0)},
	} {
		t.Run(test.name, func(t *testing.T) {
			finalBizID := int64(9)
			dimensions := domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("collect-remote")}
			instanceFields := map[string]any{rules.FieldBKInstID: int64(2), rules.FieldBKBizID: int64(9)}
			for key, value := range test.instanceCloud {
				instanceFields[key] = value
			}
			hostFields := map[string]any{rules.FieldBKHostID: int64(101)}
			for key, value := range test.hostCloud {
				hostFields[key] = value
			}
			reader := &collectReader{
				config: models.CollectConfig{UID: "uid-remote", BKTenantID: "tenant-a", BKCollectTaskID: "collect-remote", BKObjectCode: "cw-Service", BKInstID: 2, FinalBizID: &finalBizID, IsRemoteCollect: true}, configFound: true,
				instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2", Fields: instanceFields}, instanceFound: true,
				model: enrich.Model{TenantID: "tenant-a", ModelID: "20", ModelCode: "cw-Service", Fields: map[string]any{rules.FieldHostRelatedField: "service_run_host"}}, modelFound: true,
				relatedHost: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "101", Fields: hostFields}, relatedFound: true,
				topology: models.ResourceTopology{BKBizID: 9}, topologyFound: true,
			}
			scope, err := enrich.NewScope(collectAlert(t, dimensions), enrich.Sources{CollectConfig: reader, Model: reader, OneModel: reader, CollectTopology: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := Enrich(context.Background(), scope, dimensions, 2, "source subject")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(result.Resource.BKCloudID, test.wantID) || result.Resource.BKCloudName != test.wantName || len(result.ResourceDiagnostics) != 0 {
				t.Fatalf("resource=%#v diagnostics=%#v", result.Resource, result.ResourceDiagnostics)
			}
		})
	}
}

func TestRemoteCollectAppliesRelatedHostTopologyAndLabels(t *testing.T) {
	t.Parallel()
	finalBizID := int64(9)
	dimensions := domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("collect-remote")}
	reader := &collectReader{
		config: models.CollectConfig{
			UID: "uid-remote", BKTenantID: "tenant-a", BKCollectTaskID: "collect-remote",
			BKObjectCode: "cw-Service", BKInstID: 2, FinalBizID: &finalBizID, IsRemoteCollect: true,
		}, configFound: true,
		instance: enrich.Instance{
			TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2",
			// 远程采集即使实例自身已有业务，也沿执行主机补充完整拓扑。
			Fields: map[string]any{rules.FieldBKInstID: int64(2), rules.FieldBKBizID: int64(9)},
		}, instanceFound: true,
		model: enrich.Model{
			TenantID: "tenant-a", ModelID: "20", ModelCode: "cw-Service",
			Fields: map[string]any{
				rules.FieldObjectModelName: "服务", rules.FieldBKCMDBObjectID: "service_instance",
				rules.FieldHostRelatedField: "service_run_host",
			},
		}, modelFound: true,
		relatedHost: enrich.Instance{
			TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "101",
			Fields: map[string]any{rules.FieldBKHostID: int64(101), rules.FieldBKCloudID: int64(0), rules.FieldBKCloudName: "默认区域"},
		}, relatedFound: true,
		topology: models.ResourceTopology{
			BKBizID: 3, BKBizName: "订单业务", BKSetID: 4, BKSetName: "生产集群",
			BKModuleID: 5, BKModuleName: "订单模块",
		}, topologyFound: true,
	}
	scope, err := enrich.NewScope(collectAlert(t, dimensions), enrich.Sources{
		CollectConfig: reader, Model: reader, OneModel: reader, CollectTopology: reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Enrich(context.Background(), scope, dimensions, 2, "source subject")
	if err != nil {
		t.Fatal(err)
	}
	wantLabels := []string{
		"bk_biz_id", "bk_biz_id|3", "bk_set_id", "bk_set_id|4", "cw-Service", "cw-Service|2",
	}
	if result.Resource.BKBizID != int64(3) || result.Resource.BKSetID != int64(4) ||
		result.Resource.BKModuleID != int64(5) || result.Resource.BKCloudID != int64(0) || result.Resource.BKCloudName != "默认区域" ||
		!slices.Equal(result.Resource.CWLabels, wantLabels) ||
		len(result.Resource.DynamicGroupID) != 0 {
		t.Fatalf("result=%#v", result)
	}
}

func TestResolverCachesDependenciesAndKeepsAlertUnchanged(t *testing.T) {
	t.Parallel()
	dimensions := domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("collect-1")}
	alert := collectAlert(t, dimensions)
	original := alert.Clone()
	reader := &collectReader{
		config: models.CollectConfig{
			UID: "uid-1", BKTenantID: "tenant-a", BKCollectTaskID: "collect-1",
			BKObjectCode: "cw-Service", BKInstID: 2,
		}, configFound: true,
		instance: enrich.Instance{
			TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2",
			Fields: map[string]any{rules.FieldBKInstDisplayName: "订单服务"},
		}, instanceFound: true,
	}
	scope, err := enrich.NewScope(alert, enrich.Sources{
		CollectConfig: reader, Model: reader, OneModel: reader, CollectTopology: reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := Enrich(context.Background(), scope, dimensions, 2, alert.SubjectName); err != nil {
			t.Fatal(err)
		}
	}
	if reader.configCalls != 1 || reader.instanceCalls != 1 || reader.modelCalls != 1 {
		t.Fatalf("calls config=%d instance=%d model=%d", reader.configCalls, reader.instanceCalls, reader.modelCalls)
	}
	if !reflect.DeepEqual(alert, original) {
		t.Fatalf("collect module changed Alert: before=%#v after=%#v", original, alert)
	}
}

type collectReader struct {
	config            models.CollectConfig
	configFound       bool
	configErr         error
	configCalls       int
	instance          enrich.Instance
	instanceFound     bool
	instanceErr       error
	secondaryInstance enrich.Instance
	secondaryFound    bool
	secondaryErr      error
	secondaryQuery    enrich.InstanceQuery
	instanceCalls     int
	lastQuery         enrich.InstanceQuery
	modelCalls        int
	relatedCalls      int
	topologyCalls     int
	model             enrich.Model
	modelFound        bool
	modelErr          error
	relatedHost       enrich.Instance
	relatedFound      bool
	relatedErr        error
	topology          models.ResourceTopology
	topologyFound     bool
	topologyErr       error
}

func (r *collectReader) GetCollectConfig(context.Context, string, string) (models.CollectConfig, bool, error) {
	r.configCalls++
	return r.config, r.configFound, r.configErr
}

func (r *collectReader) GetModelByCode(context.Context, string, string) (enrich.Model, bool, error) {
	r.modelCalls++
	return r.model, r.modelFound, r.modelErr
}

func (r *collectReader) FindRelatedHost(context.Context, string, string, string, string) (enrich.Instance, bool, error) {
	r.relatedCalls++
	return r.relatedHost, r.relatedFound, r.relatedErr
}

func (r *collectReader) FindHostTopology(context.Context, string, string) (models.ResourceTopology, bool, error) {
	r.topologyCalls++
	return r.topology, r.topologyFound, r.topologyErr
}

func (r *collectReader) FindInstance(_ context.Context, _ string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.instanceCalls++
	if query.ModelCode == rules.HostModelCode && r.instance.ModelCode != rules.HostModelCode {
		r.secondaryQuery = query
		return r.secondaryInstance, r.secondaryFound, r.secondaryErr
	}
	r.lastQuery = query
	return r.instance, r.instanceFound, r.instanceErr
}

func collectNumber(t *testing.T, value float64) domain.Scalar {
	t.Helper()
	result, err := domain.NewNumberScalar(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func collectAlert(t *testing.T, dimensions domain.DimensionMap) domain.Alert {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	return domain.Alert{
		EventSourceVersion: 1, AlertID: "alert-collect-module", BKTenantID: "tenant-a",
		EventSourceID: "built_in_bk", Fingerprint: "collect-module", Title: "source title",
		Content: "source content", Severity: "warning", SubjectName: "source subject",
		Dimensions: dimensions, Labels: domain.DimensionMap{}, ExtraData: domain.JSONObject{},
		Status: domain.AlertStatusActive, LatestEventID: "event-1", TriggerEventID: "event-1",
		LastOccurredAt: now, UpdateAt: now, BeginAt: now, CreateAt: now,
		EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
}

func dependency(diagnostics []enrich.Diagnostic) string {
	for _, diagnostic := range diagnostics {
		if diagnostic.Dependency != "" {
			return diagnostic.Dependency
		}
	}
	return ""
}

func diagnosticCode(diagnostics []enrich.Diagnostic) enrich.DiagnosticCode {
	if len(diagnostics) == 0 {
		return ""
	}
	return diagnostics[0].Code
}
