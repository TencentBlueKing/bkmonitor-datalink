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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
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

	// 模拟一个时间和请求，制造假的alias信息
	start := time.Now().Add(-24 * time.Hour)
	now := time.Now()
	index := "testbb_ttt"
	body := "{\"size\":5}"
	startTimeFormat := start.Format("20060102")
	nowTimeFormat := now.Format("20060102")
	startTime := fmt.Sprintf("%s_%s_read", index, startTimeFormat)
	nowTime := fmt.Sprintf("%s_%s_read", index, nowTimeFormat)
	aliases := []string{startTime, nowTime}
	aliasInfo := map[string]*es.AliasInfo{
		"testbb_ttt_20210407_01": {
			Aliases: map[string]any{
				startTime: map[string]any{},
				nowTime:   map[string]any{},
			},
		},
	}
	aliasInfoStr, _ := json.Marshal(aliasInfo)
	client.EXPECT().AliasWithIndex("*"+index+"*").Return(string(aliasInfoStr), nil)
	client.EXPECT().Search(body, aliases).Return(`any result`, nil)
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
	es.RefreshAllAlias()
	g := gin.Default()
	g.POST("/es_query", servicehttp.HandleESQueryRequest)

	testCases := []struct {
		data   string
		result string
	}{
		{
			data:   `{"table_id":"testbb.ttt","time":{"start":` + strconv.FormatInt(start.Unix(), 10) + `,"end":` + strconv.FormatInt(now.Unix(), 10) + `},"query":{"body":"{\"size\":5}"}}`,
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
