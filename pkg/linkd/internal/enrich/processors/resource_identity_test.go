// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processors

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestCanonicalResourceIdentityPrecedesCollectTask(t *testing.T) {
	t.Parallel()
	for _, task := range []string{"43", "44", "48", ""} {
		name := task
		if name == "" {
			name = "without collect task"
		}
		t.Run(name, func(t *testing.T) {
			reader := canonicalResourceReader()
			event := canonicalResourceEvent(t, task)
			if task == "" {
				delete(event.Dimensions, rules.FieldBKCollectConfigID)
			}
			if task == "44" {
				event.Dimensions[rules.FieldNoDataDimension] = domain.NewBoolScalar(true)
			}
			if task == "48" {
				event.Dimensions[rules.FieldBKInstID] = domain.NewStringScalar("101")
			}
			original := event.Clone()
			resource, display := runCanonicalResource(t, event, reader)
			if resource.Status != domain.EnrichStatusSucceeded || string(resource.Value["model_id"]) != `"cw-MySQL"` ||
				string(resource.Value["model_inst_id"]) != `"22"` || string(display.Value["object"]) != `"订单数据库"` ||
				string(display.Value["title"]) != `"订单数据库发生了可用率告警"` {
				t.Fatalf("status=%s identity=%s/%s object=%s title=%s", resource.Status, resource.Value["model_id"], resource.Value["model_inst_id"], display.Value["object"], display.Value["title"])
			}
			var labels []string
			if err := json.Unmarshal(resource.Value["cw_labels"], &labels); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(labels, "cw-MySQL|22") || slices.Contains(labels, "cw-Host|101") || reader.collectCalls != 0 ||
				len(reader.queries) != 1 || reader.queries[0].ModelCode != "cw-MySQL" || reader.queries[0].InstanceID != "22" {
				t.Fatalf("labels=%v collect=%d queries=%#v", labels, reader.collectCalls, reader.queries)
			}
			if !reflect.DeepEqual(event, original) {
				t.Fatal("enrichment modified the source Event")
			}
		})
	}
}

func TestCanonicalHostIdentityPrecedesCollector(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name         string
		hostID       string
		subjectID    string
		wantInstance string
	}{
		{name: "matching host", hostID: "101", subjectID: "101", wantInstance: "101"},
		{name: "native host differs", hostID: "999", subjectID: "101", wantInstance: "101"},
		{name: "subject differs", hostID: "101", subjectID: "999", wantInstance: "101"},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := processorBaseTargetAlert(t, domain.DimensionMap{
				"model_id": domain.NewStringScalar("cw-Host"), "model_inst_id": domain.NewStringScalar("101"),
				"bk_target_host_id": domain.NewStringScalar(test.hostID),
			})
			event.SubjectSystem, event.SubjectType, event.SubjectID = "cmdb", "host", test.subjectID
			reader := &businessFallbackReader{
				processorBaseTargetReader: processorHostReader(),
				business:                  enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-biz", InstanceID: "2", Attributes: map[string]any{"bk_biz_name": "业务"}},
			}
			scope, err := enrich.NewScope(event, enrich.Sources{
				CWStrategy: processorBaseTargetStrategy{modelCode: "cw-Host"}, Model: reader, OneModel: reader, CollectTopology: reader,
			})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (MonitorResource{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			if scope.Context().Resource.Values.ModelInstID != test.wantInstance {
				t.Fatalf("values=%#v diagnostics=%v", scope.Context().Resource.Values, result.Diagnostics)
			}
			if test.wantInstance == "" && (reader.instanceCalls != 0 || len(result.Diagnostics) == 0) {
				t.Fatalf("queries=%d diagnostics=%v", reader.instanceCalls, result.Diagnostics)
			}
		})
	}
}

func TestStrategyInstanceQueryPrefersCanonicalIdentity(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		instance domain.Scalar
		wantOK   bool
	}{
		{name: "canonical string", instance: domain.NewStringScalar("22"), wantOK: true},
		{name: "canonical number", instance: processorNumber(t, 22), wantOK: true},
		{name: "empty identity", instance: domain.NewStringScalar("")},
		{name: "invalid identity", instance: domain.NewStringScalar("022")},
	} {
		t.Run(test.name, func(t *testing.T) {
			event := canonicalResourceEvent(t, "43")
			event.Dimensions[rules.FieldModelInstID] = test.instance
			event.Dimensions[rules.FieldBKInstID] = domain.NewStringScalar("101")
			query, ok := strategyInstanceQuery(rules.ClassifyBaseTarget("cw-MySQL", event.Dimensions), "cw-MySQL", event.Dimensions)
			if ok != test.wantOK || ok && (query.ModelCode != "cw-MySQL" || query.InstanceID != "22") {
				t.Fatalf("query=%#v ok=%t", query, ok)
			}
		})
	}
}

