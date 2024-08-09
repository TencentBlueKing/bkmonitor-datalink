// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package log_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/apis/log"
	httpSvr "github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func TestLogLevelParamsError(t *testing.T) {
	r := gin.Default()

	r.Use(httpSvr.ResponseMiddleware())
	r.PUT("/hello", log.SetLogLevel)

	req, err := http.NewRequest("PUT", "/hello", nil)
	if err != nil {
		t.Fatalf("Failed to create hello request: %v", err)
	}
	// 创建一个记录器
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestLogLevel(t *testing.T) {
	r := gin.Default()

	r.Use(httpSvr.ResponseMiddleware())
	r.PUT("/hello", log.SetLogLevel)

	req, err := http.NewRequest("PUT", "/hello?level=error", nil)
	if err != nil {
		t.Fatalf("Failed to create hello request: %v", err)
	}
	// 创建一个记录器
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	assert.Equal(t, "error", logger.LoggerLevel())
}
