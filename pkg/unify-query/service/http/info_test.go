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
	"testing"

	"github.com/gin-gonic/gin"

	featureFlagService "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/featureFlag"
)

func TestNormalizeFeatureFlagSource(t *testing.T) {
	if got := normalizeFeatureFlagSource("redis"); got != "redis" {
		t.Fatalf("expected redis source, got %q", got)
	}
	if got := normalizeFeatureFlagSource("invalid"); got != "consul" {
		t.Fatalf("expected invalid source to fall back to consul, got %q", got)
	}
}

func TestHandleFeatureFlagRejectsCrossSourceRefresh(t *testing.T) {
	originalSource := featureFlagService.DataSource
	featureFlagService.DataSource = "redis"
	defer func() { featureFlagService.DataSource = originalSource }()

	gin.SetMode(gin.TestMode)
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/ff?r=1&source=consul", nil)

	HandleFeatureFlag(ctx)

	if writer.Code != http.StatusBadRequest {
		t.Fatalf("expected cross-source refresh to be rejected, got status %d", writer.Code)
	}
}
