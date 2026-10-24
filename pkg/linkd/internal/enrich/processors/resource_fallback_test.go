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
	"errors"
	"reflect"
	"slices"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestNoDataHostOriginalDimensionsFlow(t *testing.T) {
	t.Parallel()
	event := processorBaseTargetAlert(t, domain.DimensionMap{"__NO_DATA_DIMENSION__": domain.NewBoolScalar(true), "bk_host_id": domain.NewStringScalar("101")})
	event.SubjectSystem, event.SubjectType, event.SubjectID, event.SubjectName = "cmdb", "host", "101", ""
	original := event.Clone()
	reader := processorHostReader()
	chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, Display{}, Metric{}}, enrich.Sources{CWStrategy: processorBaseTargetStrategy{modelCode: rules.HostModelCode}, Metric: processorBaseTargetMetric{}, Model: reader, OneModel: reader, CollectTopology: reader})
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
	resource := payload.Processors[0][rules.MonitorResourceProcessor]
	display := payload.Processors[1][rules.DisplayProcessor]
	metric := payload.Processors[2][rules.MetricProcessor]
	if result.Status != domain.EnrichStatusSucceeded || string(resource.Value["model_inst_id"]) != `"101"` || string(display.Value["object"]) != `"主机 101"` || string(metric.Value["where_condition"]) != `""` || !reflect.DeepEqual(event, original) {
		t.Fatalf("result=%#v resource=%#v display=%#v metric=%#v", result, resource, display, metric)
	}
}

func TestResourceBusinessCompensationPreservesModelWithoutBinding(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name     string
		business enrich.Instance
		readErr  error
		wantName string
	}{
		{name: "business name", business: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-biz", InstanceID: "2", Attributes: map[string]any{"bk_biz_name": "来源业务"}}, wantName: "来源业务"},
		{name: "foreign business rejected", business: enrich.Instance{TenantID: "tenant-b", ModelCode: "cw-biz", InstanceID: "2", Attributes: map[string]any{"bk_biz_name": "其他租户"}}},
		{name: "wrong business rejected", business: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-biz", InstanceID: "9", Attributes: map[string]any{"bk_biz_name": "其他业务"}}},
		{name: "business failure retains model", readErr: errors.New("business unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &businessFallbackReader{processorBaseTargetReader: processorServiceReader(), business: test.business, readErr: test.readErr}
			event := processorBaseTargetAlert(t, domain.DimensionMap{"__NO_DATA_DIMENSION__": domain.NewBoolScalar(true)})
			scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: processorBaseTargetStrategy{modelCode: "cw-Service"}, Model: reader, OneModel: reader})
			if err != nil {
				t.Fatal(err)
			}
			result, err := (MonitorResource{}).Process(t.Context(), scope)
			if err != nil {
				t.Fatal(err)
			}
			values := scope.Context().Resource.Values
			if result.Status != domain.EnrichStatusPartial || values.ModelName != "服务" || values.BKBizName != test.wantName || values.ModelInstID != "" || reader.instanceCalls != 0 || slices.Contains(values.CWLabels, "cw-Service|2") {
				t.Fatalf("result=%#v values=%#v", result, values)
			}
		})
	}
}

type businessFallbackReader struct {
	*processorBaseTargetReader
	business enrich.Instance
	readErr  error
}

func (r *businessFallbackReader) FindInstance(ctx context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	if query.ModelCode == "cw-biz" {
		return r.business, r.business.InstanceID != "", r.readErr
	}
	return r.processorBaseTargetReader.FindInstance(ctx, tenant, query)
}

func TestNoDataIdentityDoesNotBecomeMetricFilter(t *testing.T) {
	t.Parallel()
	dims := domain.DimensionMap{"__NO_DATA_DIMENSION__": domain.NewBoolScalar(true), "model_id": domain.NewStringScalar("cw-Host"), "model_inst_id": domain.NewStringScalar("101"), "device": domain.NewStringScalar("cpu-total")}
	original := dims.Clone()
	query := models.StrategyQueryConfig{AggregateBy: []string{"model_id", "model_inst_id", "device"}}
	selected := selectQueryDimensions(query, metricDimensions(dims))
	if len(selected) != 1 || selected["device"] != "cpu-total" || cleanWhereCondition(metricDimensions(dims)) != "device='cpu-total'" || !reflect.DeepEqual(dims, original) {
		t.Fatalf("selected=%#v dimensions=%#v", selected, dims)
	}
	dims["__NO_DATA_DIMENSION__"] = domain.NewBoolScalar(false)
	if len(selectQueryDimensions(query, metricDimensions(dims))) != 3 {
		t.Fatal("ordinary metric identity dimensions were removed")
	}
}

