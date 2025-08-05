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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

func TestQueryRawWithScroll_ESFlow(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	testTableId := "result_table.es"
	testDataLabel := "es"

	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err, "Failed to get space router")
	err = router.Add(ctx, ir.ResultTableDetailKey, testTableId, &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     testTableId,
		DB:          "es_index",
		StorageType: consul.ElasticsearchStorageType,
		DataLabel:   "es",
	})
	assert.NoError(t, err)

	resultTableList := ir.ResultTableList{testTableId}
	err = router.Add(ctx, ir.DataLabelToResultTableKey, "es", &resultTableList)
	assert.NoError(t, err)

	err = router.Add(ctx, ir.DataLabelToResultTableKey, testTableId, &resultTableList)
	assert.NoError(t, err)

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

	err = redis.SetInstance(ctx, "test-scroll", options)
	require.NoError(t, err, "Failed to set unify-query redis instance")

	initEsMockData := map[string]any{
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":0,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_0","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"es_test1"}}]}}`,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":1,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_1","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"es_test2"}}]}}`,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":2,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_2","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"es_test3"}}]}}`,
	}

	secondRoundEsMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_0"}`: `{"_scroll_id":"scroll_id_0_next","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"4","_source":{"dtEventTimeStamp":"1723594004000","data":"es_test4"}}]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_1"}`: `{"_scroll_id":"scroll_id_1_next","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"5","_source":{"dtEventTimeStamp":"1723594005000","data":"es_test5"}}]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_2"}`: `{"_scroll_id":"scroll_id_2_next","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"6","_source":{"dtEventTimeStamp":"1723594006000","data":"es_test6"}}]}}`,
	}

	thirdRoundEsMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_0_next"}`: `{"_scroll_id":"","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_1_next"}`: `{"_scroll_id":"scroll_id_1_final","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"7","_source":{"dtEventTimeStamp":"1723594007000","data":"es_test7"}}]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_2_next"}`: `{"_scroll_id":"scroll_id_2_final","hits":{"total":{"value":1,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"8","_source":{"dtEventTimeStamp":"1723594008000","data":"es_test8"}}]}}`,
	}

	allDoneMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_1_final"}`: `{"_scroll_id":"","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
		`{"scroll":"9m","scroll_id":"scroll_id_2_final"}`: `{"_scroll_id":"","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
	}

	start := "1723594000"
	end := "1723595000"
	type testCase struct {
		queryTs  *structured.QueryTs
		expected []expectResult
	}

	tCase := testCase{
		queryTs: &structured.QueryTs{
			SpaceUid: spaceUid,
			QueryList: []*structured.Query{
				{
					TableID: structured.TableID(testDataLabel),
				},
			},
			Scroll:   "9m",
			Timezone: "Asia/Shanghai",
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
				mockData: secondRoundEsMockData,
			},
			{
				desc:     "Third scroll request - slice 0 ends, others continue",
				total:    2,
				done:     false,
				hasData:  true,
				mockData: thirdRoundEsMockData,
			},
			{
				desc:     "Fourth scroll request - should be done",
				total:    0,
				done:     true,
				hasData:  false,
				mockData: allDoneMockData,
			},
		},
	}
	user := &metadata.User{
		Key:       "username:test_scroll_user",
		SpaceUID:  spaceUid,
		SkipSpace: "true",
	}

	sessionKeySuffix, err := generateScrollKey(user.Name, *tCase.queryTs)
	require.NoError(t, err, "Failed to generate scroll key")

	for i, c := range tCase.expected {
		t.Logf("Running step %d: %s", i+1, c.desc)

		testCtx := metadata.InitHashID(context.Background())
		metadata.SetUser(testCtx, user)

		mock.Es.Set(c.mockData)

		queryTsBytes, err := json.Marshal(tCase.queryTs)
		require.NoError(t, err, "Failed to marshal queryTs")

		var queryTsCopy structured.QueryTs
		err = json.Unmarshal(queryTsBytes, &queryTsCopy)
		require.NoError(t, err, "Failed to unmarshal queryTs")

		total, list, _, done, err := queryRawWithScroll(testCtx, &queryTsCopy, sessionKeySuffix, 3)
		hasData := len(list) > 0
		assert.NoError(t, err, "QueryRawWithScroll should not return error for step %d", i+1)
		assert.Equal(t, c.total, total, "Total should match expected value for step %d", i+1)
		assert.Equal(t, c.done, done, "Done should match expected value for step %d", i+1)
		assert.Equal(t, c.hasData, hasData, "HasData should match expected value for step %d", i+1)

		if c.hasData {
			assert.Greater(t, len(list), 0, "Should have data when hasData is true for step %d", i+1)
		} else {
			assert.Equal(t, 0, len(list), "Should have no data when hasData is false for step %d", i+1)
		}
	}
}

func TestQueryRawWithScroll_DorisFlow(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	testTableId := "result_table.doris"

	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err, "Failed to get space router")

	err = router.Add(ctx, ir.ResultTableDetailKey, testTableId, &ir.ResultTableDetail{
		StorageId:   4,
		TableId:     testTableId,
		DB:          "doris_db",
		StorageType: consul.BkSqlStorageType,
		DataLabel:   "doris_test",
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

	err = redis.SetInstance(ctx, "test", options)
	require.NoError(t, err, "Failed to set unify-query redis instance")

	initDorisMockData := map[string]any{
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10`:           `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594001000","data":"doris_test1"},{"dtEventTimeStamp":"1723594002000","data":"doris_test2"}]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 10`: `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594003000","data":"doris_test3"},{"dtEventTimeStamp":"1723594004000","data":"doris_test4"}]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 20`: `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594005000","data":"doris_test5"},{"dtEventTimeStamp":"1723594006000","data":"doris_test6"}]}}`,
	}

	inProgressDorisMockData := map[string]any{
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 30`: `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594007000","data":"doris_test7"},{"dtEventTimeStamp":"1723594008000","data":"doris_test8"}]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 40`: `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594009000","data":"doris_test9"},{"dtEventTimeStamp":"1723594010000","data":"doris_test10"}]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 50`: `{"result":true,"code":"00","message":"","data":{"totalRecords":2,"total_record_size":2,"list":[{"dtEventTimeStamp":"1723594011000","data":"doris_test11"},{"dtEventTimeStamp":"1723594012000","data":"doris_test12"}]}}`,
	}

	thirdRoundDorisMockData := map[string]any{
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 60`: `{"result":true,"code":"00","message":"","data":{"totalRecords":0,"total_record_size":0,"list":[]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 70`: `{"result":true,"code":"00","message":"","data":{"totalRecords":0,"total_record_size":0,"list":[]}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`doris_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 80`: `{"result":true,"code":"00","message":"","data":{"totalRecords":0,"total_record_size":0,"list":[]}}`,
	}

	start := "1723594000"
	end := "1723595000"
	type testCase struct {
		queryTs  *structured.QueryTs
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
				desc:     "First scroll request - slice 0,1,2 with OFFSET 0,10,20",
				total:    6,
				done:     false,
				hasData:  true,
				mockData: initDorisMockData,
			},
			{
				desc:     "Second scroll request - slice 0,1,2 with OFFSET 30,40,50",
				total:    6,
				done:     false,
				hasData:  true,
				mockData: inProgressDorisMockData,
			},
			{
				desc:     "Third scroll request - slice 0,1,2 with OFFSET 60,70,80 - should be done",
				total:    0,
				done:     true,
				hasData:  false,
				mockData: thirdRoundDorisMockData,
			},
			{
				desc:     "Fourth scroll request - should still be done",
				total:    0,
				done:     true,
				hasData:  false,
				mockData: thirdRoundDorisMockData,
			},
		},
	}
	user := &metadata.User{
		Key:       "username:test_doris_scroll_user",
		SpaceUID:  spaceUid,
		SkipSpace: "true",
	}
	testCtx := metadata.InitHashID(context.Background())
	metadata.SetUser(testCtx, user)

	for i, c := range tCase.expected {
		t.Logf("Running step %d: %s", i+1, c.desc)

		mock.BkSQL.Set(c.mockData)

		queryTsBytes, _ := json.Marshal(tCase.queryTs)
		var queryTsCopy structured.QueryTs
		json.Unmarshal(queryTsBytes, &queryTsCopy)

		sessionKeySuffix, _ := generateScrollKey(user.Name, *tCase.queryTs)
		total, list, _, done, err := queryRawWithScroll(testCtx, &queryTsCopy, sessionKeySuffix, 3)
		hasData := len(list) > 0

		assert.NoError(t, err, "QueryRawWithScroll should not return error for step %d", i+1)
		assert.Equal(t, c.total, total, "Total should match expected value for step %d", i+1)
		assert.Equal(t, c.done, done, "Done should match expected value for step %d", i+1)
		assert.Equal(t, c.hasData, hasData, "HasData should match expected value for step %d", i+1)
	}
}
