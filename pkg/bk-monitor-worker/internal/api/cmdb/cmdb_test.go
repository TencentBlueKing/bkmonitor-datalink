// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdb_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
)

func TestMain(m *testing.M) {
	// config.FilePath = "../../../bmw_test.yaml"
	// config.InitConfig()

	// m.Run()
}

var tenantId = tenant.DefaultTenantId

func TestSearchBusiness(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestSearchBusiness failed, err: %v", err)
		return
	}

	var result cmdb.SearchBusinessResp
	_, err = cmdbApi.SearchBusiness().SetPathParams(map[string]string{"bk_supplier_account": "0"}).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestSearchBusiness failed, err: %v", err)
		return
	}
	t.Logf("TestSearchBusiness success, result: %v", result.Data.Count)
}

func TestSearchCloudArea(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestSearchCloudArea failed, err: %v", err)
		return
	}

	var result cmdb.SearchCloudAreaResp
	_, err = cmdbApi.SearchCloudArea().SetPathParams(map[string]string{"bk_supplier_account": "0"}).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestSearchCloudArea failed, err: %v", err)
		return
	}
	t.Logf("TestSearchCloudArea success, result: %v", result.Data.Count)
}

func TestListBizHostsTopo(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestListBizHostsTopo failed, err: %v", err)
		return
	}

	params := map[string]any{
		"bk_biz_id": 2,
		"page": map[string]any{
			"start": 0,
			"limit": 10,
		},
	}

	var result cmdb.ListBizHostsTopoResp
	_, err = cmdbApi.ListBizHostsTopo().SetPathParams(map[string]string{"bk_biz_id": "2"}).SetBody(&params).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestListBizHostsTopo failed, err: %v", err)
		return
	}
	assert.Greater(t, result.Data.Count, 0)
}

func TestListHostsWithoutBiz(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestListHostsWithoutBiz failed, err: %v", err)
		return
	}

	params := map[string]any{
		"page": map[string]any{
			"start": 0,
			"limit": 10,
		},
	}

	var result cmdb.ListHostsWithoutBizResp
	_, err = cmdbApi.ListHostsWithoutBiz().SetBody(&params).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestListHostsWithoutBiz failed, err: %v", err)
		return
	}
	assert.Greater(t, result.Data.Count, 0)
	t.Logf("TestListHostsWithoutBiz success, result: %v", result.Data.Count)
}

func TestFindHostBizRelation(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestFindHostBizRelation failed, err: %v", err)
		return
	}

	params := map[string]any{
		"bk_host_id": []int{1, 2, 3},
	}

	var result cmdb.FindHostBizRelationResp
	_, err = cmdbApi.FindHostBizRelation().SetBody(&params).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestFindHostBizRelation failed, err: %v", err)
		return
	}
	assert.Greater(t, len(result.Data), 0)
	t.Logf("TestFindHostBizRelation success, result: %v", len(result.Data))
}

func TestSearchBizInstTopo(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestSearchBizInstTopo failed, err: %v", err)
		return
	}

	var result cmdb.SearchBizInstTopoResp
	_, err = cmdbApi.SearchBizInstTopo().SetPathParams(map[string]string{"bk_biz_id": "2"}).SetBody(map[string]any{"bk_biz_id": 2}).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestSearchBizInstTopo failed, err: %v", err)
		return
	}
	assert.Greater(t, len(result.Data), 0)
	t.Logf("TestSearchBizInstTopo success, result: %v", len(result.Data))
}

func TestGetBizInternalModule(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestGetBizInternalModule failed, err: %v", err)
		return
	}

	var result cmdb.GetBizInternalModuleResp
	params := map[string]any{
		"bk_supplier_account": "0",
		"bk_biz_id":           2,
	}
	_, err = cmdbApi.GetBizInternalModule().SetPathParams(map[string]string{"bk_supplier_account": "0", "bk_biz_id": "2"}).SetBody(params).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestGetBizInternalModule failed, err: %v", err)
		return
	}
	assert.Greater(t, len(result.Data.Module), 0)
	t.Logf("TestGetBizInternalModule success, result: %v", len(result.Data.Module))
}

func TestSearchObjectAttribute(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestSearchObjectAttribute failed, err: %v", err)
		return
	}

	params := map[string]any{
		"bk_obj_id": "biz",
	}

	var result cmdb.SearchObjectAttributeResp
	_, err = cmdbApi.SearchObjectAttribute().SetBody(&params).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestSearchObjectAttribute failed, err: %v", err)
		return
	}
	assert.Greater(t, len(result.Data), 0)
	t.Logf("TestSearchObjectAttribute success, result: %v", len(result.Data))
}