func TestDataDisplayKeepsValidatedInstanceName(t *testing.T) {
	t.Parallel()
	reader := &dataSliceReader{model: "cw-MySQL", found: true, instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-MySQL", InstanceID: "17", Attributes: map[string]any{"bk_inst_id": int64(17), "bk_biz_id": int64(2), "bk_inst_display_name": "订单数据库"}}}
	event := processorBaseTargetAlert(t, domain.DimensionMap{"model_id": domain.NewStringScalar("cw-MySQL"), "model_inst_id": domain.NewStringScalar("17")})
	chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, Display{}}, enrich.Sources{CWStrategy: reader, Metric: reader, Model: reader, OneModel: reader})
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
	if string(payload.Processors[1][rules.DisplayProcessor].Value["object"]) != `"订单数据库"` {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestHostDisplayKeepsSourceIPOnInstanceFailure(t *testing.T) {
	t.Parallel()
	reader := processorHostReader()
	reader.instanceFound = false
	event := processorBaseTargetAlert(t, domain.DimensionMap{"ip": domain.NewStringScalar("192.0.2.11"), "bk_cloud_id": processorNumber(t, 0)})
	event.SubjectName = ""
	chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, Display{}}, enrich.Sources{CWStrategy: processorBaseTargetStrategy{modelCode: rules.HostModelCode}, Metric: processorBaseTargetMetric{}, Model: reader, OneModel: reader, CollectTopology: reader})
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
	if result.Status != domain.EnrichStatusPartial || string(payload.Processors[1][rules.DisplayProcessor].Value["object"]) != `"192.0.2.11"` {
		t.Fatalf("payload=%#v", payload)
	}
}

func TestBusinessCompensationSurvivesModelAndInstanceFailure(t *testing.T) {
	t.Parallel()
	base := processorServiceReader()
	base.modelErr = errors.New("model unavailable")
	base.instanceFound = false
	reader := &businessFallbackReader{processorBaseTargetReader: base, business: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-biz", InstanceID: "2", Attributes: map[string]any{"bk_biz_name": "保留业务"}}}
	event := processorBaseTargetAlert(t, domain.DimensionMap{"__NO_DATA_DIMENSION__": domain.NewBoolScalar(true), "model_id": domain.NewStringScalar("cw-Service"), "model_inst_id": domain.NewStringScalar("2")})
	scope, err := enrich.NewScope(event, enrich.Sources{CWStrategy: processorBaseTargetStrategy{modelCode: "cw-Service"}, Model: reader, OneModel: reader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := (MonitorResource{}).Process(t.Context(), scope)
	if err != nil {
		t.Fatal(err)
	}
	values := scope.Context().Resource.Values
	if result.Status != domain.EnrichStatusPartial || values.BKBizName != "保留业务" || values.ModelName != "" || values.ModelInstID != "" || values.BKInstID != nil {
		t.Fatalf("result=%#v values=%#v", result, values)
	}
}

func TestNoDataK8sKeepsNaturalIdentity(t *testing.T) {
	t.Parallel()
	code := rules.K8sNodeModelCode
	reader := &processorBaseTargetReader{model: processorModel("tenant-a", code, "K8s节点", ""), modelFound: true}
	k8sReader := &k8sTestReader{instance: enrich.Instance{TenantID: "tenant-a", ModelCode: code, InstanceID: "cluster-1|node-1", Attributes: map[string]any{"bk_biz_id": int64(2), "bk_biz_name": "业务", "display_name": "node-1"}}, found: true}
	event := processorBaseTargetAlert(t, domain.DimensionMap{"__NO_DATA_DIMENSION__": domain.NewBoolScalar(true), "bcs_cluster_id": domain.NewStringScalar("cluster-1"), "node": domain.NewStringScalar("node-1")})
	event.SubjectName = ""
	chain, err := enrich.NewChain([]enrich.Processor{MonitorResource{}, K8s{}, Display{}}, enrich.Sources{CWStrategy: processorBaseTargetStrategy{modelCode: code}, Metric: processorBaseTargetMetric{}, Model: reader, OneModel: reader, K8s: k8sReader})
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
	if result.Status != domain.EnrichStatusSucceeded || string(payload.Processors[0][rules.MonitorResourceProcessor].Value["model_inst_id"]) != `"cluster-1|node-1"` || string(payload.Processors[2][rules.DisplayProcessor].Value["object"]) != `"node-1"` {
		t.Fatalf("payload=%#v", payload)
	}
	count := 0
	for _, model := range k8sReader.models {
		if model == code {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("node lookups=%d", count)
	}
}
