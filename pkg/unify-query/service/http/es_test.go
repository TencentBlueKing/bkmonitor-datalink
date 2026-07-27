// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http_test

import (
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang/mock/gomock"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	servicehttp "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http"
)

// TestHandleESRequest
func TestHandleESRequest(t *testing.T) {
	log.InitTestLogger()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	// mock掉client，模拟真实查询es数据动作
	client := mocktest.NewMockClient(ctrl)

	// 模拟一个时间和请求，确认 HTTP 请求直接使用按日期生成的 alias。
	start := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	index := "testbb_ttt"
	body := "{\"size\":5}"
	startTimeFormat := start.Format("20060102")
	endTimeFormat := end.Format("20060102")
	startTime := fmt.Sprintf("%s_%s*_read", index, startTimeFormat)
	endTime := fmt.Sprintf("%s_%s*_read", index, endTimeFormat)
	client.EXPECT().Search(gomock.Any(), body, startTime, endTime).Return(`any result`, nil)
	stubs := gostub.StubFunc(&es.NewClient, client, nil)
	defer stubs.Reset()

	infos := map[string]*es.TableInfo{
		"testbb.ttt": {
			StorageID:   2,
			AliasFormat: "{index}_{time}_read",
			DateFormat:  "20060102",
			DateStep:    2,
		},
	}
	storages := map[string]*es.ESInfo{
		"2": {
			Host:           "http://127.0.0.1:9200",
			MaxConcurrency: 20,
		},
	}

	es.ReloadStorage(storages)
	es.ReloadTableInfo(infos)
	g := gin.Default()
	g.POST("/es_query", servicehttp.HandleESQueryRequest)

	testCases := []struct {
		data   string
		result string
	}{
		{
			data:   `{"table_id":"testbb.ttt","time":{"start":` + strconv.FormatInt(start.Unix(), 10) + `,"end":` + strconv.FormatInt(end.Unix(), 10) + `},"query":{"body":"{\"size\":5}"}}`,
			result: `any result`,
		},
	}
	for _, testCase := range testCases {
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/es_query", strings.NewReader(testCase.data))
		g.ServeHTTP(w, req)
		assert.Equal(t, 200, w.Code)
		assert.Equal(t, testCase.result, w.Body.String())
	}
}

func TestHandleESRequestRejectsInvalidTimeRange(t *testing.T) {
	log.InitTestLogger()
	t.Setenv("UNIFY_QUERY_ES_MAX_QUERY_TIME_RANGE", "168h")
	g := gin.Default()
	g.POST("/es_query", servicehttp.HandleESQueryRequest)

	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.Local)
	end := start.Add(7*24*time.Hour + time.Second)
	data := `{"table_id":"testbb.ttt","time":{"start":` + strconv.FormatInt(start.Unix(), 10) +
		`,"end":` + strconv.FormatInt(end.Unix(), 10) + `},"query":{"body":"{}"}}`
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/es_query", strings.NewReader(data))
	g.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "query time range is too large")
}

func TestHandleESRequestRejectsMissingFields(t *testing.T) {
	log.InitTestLogger()
	g := gin.Default()
	g.POST("/es_query", servicehttp.HandleESQueryRequest)

	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/es_query", strings.NewReader(`{"table_id":"testbb.ttt"}`))
	g.ServeHTTP(w, req)

	assert.Equal(t, 400, w.Code)
	assert.Contains(t, w.Body.String(), "invalid es query request")
}
