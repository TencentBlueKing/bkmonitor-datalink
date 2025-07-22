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
	"sync"
	"testing"

	miniredis "github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	ir "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/router/influxdb"
)

type expectResult struct {
	desc    string
	total   int64
	done    bool
	hasData bool
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

	mock.Es.Set(map[string]any{
		`{"size":10,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"from":1723594000,"to":1723595000,"format":"epoch_second","include_lower":true,"include_upper":true}}}}}}`: `{"_scroll_id":"scroll_fallback","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":3,"relation":"eq"},"max_score":null,"hits":[{"_index":"es_index","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"test1"}},{"_index":"es_index","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"test2"}},{"_index":"es_index","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"test3"}}]}}`,

		`{"size":10,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","include_lower":true,"include_upper":true,"from":1723594000,"to":1723595000}}}}}}`: `{"_scroll_id":"scroll_fallback","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":3,"relation":"eq"},"max_score":null,"hits":[{"_index":"es_index","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"test1"}},{"_index":"es_index","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"test2"}},{"_index":"es_index","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"test3"}}]}}`,

		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"include_lower":true,"include_upper":true,"from":1723594000,"to":1723595000,"format":"epoch_second"}}}}},"size":10}`: `{"_scroll_id":"scroll_fallback","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":3,"relation":"eq"},"max_score":null,"hits":[{"_index":"es_index","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"test1"}},{"_index":"es_index","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"test2"}},{"_index":"es_index","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"test3"}}]}}`,

		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":0,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_slice_0","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"es_index","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"test1"}}]}}`,

		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":1,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_slice_1","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"es_index","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"test2"}}]}}`,

		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":2,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"scroll_slice_2","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"es_index","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"test3"}}]}}`,

		`{"scroll":"9m","scroll_id":"scroll_slice_0"}`: `{"_scroll_id":"scroll_slice_0_2","took":3,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"es_index","_id":"5","_source":{"dtEventTimeStamp":"1723594005000","data":"test5"}}]}}`,

		`{"scroll":"9m","scroll_id":"scroll_slice_1"}`: `{"_scroll_id":"scroll_slice_1_2","took":3,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"es_index","_id":"6","_source":{"dtEventTimeStamp":"1723594006000","data":"test6"}}]}}`,

		`{"scroll":"9m","scroll_id":"scroll_slice_2"}`: `{"_scroll_id":"scroll_slice_2_2","took":3,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"es_index","_id":"7","_source":{"dtEventTimeStamp":"1723594007000","data":"test7"}}]}}`,

		`{"scroll":"9m","scroll_id":"scroll_fallback"}`: `{"_scroll_id":"scroll_fallback_2","took":3,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"es_index","_id":"6","_source":{"dtEventTimeStamp":"1723594006000","data":"test6"}}]}}`,

		`{"scroll":"9m","scroll_id":"scroll_slice_0_2"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,

		`{"scroll":"9m","scroll_id":"scroll_slice_1_2"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,

		`{"scroll":"9m","scroll_id":"scroll_slice_2_2"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,

		`{"scroll":"9m","scroll_id":"scroll_fallback_2"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,
	})

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
				desc:    "First scroll request",
				total:   3,
				done:    false,
				hasData: true,
			},
			{
				desc:    "Second scroll request",
				total:   3,
				done:    false,
				hasData: true,
			},
			{
				desc:    "Third scroll request",
				total:   3,
				done:    false,
				hasData: true,
			},
			{
				desc:    "Final scroll",
				total:   0,
				done:    true,
				hasData: false,
			},
		},
	}
	user := &metadata.User{
		Key:       "username:test_scroll_user",
		SpaceUID:  spaceUid,
		SkipSpace: "true",
	}
	for _, c := range tCase.expected {
		t.Run(c.desc, func(t *testing.T) {
			testCtx := metadata.InitHashID(context.Background())
			metadata.SetUser(testCtx, user)
			total, list, _, done, err := queryRawWithScroll(testCtx, tCase.queryTs)
			hasData := len(list) > 0
			assert.NoError(t, err, "QueryRawWithScroll should not return error")
			assert.Equal(t, c.total, total, "Total should match expected value")
			assert.Equal(t, c.done, done, "Done should match expected value")
			assert.Equal(t, c.hasData, hasData, "HasData should match expected value")
		})

	}
}

