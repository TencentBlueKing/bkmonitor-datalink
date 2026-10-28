// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package kachook

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/datasources"
	"linkd/internal/enrich/processors"
	"linkd/internal/enrich/view"
)

// 样本仅由已授权只读接口提取；删除原始消息和无关基础设施属性，保留冻结丰富与定位输入。
type k8sReplayFixture struct {
	Alert     domain.Alert `json:"alert"`
	Recovery  domain.Event `json:"recovery"`
	Instances []struct {
		Tenant     string         `json:"bk_tenant_id"`
		Model      string         `json:"model_id"`
		ID         string         `json:"model_inst_id"`
		Attributes map[string]any `json:"attributes"`
	} `json:"instances"`
}

func loadK8sReplay(t *testing.T) k8sReplayFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/k8s_p130_15710.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture k8sReplayFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func TestK8sFrozenAlertCompatibilityProjection(t *testing.T) {
	t.Parallel()
	fixture := loadK8sReplay(t)
	raw, err := CompatibilityPayload(fixture.Alert, kacLevel)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"bcs_cluster_id": "BCS-K8S-00000", "cluster_name": "蓝鲸7.0", "namespace": "kingeye",
		"pod_name": "trace-fe44b270e9e5-taskmanager-1-6", "container_name": "flink-main-container",
	} {
		if fields[key] != want {
			t.Errorf("KAC %s=%v want=%s", key, fields[key], want)
		}
	}
	if fields["Namespace"] != "" {
		t.Errorf("legacy Namespace changed: %v", fields["Namespace"])
	}
	t.Logf("local frozen Alert projection: namespace=%v cluster_name=%v", fields["namespace"], fields["cluster_name"])
}

func TestK8sCompatibilityProjectsEffectiveFields(t *testing.T) {
	t.Parallel()
	alert := testAlert()
	// 固定字段不走自定义目录；labels 应优先于 extra_data，显式空值应隐藏扩展字段。
	alert.ExtraData["namespace"] = json.RawMessage(`"old"`)
	alert.Labels["namespace"] = domain.NewStringScalar("new")
	alert.Labels["service"] = domain.NewStringScalar("api")
	alert.Labels["workload_kind"] = domain.NewStringScalar("Deployment")
	alert.Labels["workload_name"] = domain.NewStringScalar("api-worker")
	alert.Labels["cluster_name"] = domain.NewStringScalar("")
	raw, err := CompatibilityPayload(alert, kacLevel)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"namespace": "new", "service": "api", "workload_kind": "Deployment", "workload_name": "api-worker"} {
		if fields[key] != want {
			t.Errorf("%s=%v want=%s", key, fields[key], want)
		}
	}
	if _, found := fields["cluster_name"]; found {
		t.Fatal("empty extension sent")
	}
}

func TestK8sRecoveryReplayWithMissingContainer(t *testing.T) {
	t.Parallel()
	fixture := loadK8sReplay(t)
	event := fixture.Recovery.Clone()
	// 重放当次 K8s 入口：保留 resource 步骤的残缺模型身份；不重算策略或指标。
	event.Enrich = domain.EventEnrichData{}
	event.EnrichStatus = domain.EnrichStatusPending
	event.Labels["model_id"] = domain.NewStringScalar("cw-K8s_Container")
	event.Labels["model_inst_id"] = domain.NewStringScalar("")
	reader := &k8sReplayReader{fixture: fixture}
	k8sReader, err := datasources.NewOneModelK8sReader(reader)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := enrich.NewChain([]enrich.Processor{processors.K8s{}}, enrich.Sources{K8s: k8sReader})
	if err != nil {
		t.Fatal(err)
	}
	result, err := chain.Enrich(t.Context(), enrich.Input{Event: event})
	if err != nil {
		t.Fatal(err)
	}
	event.Enrich = result.Data
	effective, err := view.EnrichedEvent(event, "critical")
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"namespace": "kingeye", "cluster_name": "蓝鲸7.0", "bk_biz_id": "2", "bk_biz_name": "蓝鲸2修改", "model_id": "", "model_inst_id": ""} {
		got, _ := effective.Labels[key].StringValue()
		if got != want {
			t.Errorf("recovery %s=%q want=%q", key, got, want)
		}
	}
	payload, err := enrich.DecodePayload(result.Data.Evaluations[0].Data)
	if err != nil {
		t.Fatal(err)
	}
	step := payload.Processors[0]["k8s"]
	if step.Status != domain.EnrichStatusPartial || len(step.Diagnostics) != 1 || step.Diagnostics[0].Dependency != "onemodel" || !reflect.DeepEqual(step.Diagnostics[0].Fields, []string{"cw-K8s_Container"}) {
		t.Errorf("degradation=%+v", step)
	}
	if !reflect.DeepEqual(reader.models, []string{"cw-K8s_Container", "cw-K8s_Namespace", "cw-K8s_Cluster"}) {
		t.Errorf("queries=%v", reader.models)
	}
	// 本地候选 Alert 只用于验证恢复快照到 KAC 的转换，不写回 opening Alert 或远端文档。
	candidate := fixture.Alert.Clone()
	candidate.Enrich = result.Data.Evaluations[0].Data
	candidate.Labels = event.Labels.Clone()
	candidate.ExtraData = event.ExtraData.Clone()
	candidate.EnrichStatus = result.Status
	raw, err := CompatibilityPayload(candidate, kacLevel)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["namespace"] != "kingeye" || fields["cluster_name"] != "蓝鲸7.0" || fields["model_inst_id"] != "" {
		t.Errorf("recovery projection=%s", raw)
	}
	t.Logf("local recovery replay: namespace=%v cluster_name=%v status=%s identity=%q", fields["namespace"], fields["cluster_name"], step.Status, fields["model_inst_id"])
}

type k8sReplayReader struct {
	fixture k8sReplayFixture
	models  []string
}

func (r *k8sReplayReader) FindInstance(_ context.Context, tenant string, query enrich.InstanceQuery) (enrich.Instance, bool, error) {
	r.models = append(r.models, query.ModelCode)
	for _, row := range r.fixture.Instances {
		if row.Tenant != tenant || row.Model != query.ModelCode {
			continue
		}
		matches := true
		for _, filter := range query.AttributeFilters {
			if row.Attributes[filter.Field] != filter.Value {
				matches = false
			}
		}
		if matches {
			return enrich.Instance{TenantID: row.Tenant, ModelCode: row.Model, InstanceID: row.ID, Attributes: row.Attributes}, true, nil
		}
	}
	return enrich.Instance{}, false, nil
}
