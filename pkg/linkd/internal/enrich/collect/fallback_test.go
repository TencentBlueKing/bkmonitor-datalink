// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package collect

import (
	"testing"

	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/enrich/models"
	"linkd/internal/enrich/rules"
)

func TestCollectFailurePreservesModelAndHostTopology(t *testing.T) {
	t.Parallel()
	reader := &collectReader{
		config: models.CollectConfig{BKTenantID: "tenant-a", BKCollectTaskID: "task-1", BKObjectCode: rules.HostModelCode, BKInstID: 101}, configFound: true,
		model: enrich.Model{TenantID: "tenant-a", ModelID: "1", ModelCode: rules.HostModelCode, Fields: map[string]any{rules.FieldObjectModelName: "主机", rules.FieldBKCMDBObjectID: "host"}}, modelFound: true,
		instance: enrich.Instance{ModelCode: rules.HostModelCode},
		topology: models.ResourceTopology{BKBizID: 2, BKBizName: "来源业务", BKSetID: 3, BKSetName: "来源集群"}, topologyFound: true,
	}
	dims := domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("task-1")}
	scope, err := enrich.NewScope(collectAlert(t, dims), enrich.Sources{CollectConfig: reader, Model: reader, OneModel: reader, CollectTopology: reader}, enrich.ScopeOptions{CMDBSetModuleEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Enrich(t.Context(), scope, dims, 2, "source subject")
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource.ModelName != "主机" || result.Resource.BKObjID != "host" || result.Resource.BKSetName != "来源集群" || result.Resource.ModelInstID != "" || dependency(result.ResourceDiagnostics) != rules.DependencyOneModel {
		t.Fatalf("result=%#v", result)
	}
}

func TestCollectRejectsForeignRelatedHostCompensation(t *testing.T) {
	t.Parallel()
	reader := &collectReader{
		config: models.CollectConfig{BKTenantID: "tenant-a", BKCollectTaskID: "task-1", BKObjectCode: "cw-Service", BKInstID: 2, IsRemoteCollect: true}, configFound: true,
		model: enrich.Model{TenantID: "tenant-a", ModelID: "1", ModelCode: "cw-Service", Fields: map[string]any{rules.FieldHostRelatedField: "service_run_host"}}, modelFound: true,
		instance: enrich.Instance{TenantID: "tenant-a", ModelCode: "cw-Service", InstanceID: "2"}, instanceFound: true,
		relatedHost: enrich.Instance{TenantID: "tenant-b", ModelCode: rules.HostModelCode, InstanceID: "101", Fields: map[string]any{rules.FieldBKHostID: int64(101), rules.FieldBKCloudID: int64(0), rules.FieldBKCloudName: "外部租户云区域"}}, relatedFound: true,
	}
	dims := domain.DimensionMap{rules.FieldBKCollectConfigID: domain.NewStringScalar("task-1")}
	scope, err := enrich.NewScope(collectAlert(t, dims), enrich.Sources{CollectConfig: reader, Model: reader, OneModel: reader, CollectTopology: reader}, enrich.ScopeOptions{CMDBSetModuleEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	result, err := Enrich(t.Context(), scope, dims, 2, "source subject")
	if err != nil {
		t.Fatal(err)
	}
	if result.Resource.BKCloudName != "" || reader.topologyCalls != 0 || result.Resource.ModelInstID != "2" {
		t.Fatalf("result=%#v topology calls=%d", result, reader.topologyCalls)
	}
}
