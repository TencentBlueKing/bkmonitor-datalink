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
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/likexian/gokit/assert"
	"github.com/prashantv/gostub"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es/mocktest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func TestRefreshAlias(t *testing.T) {
	log.InitTestLogger()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	// mock掉client，模拟真实查询es数据动作
	client := mocktest.NewMockClient(ctrl)
	client.EXPECT().AliasWithIndex("*testbb_ttt*").Return(`{"testbb_ttt_20210407_01":{"aliases":{"testbb_ttt_20210407_read":{}}}}`, nil)
	stubs := gostub.StubFunc(&es.NewClient, client, nil)
	defer stubs.Reset()

	// 手动配置table和storage
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

	// 测试别名是否能够正确读取
	es.RefreshAllAlias()
	assert.Nil(t, err)
	assert.True(t, es.AliasExist("testbb.ttt", "testbb_ttt_20210407_read"))
}
