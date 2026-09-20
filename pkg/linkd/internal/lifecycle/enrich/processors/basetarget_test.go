// Tencent is pleased to support the open source community. All rights reserved.
// Licensed under the MIT License.

package processors

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/lifecycle/enrich/models"
	"linkd/internal/lifecycle/enrich/rules"
)

func TestBaseTargetFullProcessorPayloads(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		modelCode  string
		dimensions domain.DimensionMap
		reader     *processorBaseTargetReader
		wantStatus domain.EnrichStatus
	}{
		{name: "monitor source", modelCode: rules.HostModelCode, dimensions: domain.DimensionMap{rules.FieldBKInstID: processorNumber(t, 101)}, reader: processorHostReader(), wantStatus: domain.EnrichStatusSucceeded},
		{name: "no data", modelCode: "cw-Service", dimensions: domain.DimensionMap{rules.FieldNoDataDimension: domain.NewBoolScalar(true), rules.FieldModelID: domain.NewStringScalar("cw-Service"), rules.FieldModelInstID: processorNumber(t, 2)}, reader: processorServiceReader(), wantStatus: domain.EnrichStatusSucceeded},
		{name: "system metric fallback", modelCode: rules.HostModelCode, dimensions: domain.DimensionMap{rules.FieldBKTargetIP: domain.NewStringScalar("10.0.0.1"), rules.FieldBKTargetCloudID: processorNumber(t, 0)}, reader: processorHostReader(), wantStatus: domain.EnrichStatusPartial},
		{name: "basic", modelCode: "cw-Disk", dimensions: domain.DimensionMap{}, reader: &processorBaseTargetReader{model: processorModel("tenant-a", "cw-Disk", "磁盘", "disk"), modelFound: true}, wantStatus: domain.EnrichStatusSucceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			alert := processorBaseTargetAlert(t, test.dimensions)
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{
				CWStrategy: processorBaseTargetStrategy{modelCode: test.modelCode}, Metric: processorBaseTargetMetric{},
				Model: test.reader, OneModel: test.reader, CollectTopology: test.reader,
				AlarmSource: processorBaseTargetAlarmSource{},
			})
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
			if result.Status != test.wantStatus || len(payload.Processors) != 5 {
				t.Fatalf("status=%q processors=%#v", result.Status, payload.Processors)
			}
			for index, name := range []string{rules.StrategyProcessor, rules.ResourceProcessor, rules.DisplayProcessor, rules.MetricProcessor, rules.SourceProcessor} {
				if _, exists := payload.Processors[index][name]; !exists {
					t.Fatalf("processors[%d]=%#v", index, payload.Processors[index])
				}
			}
			resource := payload.Processors[1][rules.ResourceProcessor]
			if resource.Status != test.wantStatus || len(resource.Value) == 0 ||
				string(payload.Processors[4][rules.SourceProcessor].Value["source_name"]) != `"鲸眼监控"` {
				t.Fatalf("resource=%#v source=%#v", resource, payload.Processors[4])
			}
		})
	}
}

func TestBaseTargetBranchesFlowThroughResourceAndDisplay(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		modelCode  string
		dimensions domain.DimensionMap
		reader     *processorBaseTargetReader
		wantStatus domain.EnrichStatus
		wantInst   string
		wantObject string
	}{
		{name: "monitor source", modelCode: rules.HostModelCode, dimensions: domain.DimensionMap{rules.FieldBKInstID: processorNumber(t, 101)}, reader: processorHostReader(), wantStatus: domain.EnrichStatusSucceeded, wantInst: "101", wantObject: "主机 101"},
		{name: "no data", modelCode: "cw-Service", dimensions: domain.DimensionMap{rules.FieldNoDataDimension: domain.NewBoolScalar(true), rules.FieldModelID: domain.NewStringScalar("cw-Service"), rules.FieldModelInstID: processorNumber(t, 2)}, reader: processorServiceReader(), wantStatus: domain.EnrichStatusSucceeded, wantInst: "2", wantObject: "服务 2"},
		{name: "system metric", modelCode: rules.HostModelCode, dimensions: domain.DimensionMap{rules.FieldBKTargetIP: domain.NewStringScalar("10.0.0.1"), rules.FieldBKTargetCloudID: processorNumber(t, 0)}, reader: processorHostReader(), wantStatus: domain.EnrichStatusPartial, wantInst: "101", wantObject: "主机 101"},
		{name: "basic", modelCode: "cw-Disk", dimensions: domain.DimensionMap{}, reader: &processorBaseTargetReader{model: processorModel("tenant-a", "cw-Disk", "磁盘", "disk"), modelFound: true}, wantStatus: domain.EnrichStatusSucceeded, wantObject: "source subject"},
	} {
		t.Run(test.name, func(t *testing.T) {
			alert := processorBaseTargetAlert(t, test.dimensions)
			original := alert.Clone()
			chain, err := enrich.NewChain([]enrich.Processor{Resource{}, Display{}}, enrich.Sources{
				CWStrategy: processorBaseTargetStrategy{modelCode: test.modelCode}, Metric: processorBaseTargetMetric{},
				Model: test.reader, OneModel: test.reader, CollectTopology: test.reader,
			})
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
			resource := payload.Processors[0][rules.ResourceProcessor]
			display := payload.Processors[1][rules.DisplayProcessor]
			if resource.Status != test.wantStatus || string(resource.Value["model_inst_id"]) != `"`+test.wantInst+`"` ||
				string(display.Value["object"]) != `"`+test.wantObject+`"` {
				t.Fatalf("resource=%#v display=%#v", resource, display)
			}
			if !reflect.DeepEqual(alert, original) {
				t.Fatalf("Alert changed: before=%#v after=%#v", original, alert)
			}
			if test.reader.modelCalls != 1 || test.reader.instanceCalls > 1 || test.reader.topologyCalls > 1 {
				t.Fatalf("calls model=%d instance=%d topology=%d", test.reader.modelCalls, test.reader.instanceCalls, test.reader.topologyCalls)
			}
		})
	}
}

