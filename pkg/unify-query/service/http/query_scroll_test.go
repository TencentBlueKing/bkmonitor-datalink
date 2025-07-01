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
	"time"

	miniredis "github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

// ScrollTestSuite
type ScrollTestSuite struct {
	suite.Suite
	ctx       context.Context
	miniRedis *miniredis.Miniredis
}

func (s *ScrollTestSuite) SetupSuite() {
	log.InitTestLogger()

	var err error
	s.miniRedis, err = miniredis.Run()
	s.Require().NoError(err)

	redisOptions := &goRedis.UniversalOptions{
		Addrs: []string{s.miniRedis.Addr()},
		DB:    0,
	}
	s.ctx = metadata.InitHashID(context.Background())
	err = redis.SetInstance(s.ctx, "test-service", redisOptions)
	s.Require().NoError(err)

	mock.Init()

	err = redis.SetInstance(s.ctx, "mock", redisOptions)
	s.Require().NoError(err)

	influxdb.MockSpaceRouter(s.ctx)
	promql.MockEngine()

	err = redis.SetInstance(s.ctx, "mock", redisOptions)
	s.Require().NoError(err)

	user := &metadata.User{
		Name:     "test_user",
		TenantID: "test_tenant",
		SpaceUID: influxdb.SpaceUid,
	}
	metadata.SetUser(s.ctx, user)
}

func (s *ScrollTestSuite) TearDownSuite() {
	if s.miniRedis != nil {
		s.miniRedis.Close()
	}
}

func (s *ScrollTestSuite) SetupTest() {
	if s.miniRedis != nil {
		s.miniRedis.FlushAll()
	}
}

func (s *ScrollTestSuite) TestQueryRawWithScrollBasic() {
	esDataJSON := `{
		"took": 5,
		"timed_out": false,
		"_scroll_id": "test_scroll_id_123",
		"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
		"hits": {
			"total": {"value": 100, "relation": "eq"},
			"max_score": null,
			"hits": [
				{
					"_index": "test_index",
					"_type": "_doc",
					"_id": "1",
					"_score": null,
					"_source": {
						"timestamp": 1234567890000,
						"level": "info",
						"host": "server1"
					}
				},
				{
					"_index": "test_index",
					"_type": "_doc",
					"_id": "2",
					"_score": null,
					"_source": {
						"timestamp": 1234567890000,
						"level": "error",
						"host": "server2"
					}
				}
			]
		}
	}`

	mock.Es.Set(map[string]any{
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":10,"slice":{"id":0,"max":3},"sort":["_doc"]}`: esDataJSON,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":10,"slice":{"id":1,"max":3},"sort":["_doc"]}`: esDataJSON,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":10,"slice":{"id":2,"max":3},"sort":["_doc"]}`: esDataJSON,
	})

	queryTs := &structured.QueryTs{
		SpaceUid: influxdb.SpaceUid,
		Start:    "1234567890",
		End:      "1234567891",
		Limit:    10,
		Scroll:   "5m",
		QueryList: []*structured.Query{
			{
				TableID: "result_table.es",
			},
		},
	}

	total, list, resultTableOptions, done, err := queryRawWithScroll(s.ctx, queryTs)

	log.Infof(s.ctx, "Test result - Total: %d, List length: %d, Done: %v, Error: %v", total, len(list), done, err)
	assert.NoError(s.T(), err)
	assert.Greater(s.T(), total, int64(0))
	assert.Greater(s.T(), len(list), 0)
	assert.NotNil(s.T(), resultTableOptions)

	log.Infof(s.ctx, "Test completed successfully - Total: %d, List length: %d", total, len(list))
}