func TestCanonicalResourceFailureDoesNotBindCollector(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		change func(*domain.Event, *canonicalIdentityReader)
	}{
		{name: "missing model", change: func(e *domain.Event, _ *canonicalIdentityReader) { delete(e.Dimensions, "model_id") }},
		{name: "missing instance", change: func(e *domain.Event, _ *canonicalIdentityReader) { delete(e.Dimensions, "model_inst_id") }},
		{name: "empty instance", change: func(e *domain.Event, _ *canonicalIdentityReader) {
			e.Dimensions["model_inst_id"] = domain.NewStringScalar("")
		}},
		{name: "invalid instance", change: func(e *domain.Event, _ *canonicalIdentityReader) {
			e.Dimensions["model_inst_id"] = domain.NewStringScalar("022")
		}},
		{name: "model conflict", change: func(e *domain.Event, _ *canonicalIdentityReader) {
			e.Dimensions["model_id"] = domain.NewStringScalar("cw-Host")
		}},
		{name: "instance missing", change: func(_ *domain.Event, r *canonicalIdentityReader) { r.instanceFound = false }},
		{name: "instance error", change: func(_ *domain.Event, r *canonicalIdentityReader) { r.instanceErr = errors.New("OneModel unavailable") }},
		{name: "foreign tenant", change: func(_ *domain.Event, r *canonicalIdentityReader) { r.instance.TenantID = "tenant-b" }},
		{name: "wrong instance", change: func(_ *domain.Event, r *canonicalIdentityReader) { r.instance.InstanceID = "23" }},
		{name: "wrong model", change: func(_ *domain.Event, r *canonicalIdentityReader) { r.instance.ModelCode = "cw-Host" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := canonicalResourceReader()
			event := canonicalResourceEvent(t, "43")
			// 旧实例与采集任务均可读，显式规范身份失败也不能改绑它们。
			event.Dimensions["bk_inst_id"] = domain.NewStringScalar("101")
			test.change(&event, reader)
			resource, display := runCanonicalResource(t, event, reader)
			if resource.Status != domain.EnrichStatusPartial || len(resource.Diagnostics) == 0 ||
				string(resource.Value["model_inst_id"]) != `""` || string(display.Value["object"]) == `"采集主机"` || reader.collectCalls != 0 {
				t.Fatalf("status=%s identity=%s/%s diagnostics=%v object=%s collect=%d", resource.Status, resource.Value["model_id"], resource.Value["model_inst_id"], resource.Diagnostics, display.Value["object"], reader.collectCalls)
			}
		})
	}
}

func canonicalResourceEvent(t *testing.T, task string) domain.Event {
	t.Helper()
	return processorBaseTargetAlert(t, domain.DimensionMap{
		"model_id": domain.NewStringScalar("cw-MySQL"), "model_inst_id": domain.NewStringScalar("22"),
		"entity_uid": domain.NewStringScalar("cw-MySQL|22"), "bk_collect_config_id": domain.NewStringScalar(task),
		"cw_object_model_id":      domain.NewStringScalar("cw_object_model_id"),
		"cw_object_model_inst_id": domain.NewStringScalar("cw_object_model_inst_id"),
	})
}

func runCanonicalResource(t *testing.T, event domain.Event, reader *canonicalIdentityReader) (enrich.ProcessorEnvelope, enrich.ProcessorEnvelope) {
	t.Helper()
	chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, Display{}}, enrich.Sources{
		CWStrategy: processorBaseTargetStrategy{modelCode: "cw-MySQL"}, Metric: processorBaseTargetMetric{},
		Model: reader, OneModel: reader, CollectConfig: reader, CollectTopology: reader,
	})
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
	return payload.Processors[0][rules.MonitorResourceProcessor], payload.Processors[1][rules.DisplayProcessor]
}

type canonicalIdentityReader struct {
	*processorBaseTargetReader
	queries      []enrich.InstanceQuery
	collectCalls int
}

func canonicalResourceReader() *canonicalIdentityReader {
	return &canonicalIdentityReader{processorBaseTargetReader: &processorBaseTargetReader{
		model: processorModel("tenant-a", "cw-MySQL", "MySQL", "mysql"), modelFound: true,
		instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-MySQL", InstanceID: "22", Attributes: map[string]any{
			"bk_inst_id": int64(22), "bk_biz_id": int64(2), "bk_biz_name": "业务", "bk_inst_display_name": "订单数据库",
		}}, instanceFound: true,
	}}
}

func (r *canonicalIdentityReader) GetCollectConfig(_ context.Context, tenant, task string) (models.CollectConfig, bool, error) {
	r.collectCalls++
	return models.CollectConfig{UID: "collect-uid", BKTenantID: tenant, BKCollectTaskID: task, BKObjectCode: "cw-Host", BKInstID: 101}, true, nil
}

func (r *canonicalIdentityReader) FindInstance(ctx context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.queries = append(r.queries, query)
	if query.ModelCode == "cw-Host" {
		return enrich.Instance{TenantID: tenant, ModelCode: "cw-Host", InstanceID: "101", Attributes: map[string]any{"bk_biz_id": int64(2), "bk_inst_display_name": "采集主机"}}, true, nil
	}
	return r.processorBaseTargetReader.FindInstance(ctx, tenant, query)
}
