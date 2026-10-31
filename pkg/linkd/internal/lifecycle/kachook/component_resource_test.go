package kachook

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/processors"
	"linkd/internal/enrich/rules"
	"linkd/internal/enrich/view"
)

func TestComponentResourceCloudAndBusinessReachKAC(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		instanceCloud bool
		inputCloud    bool
		businessErr   bool
		data          bool
		noData        bool
	}{
		{name: "business array without host topology"},
		{name: "component attributes contain cloud", instanceCloud: true},
		{name: "source labels contain cloud", inputCloud: true},
		{name: "business lookup failure retains component identity", businessErr: true},
		{name: "DATA component", data: true, instanceCloud: true},
		{name: "no data component", noData: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &componentResourceReader{instanceCloud: tc.instanceCloud, businessErr: tc.businessErr, data: tc.data}
			alert := testAlert()
			alert.SubjectSystem, alert.SubjectType, alert.SubjectID, alert.SubjectName = "", "", "", ""
			alert.Dimensions = domain.DimensionMap{
				"model_id": domain.NewStringScalar("cw-MySQL"), "model_inst_id": domain.NewStringScalar("22"),
				"ip": domain.NewStringScalar("192.0.2.11"), "bk_cloud_id": domain.NewStringScalar("0"),
			}
			if tc.noData {
				alert.Dimensions["__NO_DATA_DIMENSION__"] = domain.NewBoolScalar(true)
			}
			if err := json.Unmarshal([]byte(`{"strategy_id":230,"strategy_version":1,"bk_biz_id":2}`), &alert.Labels); err != nil {
				t.Fatal(err)
			}
			if tc.inputCloud {
				alert.Labels["bk_cloud_id"] = domain.NewStringScalar("0")
				alert.Labels["bk_cloud_name"] = domain.NewStringScalar("Default Area")
			}
			event := domain.Event{BKTenantID: alert.BKTenantID, EventSourceID: alert.EventSourceID, Dimensions: alert.Dimensions, Labels: alert.Labels, Evaluations: []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}}
			event.Values = domain.EventValues{}
			original := event.Clone()
			chain, err := enrich.NewChain([]enrich.Processor{processors.MonitorResource{}}, enrich.Sources{CWStrategy: reader, Model: reader, OneModel: reader, CollectTopology: reader, Metric: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := chain.Enrich(t.Context(), enrich.Input{Event: event, Preview: true})
			if err != nil {
				t.Fatal(err)
			}
			alert.Enrich, alert.EnrichStatus = result.Data.Evaluations[0].Data, result.Status
			effective, err := view.EnrichedAlert(alert)
			if err != nil {
				t.Fatal(err)
			}
			if rules.DimensionText(effective.Labels, "bk_cloud_id") != "" || rules.DimensionText(effective.Labels, "bk_cloud_name") != "" {
				t.Errorf("component labels contain cloud context: %+v", effective.Labels)
			}
			if rules.DimensionText(effective.Labels, "bk_host_id") != "101" || rules.DimensionText(effective.Labels, "bk_host_name") != "host-101" {
				t.Fatal("component host context lost")
			}
			message, err := CompatibilityMessage(alert, kacLevel)
			if err != nil {
				t.Fatal(err)
			}
			wantName, wantStatus := "业务二", domain.EnrichStatusSucceeded
			if tc.businessErr {
				wantName, wantStatus = "", domain.EnrichStatusPartial
			}
			if message.BKCloudID != "" || message.BKCloudName != "" || message.BKBizID != "2" || message.BKBizName != wantName || message.ModelID != "cw-MySQL" || message.ModelInstID != "22" || result.Status != wantStatus {
				t.Errorf("KAC cloud=%q/%q business=%q/%q identity=%s/%s status=%s", message.BKCloudID, message.BKCloudName, message.BKBizID, message.BKBizName, message.ModelID, message.ModelInstID, result.Status)
			}
			if reader.relatedCalls != 0 || reader.businessCalls != 1 {
				t.Errorf("related host calls=%d business calls=%d", reader.relatedCalls, reader.businessCalls)
			}
			if !reflect.DeepEqual(event, original) || !reflect.DeepEqual(effective.Dimensions, original.Dimensions) {
				t.Fatal("source Event or original dimensions changed")
			}
		})
	}
}

type componentResourceReader struct {
	instanceCloud bool
	businessErr   bool
	relatedCalls  int
	businessCalls int
	data          bool
}

func (r *componentResourceReader) GetByStrategyID(context.Context, models.StrategyRevisionQuery) (models.CWStrategy, bool, error) {
	code := "cw-MySQL"
	strategy := models.CWStrategy{ObjectModelCode: &code}
	if r.data {
		strategy.Spec = models.CWStrategySpec{ConfigType: models.CWStrategyConfigTypeData, TableID: "mysql.status", FieldName: "duration", StrategyItem: &models.CWStrategyItem{Expression: "A", QueryConfigs: []models.StrategyQueryConfig{{ResultTableID: "mysql.status", MetricField: "duration"}}}}
	}
	return strategy, true, nil
}

func (*componentResourceReader) FindMetric(context.Context, models.MetricQuery) (models.MetricMetadata, bool, error) {
	return models.MetricMetadata{ObjectModelCode: "cw-MySQL", ModelIDs: []string{"cw-MySQL"}}, true, nil
}

func (*componentResourceReader) GetModelByCode(_ context.Context, tenant, code string) (enrich.Model, bool, error) {
	return enrich.Model{TenantID: tenant, ModelID: "20", ModelCode: code, Fields: map[string]any{
		"object_model_name": "MySQL", "bk_cmdb_obj_id": "mysql", "host_related_field": "mysql_installed_host",
	}}, true, nil
}

func (r *componentResourceReader) FindInstance(_ context.Context, tenant string, q enrich.InstanceQuery) (enrich.Instance, bool, error) {
	instance := enrich.Instance{TenantID: tenant, ModelCode: q.ModelCode, InstanceID: q.InstanceID}
	switch q.ModelCode {
	case "cw-MySQL":
		instance.Fields = map[string]any{"bk_biz_ids": []any{json.Number("2")}}
		instance.Attributes = map[string]any{"bk_biz_id": nil}
		if r.instanceCloud {
			instance.Attributes["bk_cloud_id"], instance.Attributes["bk_cloud_name"] = int64(0), "Default Area"
		}
	case "cw-biz":
		r.businessCalls++
		if r.businessErr {
			return enrich.Instance{}, false, errors.New("business unavailable")
		}
		instance.Attributes = map[string]any{"bk_biz_name": "业务二"}
	case "cw-Host":
		instance.InstanceID = "101"
		instance.Attributes = map[string]any{"bk_host_id": int64(101), "bk_host_name": "host-101", "bk_cloud_id": int64(0), "bk_cloud_name": "Default Area"}
	}
	return instance, true, nil
}

func (r *componentResourceReader) FindRelatedHost(ctx context.Context, tenant, _, _, _ string) (enrich.Instance, bool, error) {
	r.relatedCalls++
	return r.FindInstance(ctx, tenant, enrich.InstanceQuery{ModelCode: "cw-Host", InstanceID: "101"})
}

func (*componentResourceReader) FindHostTopology(context.Context, string, string, bool) (models.ResourceTopology, bool, error) {
	return models.ResourceTopology{BKBizID: 9, BKBizName: "关联主机业务"}, true, nil
}