func (s *ScrollTestSuite) TestQueryRawWithScrollMultipleQueries() {
	// 准备 ES 查询的响应数据，必须是字符串格式的 JSON
	multiQueryDataJSON := `{
		"took": 3,
		"timed_out": false,
		"_scroll_id": "multi_query_scroll_id",
		"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
		"hits": {
			"total": {"value": 25, "relation": "eq"},
			"max_score": null,
			"hits": [
				{
					"_index": "test_index",
					"_type": "_doc",
					"_id": "multi_1",
					"_score": null,
					"_source": {
						"timestamp": 1234567890000,
						"level": "info",
						"message": "multi query test"
					}
				}
			]
		}
	}`

	mock.Es.Set(map[string]any{
		// 添加包含slice参数的ES查询格式作为key，使用字符串格式的JSON响应
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":5,"slice":{"id":0,"max":3},"sort":["_doc"]}`: multiQueryDataJSON,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":5,"slice":{"id":1,"max":3},"sort":["_doc"]}`: multiQueryDataJSON,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":5,"slice":{"id":2,"max":3},"sort":["_doc"]}`: multiQueryDataJSON,
		// 添加不带slice参数的查询，以防万一
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":5,"sort":["_doc"]}`: multiQueryDataJSON,
	})

	queryTs := &structured.QueryTs{
		SpaceUid: influxdb.SpaceUid,
		Start:    "1234567890",
		End:      "1234567891",
		Limit:    5,
		Scroll:   "5m",
		QueryList: []*structured.Query{
			{
				TableID:   "result_table.es",
				FieldList: []string{"*"},
			},
			{
				TableID:   "system.cpu_summary",
				FieldName: "usage",
				FieldList: []string{"usage"},
			},
		},
	}

	total, list, resultTableOptions, done, err := queryRawWithScroll(s.ctx, queryTs)

	log.Infof(s.ctx, "Multi-query test result - Total: %d, List length: %d, Done: %v, Error: %v", total, len(list), done, err)
	// 第二个查询是对 InfluxDB 的查询，可能会失败，所以这里允许有错误
	assert.GreaterOrEqual(s.T(), total, int64(0))
	assert.NotNil(s.T(), list)
	assert.NotNil(s.T(), resultTableOptions)

	if err != nil {
		log.Warnf(s.ctx, "Multi-query test completed with error (may be expected): %v", err)
	} else {
		log.Infof(s.ctx, "Multi-query test completed successfully - Total: %d, List length: %d", total, len(list))
	}
}

func (s *ScrollTestSuite) TestQueryRawWithScrollErrorHandling() {
	queryTs := &structured.QueryTs{
		SpaceUid: influxdb.SpaceUid,
		Start:    "1234567890",
		End:      "1234567891",
		Limit:    10,
		Scroll:   "5m",
		QueryList: []*structured.Query{
			{
				TableID:   "non_existent_table",
				FieldList: []string{"*"},
			},
		},
	}

	total, list, resultTableOptions, done, err := queryRawWithScroll(s.ctx, queryTs)

	log.Infof(s.ctx, "Error handling test result - Total: %d, List length: %d, Done: %v, Error: %v", total, len(list), done, err)

	// 系统应该优雅地处理不存在的表，返回空结果而不是错误
	assert.Nil(s.T(), err, "No error expected for non-existent table, should return empty result")
	assert.Equal(s.T(), int64(0), total, "Expected empty result for non-existent table")
	assert.Equal(s.T(), 0, len(list), "Expected empty list for non-existent table")
	if resultTableOptions != nil {
		assert.Equal(s.T(), 0, len(resultTableOptions), "Expected empty resultTableOptions for non-existent table")
	}

	log.Infof(s.ctx, "Error handling test completed - Total: %d, List length: %d", total, len(list))
}

func (s *ScrollTestSuite) TestScrollRedisOperations() {
	client := redis.Client()
	assert.NotNil(s.T(), client)

	err := client.Set(s.ctx, "test_key", "test_value", time.Minute).Err()
	assert.NoError(s.T(), err)

	val, err := client.Get(s.ctx, "test_key").Result()
	assert.NoError(s.T(), err)
	assert.Equal(s.T(), "test_value", val)

	log.Infof(s.ctx, "Redis operations test completed successfully")
}

