// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apiservice

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/bkgse"
)

func TestGatewayRequestsUseTenantAdmin(t *testing.T) {
	t.Setenv("BK_ADMIN_USERNAME", "worker-login")
	oldHost, oldGSE, oldData := cfg.BkApiUrl, cfg.BkApiGseApiGwUrl, cfg.BkApiBkdataApiBaseUrl
	oldMode, oldCode, oldSecret := cfg.EnableMultiTenantMode, cfg.BkApiAppCode, cfg.BkApiAppSecret
	t.Cleanup(func() {
		cfg.BkApiUrl, cfg.BkApiGseApiGwUrl, cfg.BkApiBkdataApiBaseUrl = oldHost, oldGSE, oldData
		cfg.EnableMultiTenantMode, cfg.BkApiAppCode, cfg.BkApiAppSecret = oldMode, oldCode, oldSecret
	})

	type request struct{ tenant, username, operator string }
	received := make(chan request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		tenantID := r.Header.Get("X-Bk-Tenant-Id")
		if strings.HasSuffix(r.URL.Path, "/lookup/") {
			fmt.Fprintf(w, `{"data":[{"bk_username":%q}]}`, "user-"+tenantID)
			return
		}
		var auth map[string]string
		assert.NoError(t, json.Unmarshal([]byte(r.Header.Get("X-Bkapi-Authorization")), &auth))
		call := request{tenant: tenantID, username: auth["bk_username"]}
		switch r.URL.Path {
		case "/api/v2/data/query_route":
			var params bkgse.QueryRouteParams
			assert.NoError(t, json.NewDecoder(r.Body).Decode(&params))
			call.operator = params.Operation.OperatorName
			fmt.Fprint(w, `{"result":true}`)
		case "/v4/dd/":
			assert.Equal(t, tenantID, r.URL.Query().Get("bk_tenant_id"))
			fmt.Fprint(w, `{"result":true,"data":{"metrics":[{"name":"cpu"}]}}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		received <- call
	}))
	defer server.Close()
	t.Setenv("BKAPP_USER_API_BASE_URL", server.URL)
	cfg.BkApiUrl, cfg.BkApiGseApiGwUrl, cfg.BkApiBkdataApiBaseUrl = server.URL, server.URL, server.URL
	cfg.EnableMultiTenantMode, cfg.BkApiAppCode, cfg.BkApiAppSecret = true, "app", "test-secret"

	for _, operator := range []string{"", "explicit-operator"} {
		tenantID := uuid.NewString()
		params := bkgse.QueryRouteParams{Condition: bkgse.RouteMetadata{ChannelId: 1}, Operation: bkgse.Operation{OperatorName: operator}}
		_, err := Gse.QueryRoute(tenantID, params)
		require.NoError(t, err)
		username := "user-" + tenantID
		if operator == "" {
			operator = username
		}
		assert.Equal(t, request{tenantID, username, operator}, <-received)
		_, err = (BkdataService{}).QueryMetricAndDimension(tenantID, "vm", "test_table", "")
		require.NoError(t, err)
		assert.Equal(t, request{tenant: tenantID, username: username}, <-received)
	}
}
