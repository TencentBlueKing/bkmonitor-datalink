// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	metadataTask "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/testutil/routefixture"
)

func TestRouteRefreshAPIIntegration(t *testing.T) {
	fixture := routefixture.New(t)
	fixture.ResetOutput(t)
	patch := gomonkey.ApplyGlobalVar(&cfg.EnableMultiTenantMode, true)
	defer patch.Reset()
	router := NewHTTPService()
	post := func(path, body string, status int) {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest("POST", "/bmw/metadata/routes/"+path+"/refresh", strings.NewReader(body)))
		require.Equal(t, status, response.Code, response.Body.String())
	}
	require.NoError(t, metadataTask.PushAndPublishSpaceRouterInfo(context.Background(), nil))
	full := fixture.Redis.HGetAll(cfg.SpaceToResultTableKey)
	fixture.Server.FlushAll()
	for _, sp := range [][2]string{{"bkcc", "1"}, {"bkci", "project"}, {"bksaas", "app"}} {
		field := sp[0] + "__" + sp[1] + "|system"
		require.NotEmpty(t, full[field])
		post("space", fmt.Sprintf(`{"bk_tenant_id":"system","space_type":"%s","space_id":"%s"}`, sp[0], sp[1]), 200)
		require.JSONEq(t, full[field], fixture.Redis.HGet(cfg.SpaceToResultTableKey, field))
	}
	require.Len(t, fixture.Redis.HGetAll(cfg.SpaceToResultTableKey), 3)
	post("space", `{"bk_tenant_id":"other","space_type":"bkcc","space_id":"1"}`, 404)
	post("data-label", `{"bk_tenant_id":"system","data_labels":["legacy","removed"]}`, 200)
	mappings := fixture.Redis.HGetAll(cfg.DataLabelToResultTableKey)
	require.Len(t, mappings, 2)
	require.JSONEq(t, `[]`, mappings["removed|system"])
	var ids []string
	require.NoError(t, json.Unmarshal([]byte(mappings["legacy|system"]), &ids))
	require.ElementsMatch(t, []string{"cpu.used", "cpu.idle"}, ids)
	post("data-label", `{"bk_tenant_id":"other","table_ids":["cpu.used"]}`, 200)
	require.NotEmpty(t, fixture.Redis.HGet(cfg.DataLabelToResultTableKey, "foreign_alias|other"))
	post("result-table", `{"bk_tenant_id":"system","table_ids":["virtual.logs","cpu.used"],"is_publish":false}`, 200)
	require.Len(t, fixture.Redis.HGetAll(cfg.ResultTableDetailKey), 2)
	require.NotEmpty(t, fixture.Redis.HGet(cfg.ResultTableDetailKey, "virtual.logs|system"))
	post("result-table", `{"bk_tenant_id":"system","table_ids":["rule.cpu"],"is_publish":false}`, 200)
	require.Len(t, fixture.Redis.HGetAll(cfg.ResultTableDetailKey), 3)
	require.NotEmpty(t, fixture.Redis.HGet(cfg.ResultTableDetailKey, "rule.cpu|system"))
}