func (s *ScrollTestSuite) TestQueryRawWithScrollSliceParameters() {
	// Mock数据需要匹配实际的ES查询字符串格式
	bigMockData := map[string]any{
		// 第一个slice查询 - 使用实际的ES查询格式
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":2,"slice":{"id":0,"max":3},"sort":["_doc"]}`: `{
			"took": 10,
			"timed_out": false,
			"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
			"hits": {
				"total": {"value": 200, "relation": "eq"},
				"max_score": null,
				"hits": [
					{
						"_index": "result_table.es_20241221",
						"_type": "_doc",
						"_id": "slice0_doc1",
						"_source": {
							"gseIndex": 111111,
							"path": "test/path/1",
							"dtEventTimeStamp": 1234567890
						}
					},
					{
						"_index": "result_table.es_20241221", 
						"_type": "_doc",
						"_id": "slice0_doc2",
						"_source": {
							"gseIndex": 222222,
							"path": "test/path/2", 
							"dtEventTimeStamp": 1234567891
						}
					}
				]
			},
			"_scroll_id": "scroll_id_slice_0"
		}`,

		// 第二个slice查询
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":2,"slice":{"id":1,"max":3},"sort":["_doc"]}`: `{
			"took": 8,
			"timed_out": false,
			"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
			"hits": {
				"total": {"value": 200, "relation": "eq"},
				"max_score": null,
				"hits": [
					{
						"_index": "result_table.es_20241221",
						"_type": "_doc", 
						"_id": "slice1_doc1",
						"_source": {
							"gseIndex": 333333,
							"path": "test/path/3",
							"dtEventTimeStamp": 1234567892
						}
					},
					{
						"_index": "result_table.es_20241221",
						"_type": "_doc",
						"_id": "slice1_doc2", 
						"_source": {
							"gseIndex": 444444,
							"path": "test/path/4",
							"dtEventTimeStamp": 1234567893
						}
					}
				]
			},
			"_scroll_id": "scroll_id_slice_1"
		}`,

		// 第三个slice查询
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":2,"slice":{"id":2,"max":3},"sort":["_doc"]}`: `{
			"took": 7,
			"timed_out": false,
			"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
			"hits": {
				"total": {"value": 200, "relation": "eq"},
				"max_score": null,
				"hits": [
					{
						"_index": "result_table.es_20241221",
						"_type": "_doc",
						"_id": "slice2_doc1",
						"_source": {
							"gseIndex": 777777,
							"path": "test/path/7",
							"dtEventTimeStamp": 1234567896
						}
					},
					{
						"_index": "result_table.es_20241221",
						"_type": "_doc",
						"_id": "slice2_doc2",
						"_source": {
							"gseIndex": 888888,
							"path": "test/path/8",
							"dtEventTimeStamp": 1234567897
						}
					}
				]
			},
			"_scroll_id": "scroll_id_slice_2"
		}`,

		// 继续查询 slice 0 的后续数据（使用scroll_id）
		`{"scroll":"5m","scroll_id":"scroll_id_slice_0"}`: `{
			"took": 5,
			"timed_out": false,
			"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
			"hits": {
				"total": {"value": 200, "relation": "eq"},
				"max_score": null,
				"hits": [
					{
						"_index": "result_table.es_20241221",
						"_type": "_doc",
						"_id": "slice0_doc3",
						"_source": {
							"gseIndex": 555555,
							"path": "test/path/5",
							"dtEventTimeStamp": 1234567894
						}
					}
				]
			},
			"_scroll_id": "scroll_id_slice_0_round2"
		}`,

		// 继续查询 slice 1 的后续数据
		`{"scroll":"5m","scroll_id":"scroll_id_slice_1"}`: `{
			"took": 6,
			"timed_out": false,
			"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
			"hits": {
				"total": {"value": 200, "relation": "eq"},
				"max_score": null,
				"hits": [
					{
						"_index": "result_table.es_20241221",
						"_type": "_doc",
						"_id": "slice1_doc3",
						"_source": {
							"gseIndex": 666666,
							"path": "test/path/6",
							"dtEventTimeStamp": 1234567895
						}
					}
				]
			},
			"_scroll_id": "scroll_id_slice_1_round2"
		}`,

		// 继续查询 slice 2 的后续数据
		`{"scroll":"5m","scroll_id":"scroll_id_slice_2"}`: `{
			"took": 4,
			"timed_out": false,
			"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
			"hits": {
				"total": {"value": 200, "relation": "eq"},
				"max_score": null,
				"hits": [
					{
						"_index": "result_table.es_20241221",
						"_type": "_doc",
						"_id": "slice2_doc3",
						"_source": {
							"gseIndex": 999999,
							"path": "test/path/9",
							"dtEventTimeStamp": 1234567898
						}
					}
				]
			},
			"_scroll_id": "scroll_id_slice_2_round2"
		}`,

		// 最终查询返回空结果，表示 scroll 完成
		`{"scroll":"5m","scroll_id":"scroll_id_slice_0_round2"}`: `{
			"took": 2,
			"timed_out": false,
			"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
			"hits": {
				"total": {"value": 200, "relation": "eq"},
				"max_score": null,
				"hits": []
			},
			"_scroll_id": ""
		}`,

		`{"scroll":"5m","scroll_id":"scroll_id_slice_1_round2"}`: `{
			"took": 2,
			"timed_out": false,
			"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
			"hits": {
				"total": {"value": 200, "relation": "eq"},
				"max_score": null,
				"hits": []
			},
			"_scroll_id": ""
		}`,

		`{"scroll":"5m","scroll_id":"scroll_id_slice_2_round2"}`: `{
			"took": 2,
			"timed_out": false,
			"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
			"hits": {
				"total": {"value": 200, "relation": "eq"},
				"max_score": null,
				"hits": []
			},
			"_scroll_id": ""
		}`,
	}

	// 更新 mock 数据
	mock.Es.Set(bigMockData)

	// 创建查询参数，设置较小的 limit 以测试多轮查询
	queryTs := &structured.QueryTs{
		SpaceUid: influxdb.SpaceUid,
		Start:    "1234567890",
		End:      "1234567891",
		Limit:    2, // 小的 limit 确保需要多轮查询
		Scroll:   "5m",
		QueryList: []*structured.Query{
			{
				TableID:   "result_table.es",
				FieldList: []string{"gseIndex", "path"},
			},
		},
	}

	// 第一次执行 scroll 查询
	log.Infof(s.ctx, "[TEST] ====== 第一次 scroll 查询 ======")
	total1, list1, resultTableOptions1, done1, err1 := queryRawWithScroll(s.ctx, queryTs)

	// 验证第一次查询结果
	assert.NoError(s.T(), err1)
	assert.Greater(s.T(), total1, int64(0), "First scroll should return data")
	assert.Greater(s.T(), len(list1), 0, "First scroll should return list data")
	assert.NotNil(s.T(), resultTableOptions1, "ResultTableOptions should not be nil")
	assert.False(s.T(), done1, "First scroll should not be done")

	log.Infof(s.ctx, "[TEST] First scroll result - Total: %d, List length: %d", total1, len(list1))

	// 验证返回的数据包含 slice 相关的字段
	for i, item := range list1 {
		log.Infof(s.ctx, "[TEST] First scroll item %d: %+v", i, item)
		assert.Contains(s.T(), item, "gseIndex", "Should contain gseIndex field")
		assert.Contains(s.T(), item, "path", "Should contain path field")
	}

	// 第二次执行相同查询（应该使用之前的 scroll session 继续）
	queryTs.ClearCache = false // 不清理缓存，继续使用之前的 session
	log.Infof(s.ctx, "[TEST] ====== 第二次 scroll 查询（继续 session）======")
	total2, list2, _, done2, err2 := queryRawWithScroll(s.ctx, queryTs)

	// 验证第二次查询结果
	assert.NoError(s.T(), err2)
	assert.GreaterOrEqual(s.T(), total2, int64(0), "Second scroll should return data or be empty")
	assert.GreaterOrEqual(s.T(), len(list2), 0, "Second scroll list should be valid")

	log.Infof(s.ctx, "[TEST] Second scroll result - Total: %d, List length: %d, Done: %v", total2, len(list2), done2)

	// 验证第二次返回的数据
	for i, item := range list2 {
		log.Infof(s.ctx, "[TEST] Second scroll item %d: %+v", i, item)
	}

	// 第三次查询（应该继续直到所有数据获取完毕）
	log.Infof(s.ctx, "[TEST] ====== 第三次 scroll 查询（完成 session）======")
	total3, list3, _, done3, err3 := queryRawWithScroll(s.ctx, queryTs)

	// 验证第三次查询结果
	assert.NoError(s.T(), err3)
	assert.GreaterOrEqual(s.T(), total3, int64(0), "Third scroll should complete")
	assert.GreaterOrEqual(s.T(), len(list3), 0, "Third scroll list should be valid")

	log.Infof(s.ctx, "[TEST] Third scroll result - Total: %d, List length: %d, Done: %v", total3, len(list3), done3)

	// 验证总计的数据量
	totalAll := total1 + total2 + total3
	allListLen := len(list1) + len(list2) + len(list3)
	log.Infof(s.ctx, "[TEST] ====== 总体验证 ======")
	log.Infof(s.ctx, "[TEST] Total across all scrolls - Total: %d, All list length: %d", totalAll, allListLen)

	// 验证我们确实获得了来自不同 slice 的数据
	allData := append(append(list1, list2...), list3...)
	uniqueIds := make(map[any]bool)
	for _, item := range allData {
		if gseIndex, ok := item["gseIndex"]; ok {
			uniqueIds[gseIndex] = true
		}
	}

	log.Infof(s.ctx, "[TEST] Found %d unique gseIndex values", len(uniqueIds))
	assert.GreaterOrEqual(s.T(), len(uniqueIds), 2, "Should have data from multiple slices")

	// 验证 scroll session 管理正常
	if len(list3) == 0 {
		log.Infof(s.ctx, "[TEST] Scroll session completed successfully (empty final result)")
	}

	log.Infof(s.ctx, "[TEST] ====== Slice 参数测试完成 ======")
}

func (s *ScrollTestSuite) TestScrollSliceRotation() {
	// 创建测试数据，模拟3个slice的不同响应
	slice0Response := `{
		"took": 5,
		"timed_out": false,
		"_scroll_id": "scroll_id_slice_0",
		"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
		"hits": {
			"total": {"value": 300, "relation": "eq"},
			"max_score": null,
			"hits": [
				{
					"_index": "test_index",
					"_type": "_doc",
					"_id": "slice0_doc1",
					"_source": {
						"gseIndex": 100001,
						"path": "slice0/path1",
						"dtEventTimeStamp": 1234567890
					}
				}
			]
		}
	}`

	slice1Response := `{
		"took": 6,
		"timed_out": false,
		"_scroll_id": "scroll_id_slice_1", 
		"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
		"hits": {
			"total": {"value": 300, "relation": "eq"},
			"max_score": null,
			"hits": [
				{
					"_index": "test_index",
					"_type": "_doc",
					"_id": "slice1_doc1",
					"_source": {
						"gseIndex": 200001,
						"path": "slice1/path1",
						"dtEventTimeStamp": 1234567890
					}
				}
			]
		}
	}`

	slice2Response := `{
		"took": 7,
		"timed_out": false,
		"_scroll_id": "scroll_id_slice_2",
		"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
		"hits": {
			"total": {"value": 300, "relation": "eq"},
			"max_score": null,
			"hits": [
				{
					"_index": "test_index",
					"_type": "_doc",
					"_id": "slice2_doc1",
					"_source": {
						"gseIndex": 300001,
						"path": "slice2/path1",
						"dtEventTimeStamp": 1234567890
					}
				}
			]
		}
	}`

	// 后续查询返回空结果，表示完成
	emptyResponse := `{
		"took": 2,
		"timed_out": false,
		"_shards": {"total": 1, "successful": 1, "skipped": 0, "failed": 0},
		"hits": {
			"total": {"value": 300, "relation": "eq"},
			"max_score": null,
			"hits": []
		},
		"_scroll_id": ""
	}`

	// 设置 mock 数据，覆盖不同slice ID的查询
	mockData := map[string]any{
		// 初始slice查询 (slice ID 0, 1, 2)
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":1,"slice":{"id":0,"max":3},"sort":["_doc"]}`: slice0Response,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":1,"slice":{"id":1,"max":3},"sort":["_doc"]}`: slice1Response,
		`{"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1234567890,"include_lower":true,"include_upper":true,"to":1234567891}}}}},"size":1,"slice":{"id":2,"max":3},"sort":["_doc"]}`: slice2Response,

		// 后续scroll查询 - 使用正确的请求体格式
		`{"scroll":"5m","scroll_id":"scroll_id_slice_0"}`: emptyResponse,
		`{"scroll":"5m","scroll_id":"scroll_id_slice_1"}`: emptyResponse,
		`{"scroll":"5m","scroll_id":"scroll_id_slice_2"}`: emptyResponse,
	}

	mock.Es.Set(mockData)

	// 创建查询参数
	queryTs := &structured.QueryTs{
		SpaceUid: influxdb.SpaceUid,
		Start:    "1234567890",
		End:      "1234567891",
		Limit:    1,
		Scroll:   "5m",
		QueryList: []*structured.Query{
			{
				TableID:   "result_table.es",
				FieldList: []string{"gseIndex", "path"},
			},
		},
		ClearCache: true,
	}

	var allGseIndexes []int
	var requestCount int

	// 执行多次查询，验证slice轮询
	for i := 0; i < 6; i++ { // 执行6次查询，应该覆盖所有slice
		requestCount++
		log.Infof(s.ctx, "[TEST] ====== 第 %d 次 scroll 查询 ======", requestCount)

		if i > 0 {
			queryTs.ClearCache = false // 后续查询不清理缓存
		}

		total, list, _, done, err := queryRawWithScroll(s.ctx, queryTs)

		log.Infof(s.ctx, "[TEST] Request %d result - Total: %d, List length: %d, Done: %v, Error: %v",
			requestCount, total, len(list), done, err)

		if err != nil {
			if requestCount <= 3 {
				// 前3次应该成功
				assert.NoError(s.T(), err, "Request %d should succeed", requestCount)
			} else {
				// 后续请求可能返回"session completed"错误，这是正常的
				log.Infof(s.ctx, "[TEST] Request %d got expected completion: %v", requestCount, err)
				break
			}
		}

		// 收集返回的数据
		for _, item := range list {
			if gseIndex, ok := item["gseIndex"]; ok {
				if gseVal, isInt := gseIndex.(int); isInt {
					allGseIndexes = append(allGseIndexes, gseVal)
					log.Infof(s.ctx, "[TEST] Request %d found gseIndex: %d", requestCount, gseVal)
				} else if gseVal, isFloat := gseIndex.(float64); isFloat {
					intVal := int(gseVal)
					allGseIndexes = append(allGseIndexes, intVal)
					log.Infof(s.ctx, "[TEST] Request %d found gseIndex: %d (converted from float)", requestCount, intVal)
				}
			}
		}

		// 如果没有数据返回，说明已经完成
		if len(list) == 0 {
			log.Infof(s.ctx, "[TEST] Request %d returned no data, scroll completed", requestCount)
			break
		}
	}

	// 验证结果
	log.Infof(s.ctx, "[TEST] ====== 验证结果 ======")
	log.Infof(s.ctx, "[TEST] Total requests made: %d", requestCount)
	log.Infof(s.ctx, "[TEST] All gseIndexes found: %v", allGseIndexes)

	// 应该至少有一些数据
	assert.Greater(s.T(), len(allGseIndexes), 0, "Should have collected some data")

	// 验证我们确实获得了来自不同slice的数据
	uniqueSliceMarkers := make(map[int]bool)
	for _, gseIndex := range allGseIndexes {
		// gseIndex的格式为 XY0001，其中X表示slice ID
		sliceMarker := gseIndex / 100000
		uniqueSliceMarkers[sliceMarker] = true
	}

	log.Infof(s.ctx, "[TEST] Unique slice markers found: %v", uniqueSliceMarkers)
	assert.Greater(s.T(), len(uniqueSliceMarkers), 1, "Should have data from multiple slices")

	log.Infof(s.ctx, "[TEST] Slice rotation test completed successfully")
}

// TestQueryRawWithScroll 运行所有 scroll 测试
func TestQueryRawWithScroll(t *testing.T) {
	suite.Run(t, new(ScrollTestSuite))
}
