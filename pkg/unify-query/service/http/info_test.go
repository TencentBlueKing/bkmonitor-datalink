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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

func TestHandleFeatureFlagDoesNotRejectSourceParameter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/ff?r=1&source=consul", nil)

	HandleFeatureFlag(ctx)

	if writer.Code != http.StatusOK {
		t.Fatalf("expected source parameter not to reject fallback refresh, got status %d", writer.Code)
	}
}

func TestHandlePrintUsesCurrentDiagnosticPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)
	paths := map[string]string{
		SpacePrintHandlePathConfigPath:    "/current-spaces",
		TsDBPrintHandlePathConfigPath:     "/current-storage",
		InfluxDBPrintHandlePathConfigPath: "/current-influxdb",
	}
	for key, path := range paths {
		previous := viper.Get(key)
		viper.Set(key, path)
		t.Cleanup(func() { viper.Set(key, previous) })
	}
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/print", nil)

	HandlePrint(ctx)

	if writer.Code != http.StatusOK {
		t.Fatalf("expected compatible diagnostic entry, got status %d", writer.Code)
	}
	body := writer.Body.String()
	if !strings.Contains(body, "retired") {
		t.Fatalf("expected retired diagnostic notice, got %q", body)
	}
	for _, path := range paths {
		if !strings.Contains(body, path) {
			t.Errorf("expected current diagnostic path %q in %q", path, body)
		}
	}
}
