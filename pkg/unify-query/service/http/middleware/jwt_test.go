// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package middleware

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/bkapi"
)

func handler(c *gin.Context) {
	var (
		err       error
		bkAppCode string
	)

	bkapi.GetBkAPI().GetCode()

	if code, ok := c.Get("bk_app_code"); !ok {
		err = fmt.Errorf("bk_app_code is empty")
	} else {
		if bkAppCode, ok = code.(string); !ok {
			err = fmt.Errorf("bk_app_code is not string %v", code)
		}
	}

	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, bkAppCode)
	return
}

func TestJwtAuthMiddleware(t *testing.T) {
	r := gin.Default()
	r.Use(JwtAuthMiddleware())

	url := "/protected"

	r.GET(url, handler)

	// 解析 jwt
	s := `eyJ0eXAiOiJKV1QiLCJraWQiOiJiay11bmlmeS1xdWVyeSIsImFsZyI6IlJTNTEyIiwiaXNzIjoiQVBJR1ciLCJpYXQiOjE3MjczNTYxMTB9.eyJhcHAiOnsidmFsaWRfZXJyb3JfbWVzc2FnZSI6IiIsInZlcnNpb24iOjEsInZlcmlmaWVkIjp0cnVlLCJhcHBfY29kZSI6ImJrX2xvZ19zZWFyY2gifSwiaXNzIjoiQVBJR1ciLCJleHAiOjE3MjczNTc2MTAsInVzZXIiOnsidmFsaWRfZXJyb3JfbWVzc2FnZSI6IiIsInVzZXJuYW1lIjoic2hhbWNsZXJlbiIsInZlcmlmaWVkIjp0cnVlLCJ2ZXJzaW9uIjoxfSwibmJmIjoxNzI3MzU1ODEwfQ.QCuBUmrLM-bqBrEMd8PsEGnKCQjGLL2wy2z4s6wSWv1XFmYjtFw2d_X5BV_xmYXYQP2DFpXTkff124CqFFJNbmkuiGOWbie68tyJdVpqoo3ej1fscWJ__dZ5lOl2W_0lHrKZLi8vqKKujtIPEkfqAwdl7t4-8SQsw6_gQ2cBSXT8jP6ewBWE3aNN_JsgIkLn0OqV4shy34PVmshIS6mUE19y_d4VlSOqOwXD0Hjg-hqGH2JJRjooTdkJd3NUf3ZfRyeliSf4HgTawxNuXRU2XTr_73kKqAGBOiNzvzSwdTYC61bitHX4ZVST1CzOA8QnUpfyTX_nI7SH8RX30WCNUw`
	reqWithToken, _ := http.NewRequest(http.MethodGet, url, nil)
	reqWithToken.Header.Set(JwtHeaderKey, s)

	wWithToken := httptest.NewRecorder()
	r.ServeHTTP(wWithToken, reqWithToken)
	assert.Equal(t, http.StatusOK, wWithToken.Code)

	bkAppCode, _ := io.ReadAll(wWithToken.Body)
	assert.Equal(t, "bkmonitorv3", string(bkAppCode))

	// 测试不传 jwtHeader 的请求，直接返回正常
	w := httptest.NewRecorder()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
