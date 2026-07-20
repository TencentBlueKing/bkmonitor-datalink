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
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/likexian/gokit/assert"
	"github.com/prashantv/gostub"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/es/mocktest"
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
	t.Setenv(inner.MaxQueryTimeRangeEnv, "168h")

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	// mock掉client，模拟真实查询es数据动作
	client := mocktest.NewMockClient(ctrl)

	// 模拟一个时间和请求，确认查询直接使用按日期生成的 alias。
	start := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	tableID := "testbb.ttt"
	index := "testbb_ttt"
	body := "{\"size\":5}"
	startTimeFormat := start.Format("20060102")
	endTimeFormat := end.Format("20060102")
	startTime := fmt.Sprintf("%s_%s*_read", index, startTimeFormat)
	endTime := fmt.Sprintf("%s_%s*_read", index, endTimeFormat)
	client.EXPECT().Search(gomock.Any(), body, startTime, endTime).Return(`any result`, nil)
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
	q := &inner.Params{
		TableID: tableID,
		Body:    body,
		Start:   start.Unix(),
		End:     end.Unix(),
	}

	// 测试基于假的alias,storage,table信息能否正常返回查询结果
	result, err := inner.Query(context.Background(), q)
	assert.Nil(t, err)
	assert.Equal(t, "any result", result)

	// fuzzy 模式也必须先通过跨度校验，超限请求不能触发 ES 查询。
	q.FuzzyMatching = true
	q.End = start.Add(7*24*time.Hour + time.Second).Unix()
	_, err = inner.Query(context.Background(), q)
	assert.True(t, errors.Is(err, inner.ErrTimeRangeTooLarge))
}
