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
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/agiledragon/gomonkey/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	metadataTask "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/task"
)

func TestPartialRouteRefreshRequiresTargets(t *testing.T) {
	router := gin.New()
	registerRouteRefresh(router.Group(RouterPrefix))
	for _, tc := range []struct{ path, body string }{
		{"result-table", `{"bk_tenant_id":"system"}`},
		{"result-table", `{"bk_tenant_id":"system","table_ids":[]}`},
		{"result-table", `{"bk_tenant_id":"system","table_ids":[""]}`},
		{"result-table", `{"table_ids":["system.cpu"]}`},
		{"data-label", `{"bk_tenant_id":"system"}`},
		{"data-label", `{"bk_tenant_id":"system","data_labels":["system"],"table_ids":["system.cpu"]}`},
		{"space", `{"bk_tenant_id":"system","space_type":"bkcc"}`},
		{"space", `{"bk_tenant_id":"system","space_type":"unknown","space_id":"1"}`},
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("POST", "/bmw/metadata/routes/"+tc.path+"/refresh", strings.NewReader(tc.body)))
		require.Equal(t, 400, w.Code, tc.body)
	}
}

func TestRefreshResultTablePublishAndExecutionError(t *testing.T) {
	router := gin.New()
	registerRouteRefresh(router.Group(RouterPrefix))
	for _, tc := range []struct {
		name, body string
		publish    bool
		err        error
		status     int
	}{
		{"publish by default", `{"bk_tenant_id":"system","table_ids":["x.y"]}`, true, nil, 200},
		{"write only", `{"bk_tenant_id":"system","table_ids":["x.y"],"is_publish":false}`, false, nil, 200},
		{"refresh failed", `{"bk_tenant_id":"system","table_ids":["x.y"]}`, true, errors.New("storage unavailable"), 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			patch := gomonkey.ApplyMethodFunc(&service.SpacePusher{}, "PushTableIdDetail", func(tenantID string, tableIDs []string, publish bool) error {
				require.Equal(t, "system", tenantID)
				require.Equal(t, []string{"x.y"}, tableIDs)
				require.Equal(t, tc.publish, publish)
				called = true
				return tc.err
			})
			defer patch.Reset()
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("POST", "/bmw/metadata/routes/result-table/refresh", strings.NewReader(tc.body)))
			require.Equal(t, tc.status, w.Code)
			require.True(t, called)
		})
	}
}

func TestRefreshSpaceReportsLookupAndRefreshErrors(t *testing.T) {
	router := gin.New()
	registerRouteRefresh(router.Group(RouterPrefix))
	for _, tc := range []struct {
		err    error
		status int
	}{{metadataTask.ErrSpaceNotFound, 404}, {errors.New("preload failed"), 500}} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			patch := gomonkey.ApplyFuncReturn(metadataTask.RefreshSpaceRoute, tc.err)
			defer patch.Reset()
			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest("POST", "/bmw/metadata/routes/space/refresh", strings.NewReader(`{"bk_tenant_id":"system","space_type":"bkcc","space_id":"2"}`)))
			require.Equal(t, tc.status, w.Code)
		})
	}
}
