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
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestUptimeScenarioFlowsThroughResourceAndDisplay(t *testing.T) {
	t.Parallel()
	alert := uptimeProcessorAlert(t)
	original := alert.Clone()
	reader := &processorUptimeReader{
		task: models.UptimeTask{
			ID: 55, TaskID: 10079, Name: "拨测任务", Protocol: models.UptimeProtocolICMP,
			BKBizID: 2, BKTenantID: "tenant-a",
		},
		node: models.UptimeNode{
			ID: 8, Name: "北京节点", PlatID: 0, IP: "10.0.0.8", BKTenantID: "tenant-a",
		},
	}
	chain, err := enrich.NewChain(
		[]enrich.Processor{Resource{}, Display{}},
		enrich.Sources{
			CWStrategy: processorUptimeStrategy{}, Metric: processorUptimeMetric{},
			Model: reader, OneModel: reader, Uptime: reader, UptimeNode: reader,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), enrich.Input{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(alert, original) {
		t.Fatalf("enrichment changed Alert: before=%#v after=%#v", original, alert)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	resource := payload.Processors[0][rules.ResourceProcessor]
	if resource.Status != domain.EnrichStatusSucceeded ||
		string(resource.Value["model_id"]) != `"cw-web_service"` ||
		string(resource.Value["model_inst_id"]) != `"55"` ||
		string(resource.Value["model_name"]) != `"Website Service"` ||
		string(resource.Value["bk_biz_id"]) != "2" ||
		string(resource.Value["cw_labels"]) != `["bk_biz_id","bk_biz_id|2","cw-web_service","cw-web_service|55"]` {
		t.Fatalf("resource=%#v", resource)
	}
	display := payload.Processors[1][rules.DisplayProcessor]
	if display.Status != domain.EnrichStatusSucceeded ||
		string(display.Value["object"]) != `"拨测任务"` ||
		string(display.Value["title"]) != `"拨测任务发生了可用率告警"` {
		t.Fatalf("display=%#v", display)
	}
	var dimensions []models.DimensionDisplay
	if err := json.Unmarshal(display.Value["dimensions"], &dimensions); err != nil {
		t.Fatal(err)
	}
	wantNames := []string{"任务", "节点", "业务", "目标地址", "目标地址类型"}
	if len(dimensions) != len(wantNames) {
		t.Fatalf("dimensions=%#v", dimensions)
	}
	for index, name := range wantNames {
		if dimensions[index].Name != name {
			t.Fatalf("dimensions[%d]=%#v", index, dimensions[index])
		}
	}
	if reader.taskCalls != 1 || reader.nodeCalls != 1 || reader.modelCalls != 1 {
		t.Fatalf("calls task=%d node=%d model=%d", reader.taskCalls, reader.nodeCalls, reader.modelCalls)
	}
}

func TestUptimeMissingTaskKeepsDisplayFallback(t *testing.T) {
	t.Parallel()
	alert := uptimeProcessorAlert(t)
	reader := &processorUptimeReader{taskFound: boolPointer(false)}
	chain, err := enrich.NewChain(
		[]enrich.Processor{Resource{}, Display{}},
		enrich.Sources{
			CWStrategy: processorUptimeStrategy{}, Metric: processorUptimeMetric{},
			Model: reader, OneModel: reader, Uptime: reader, UptimeNode: reader,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(context.Background(), enrich.Input{Alert: alert})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := enrich.DecodePayload(result.Data)
	if err != nil {
		t.Fatal(err)
	}
	resource := payload.Processors[0][rules.ResourceProcessor]
	if resource.Status != domain.EnrichStatusPartial || len(resource.Diagnostics) != 1 ||
		resource.Diagnostics[0].Dependency != rules.DependencyUptime ||
		string(resource.Value["model_id"]) != `"cw-web_service"` ||
		string(resource.Value["model_inst_id"]) != `""` || string(resource.Value["bk_biz_id"]) != "2" ||
		string(resource.Value["cw_labels"]) != `["bk_biz_id","bk_biz_id|2"]` {
		t.Fatalf("resource=%#v", resource)
	}
	display := payload.Processors[1][rules.DisplayProcessor]
	if string(display.Value["object"]) != `"10079（该拨测任务已被删除）"` ||
		string(display.Value["title"]) != `"10079（该拨测任务已被删除）发生了可用率告警"` {
		t.Fatalf("display=%#v", display)
	}
}

func uptimeProcessorAlert(t *testing.T) domain.Alert {
	t.Helper()
	now := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	strategyID, _ := domain.NewNumberScalar(123)
	strategyVersion, _ := domain.NewNumberScalar(1)
	bizID, _ := domain.NewNumberScalar(2)
	return domain.Alert{
		EventSourceVersion: 1, AlertID: "alert-uptime", BKTenantID: "tenant-a",
		EventSourceID: "built_in_bk", Fingerprint: "uptime-fingerprint", Title: "source title",
		Content: "source content", Severity: "warning",
		Dimensions: domain.DimensionMap{
			rules.FieldTaskID:     domain.NewStringScalar("10079"),
			rules.FieldNodeID:     domain.NewStringScalar("0:10.0.0.8"),
			rules.FieldTarget:     domain.NewStringScalar("10.11.10.12"),
			rules.FieldTargetType: domain.NewStringScalar("ip"),
		},
		SubjectSystem: "bkmonitor", SubjectType: "uptime", SubjectID: "source-subject", SubjectName: "source subject",
		SourceEventID: "source-event", SourceAlertID: "source-alert",
		Labels: domain.DimensionMap{
			"strategy_id": strategyID, "strategy_version": strategyVersion, "bk_biz_id": bizID,
		},
		ExtraData: domain.JSONObject{}, Status: domain.AlertStatusActive,
		LatestEventID: "event-1", TriggerEventID: "event-1", LastOccurredAt: now, UpdateAt: now,
		BeginAt: now, CreateAt: now, EnrichStatus: domain.EnrichStatusPending, Enrich: domain.JSONObject{},
	}
}

type processorUptimeReader struct {
	task       models.UptimeTask
	taskFound  *bool
	node       models.UptimeNode
	taskCalls  int
	nodeCalls  int
	modelCalls int
}

func (r *processorUptimeReader) GetUptimeTask(context.Context, string, string) (models.UptimeTask, bool, error) {
	r.taskCalls++
	if r.taskFound != nil {
		return r.task, *r.taskFound, nil
	}
	return r.task, r.task.ID > 0, nil
}

func (r *processorUptimeReader) GetUptimeNode(context.Context, string, string) (models.UptimeNode, bool, error) {
	r.nodeCalls++
	return r.node, r.node.ID > 0, nil
}

func (r *processorUptimeReader) GetModelByCode(_ context.Context, tenantID, modelCode string) (enrich.Model, bool, error) {
	r.modelCalls++
	return enrich.Model{
		TenantID: tenantID, ModelID: modelCode, ModelCode: modelCode,
		Fields: map[string]any{rules.FieldObjectModelName: "Website Service"},
	}, true, nil
}

func (*processorUptimeReader) FindInstance(context.Context, string, enrich.InstanceQuery) (enrich.Instance, bool, error) {
	return enrich.Instance{}, false, nil
}

type processorUptimeStrategy struct{}

func (processorUptimeStrategy) GetByBKStrategyID(context.Context, string, int64) (models.CWStrategy, bool, error) {
	modelCode := rules.UptimeModelCode
	bizID := int64(2)
	return models.CWStrategy{
		ObjectModelCode: &modelCode, BKBizID: &bizID,
		Spec: models.CWStrategySpec{
			Name: "可用率", TableID: "uptimecheck.icmp", FieldName: "available",
			StrategyItem: &models.CWStrategyItem{
				QueryConfigs: []models.StrategyQueryConfig{{
					MetricField: "available", ResultTableID: "uptimecheck.icmp",
					AggregateBy: []string{"task_id", "target", "target_type", "bk_biz_id"},
				}},
			},
		},
	}, true, nil
}

type processorUptimeMetric struct{}

func (processorUptimeMetric) FindMetricLibrary(context.Context, models.MetricLibraryQuery) (models.MetricMetadata, bool, error) {
	return models.MetricMetadata{}, false, nil
}

func boolPointer(value bool) *bool { return &value }
