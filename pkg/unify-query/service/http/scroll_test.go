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
	"github.com/spf13/viper"
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

func TestQueryRawWithScrollOnly(t *testing.T) {
	viper.Set(ScrollMaxSliceConfigPath, 1)
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
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":0,"max":1},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_0","hits":{"total":{"value":3,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"es_test1"}},{"_index":"result_table.es","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"es_test2"}},{"_index":"result_table.es","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"es_test3"}}]}}`,
	}

	inProgressEsMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_0"}`: `{"_scroll_id":"scroll_id_0","hits":{"total":{"value":3,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"4","_source":{"dtEventTimeStamp":"1723594004000","data":"es_test4"}},{"_index":"result_table.es","_id":"5","_source":{"dtEventTimeStamp":"1723594005000","data":"es_test5"}},{"_index":"result_table.es","_id":"6","_source":{"dtEventTimeStamp":"1723594006000","data":"es_test6"}}]}}`,
	}

	allDOneEsMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_0"}`: `{"_scroll_id":"scroll_id_0","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
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
				total:    0,
				done:     true,
				hasData:  false,
				mockData: allDOneEsMockData,
			},
			{
				desc:     "Final scroll",
				total:    0,
				done:     true,
				hasData:  false,
				mockData: allDOneEsMockData,
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
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":0,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_0","hits":{"total":{"value":3,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"es_test1"}},{"_index":"result_table.es","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"es_test2"}},{"_index":"result_table.es","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"es_test3"}}]}}`,
		//`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":1,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_1","hits":{"total":{"value":3,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"4","_source":{"dtEventTimeStamp":"1723594004000","data":"es_test4"}},{"_index":"result_table.es","_id":"5","_source":{"dtEventTimeStamp":"1723594005000","data":"es_test5"}},{"_index":"result_table.es","_id":"6","_source":{"dtEventTimeStamp":"1723594006000","data":"es_test6"}}]}}`,
		//`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":2,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_id_2","hits":{"total":{"value":3,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"4","_source":{"dtEventTimeStamp":"1723594004000","data":"es_test4"}},{"_index":"result_table.es","_id":"5","_source":{"dtEventTimeStamp":"1723594005000","data":"es_test5"}},{"_index":"result_table.es","_id":"6","_source":{"dtEventTimeStamp":"1723594006000","data":"es_test6"}}]}}`,
	}

	inProgressEsMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_0"}`: `{"_scroll_id":"scroll_id_0","hits":{"total":{"value":3,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"4","_source":{"dtEventTimeStamp":"1723594004000","data":"es_test4"}},{"_index":"result_table.es","_id":"5","_source":{"dtEventTimeStamp":"1723594005000","data":"es_test5"}},{"_index":"result_table.es","_id":"6","_source":{"dtEventTimeStamp":"1723594006000","data":"es_test6"}}]}}`,
		//`{"scroll":"9m","scroll_id":"scroll_id_1"}`: `{"_scroll_id":"scroll_id_1","hits":{"total":{"value":3,"relation":"eq"},"hits":[{"_index":"result_table.es","_id":"7","_source":{"dtEventTimeStamp":"1723594007000","data":"es_test7"}},{"_index":"result_table.es","_id":"8","_source":{"dtEventTimeStamp":"1723594008000","data":"es_test8"}},{"_index":"result_table.es","_id":"9","_source":{"dtEventTimeStamp":"1723594009000","data":"es_test9"}}]}}`,
		//`{"scroll":"9m","scroll_id":"scroll_id_2"}`: `{"_scroll_id":"scroll_id_2","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
	}

	allDOneEsMockData := map[string]any{
		`{"scroll":"9m","scroll_id":"scroll_id_0"}`: `{"_scroll_id":"scroll_id_0","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
		//`{"scroll":"9m","scroll_id":"scroll_id_1"}`: `{"_scroll_id":"scroll_id_1","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
		//`{"scroll":"9m","scroll_id":"scroll_id_2"}`: `{"_scroll_id":"scroll_id_2","hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`,
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
				total:    9,
				done:     false,
				hasData:  true,
				mockData: initEsMockData,
			},
			{
				desc:     "Second scroll request",
				total:    9,
				done:     false,
				hasData:  true,
				mockData: inProgressEsMockData,
			},
			{
				desc:     "Third scroll request",
				total:    0,
				done:     true,
				hasData:  false,
				mockData: allDOneEsMockData,
			},
			{
				desc:     "Final scroll",
				total:    0,
				done:     true,
				hasData:  false,
				mockData: allDOneEsMockData,
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

//
//func TestQueryRawWithScrollBkSql(t *testing.T) {
//	ctx := metadata.InitHashID(context.Background())
//	spaceUid := influxdb.SpaceUid
//
//	mock.Init()
//	influxdb.MockSpaceRouter(ctx)
//	promql.MockEngine()
//
//	testTableId := "result_table.bksql"
//
//	router, err := influxdb.GetSpaceTsDbRouter()
//	require.NoError(t, err, "Failed to get space router")
//
//	err = router.Add(ctx, ir.ResultTableDetailKey, testTableId, &ir.ResultTableDetail{
//		StorageId:   4,
//		TableId:     testTableId,
//		DB:          "bksql_db",
//		StorageType: consul.BkSqlStorageType,
//		DataLabel:   "bksql_label",
//	})
//	require.NoError(t, err, "Failed to add BkSql route")
//
//	s, err := miniredis.Run()
//	require.NoError(t, err, "Failed to start miniredis")
//	defer s.Close()
//
//	options := &goRedis.UniversalOptions{
//		Addrs: []string{s.Addr()},
//		DB:    0,
//	}
//
//	err = redisUtil.SetInstance(ctx, "test", options)
//	require.NoError(t, err, "Failed to set redis instance")
//
//	mock.BkSQL.Set(map[string]any{
//		// First scroll request - should return 1 record to match ES behavior
//		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`bksql_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10`: `{"result":true,"code":"00","message":"","data":{"list":[{"dtEventTimeStamp":"1723594001000","data":"bksql_test1"}],"total_records":1,"total_record_size":1}}`,
//
//		// Second scroll request - should return 1 record
//		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`bksql_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 10`: `{"result":true,"code":"00","message":"","data":{"list":[{"dtEventTimeStamp":"1723594002000","data":"bksql_test2"}],"total_records":1,"total_record_size":1}}`,
//
//		// Third scroll request - should return 1 record
//		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`bksql_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 20`: `{"result":true,"code":"00","message":"","data":{"list":[{"dtEventTimeStamp":"1723594003000","data":"bksql_test3"}],"total_records":1,"total_record_size":1}}`,
//
//		// Fourth scroll request - should return 0 records (done)
//		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`bksql_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 30`: `{"result":true,"code":"00","message":"","data":{"list":[],"total_records":0,"total_record_size":0}}`,
//	})
//
//	start := "1723594000"
//	end := "1723595000"
//
//	type testCase struct {
//		queryTs  *structured.QueryTs
//		expected []expectResult
//	}
//
//	tCase := testCase{
//		queryTs: &structured.QueryTs{
//			SpaceUid: spaceUid,
//			QueryList: []*structured.Query{
//				{
//					TableID: structured.TableID(testTableId),
//				},
//			},
//			Timezone: "Asia/Shanghai",
//			Scroll:   "9m",
//			Limit:    10,
//			Start:    start,
//			End:      end,
//		},
//		expected: []expectResult{
//			{
//				desc:    "First BkSql scroll request",
//				total:   1,
//				done:    false,
//				hasData: true,
//			},
//			{
//				desc:    "Second BkSql scroll request",
//				total:   1,
//				done:    false,
//				hasData: true,
//			},
//			{
//				desc:    "Third BkSql scroll request",
//				total:   0,
//				done:    true,
//				hasData: false,
//			},
//		},
//	}
//
//	user := &metadata.User{
//		Key:       "username:test_bksql_user",
//		SpaceUID:  spaceUid,
//		SkipSpace: "true",
//	}
//	testCtx := ctx
//	metadata.SetUser(testCtx, user)
//
//	for _, c := range tCase.expected {
//		t.Run(c.desc, func(t *testing.T) {
//			queryTsBytes, _ := json.Marshal(tCase.queryTs)
//			var queryTsCopy structured.QueryTs
//			json.Unmarshal(queryTsBytes, &queryTsCopy)
//
//			total, list, options, done, err := queryRawWithScroll(testCtx, &queryTsCopy)
//			hasData := len(list) > 0
//
//			t.Logf("Actual: total=%d, listLen=%d, done=%v, err=%v", total, len(list), done, err)
//			t.Logf("Expected: total=%d, done=%v, hasData=%v", c.total, c.done, c.hasData)
//
//			assert.NoError(t, err, "QueryRawWithScroll should not return error")
//			assert.Equal(t, c.total, total, "Total should match expected value")
//			assert.Equal(t, c.done, done, "Done should match expected value")
//			assert.Equal(t, c.hasData, hasData, "HasData should match expected value")
//
//			if c.desc == "First BkSql scroll request" {
//				assert.NotNil(t, options, "First call should return options")
//			}
//		})
//	}
//}
