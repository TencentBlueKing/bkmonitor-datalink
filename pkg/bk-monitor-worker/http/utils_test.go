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
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/jsonx"
)

func TestMerge(t *testing.T) {
	tests := []struct {
		name          string
		param         *gin.H
		mergedParam   *gin.H
		expectedParam gin.H
	}{
		{"merge the params without merged param", &gin.H{"result": false}, nil, gin.H{"result": false}},
		{"merge the params by overwrite", &gin.H{"result": false}, &gin.H{"result": true}, gin.H{"result": true}},
		{"merge the params", &gin.H{"result": false, "test1": "a"}, &gin.H{"result": true, "test2": 2}, gin.H{"result": true, "test1": "a", "test2": 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			real := MergeGinH(test.param, test.mergedParam)
			if !reflect.DeepEqual(*real, test.expectedParam) {
				t.Errorf("MergeGinH error, name: %s, param: %v, mergedParam: %v, expectedParam: %v, real: %v", test.name, test.param, test.mergedParam, test.expectedParam, real)
			}
		})
	}
}

func TestGetMessage(t *testing.T) {
	tests := []struct {
		name        string
		candidate   string
		format      string
		v           []any
		expectedStr string
	}{
		{"candidate without format and args", "only candidate", "", nil, "only candidate"},
		{"candidate with format and args", "candidate", "message", nil, "message"},
		{"candidate with format and args gt 0", "candidate", "message: %s %s", []any{"this is", "a test"}, "message: this is a test"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			real := GetMessage(test.candidate, test.format, test.v)
			if real != test.expectedStr {
				t.Errorf("GetMessage error, name: %s, candidate: %s, format: %s, v: %v, expectedStr: %s, real: %s", test.name, test.candidate, test.format, test.v, test.v, real)
			}
		})
	}
}

func response(c *gin.Context) {
	Response(c, nil)
}

func TestResponse(t *testing.T) {
	router := gin.Default()
	router.GET("/test", response)

	req, _ := http.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

const defaultTestMessage = "this is a test"

func responseWithMessage(c *gin.Context) {
	ResponseWithMessage(c, nil, defaultTestMessage)
}

func TestResponseWithMessage(t *testing.T) {
	router := gin.Default()
	router.GET("/test", responseWithMessage)

	req, _ := http.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	type tt struct {
		Message string `json:"message"`
	}
	var ttt tt
	err := jsonx.Unmarshal(rec.Body.Bytes(), &ttt)
	assert.NoError(t, err)
	assert.Equal(t, ttt.Message, defaultTestMessage)
}

func badResponse(c *gin.Context) {
	BadReqResponse(c, defaultTestMessage)
}

func TestBadReqResponse(t *testing.T) {
	router := gin.Default()
	router.GET("/test", badResponse)

	req, _ := http.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func serverErrResponse(c *gin.Context) {
	ServerErrResponse(c, defaultTestMessage)
}

func TestServerErrResponse(t *testing.T) {
	router := gin.Default()
	router.GET("/test", serverErrResponse)

	req, _ := http.NewRequest("GET", "/test", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