func TestNoDataFiveProcessorFailures(t *testing.T) {
	t.Parallel()
	modelCode := "cw-Service"
	for _, test := range []struct {
		name       string
		dimensions domain.DimensionMap
		reader     *processorBaseTargetReader
		wantStatus domain.EnrichStatus
		wantCode   enrich.DiagnosticCode
		wantDep    string
		wantObject string
	}{
		{name: "missing instance field", dimensions: domain.DimensionMap{rules.FieldNoDataDimension: domain.NewBoolScalar(true), rules.FieldModelID: domain.NewStringScalar(modelCode)}, reader: processorServiceReader(), wantStatus: domain.EnrichStatusFailed, wantCode: enrich.DiagnosticCodeMissingField, wantObject: "source subject"},
		{name: "invalid instance field", dimensions: domain.DimensionMap{rules.FieldNoDataDimension: domain.NewBoolScalar(true), rules.FieldModelID: domain.NewStringScalar(modelCode), rules.FieldModelInstID: domain.NewStringScalar("02")}, reader: processorServiceReader(), wantStatus: domain.EnrichStatusFailed, wantCode: enrich.DiagnosticCodeInvalidField, wantObject: "source subject"},
		{name: "instance not found", dimensions: domain.DimensionMap{rules.FieldNoDataDimension: domain.NewBoolScalar(true), rules.FieldModelID: domain.NewStringScalar(modelCode), rules.FieldModelInstID: processorNumber(t, 2)}, reader: func() *processorBaseTargetReader { r := processorServiceReader(); r.instanceFound = false; return r }(), wantStatus: domain.EnrichStatusFailed, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantDep: rules.DependencyOneModel, wantObject: "source subject"},
		{name: "model dependency failed", dimensions: domain.DimensionMap{rules.FieldNoDataDimension: domain.NewBoolScalar(true), rules.FieldModelID: domain.NewStringScalar(modelCode), rules.FieldModelInstID: processorNumber(t, 2)}, reader: func() *processorBaseTargetReader {
			r := processorServiceReader()
			r.modelErr = errors.New("unavailable")
			return r
		}(), wantStatus: domain.EnrichStatusPartial, wantCode: enrich.DiagnosticCodeDependencyInvalid, wantDep: rules.DependencyOneModel, wantObject: "服务 2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			alert := processorBaseTargetAlert(t, test.dimensions)
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{
				CWStrategy: processorBaseTargetStrategy{modelCode: modelCode}, Metric: processorBaseTargetMetric{},
				Model: test.reader, OneModel: test.reader, CollectTopology: test.reader, AlarmSource: processorBaseTargetAlarmSource{},
			})
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
			display := payload.Processors[2][rules.DisplayProcessor]
			if result.Status != domain.EnrichStatusPartial || resource.Status != test.wantStatus ||
				string(display.Value["object"]) != `"`+test.wantObject+`"` || len(resource.Diagnostics) != 1 ||
				resource.Diagnostics[0].Code != test.wantCode || resource.Diagnostics[0].Dependency != test.wantDep {
				t.Fatalf("result=%#v resource=%#v display=%#v", result, resource, display)
			}
			for index := 2; index < 5; index++ {
				for _, envelope := range payload.Processors[index] {
					if envelope.Status != domain.EnrichStatusSucceeded {
						t.Fatalf("processors[%d]=%#v", index, payload.Processors[index])
					}
				}
			}
		})
	}
}

func TestBaseTargetFiveProcessorFailureBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name               string
		modelCode          string
		dimensions         domain.DimensionMap
		reader             *processorBaseTargetReader
		wantDep            string
		wantModel          string
		wantInst           string
		wantResourceStatus domain.EnrichStatus
	}{
		{name: "basic model not found", modelCode: "cw-Disk", dimensions: domain.DimensionMap{}, reader: &processorBaseTargetReader{}, wantDep: rules.DependencyOneModel, wantModel: "cw-Disk"},
		{name: "basic model error", modelCode: "cw-Disk", dimensions: domain.DimensionMap{}, reader: &processorBaseTargetReader{modelErr: errors.New("unavailable")}, wantDep: rules.DependencyOneModel, wantModel: "cw-Disk"},
		{name: "monitor instance not found", modelCode: "cw-Service", dimensions: domain.DimensionMap{rules.FieldBKInstID: processorNumber(t, 2)}, reader: &processorBaseTargetReader{model: processorModel("tenant-a", "cw-Service", "服务", "service"), modelFound: true}, wantDep: rules.DependencyOneModel, wantModel: "cw-Service", wantResourceStatus: domain.EnrichStatusFailed},
		{name: "monitor instance wrong tenant", modelCode: "cw-Service", dimensions: domain.DimensionMap{rules.FieldBKInstID: processorNumber(t, 2)}, reader: func() *processorBaseTargetReader {
			r := processorServiceReader()
			r.instance.TenantID = "tenant-b"
			return r
		}(), wantDep: rules.DependencyOneModel, wantModel: "cw-Service", wantResourceStatus: domain.EnrichStatusFailed},
		{name: "monitor instance wrong model", modelCode: "cw-Service", dimensions: domain.DimensionMap{rules.FieldBKInstID: processorNumber(t, 2)}, reader: func() *processorBaseTargetReader {
			r := processorServiceReader()
			r.instance.ModelCode = "cw-Other"
			return r
		}(), wantDep: rules.DependencyOneModel, wantModel: "cw-Service", wantResourceStatus: domain.EnrichStatusFailed},
		{name: "monitor instance wrong ID", modelCode: "cw-Service", dimensions: domain.DimensionMap{rules.FieldBKInstID: processorNumber(t, 2)}, reader: func() *processorBaseTargetReader {
			r := processorServiceReader()
			r.instance.InstanceID = "999"
			return r
		}(), wantDep: rules.DependencyOneModel, wantModel: "cw-Service", wantResourceStatus: domain.EnrichStatusFailed},
		{name: "system topology invalid", modelCode: rules.HostModelCode, dimensions: domain.DimensionMap{rules.FieldBKHostID: processorNumber(t, 101)}, reader: func() *processorBaseTargetReader {
			r := processorHostReader()
			r.topologyFound = false
			r.topologyErr = enrich.ErrInvalidDataSourceResponse
			return r
		}(), wantDep: rules.DependencyCollectTopology, wantModel: rules.HostModelCode, wantInst: "101"},
	} {
		t.Run(test.name, func(t *testing.T) {
			alert := processorBaseTargetAlert(t, test.dimensions)
			chain, err := enrich.NewChain([]enrich.Processor{Strategy{}, Resource{}, Display{}, Metric{}, EventSource{}}, enrich.Sources{
				CWStrategy: processorBaseTargetStrategy{modelCode: test.modelCode}, Metric: processorBaseTargetMetric{},
				Model: test.reader, OneModel: test.reader, CollectTopology: test.reader, AlarmSource: processorBaseTargetAlarmSource{},
			})
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
			wantResourceStatus := test.wantResourceStatus
			if wantResourceStatus == "" {
				wantResourceStatus = domain.EnrichStatusPartial
			}
			if result.Status != domain.EnrichStatusPartial || resource.Status != wantResourceStatus || len(resource.Diagnostics) != 1 || resource.Diagnostics[0].Dependency != test.wantDep ||
				string(resource.Value["model_id"]) != `"`+test.wantModel+`"` || string(resource.Value["model_inst_id"]) != `"`+test.wantInst+`"` {
				t.Fatalf("result=%#v resource=%#v", result, resource)
			}
			if wantResourceStatus == domain.EnrichStatusFailed && (string(resource.Value["cw_labels"]) != `[]` || string(resource.Value["bk_biz_id"]) != `2`) {
				t.Fatalf("failed resource leaked identity or lost fallback: %#v", resource)
			}
			for index := 2; index < 5; index++ {
				for _, envelope := range payload.Processors[index] {
					if envelope.Status != domain.EnrichStatusSucceeded {
						t.Fatalf("processors[%d]=%#v", index, payload.Processors[index])
					}
				}
			}
		})
	}
}

