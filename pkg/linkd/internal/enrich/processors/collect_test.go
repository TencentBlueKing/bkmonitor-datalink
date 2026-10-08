// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

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
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestCollectScenarioFlowsThroughResourceAndDisplay(t *testing.T) {
	t.Parallel()
	alert := collectProcessorAlert(t)
	original := alert.Clone()
	reader := &processorCollectReader{
		config: models.CollectConfig{
			UID: "collect-uid", BKTenantID: "tenant-a", BKCollectTaskID: "collect-1",
			BKObjectCode: "cw-Service", BKInstID: 2,
		},
		instance: enrich.Instance{
			TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2",
			Fields: map[string]any{
				rules.FieldBKInstDisplayName: "订单服务", rules.FieldBKInstID: int64(2),
				rules.FieldBKBizID: int64(3), rules.FieldBKBizName: "订单业务",
			},
		},
	}
	chain, err := enrich.NewChain(
		[]enrich.Processor{Resource{}, Display{}},
		enrich.Sources{
			CWStrategy: processorCollectStrategy{}, Metric: processorCollectMetric{},
			CollectConfig: reader, Model: reader, OneModel: reader,
			DynamicGroup: processorDynamicGroups{ids: []string{"21", "34"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), enrich.Input{Event: alert})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(alert, original) {
		t.Fatalf("enrichment changed Alert: before=%#v after=%#v", original, alert)
	}
	payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	resource := payload.Processors[0][rules.ResourceProcessor]
	if resource.Status != domain.EnrichStatusSucceeded ||
		string(resource.Value["model_id"]) != `"cw-Service"` ||
		string(resource.Value["model_inst_id"]) != `"2"` ||
		string(resource.Value["bk_biz_id"]) != "3" ||
		string(resource.Value["cw_labels"]) != `["bk_biz_id","bk_biz_id|3","cw-Service","cw-Service|2"]` {
		t.Fatalf("resource=%#v", resource)
	}
	if string(resource.Value["dynamic_group_id"]) != `["21","34"]` {
		t.Fatalf("dynamic groups=%s", resource.Value["dynamic_group_id"])
	}
	display := payload.Processors[1][rules.DisplayProcessor]
	if display.Status != domain.EnrichStatusSucceeded ||
		string(display.Value["object"]) != `"订单服务"` ||
		string(display.Value["title"]) != `"订单服务发生了可用率告警"` {
		t.Fatalf("display=%#v", display)
	}
	if reader.configCalls != 1 || reader.instanceCalls != 1 || reader.modelCalls != 1 {
		t.Fatalf("calls config=%d instance=%d model=%d", reader.configCalls, reader.instanceCalls, reader.modelCalls)
	}
}

type processorDynamicGroups struct {
	ids []string
	err error
}

func TestCollectDynamicGroupFailureKeepsResource(t *testing.T) {
	reader := &processorCollectReader{
		config:   models.CollectConfig{UID: "collect-uid", BKTenantID: "tenant-a", BKCollectTaskID: "collect-1", BKObjectCode: "cw-Service", BKInstID: 2},
		instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2", Fields: map[string]any{rules.FieldBKBizID: int64(3)}},
	}
	chain, err := enrich.NewChain([]enrich.Processor{Resource{}}, enrich.Sources{
		CWStrategy: processorCollectStrategy{}, CollectConfig: reader, Model: reader, OneModel: reader,
		DynamicGroup: processorDynamicGroups{err: errors.New("redis unavailable")},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), enrich.Input{Event: collectProcessorAlert(t)})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	resource := payload.Processors[0][rules.ResourceProcessor]
	if resource.Status != domain.EnrichStatusPartial || string(resource.Value["model_inst_id"]) != `"2"` || string(resource.Value["dynamic_group_id"]) != `[]` ||
		len(resource.Diagnostics) == 0 || resource.Diagnostics[len(resource.Diagnostics)-1].Dependency != rules.DependencyDynamicGroup {
		t.Fatalf("resource=%#v", resource)
	}
}

func (r processorDynamicGroups) GetDynamicGroupIDs(_ context.Context, tenant, model, instance string) ([]string, error) {
	if tenant != "tenant-a" || model != "cw-Service" || instance != "2" {
		return nil, errors.New("wrong dynamic group identity")
	}
	return r.ids, r.err
}

func collectProcessorAlert(t *testing.T) domain.Event {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	strategyID, _ := domain.NewNumberScalar(123)
	strategyVersion, _ := domain.NewNumberScalar(1)
	bizID, _ := domain.NewNumberScalar(2)
	return domain.Event{
		EventSourceVersion: 1, EventID: "alert-collect", BKTenantID: "tenant-a",
		EventSourceID: "built_in_bk", Fingerprint: "collect-fingerprint", Title: "source title",
		Content: "source content", Evaluations: []domain.EventEvaluation{{Severity: "warning", Action: domain.EventActionTriggered}}, SubjectName: "source subject",
		Dimensions: domain.DimensionMap{
			rules.FieldBKCollectConfigID: domain.NewStringScalar("collect-1"),
			rules.FieldBKBizID:           bizID,
		},
		Labels: domain.DimensionMap{
			"strategy_id": strategyID, "strategy_version": strategyVersion, "bk_biz_id": bizID,
		},
		ExtraData: domain.JSONObject{}, OccurredAt: now, ProducedAt: now, ReceivedAt: now, CreateAt: now, Values: domain.EventValues{}, SourceRawData: domain.JSONObject{}, EventEnrichment: domain.EventEnrichment{EnrichStatus: domain.EnrichStatusPending},
	}
}

type processorCollectReader struct {
	config        models.CollectConfig
	instance      enrich.Instance
	configCalls   int
	instanceCalls int
	modelCalls    int
}

func (r *processorCollectReader) GetCollectConfig(context.Context, string, string) (models.CollectConfig, bool, error) {
	r.configCalls++
	return r.config, true, nil
}

func (r *processorCollectReader) GetModelByCode(_ context.Context, tenantID, modelCode string) (enrich.Model, bool, error) {
	r.modelCalls++
	return enrich.Model{TenantID: tenantID, ModelID: modelCode, ModelCode: modelCode}, true, nil
}

func (r *processorCollectReader) FindInstance(context.Context, string, enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.instanceCalls++
	return r.instance, true, nil
}

type processorCollectStrategy struct{}

func (processorCollectStrategy) GetByStrategyID(context.Context, models.StrategyQuery) (models.CWStrategy, bool, error) {
	modelCode := "cw-Service"
	bizID := int64(2)
	return models.CWStrategy{
		ObjectModelCode: &modelCode, BKBizID: &bizID,
		Spec: models.CWStrategySpec{
			Name: "可用率", TableID: "service.metric", FieldName: "available",
			StrategyItem: &models.CWStrategyItem{
				QueryConfigs: []models.StrategyQueryConfig{{
					MetricField: "available", ResultTableID: "service.metric",
					AggregateBy: []string{"bk_collect_config_id", "bk_biz_id"},
				}},
			},
		},
	}, true, nil
}

type processorCollectMetric struct{}

func (processorCollectMetric) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	return models.MetricMetadata{Dimensions: []models.MetricDimension{
		{Key: rules.FieldBKCollectConfigID, Name: "采集任务"},
		{Key: rules.FieldBKBizID, Name: "业务"},
	}}, true, nil
}
