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
	"context"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

func TestQueryRawWithScroll(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	testTableId := "result_table.es"

	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err, "Failed to get space router")
	route01 := "route1_table"
	err = router.Add(ctx, ir.ResultTableDetailKey, route01, &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     route01,
		DB:          route01,
		StorageType: consul.ElasticsearchStorageType,
		DataLabel:   route01,
	})
	assert.NoError(t, err)

	err = router.Add(ctx, ir.ResultTableDetailKey, "route2_table", &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     "route2_table",
		DB:          "route2",
		StorageType: consul.ElasticsearchStorageType,
		DataLabel:   "route2",
	})
	assert.NoError(t, err)

	type expectResult struct {
		desc     string
		total    int64
		done     bool
		hasData  bool
		mockData map[string]any
	}

	require.NoError(t, err, "Failed to add space mapping")

	s, err := miniredis.Run()
	require.NoError(t, err, "Failed to start miniredis")
	defer s.Close()

	options := &goRedis.UniversalOptions{
		Addrs: []string{s.Addr()},
		DB:    0,
	}

	err = redisUtil.SetInstance(ctx, "test", options)
	require.NoError(t, err, "Failed to set redis instance")

	initEsMockData := map[string]any{
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":0,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_0","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"es_test1"}}]}}`,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":1,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_1","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"es_test2"}}]}}`,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":2,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_2","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"es_test3"}}]}}`,
	}

	inProgressEsMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_0"}`: `{"_scroll_id":"scroll_id_0","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"4","_source":{"dtEventTimeStamp":"1723594004000","data":"es_test4"}}]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_1"}`: `{"_scroll_id":"scroll_id_1","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"5","_source":{"dtEventTimeStamp":"1723594005000","data":"es_test5"}}]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_2"}`: `{"_scroll_id":"scroll_id_2","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"6","_source":{"dtEventTimeStamp":"1723594006000","data":"es_test6"}}]}}`,
	}

	start := "1723594000"
	end := "1723595000"
	type testCase struct {
		queryTs  *structured.QueryTs
		mockDat  map[string]any
		expected []expectResult
	}

	tCase := testCase{
		queryTs: &structured.QueryTs{
			SpaceUid: spaceUid,
			QueryList: []*structured.Query{
				{
					TableID: structured.TableID(testTableId),
				},
			},
			Timezone: "Asia/Shanghai",
			Scroll:   "9m",
			Limit:    10,
			Start:    start,
			End:      end,
		},
		expected: []expectResult{
			{
				desc:     "First scroll request",
				total:    3,
				done:     false,
				hasData:  true,
				mockData: initEsMockData,
			},
			{
				desc:     "Second scroll request",
				total:    3,
				done:     false,
				hasData:  true,
				mockData: inProgressEsMockData,
			},
			{
				desc:     "Third scroll request",
				total:    3,
				done:     false,
				hasData:  true,
				mockData: inProgressEsMockData,
			},
		},
	}
	user := &metadata.User{
		Key:       "username:test_scroll_user",
		SpaceUID:  spaceUid,
		SkipSpace: "true",
	}
	testCtx := metadata.InitHashID(context.Background())
	metadata.SetUser(testCtx, user)

	for _, c := range tCase.expected {
		t.Run(c.desc, func(t *testing.T) {
			queryTsBytes, _ := json.Marshal(tCase.queryTs)
			var queryTsCopy structured.QueryTs
			json.Unmarshal(queryTsBytes, &queryTsCopy)
			mock.Es.Set(c.mockData)
			total, list, _, done, err := queryRawWithScroll(testCtx, &queryTsCopy)
			hasData := len(list) > 0
			assert.NoError(t, err, "QueryRawWithScroll should not return error")
			assert.Equal(t, c.total, total, "Total should match expected value")
			assert.Equal(t, c.done, done, "Done should match expected value")
			assert.Equal(t, c.hasData, hasData, "HasData should match expected value")
		})
	}
}