type processorBaseTargetStrategy struct{ modelCode string }

func (r processorBaseTargetStrategy) GetByBKStrategyID(context.Context, string, int64) (models.CWStrategy, bool, error) {
	bizID := int64(2)
	return models.CWStrategy{ObjectModelCode: &r.modelCode, BKBizID: &bizID, Spec: models.CWStrategySpec{
		Name: "可用率", TableID: "service.metric", FieldName: "available",
		StrategyItem: &models.CWStrategyItem{QueryConfigs: []models.StrategyQueryConfig{{MetricField: "available", ResultTableID: "service.metric"}}},
	}}, true, nil
}

type processorBaseTargetAlarmSource struct{}

func (processorBaseTargetAlarmSource) GetAlarmSourceName(context.Context, string, string) (string, bool, error) {
	return "鲸眼监控", true, nil
}

type processorBaseTargetMetric struct{}

func (processorBaseTargetMetric) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	return models.MetricMetadata{}, false, nil
}

type processorBaseTargetReader struct {
	model         enrich.Model
	modelFound    bool
	modelErr      error
	modelCalls    int
	instance      enrich.Instance
	instanceFound bool
	instanceErr   error
	instanceCalls int
	topology      models.ResourceTopology
	topologyFound bool
	topologyErr   error
	topologyCalls int
}

func processorHostReader() *processorBaseTargetReader {
	return &processorBaseTargetReader{
		model: processorModel("tenant-a", rules.HostModelCode, "主机", "host"), modelFound: true,
		instance: enrich.Instance{TenantID: "tenant-a", ModelCode: rules.HostModelCode, InstanceID: "101", Fields: map[string]any{"bk_biz_ids": []int64{2}}, Attributes: map[string]any{rules.FieldBKHostID: int64(101), rules.FieldBKInstDisplayName: "主机 101"}}, instanceFound: true,
		topology: models.ResourceTopology{BKBizID: 2, BKBizName: "业务", BKSetID: 3, BKSetName: "集群"}, topologyFound: true,
	}
}

func processorServiceReader() *processorBaseTargetReader {
	return &processorBaseTargetReader{
		model: processorModel("tenant-a", "cw-Service", "服务", "service"), modelFound: true,
		instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2", Attributes: map[string]any{rules.FieldBKInstID: int64(2), rules.FieldBKBizID: int64(2), rules.FieldBKInstDisplayName: "服务 2"}}, instanceFound: true,
	}
}

func processorModel(tenant, code, name, objectID string) enrich.Model {
	return enrich.Model{TenantID: tenant, ModelID: "20", ModelCode: code, Fields: map[string]any{rules.FieldObjectModelName: name, rules.FieldBKCMDBObjectID: objectID}}
}

func (r *processorBaseTargetReader) GetModelByCode(context.Context, string, string) (enrich.Model, bool, error) {
	r.modelCalls++
	return r.model, r.modelFound, r.modelErr
}

func (r *processorBaseTargetReader) FindInstance(context.Context, string, enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.instanceCalls++
	return r.instance, r.instanceFound, r.instanceErr
}

func (*processorBaseTargetReader) FindRelatedHost(context.Context, string, string, string, string) (enrich.Instance, bool, error) {
	return enrich.Instance{}, false, nil
}

func (r *processorBaseTargetReader) FindHostTopology(context.Context, string, string) (models.ResourceTopology, bool, error) {
	r.topologyCalls++
	return r.topology, r.topologyFound, r.topologyErr
}

func processorBaseTargetAlert(t *testing.T, dimensions domain.DimensionMap) domain.Alert {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	strategyID, _ := domain.NewNumberScalar(123)
	strategyVersion, _ := domain.NewNumberScalar(1)
	bizID, _ := domain.NewNumberScalar(2)
	return domain.Alert{
		EventSourceVersion: 1, AlertID: "alert-basetarget", BKTenantID: "tenant-a", EventSourceID: "built_in_bk",
		Fingerprint: "basetarget", Title: "source title", Content: "source content", Severity: "warning", SubjectName: "source subject",
		Dimensions: dimensions, Labels: domain.DimensionMap{"strategy_id": strategyID, "strategy_version": strategyVersion, "bk_biz_id": bizID},
		ExtraData: domain.JSONObject{}, Status: domain.AlertStatusActive, LatestEventID: "event-1", TriggerEventID: "event-1",
		LastOccurredAt: now, UpdateAt: now, BeginAt: now, CreateAt: now, EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
}

func processorNumber(t *testing.T, value float64) domain.Scalar {
	t.Helper()
	result, err := domain.NewNumberScalar(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