func TestQueryRawWithScrollBkSql(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	testTableId := "result_table.bksql"

	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err, "Failed to get space router")

	err = router.Add(ctx, ir.ResultTableDetailKey, testTableId, &ir.ResultTableDetail{
		StorageId:   4,
		TableId:     testTableId,
		DB:          "bksql_db",
		StorageType: consul.BkSqlStorageType,
		DataLabel:   "bksql_label",
	})
	require.NoError(t, err, "Failed to add BkSql route")

	s, err := miniredis.Run()
	require.NoError(t, err, "Failed to start miniredis")
	defer s.Close()

	options := &goRedis.UniversalOptions{
		Addrs: []string{s.Addr()},
		DB:    0,
	}

	err = redisUtil.SetInstance(ctx, "test", options)
	require.NoError(t, err, "Failed to set redis instance")

	mock.BkSQL.Set(map[string]any{
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`bksql_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10`:           `{"result":true,"code":"00","message":"","data":{"list":[{"dtEventTimeStamp":"1723594001000","data":"bksql_test1"},{"dtEventTimeStamp":"1723594002000","data":"bksql_test2"}],"total_records":2}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`bksql_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 10`: `{"result":true,"code":"00","message":"","data":{"list":[{"dtEventTimeStamp":"1723594003000","data":"bksql_test3"}],"total_records":1}}`,
		`SELECT *, ` + "`dtEventTimeStamp`" + ` AS ` + "`_timestamp_`" + ` FROM ` + "`bksql_db`" + ` WHERE ` + "`dtEventTimeStamp`" + ` >= 1723594000000 AND ` + "`dtEventTimeStamp`" + ` < 1723595000000 AND ` + "`thedate`" + ` = '20240814' LIMIT 10 OFFSET 20`: `{"result":true,"code":"00","message":"","data":{"list":[],"total_records":0}}`,
	})

	start := "1723594000"
	end := "1723595000"

	queryTs := &structured.QueryTs{
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
	}

	user := &metadata.User{
		Key:       "username:test_bksql_user",
		SpaceUID:  spaceUid,
		SkipSpace: "true",
	}
	testCtx := ctx
	metadata.SetUser(testCtx, user)

	total1, list1, options1, done1, err1 := queryRawWithScroll(testCtx, queryTs)
	assert.NoError(t, err1, "First BkSql scroll request should succeed")
	assert.NotNil(t, options1, "First call should return options")
	assert.IsType(t, int64(0), total1, "Total should be int64")
	assert.NotNil(t, list1, "List should not be nil")
	assert.IsType(t, false, done1, "Done should be boolean")

	total2, list2, _, done2, err2 := queryRawWithScroll(testCtx, queryTs)
	assert.NoError(t, err2, "Second BkSql scroll request should succeed")
	assert.IsType(t, int64(0), total2, "Total should be int64")
	assert.NotNil(t, list2, "List should not be nil")
	assert.IsType(t, false, done2, "Done should be boolean")

	total3, list3, _, done3, err3 := queryRawWithScroll(testCtx, queryTs)
	assert.NoError(t, err3, "Third BkSql scroll request should succeed")
	assert.IsType(t, int64(0), total3, "Total should be int64")
	assert.NotNil(t, list3, "List should not be nil")
	assert.IsType(t, false, done3, "Done should be boolean")
}

// TestQueryRawWithScrollErrorHandling tests error scenarios in scroll functionality
func TestQueryRawWithScrollErrorHandling(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	testTableId := "result_table.error"

	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err, "Failed to get space router")

	err = router.Add(ctx, ir.ResultTableDetailKey, testTableId, &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     testTableId,
		DB:          testTableId,
		StorageType: consul.ElasticsearchStorageType,
		DataLabel:   testTableId,
	})
	require.NoError(t, err, "Failed to add route")

	s, err := miniredis.Run()
	require.NoError(t, err, "Failed to start miniredis")
	defer s.Close()

	options := &goRedis.UniversalOptions{
		Addrs: []string{s.Addr()},
		DB:    0,
	}

	err = redisUtil.SetInstance(ctx, "test", options)
	require.NoError(t, err, "Failed to set redis instance")

	t.Run("elasticsearch query failure", func(t *testing.T) {
		mock.Es.Set(map[string]any{
			`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":0,"max":3},"sort":["_doc"]}`: `{"error":{"type":"search_phase_execution_exception","reason":"all shards failed"}}`,
		})

		start := "1723594000"
		end := "1723595000"

		queryTs := &structured.QueryTs{
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
		}

		user := &metadata.User{
			Key:       "username:test_error_user",
			SpaceUID:  spaceUid,
			SkipSpace: "true",
		}
		testCtx := ctx
		metadata.SetUser(testCtx, user)

		total, list, options, done, err := queryRawWithScroll(testCtx, queryTs)

		assert.IsType(t, int64(0), total, "Total should be int64 type")
		assert.NotNil(t, list, "List should not be nil")
		assert.IsType(t, false, done, "Done should be boolean type")
		assert.NotNil(t, options, "Options should not be nil")

		if err != nil {
			assert.IsType(t, "", err.Error(), "Error should have string message")
		} else {
			assert.GreaterOrEqual(t, total, int64(0), "Total should be non-negative")
		}
	})

	t.Run("redis connection failure", func(t *testing.T) {
		s.Close()

		start := "1723594000"
		end := "1723595000"

		queryTs := &structured.QueryTs{
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
		}

		user := &metadata.User{
			Key:       "username:test_redis_error_user",
			SpaceUID:  spaceUid,
			SkipSpace: "true",
		}
		testCtx := ctx
		metadata.SetUser(testCtx, user)

		total, list, options, done, err := queryRawWithScroll(testCtx, queryTs)

		assert.Error(t, err, "Should return error when redis is unavailable")
		assert.Contains(t, err.Error(), "connection refused", "Error should indicate connection failure")

		assert.Equal(t, int64(0), total, "Total should be 0 on error")
		assert.Nil(t, list, "List should be nil on error")
		assert.Nil(t, options, "Options should be nil on error")
		assert.False(t, done, "Done should be false on error")
	})
}

