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
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/apis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/utils/jsonx"
)

func TestResponseMiddleware(t *testing.T) {
	r := gin.Default()

	r.Use(ResponseMiddleware())
	message := "Hello, World!"
	r.GET("/hello", func(c *gin.Context) {
		apis.NewResponse(c, 200, true, 0, message, nil)
	})

	req, err := http.NewRequest("GET", "/hello", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	// 创建一个记录器
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp apis.RespFields
	jsonx.Unmarshal(w.Body.Bytes(), &resp)

	assert.Equal(t, message, resp.Message)
}

func TestMetricMiddleware(t *testing.T) {
	r := gin.Default()

	r.Use(MetricMiddleware())
	r.GET("/metrics", prometheusHandler())
	r.GET("/hello", func(c *gin.Context) {
		c.String(200, "Hello, World!")
	})

	req, err := http.NewRequest("GET", "/hello", nil)
	if err != nil {
		t.Fatalf("Failed to create hello request: %v", err)
	}
	// 创建一个记录器
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	req, err = http.NewRequest("GET", "/metrics", nil)
	if err != nil {
		t.Fatalf("Failed to create metrics request: %v", err)
	}

	// 创建一个记录器
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// 测试包含指定的指标
	assert.Contains(t, w.Body.String(), "bkmonitor_api_server_api_request_duration_seconds_count")
	assert.Contains(t, w.Body.String(), "bkmonitor_api_server_api_request_duration_seconds_bucket")
	assert.Contains(t, w.Body.String(), "bkmonitor_api_server_api_request_duration_seconds_sum")
}
