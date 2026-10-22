// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
)

func TestGetServiceInstancesPagesWithUniqueSort(t *testing.T) {
	const total = cmdbApiPageSize + 1
	var mu sync.Mutex
	sorts := make([]any, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Page map[string]any `json:"page"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		sorts = append(sorts, body.Page["sort"])
		mu.Unlock()

		start, limit := int(body.Page["start"].(float64)), int(body.Page["limit"].(float64))
		info := make([]map[string]any, 0)
		for id := total - start; id > 0 && len(info) < limit; id-- {
			info = append(info, map[string]any{"id": id, "bk_host_id": 1, "bk_module_id": 1})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "data": map[string]any{"count": total, "info": info}})
	}))
	defer server.Close()

	bkTenantId := "service-instance-page-sort-test"
	oldUrl := cfg.BkApiCmdbApiGatewayUrl
	cfg.BkApiCmdbApiGatewayUrl = server.URL
	patches := gomonkey.ApplyFunc(tenant.GetTenantAdminUser, func(string) (string, error) { return "admin", nil })
	defer func() {
		patches.Reset()
		cfg.BkApiCmdbApiGatewayUrl = oldUrl
		cmdbApiClientMutex.Lock()
		delete(cmdbApiClients, bkTenantId)
		cmdbApiClientMutex.Unlock()
	}()

	instances, err := getServiceInstances(context.Background(), bkTenantId, 2)
	assert.NoError(t, err)

	ids := make(map[int]struct{})
	for _, instance := range instances {
		ids[instance.ID] = struct{}{}
	}
	assert.Len(t, ids, total)
	assert.Len(t, sorts, 2)
	for _, sort := range sorts {
		assert.Equal(t, "-id", sort)
	}
}

func TestCmdbPagedRequestsUseUniqueSort(t *testing.T) {
	var sort any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Page map[string]any `json:"page"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sort = body.Page["sort"]
		_ = json.NewEncoder(w).Encode(map[string]any{"result": true, "data": map[string]any{"count": 0, "info": []any{}}})
	}))
	defer server.Close()

	bkTenantId := "cmdb-page-sort-test"
	oldUrl := cfg.BkApiCmdbApiGatewayUrl
	cfg.BkApiCmdbApiGatewayUrl = server.URL
	errStop := errors.New("stop after first page")
	patches := gomonkey.ApplyFunc(tenant.GetTenantAdminUser, func(string) (string, error) { return "admin", nil })
	patches.ApplyFunc(api.GetCmdbApi, func(tenantId string) (*cmdb.Client, error) { return getCmdbApi(tenantId), nil })
	patches.ApplyFunc(api.BatchApiRequest, func(_ int, _ func(any) (int, error), getReqFunc func(int) define.Operation, _ int) ([]any, error) {
		var resp any
		_, _ = getReqFunc(1).SetResult(&resp).Request()
		return nil, errStop
	})
	defer func() {
		patches.Reset()
		cfg.BkApiCmdbApiGatewayUrl = oldUrl
		cmdbApiClientMutex.Lock()
		delete(cmdbApiClients, bkTenantId)
		cmdbApiClientMutex.Unlock()
	}()

	ctx := context.Background()
	cases := []struct {
		name string
		call func() error
		sort string
	}{
		{"module", func() error { _, err := getModuleListByBizID(ctx, bkTenantId, 2); return err }, "bk_module_id"},
		{"set", func() error { _, err := getSetListByBizID(ctx, bkTenantId, 2); return err }, "bk_set_id"},
		{"business", func() error { _, err := getBusinessList(ctx, bkTenantId); return err }, "bk_biz_id"},
		{"dynamic group list", func() error { _, err := getDynamicGroupList(ctx, bkTenantId, 2); return err }, "id"},
		{"dynamic group host", func() error { _, err := getDynamicGroupRelatedIds(ctx, bkTenantId, 2, "g", "host"); return err }, "bk_host_id"},
		{"dynamic group set", func() error { _, err := getDynamicGroupRelatedIds(ctx, bkTenantId, 2, "g", "set"); return err }, "bk_set_id"},
	}
	for _, c := range cases {
		sort = nil
		assert.ErrorIs(t, c.call(), errStop, c.name)
		assert.Equal(t, c.sort, sort, c.name)
	}
}