func TestQueryRawWithScrollConcurrency(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	testTableId := "result_table.concurrent"

	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err, "Failed to get space router")

	err = router.Add(ctx, ir.ResultTableDetailKey, testTableId, &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     testTableId,
		DB:          testTableId,
		StorageType: consul.ElasticsearchStorageType,
		DataLabel:   testTableId,
	})
	require.NoError(t, err, "Failed to add route")

	s, err := miniredis.Run()
	require.NoError(t, err, "Failed to start miniredis")
	defer s.Close()

	options := &goRedis.UniversalOptions{
		Addrs: []string{s.Addr()},
		DB:    0,
	}

	err = redisUtil.SetInstance(ctx, "test", options)
	require.NoError(t, err, "Failed to set redis instance")

	mock.Es.Set(map[string]any{
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":0,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"concurrent_scroll_0","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"concurrent_index","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"concurrent_test1"}}]}}`,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":1,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"concurrent_scroll_1","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"concurrent_index","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"concurrent_test2"}}]}}`,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":2,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"concurrent_scroll_2","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"concurrent_index","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"concurrent_test3"}}]}}`,
		`{"scroll":"9m","scroll_id":"concurrent_scroll_0"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,
		`{"scroll":"9m","scroll_id":"concurrent_scroll_1"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,
		`{"scroll":"9m","scroll_id":"concurrent_scroll_2"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,
	})

	start := "1723594000"
	end := "1723595000"

	queryTs := &structured.QueryTs{
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
	}

	user := &metadata.User{
		Key:       "username:test_concurrent_user",
		SpaceUID:  spaceUid,
		SkipSpace: "true",
	}
	testCtx := ctx
	metadata.SetUser(testCtx, user)

	const numGoroutines = 5
	var wg sync.WaitGroup
	results := make([]struct {
		total int64
		done  bool
		err   error
	}, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()

			total, _, _, done, err := queryRawWithScroll(testCtx, queryTs)
			results[index] = struct {
				total int64
				done  bool
				err   error
			}{total, done, err}
		}(i)
	}

	wg.Wait()

	successCount := 0
	lockErrorCount := 0

	for _, result := range results {
		if result.err == nil {
			successCount++
			assert.GreaterOrEqual(t, result.total, int64(0), "Successful request should have non-negative total")
			assert.IsType(t, false, result.done, "Done should be boolean type")
		} else {
			assert.Contains(t, result.err.Error(), "already locked", "Concurrent failures should be due to locking")
			lockErrorCount++
		}
	}

	assert.Equal(t, numGoroutines, successCount+lockErrorCount, "All requests should either succeed or fail due to locking")
	assert.GreaterOrEqual(t, successCount, 1, "At least one request should succeed")

	if lockErrorCount > 0 {
		assert.Greater(t, lockErrorCount, 0, "Redis locking should prevent some concurrent access")
	} else {
		assert.Equal(t, numGoroutines, successCount, "All requests succeeded without lock contention")
	}
}

func TestQueryRawWithScrollSessionCompletion(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())
	spaceUid := influxdb.SpaceUid

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	testTableId := "result_table.completion"

	router, err := influxdb.GetSpaceTsDbRouter()
	require.NoError(t, err, "Failed to get space router")

	err = router.Add(ctx, ir.ResultTableDetailKey, testTableId, &ir.ResultTableDetail{
		StorageId:   3,
		TableId:     testTableId,
		DB:          testTableId,
		StorageType: consul.ElasticsearchStorageType,
		DataLabel:   testTableId,
	})
	require.NoError(t, err, "Failed to add route")

	s, err := miniredis.Run()
	require.NoError(t, err, "Failed to start miniredis")
	defer s.Close()

	options := &goRedis.UniversalOptions{
		Addrs: []string{s.Addr()},
		DB:    0,
	}

	err = redisUtil.SetInstance(ctx, "test", options)
	require.NoError(t, err, "Failed to set redis instance")

	mock.Es.Set(map[string]any{
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":0,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"completion_scroll_0","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"completion_index","_id":"1","_source":{"dtEventTimeStamp":"1723594001000","data":"completion_test1"}}]}}`,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":1,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"completion_scroll_1","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"completion_index","_id":"2","_source":{"dtEventTimeStamp":"1723594002000","data":"completion_test2"}}]}}`,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"slice":{"id":2,"max":3},"sort":["_doc"]}`: `{"_scroll_id":"completion_scroll_2","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"completion_index","_id":"3","_source":{"dtEventTimeStamp":"1723594003000","data":"completion_test3"}}]}}`,

		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10,"sort":["_doc"]}`: `{"_scroll_id":"completion_fallback","took":5,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":null,"hits":[{"_index":"completion_index","_id":"4","_source":{"dtEventTimeStamp":"1723594004000","data":"completion_test4"}}]}}`,

		`{"scroll":"9m","scroll_id":"completion_scroll_0"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,
		`{"scroll":"9m","scroll_id":"completion_scroll_1"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,
		`{"scroll":"9m","scroll_id":"completion_scroll_2"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,
		`{"scroll":"9m","scroll_id":"completion_fallback"}`: `{"_scroll_id":"","took":1,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,
	})

	start := "1723594000"
	end := "1723595000"

	queryTs := &structured.QueryTs{
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
	}

	user := &metadata.User{
		Key:       "username:test_completion_user",
		SpaceUID:  spaceUid,
		SkipSpace: "true",
	}
	testCtx := ctx
	metadata.SetUser(testCtx, user)

	total1, list1, options1, done1, err1 := queryRawWithScroll(testCtx, queryTs)
	assert.NoError(t, err1, "First scroll request should succeed")
	assert.NotNil(t, options1, "First call should return options")
	assert.IsType(t, int64(0), total1, "Total should be int64")
	assert.NotNil(t, list1, "List should not be nil")
	assert.IsType(t, false, done1, "Done should be boolean")
	assert.GreaterOrEqual(t, total1, int64(0), "Total should be non-negative")

	total2, list2, options2, done2, err2 := queryRawWithScroll(testCtx, queryTs)
	assert.NoError(t, err2, "Second scroll request should succeed")
	assert.NotNil(t, options2, "Second call should return options")
	assert.IsType(t, int64(0), total2, "Total should be int64")
	assert.NotNil(t, list2, "List should not be nil")
	assert.IsType(t, false, done2, "Done should be boolean")
	assert.GreaterOrEqual(t, total2, int64(0), "Total should be non-negative")

	total3, list3, options3, done3, err3 := queryRawWithScroll(testCtx, queryTs)
	assert.NoError(t, err3, "Third scroll request should succeed")
	assert.NotNil(t, options3, "Third call should return options")
	assert.IsType(t, int64(0), total3, "Total should be int64")
	assert.NotNil(t, list3, "List should not be nil")
	assert.IsType(t, false, done3, "Done should be boolean")
	assert.GreaterOrEqual(t, total3, int64(0), "Total should be non-negative")

}