func TestSearchModule(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestSearchModule failed, err: %v", err)
		return
	}

	params := map[string]string{
		"bk_biz_id":           "2",
		"bk_supplier_account": "0",
		"bk_set_id":           "0",
	}

	var result cmdb.SearchModuleResp
	_, err = cmdbApi.SearchModule().SetPathParams(params).SetBody(map[string]any{"bk_biz_id": 2, "bk_supplier_account": "0"}).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestSearchModule failed, err: %v", err)
		return
	}
	assert.Greater(t, result.Data.Count, 0)
	t.Logf("TestSearchModule success, result: %v", result.Data.Count)

	params["bk_set_id"] = "2"

	_, err = cmdbApi.SearchModule().SetPathParams(params).SetBody(map[string]any{"bk_biz_id": 2, "bk_supplier_account": "0", "bk_set_id": "2"}).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestSearchModule failed, err: %v", err)
		return
	}
	assert.Greater(t, result.Data.Count, 0)
	t.Logf("TestSearchModule success, result: %v", result.Data.Count)
}

func TestSearchSet(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestSearchSet failed, err: %v", err)
		return
	}

	params := map[string]string{
		"bk_biz_id":           "2",
		"bk_supplier_account": "0",
	}

	var result cmdb.SearchSetResp
	_, err = cmdbApi.SearchSet().SetPathParams(params).SetBody(map[string]any{"bk_biz_id": 2, "bk_supplier_account": "0"}).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestSearchSet failed, err: %v", err)
		return
	}
	assert.Greater(t, result.Data.Count, 0)
	t.Logf("TestSearchSet success, result: %v", result.Data.Count)
}

func TestListServiceInstanceDetail(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestListServiceInstanceDetail failed, err: %v", err)
		return
	}

	params := map[string]any{
		"bk_biz_id": 2,
		"page": map[string]any{
			"start": 0,
			"limit": 10,
		},
	}

	var result cmdb.ListServiceInstanceDetailResp
	_, err = cmdbApi.ListServiceInstanceDetail().SetBody(params).SetResult(&result).Request()
	if err != nil {
		t.Errorf("TestListServiceInstanceDetail failed, err: %v", err)
		return
	}
	assert.Greater(t, result.Data.Count, 0)
	t.Logf("TestListServiceInstanceDetail success, result: %v", result.Data.Count)
}

func TestDynamicGroup(t *testing.T) {
	cmdbApi, err := api.GetCmdbApi(tenantId)
	if err != nil {
		t.Errorf("TestSearchDynamicGroup failed, err: %v", err)
		return
	}

	params := map[string]any{
		"bk_biz_id": 2,
		"page": map[string]any{
			"start": 0,
			"limit": 10,
		},
	}

	var searchResult cmdb.SearchDynamicGroupResp
	_, err = cmdbApi.SearchDynamicGroup().SetPathParams(map[string]string{"bk_biz_id": "2"}).SetBody(params).SetResult(&searchResult).Request()
	if err != nil {
		t.Errorf("TestSearchDynamicGroup failed, err: %v", err)
		return
	}
	assert.Greater(t, searchResult.Data.Count, 0)
	t.Logf("TestSearchDynamicGroup success, result: %v", searchResult.Data.Count)

	pathParams := map[string]string{
		"bk_biz_id": "2",
		"id":        searchResult.Data.Info[len(searchResult.Data.Info)-1].ID,
	}

	params = map[string]any{
		"bk_biz_id": 2,
		"id":        searchResult.Data.Info[len(searchResult.Data.Info)-1].ID,
		"fields":    []string{"bk_host_id"},
		"page": map[string]any{
			"start": 0,
			"limit": 10,
		},
	}

	var executeResult cmdb.ExecuteDynamicGroupResp
	_, err = cmdbApi.ExecuteDynamicGroup().SetPathParams(pathParams).SetBody(params).SetResult(&executeResult).Request()
	if err != nil {
		t.Errorf("TestExecuteDynamicGroup failed, err: %v", err)
		return
	}
	assert.True(t, executeResult.Result)
	t.Logf("TestExecuteDynamicGroup success, result: %v", executeResult.Data.Count)
}
