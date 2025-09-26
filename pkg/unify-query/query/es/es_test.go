// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/likexian/gokit/assert"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	inner "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/es"
)

// TestSuite
type TestSuite struct {
	suite.Suite
}

// TestSearch
func TestSearch(t *testing.T) {
	log.InitTestLogger()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	// mock掉client，模拟真实查询es数据动作
	client := mocktest.NewMockClient(ctrl)

	// 模拟一个时间和请求，制造假的alias信息
	start := time.Now().Add(-24 * time.Hour)
	now := time.Now()
	tableID := "testbb.ttt"
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

	// 制造假的storage和table信息
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

	err := es.ReloadTableInfo(infos)
	assert.Nil(t, err)
	err = es.ReloadStorage(storages)
	assert.Nil(t, err)
	es.RefreshAllAlias()
	assert.Nil(t, err)
	q := &inner.Params{
		TableID: tableID,
		Body:    body,
		Start:   start.Unix(),
		End:     now.Unix(),
	}

	// 测试基于假的alias,storage,table信息能否正常返回查询结果
	result, err := inner.Query(q)
	assert.Nil(t, err)
	assert.Equal(t, "any result", result)
}
