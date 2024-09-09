// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package response_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/apis/response"
	httpSvr "github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/http"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/utils/jsonx"
)

func TestResponse(t *testing.T) {
	r := gin.Default()

	expectMsg := "this is a test"
	r.Use(httpSvr.ResponseMiddleware())
	r.GET("/hello", func(c *gin.Context) {
		response.NewResponse(c, http.StatusOK, true, 0, expectMsg, nil)
	})

	req, err := http.NewRequest("GET", "/hello", nil)
	if err != nil {
		t.Fatalf("Failed to create hello request: %v", err)
	}
	// 创建一个记录器
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	type responseBody struct {
		Code    int         `json:"code"`
		Message string      `json:"message"`
		Result  bool        `json:"result"`
		Data    interface{} `json:"data"`
	}
	var res responseBody

	err = jsonx.Unmarshal(w.Body.Bytes(), &res)
	assert.Nil(t, err)
	assert.Equal(t, expectMsg, res.Message)
}

func TestNewSuccessResponse(t *testing.T) {
	r := gin.Default()

	expectOk := "ok"
	r.Use(httpSvr.ResponseMiddleware())
	r.GET("/hello", func(c *gin.Context) {
		response.NewSuccessResponse(c, nil)
	})

	req, err := http.NewRequest("GET", "/hello", nil)
	if err != nil {
		t.Fatalf("Failed to create hello request: %v", err)
	}
	// 创建一个记录器
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	type responseBody struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  bool   `json:"result"`
	}
	var res responseBody

	err = jsonx.Unmarshal(w.Body.Bytes(), &res)
	assert.Nil(t, err)
	assert.Equal(t, expectOk, res.Message)
	assert.True(t, res.Result)
	assert.Equal(t, 0, res.Code)
}

func TestNewParamsErrorResponse(t *testing.T) {
	r := gin.Default()

	expectParamsErr := "params error"
	r.Use(httpSvr.ResponseMiddleware())
	r.GET("/hello", func(c *gin.Context) {
		response.NewParamsErrorResponse(c, expectParamsErr)
	})

	req, err := http.NewRequest("GET", "/hello", nil)
	if err != nil {
		t.Fatalf("Failed to create hello request: %v", err)
	}
	// 创建一个记录器
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	type responseBody struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  bool   `json:"result"`
	}
	var res responseBody

	err = jsonx.Unmarshal(w.Body.Bytes(), &res)
	assert.Nil(t, err)
	assert.Equal(t, expectParamsErr, res.Message)
	assert.False(t, res.Result)
	assert.NotEqual(t, 0, res.Code)
}

func TestNewServerErrorResponse(t *testing.T) {
	r := gin.Default()

	expectServerErr := "internal server error"
	r.Use(httpSvr.ResponseMiddleware())
	r.GET("/hello", func(c *gin.Context) {
		response.NewServerErrorResponse(c, expectServerErr)
	})

	req, err := http.NewRequest("GET", "/hello", nil)
	if err != nil {
		t.Fatalf("Failed to create hello request: %v", err)
	}
	// 创建一个记录器
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	type responseBody struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Result  bool   `json:"result"`
	}
	var res responseBody

	err = jsonx.Unmarshal(w.Body.Bytes(), &res)
	assert.Nil(t, err)
	assert.Equal(t, expectServerErr, res.Message)
	assert.False(t, res.Result)
	assert.NotEqual(t, 0, res.Code)
}
