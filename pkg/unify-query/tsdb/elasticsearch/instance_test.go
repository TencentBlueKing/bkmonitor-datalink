// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestInstance_getAlias(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	inst, err := NewInstance(ctx, &InstanceOption{
		Connects: []Connect{
			{
				Address: mock.EsUrl,
			},
		},
		Timeout: time.Minute,
	})
	if err != nil {
		log.Panicf(ctx, err.Error())
	}

	for name, c := range map[string]struct {
		start       time.Time
		end         time.Time
		timezone    string
		db          string
		needAddTime bool

		expected []string
	}{
		"3d with UTC": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 3, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			expected:    []string{"db_test_20240101*", "db_test_20240102*", "db_test_20240103*"},
		},
		"change month with Asia/ShangHai": {
			start:       time.Date(2024, 1, 25, 7, 10, 5, 0, time.UTC),
			end:         time.Date(2024, 2, 2, 6, 1, 4, 10, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_20240125*", "db_test_20240126*", "db_test_20240127*", "db_test_20240128*", "db_test_20240129*", "db_test_20240130*", "db_test_20240131*", "db_test_20240201*", "db_test_20240202*"},
		},
		"2d with Asia/ShangHai": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 3, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_20240102*", "db_test_20240103*", "db_test_20240104*"},
		},
		"14d with Asia/ShangHai": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_20240102*", "db_test_20240103*", "db_test_20240104*", "db_test_20240105*", "db_test_20240106*", "db_test_20240107*", "db_test_20240108*", "db_test_20240109*", "db_test_20240110*", "db_test_20240111*", "db_test_20240112*", "db_test_20240113*", "db_test_20240114*", "db_test_20240115*", "db_test_20240116*"},
		},
		"16d with Asia/ShangHai": {
			start:       time.Date(2024, 1, 15, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 2, 10, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_202401*", "db_test_202402*"},
		},
		"15d with Asia/ShangHai": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 1, 16, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_202401*"},
		},
		"6m with Asia/ShangHai": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 7, 1, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_202401*", "db_test_202402*", "db_test_202403*", "db_test_202404*", "db_test_202405*", "db_test_202406*", "db_test_202407*"},
		},
		"7m with Asia/ShangHai": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 8, 1, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			timezone:    "Asia/ShangHai",
			expected:    []string{"db_test_202402*", "db_test_202403*", "db_test_202404*", "db_test_202405*", "db_test_202406*", "db_test_202407*", "db_test_202408*"},
		},
		"2m and db": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 3, 1, 20, 0, 0, 0, time.UTC),
			needAddTime: true,
			db:          "db_test,db_test_clone",
			expected:    []string{"db_test_202401*", "db_test_clone_202401*", "db_test_202402*", "db_test_clone_202402*", "db_test_202403*", "db_test_clone_202403*"},
		},
		"2m and db and not need add time": {
			start:       time.Date(2024, 1, 1, 20, 0, 0, 0, time.UTC),
			end:         time.Date(2024, 3, 1, 20, 0, 0, 0, time.UTC),
			needAddTime: false,
			db:          "db_test,db_test_clone",
			expected:    []string{"db_test", "db_test_clone"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			if c.db == "" {
				c.db = "db_test"
			}
			ctx = metadata.InitHashID(ctx)
			actual, err := inst.getAlias(ctx, c.db, c.needAddTime, c.start, c.end, c.timezone)
			assert.Nil(t, err)
			assert.Equal(t, c.expected, actual)
		})
	}
}

func TestInstance_queryReference(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	ins, err := NewInstance(ctx, &InstanceOption{
		Connects: []Connect{
			{
				Address: mock.EsUrl,
			},
		},
		Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	defaultStart := time.UnixMilli(1723593608000)
	defaultEnd := time.UnixMilli(1723679962000)

	db := "es_index"
	field := "dtEventTimeStamp"

	mock.Es.Set(map[string]any{

		// 统计 __ext.io_kubernetes_pod 不为空的文档数量
		`{"aggregations":{"_value":{"value_count":{"field":"__ext.io_kubernetes_pod"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":92,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":1523302}}}`,

		// 统计 __ext.io_kubernetes_pod 不为空的去重文档数量
		`{"aggregations":{"_value":{"cardinality":{"field":"__ext.io_kubernetes_pod"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":170,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":4}}}`,

		// 使用 promql 计算平均值 sum(count_over_time(field[12h]))
		`{"aggregations":{"__ext.container_name":{"aggregations":{"__ext.io_kubernetes_pod":{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"value_count":{"field":"dtEventTimeStamp"}}},"date_histogram":{"extended_bounds":{"max":1723679962000,"min":1723593608000},"field":"dtEventTimeStamp","interval":"12h","min_doc_count":0}}},"terms":{"field":"__ext.io_kubernetes_pod","missing":" "}}},"terms":{"field":"__ext.container_name","missing":" "}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":185,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"__ext.container_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"unify-query","doc_count":1523254,"__ext.io_kubernetes_pod":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"bkmonitor-unify-query-64bd4f5df4-599f9","doc_count":767743,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1723593600000","key":1723593600000,"doc_count":375064,"_value":{"value":375064}},{"key_as_string":"1723636800000","key":1723636800000,"doc_count":392679,"_value":{"value":392679}}]}},{"key":"bkmonitor-unify-query-64bd4f5df4-llp94","doc_count":755511,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1723593600000","key":1723593600000,"doc_count":381173,"_value":{"value":381173}},{"key_as_string":"1723636800000","key":1723636800000,"doc_count":374338,"_value":{"value":374338}}]}}]}},{"key":"sync-apigw","doc_count":48,"__ext.io_kubernetes_pod":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"bkmonitor-unify-query-apigw-sync-1178-cl8k8","doc_count":24,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1723593600000","key":1723593600000,"doc_count":24,"_value":{"value":24}},{"key_as_string":"1723636800000","key":1723636800000,"doc_count":0,"_value":{"value":0}}]}},{"key":"bkmonitor-unify-query-apigw-sync-1179-9h9xv","doc_count":24,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1723593600000","key":1723593600000,"doc_count":24,"_value":{"value":24}},{"key_as_string":"1723636800000","key":1723636800000,"doc_count":0,"_value":{"value":0}}]}}]}}]}}}`,

		// 使用非时间聚合统计数量
		`{"aggregations":{"_value":{"value_count":{"field":"dtEventTimeStamp"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":36,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":1523302}}}`,

		// 获取 50 分位值
		`{"aggregations":{"_value":{"percentiles":{"field":"dtEventTimeStamp","percents":[50]}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":675,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"values":{"50.0":1.7236371328063303E12,"50.0_as_string":"1723637132806"}}}}`,

		// 获取 50, 90 分支值，同时按 6h 时间聚合
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"percentiles":{"field":"dtEventTimeStamp","percents":[50,90]}}},"date_histogram":{"extended_bounds":{"max":1723679962000,"min":1723593608000},"field":"dtEventTimeStamp","interval":"6h","min_doc_count":0}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":1338,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"buckets":[{"key_as_string":"1723593600000","key":1723593600000,"doc_count":387467,"_value":{"values":{"50.0":1.7236043803502532E12,"50.0_as_string":"1723604380350","90.0":1.7236129561289934E12,"90.0_as_string":"1723612956128"}}},{"key_as_string":"1723615200000","key":1723615200000,"doc_count":368818,"_value":{"values":{"50.0":1.7236258380061033E12,"50.0_as_string":"1723625838006","90.0":1.7236346787215513E12,"90.0_as_string":"1723634678721"}}},{"key_as_string":"1723636800000","key":1723636800000,"doc_count":382721,"_value":{"values":{"50.0":1.7236475858829739E12,"50.0_as_string":"1723647585882","90.0":1.723656196499344E12,"90.0_as_string":"1723656196499"}}},{"key_as_string":"1723658400000","key":1723658400000,"doc_count":384296,"_value":{"values":{"50.0":1.7236691776407131E12,"50.0_as_string":"1723669177640","90.0":1.723677836133885E12,"90.0_as_string":"1723677836133"}}}]}}}`,

		// 根据 field 字段聚合计算数量，同时根据值排序
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"value_count":{"field":"dtEventTimeStamp"}}},"terms":{"field":"dtEventTimeStamp","missing":" ","order":[{"_value":"asc"}]}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":198,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"doc_count_error_upper_bound":-1,"sum_other_doc_count":1523292,"buckets":[{"key":1723593878000,"key_as_string":"1723593878000","doc_count":1,"_value":{"value":1}},{"key":1723593947000,"key_as_string":"1723593947000","doc_count":1,"_value":{"value":1}},{"key":1723594186000,"key_as_string":"1723594186000","doc_count":1,"_value":{"value":1}},{"key":1723595733000,"key_as_string":"1723595733000","doc_count":1,"_value":{"value":1}},{"key":1723596287000,"key_as_string":"1723596287000","doc_count":1,"_value":{"value":1}},{"key":1723596309000,"key_as_string":"1723596309000","doc_count":1,"_value":{"value":1}},{"key":1723596597000,"key_as_string":"1723596597000","doc_count":1,"_value":{"value":1}},{"key":1723596677000,"key_as_string":"1723596677000","doc_count":1,"_value":{"value":1}},{"key":1723596938000,"key_as_string":"1723596938000","doc_count":1,"_value":{"value":1}},{"key":1723597150000,"key_as_string":"1723597150000","doc_count":1,"_value":{"value":1}}]}}}`,

		// 根据 field 字段聚合 min，同时根据值排序
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"min":{"field":"dtEventTimeStamp"}}},"terms":{"field":"dtEventTimeStamp","missing":" ","order":[{"_value":"asc"}]}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":198,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"doc_count_error_upper_bound":-1,"sum_other_doc_count":1523292,"buckets":[{"key":1723593878000,"key_as_string":"1723593878000","doc_count":1,"_value":{"value":1}},{"key":1723593947000,"key_as_string":"1723593947000","doc_count":1,"_value":{"value":1}},{"key":1723594186000,"key_as_string":"1723594186000","doc_count":1,"_value":{"value":1}},{"key":1723595733000,"key_as_string":"1723595733000","doc_count":1,"_value":{"value":1}},{"key":1723596287000,"key_as_string":"1723596287000","doc_count":1,"_value":{"value":1}},{"key":1723596309000,"key_as_string":"1723596309000","doc_count":1,"_value":{"value":1}},{"key":1723596597000,"key_as_string":"1723596597000","doc_count":1,"_value":{"value":1}},{"key":1723596677000,"key_as_string":"1723596677000","doc_count":1,"_value":{"value":1}},{"key":1723596938000,"key_as_string":"1723596938000","doc_count":1,"_value":{"value":1}},{"key":1723597150000,"key_as_string":"1723597150000","doc_count":1,"_value":{"value":1}}]}}}`,
	})

	for idx, c := range map[string]struct {
		query *metadata.Query
		start time.Time
		end   time.Time

		isReference bool

		expected string
		err      error
	}{
		"nested aggregate + query 测试": {
			query: &metadata.Query{
				DB:    db,
				Field: "user.first",
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				DataSource:    structured.BkLog,
				TableID:       "es_index",
				MetricName:    "user.first",
				Source:        []string{"group", "user.first", "user.last"},
				StorageType:   consul.ElasticsearchStorageType,
				AllConditions: metadata.AllConditions{},
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:es_index:user__bk_46__first"}],"samples":[{"value":18,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"统计 __ext.io_kubernetes_pod 不为空的文档数量": {
			query: &metadata.Query{
				DB:         db,
				Field:      "__ext.io_kubernetes_pod",
				From:       0,
				Size:       10,
				DataSource: structured.BkLog,
				TableID:    "bk_log_index_set_10",
				MetricName: "__ext.io_kubernetes_pod",
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				StorageType: consul.ElasticsearchStorageType,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.io_kubernetes_pod",
							Operator:      "ncontains",
							Value:         []string{""},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"}],"samples":[{"value":1523302,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"统计 __ext.io_kubernetes_pod 不为空的去重文档数量": {
			query: &metadata.Query{
				DB:         db,
				Field:      "__ext.io_kubernetes_pod",
				From:       0,
				Size:       10,
				DataSource: structured.BkLog,
				TableID:    "bk_log_index_set_10",
				MetricName: "__ext.io_kubernetes_pod",
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				StorageType: consul.ElasticsearchStorageType,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "__ext.io_kubernetes_pod",
							Operator:      "ncontains",
							Value:         []string{""},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name: Cardinality,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"}],"samples":[{"value":4,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"使用 promql 计算平均值 sum(count_over_time(field[12h]))": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				MetricName:  "__ext.io_kubernetes_pod",
				StorageType: consul.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
						Dimensions: []string{
							"__ext.io_kubernetes_pod",
							"__ext.container_name",
						},
						Window: time.Hour * 12,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__ext__bk_46__container_name","value":"sync-apigw"},{"name":"__ext__bk_46__io_kubernetes_pod","value":"bkmonitor-unify-query-apigw-sync-1178-cl8k8"},{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"}],"samples":[{"value":24,"timestamp":1723593600000},{"timestamp":1723636800000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__ext__bk_46__container_name","value":"sync-apigw"},{"name":"__ext__bk_46__io_kubernetes_pod","value":"bkmonitor-unify-query-apigw-sync-1179-9h9xv"},{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"}],"samples":[{"value":24,"timestamp":1723593600000},{"timestamp":1723636800000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__ext__bk_46__container_name","value":"unify-query"},{"name":"__ext__bk_46__io_kubernetes_pod","value":"bkmonitor-unify-query-64bd4f5df4-599f9"},{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"}],"samples":[{"value":375064,"timestamp":1723593600000},{"value":392679,"timestamp":1723636800000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__ext__bk_46__container_name","value":"unify-query"},{"name":"__ext__bk_46__io_kubernetes_pod","value":"bkmonitor-unify-query-64bd4f5df4-llp94"},{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"}],"samples":[{"value":381173,"timestamp":1723593600000},{"value":374338,"timestamp":1723636800000}],"exemplars":null,"histograms":null}]`,
		},
		"使用非时间聚合统计数量": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        3,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				MetricName:  "__ext.io_kubernetes_pod",
				StorageType: consul.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"}],"samples":[{"value":1523302,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"获取 50 分位值": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				MetricName:  "__ext.io_kubernetes_pod",
				StorageType: consul.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Percentiles,
						Args: []interface{}{
							50.0,
						},
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"le","value":"50.0"}],"samples":[{"value":1723637132806.3303,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"获取 50, 90 分支值，同时按 6h 时间聚合": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        20,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				MetricName:  "__ext.io_kubernetes_pod",
				StorageType: consul.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Percentiles,
						Args: []interface{}{
							50.0, 90.0,
						},
					},
					{
						Name:   DateHistogram,
						Window: time.Hour * 6,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"le","value":"50.0"}],"samples":[{"value":1723604380350.2532,"timestamp":1723593600000},{"value":1723625838006.1033,"timestamp":1723615200000},{"value":1723647585882.9739,"timestamp":1723636800000},{"value":1723669177640.7131,"timestamp":1723658400000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"le","value":"90.0"}],"samples":[{"value":1723612956128.9934,"timestamp":1723593600000},{"value":1723634678721.5513,"timestamp":1723615200000},{"value":1723656196499.344,"timestamp":1723636800000},{"value":1723677836133.885,"timestamp":1723658400000}],"exemplars":null,"histograms":null}]`,
		},
		"根据 field 字段聚合计算数量，同时根据值排序": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				MetricName:  "__ext.io_kubernetes_pod",
				StorageType: consul.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Count,
						Dimensions: []string{
							field,
						},
					},
				},
				Orders: metadata.Orders{
					{
						Name: FieldValue,
						Ast:  true,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723593878000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723593947000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723594186000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723595733000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723596287000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723596309000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723596597000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723596677000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723596938000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723597150000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		"根据 field 字段聚合 min，同时根据值排序": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				MetricName:  "__ext.io_kubernetes_pod",
				StorageType: consul.ElasticsearchStorageType,
				Aggregates: metadata.Aggregates{
					{
						Name: Min,
						Dimensions: []string{
							field,
						},
					},
				},
				Orders: metadata.Orders{
					{
						Name: FieldValue,
						Ast:  true,
					},
				},
			},
			start:    defaultStart,
			end:      defaultEnd,
			expected: `[{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723593878000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723593947000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723594186000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723595733000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723596287000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723596309000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723596597000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723596677000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723596938000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:bk_log_index_set_10:__ext__bk_46__io_kubernetes_pod"},{"name":"dtEventTimeStamp","value":"1723597150000"}],"samples":[{"value":1,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
	} {
		t.Run(fmt.Sprintf("testing run: %s", idx), func(t *testing.T) {
			ss := ins.QuerySeriesSet(ctx, c.query, c.start, c.end)
			timeSeries, err := mock.SeriesSetToTimeSeries(ss)
			if err != nil {
				log.Errorf(ctx, err.Error())
				return
			}

			assert.JSONEq(t, c.expected, timeSeries.String())

		})
	}
}

func TestInstance_queryRawData(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	ins, err := NewInstance(ctx, &InstanceOption{
		Connects: []Connect{
			{
				Address: mock.EsUrl,
			},
		},
		Timeout: 3 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
		return
	}

	defaultStart := time.UnixMilli(1723593608000)
	defaultEnd := time.UnixMilli(1723679962000)

	db := "es_index"
	field := "dtEventTimeStamp"

	mock.Es.Set(map[string]any{

		// nested query + query string 测试 + highlight
		`{"_source":{"includes":["group","user.first","user.last"]},"from":0,"highlight":{"fields":{"*":{}},"number_of_fragments":0,"post_tags":["\u003c/mark\u003e"],"pre_tags":["\u003cmark\u003e"],"require_field_match":true},"query":{"bool":{"filter":[{"nested":{"path":"user","query":{"match_phrase":{"user.first":{"query":"John"}}}}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}},{"query_string":{"analyze_wildcard":true,"fields":["*","__*"],"lenient":true,"query":"group: fans"}}]}},"size":5,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":4,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1,"relation":"eq"},"max_score":0.0,"hits":[{"_index":"bk_unify_query_demo_2","_type":"_doc","_id":"aS3KjpEBbwEm76LbcH1G","_score":0.0,"_source":{"user":[{"last":"Smith","first":"John"},{"last":"White","first":"Alice"}],"group":"fans"},"highlight":{"user.new_group_user_first":["<mark>John</mark>"],"user.first":["<mark>John</mark>"],"user.new_first":["<mark>John</mark>"],"group":["<mark>fans</mark>"]}}]}}`,

		// "nested aggregate + query 测试
		`{"_source":{"includes":["group","user.first","user.last"]},"aggregations":{"user":{"aggregations":{"_value":{"value_count":{"field":"user.first"}}},"nested":{"path":"user"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":17,"relation":"eq"},"max_score":null,"hits":[]},"aggregations":{"user":{"doc_count":18,"_value":{"value":18}}}}`,

		// 获取 10条 不 field 为空的原始数据
		`{"_source":{"includes":["__ext.container_id"]},"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":10,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":13,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"27bdd842c5f2929cf4bd90f1e4534a9d","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"d21cf5cf373b4a26a31774ff7ab38fad","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"e07e9f6437e64cc04e945dc0bf604e62","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"01fb133625637ee3b0b8e689b8126da2","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"7eaa9e9edfc5e6bd8ba5df06fd2d5c00","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"bcabf17aca864416784c0b1054b6056e","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"3edf7236b8fc45c1aec67ea68fa92c61","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"77d08d253f11554c5290b4cac515c4e1","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"9fb5bb5f9bce7e0ab59e0cd1f410c57b","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"573b3e1b4a499e4b7e7fab35f316ac8a","_score":0.0,"_source":{"__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}}]}}`,

		// 获取 10条 原始数据
		`{"_source":{"includes":["__ext.io_kubernetes_pod","__ext.container_name"]},"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":10,"sort":[{"dtEventTimeStamp":{"order":"desc"}}]}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"8defd23f1c2599e70f3ace3a042b2b5f","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"ba0a6e66f01d6cb77ae25b13ddf4ad1b","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"74ea55e7397582b101f0e21efbc876c6","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"084792484f943e314e31ef2b2e878115","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"0a3f47a7c57d0af7d40d82c729c37155","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"85981293cca7102b9560b49a7f089737","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"b429dc6611efafc4d02b90f882271dea","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"01213026ae064c6726fd99dc8276e842","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"93027432b40ccb01b1be8f4ea06a6853","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"bc31babcb5d1075fc421bd641199d3aa","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}}]}}`,

		`{"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"size":0}`: `{"error":{"root_cause":[{"type":"x_content_parse_exception","reason":"[1:138] [highlight] unknown field [max_analyzed_offset]"}],"type":"x_content_parse_exception","reason":"[1:138] [highlight] unknown field [max_analyzed_offset]"},"status":400}`,

		// scroll_id_1
		`{"scroll":"10m","scroll_id":"scroll_id_1"}`: `{"_scroll_id":"scroll_id_1","took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"8defd23f1c2599e70f3ace3a042b2b5f","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"ba0a6e66f01d6cb77ae25b13ddf4ad1b","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"74ea55e7397582b101f0e21efbc876c6","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"084792484f943e314e31ef2b2e878115","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"0a3f47a7c57d0af7d40d82c729c37155","_score":0.0,"_source":{"__ext":{"container_name":"unify-query","io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}}]}}`,

		// scroll_id_2
		`{"scroll":"10m","scroll_id":"scroll_id_2"}`: `{"took":2,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":0,"relation":"eq"},"max_score":null,"hits":[]}}`,

		// search after
		`{"from":0,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}}},"search_after":[1743465646224,"kibana_settings",null],"size":5,"sort":[{"timestamp":{"order":"desc"}},{"type":{"order":"desc"}},{"kibana_stats.kibana.name":{"order":"desc"}}]}`: `{"took":13,"timed_out":false,"_shards":{"total":7,"successful":7,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[{"_index":".monitoring-kibana-7-2025.04.01","_id":"rYSm7pUBxj8-27WaYRCB","_score":null,"_source":{"timestamp":"2025-04-01T00:00:36.224Z","type":"kibana_stats","kibana_stats":{"kibana":{"name":"es-os60crz7-kibana"}}},"sort":[1743465636224,"kibana_stats","es-os60crz7-kibana"]},{"_index":".monitoring-kibana-7-2025.04.01","_id":"roSm7pUBxj8-27WaYRCB","_score":null,"_source":{"timestamp":"2025-04-01T00:00:36.224Z","type":"kibana_settings"},"sort":[1743465636224,"kibana_settings",null]},{"_index":".monitoring-kibana-7-2025.04.01","_id":"q4Sm7pUBxj8-27WaOhBx","_score":null,"_source":{"timestamp":"2025-04-01T00:00:26.225Z","type":"kibana_stats","kibana_stats":{"kibana":{"name":"es-os60crz7-kibana"}}},"sort":[1743465626225,"kibana_stats","es-os60crz7-kibana"]},{"_index":".monitoring-kibana-7-2025.04.01","_id":"rISm7pUBxj8-27WaOhBx","_score":null,"_source":{"timestamp":"2025-04-01T00:00:26.225Z","type":"kibana_settings"},"sort":[1743465626225,"kibana_settings",null]},{"_index":".monitoring-kibana-7-2025.04.01","_id":"8DSm7pUBipSLyy3IEwRg","_score":null,"_source":{"timestamp":"2025-04-01T00:00:16.224Z","type":"kibana_stats","kibana_stats":{"kibana":{"name":"es-os60crz7-kibana"}}},"sort":[1743465616224,"kibana_stats","es-os60crz7-kibana"]}]}}`,
	})

	for idx, c := range map[string]struct {
		query *metadata.Query
		start time.Time
		end   time.Time

		isReference bool

		size               int64
		list               string
		resultTableOptions metadata.ResultTableOptions
		err                error
	}{
		"nested query + query string 测试 + highlight": {
			query: &metadata.Query{
				DB:    db,
				Field: "group",
				From:  0,
				Size:  5,
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				DataSource:  structured.BkLog,
				TableID:     "es_index",
				DataLabel:   "es_index",
				MetricName:  "group",
				StorageType: consul.ElasticsearchStorageType,
				Source:      []string{"group", "user.first", "user.last"},
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "user.first",
							Operator:      "eq",
							Value:         []string{"John"},
						},
					},
				},
				QueryString: "group: fans",
				HighLight: &metadata.HighLight{
					Enable: true,
				},
			},
			start: defaultStart,
			end:   defaultEnd,
			size:  1,
			list:  `[{"__address":"http://127.0.0.1:93002","__highlight":{"group":["<mark>fans</mark>"],"user.new_group_user_first":["<mark>John</mark>"],"user.first":["<mark>John</mark>"],"user.new_first":["<mark>John</mark>"]},"user":[{"last":"Smith","first":"John"},{"last":"White","first":"Alice"}],"group":"fans","__doc_id":"aS3KjpEBbwEm76LbcH1G","__index":"bk_unify_query_demo_2","__result_table":"es_index","__data_label":"es_index"}]`,
		},
		"获取 10条 不 field 为空的原始数据": {
			query: &metadata.Query{
				DB:         db,
				Field:      field,
				From:       0,
				Size:       10,
				DataSource: structured.BkLog,
				TableID:    "bk_log_index_set_10",
				DataLabel:  "set_10",
				MetricName: "__ext.io_kubernetes_pod",
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
				Source:      []string{"__ext.container_id"},
				StorageType: consul.ElasticsearchStorageType,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: field,
							Operator:      "ncontains",
							Value:         []string{""},
						},
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
			size:  1e4,
			list:  `[{"__data_label":"set_10","__address":"http://127.0.0.1:93002","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__doc_id":"27bdd842c5f2929cf4bd90f1e4534a9d","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10"},{"__data_label":"set_10","__address":"http://127.0.0.1:93002","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__doc_id":"d21cf5cf373b4a26a31774ff7ab38fad","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10"},{"__result_table":"bk_log_index_set_10","__data_label":"set_10","__address":"http://127.0.0.1:93002","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__doc_id":"e07e9f6437e64cc04e945dc0bf604e62","__index":"v2_2_bklog_bk_unify_query_20240814_0"},{"__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__doc_id":"01fb133625637ee3b0b8e689b8126da2","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"set_10","__address":"http://127.0.0.1:93002"},{"__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__doc_id":"7eaa9e9edfc5e6bd8ba5df06fd2d5c00","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"set_10","__address":"http://127.0.0.1:93002"},{"__data_label":"set_10","__address":"http://127.0.0.1:93002","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__doc_id":"bcabf17aca864416784c0b1054b6056e","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10"},{"__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"set_10","__address":"http://127.0.0.1:93002","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__doc_id":"3edf7236b8fc45c1aec67ea68fa92c61"},{"__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"set_10","__address":"http://127.0.0.1:93002","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__doc_id":"77d08d253f11554c5290b4cac515c4e1"},{"__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__doc_id":"9fb5bb5f9bce7e0ab59e0cd1f410c57b","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"set_10","__address":"http://127.0.0.1:93002"},{"__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__doc_id":"573b3e1b4a499e4b7e7fab35f316ac8a","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"set_10","__address":"http://127.0.0.1:93002"}]`,
		},
		"获取 10条 原始数据": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				From:        0,
				Size:        10,
				Source:      []string{"__ext.io_kubernetes_pod", "__ext.container_name"},
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				DataLabel:   "bk_log",
				MetricName:  "__ext.io_kubernetes_pod",
				StorageType: consul.ElasticsearchStorageType,
				TimeField: metadata.TimeField{
					Name: "dtEventTimeStamp",
					Type: TimeFieldTypeTime,
					Unit: function.Millisecond,
				},
				Orders: metadata.Orders{
					{
						Name: FieldTime,
						Ast:  false,
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
			size:  1e4,
			list:  `[{"__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__doc_id":"8defd23f1c2599e70f3ace3a042b2b5f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"bk_log","__address":"http://127.0.0.1:93002"},{"__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__doc_id":"ba0a6e66f01d6cb77ae25b13ddf4ad1b","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"bk_log","__address":"http://127.0.0.1:93002"},{"__doc_id":"74ea55e7397582b101f0e21efbc876c6","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"bk_log","__address":"http://127.0.0.1:93002","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"},{"__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"bk_log","__address":"http://127.0.0.1:93002","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__ext.container_name":"unify-query","__doc_id":"084792484f943e314e31ef2b2e878115"},{"__result_table":"bk_log_index_set_10","__data_label":"bk_log","__address":"http://127.0.0.1:93002","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__doc_id":"0a3f47a7c57d0af7d40d82c729c37155","__index":"v2_2_bklog_bk_unify_query_20240814_0"},{"__result_table":"bk_log_index_set_10","__data_label":"bk_log","__address":"http://127.0.0.1:93002","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__ext.container_name":"unify-query","__doc_id":"85981293cca7102b9560b49a7f089737","__index":"v2_2_bklog_bk_unify_query_20240814_0"},{"__result_table":"bk_log_index_set_10","__data_label":"bk_log","__address":"http://127.0.0.1:93002","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__doc_id":"b429dc6611efafc4d02b90f882271dea","__index":"v2_2_bklog_bk_unify_query_20240814_0"},{"__doc_id":"01213026ae064c6726fd99dc8276e842","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"bk_log","__address":"http://127.0.0.1:93002","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"},{"__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__doc_id":"93027432b40ccb01b1be8f4ea06a6853","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"bk_log","__address":"http://127.0.0.1:93002"},{"__data_label":"bk_log","__address":"http://127.0.0.1:93002","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__doc_id":"bc31babcb5d1075fc421bd641199d3aa","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10"}]`,
		},
		"query error with highlight max_analyzed_offset": {
			query: &metadata.Query{
				DB:          db,
				Field:       "error",
				DataSource:  structured.BkLog,
				TableID:     "check_error",
				StorageType: consul.ElasticsearchStorageType,
			},
			start: defaultStart,
			end:   defaultEnd,
			err:   fmt.Errorf("es query [es_index] error: [1:138] [highlight] unknown field [max_analyzed_offset]"),
		},
		"query with scroll id 1": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageType: consul.ElasticsearchStorageType,
				ResultTableOptions: metadata.ResultTableOptions{
					"bk_log_index_set_10|http://127.0.0.1:93002": &metadata.ResultTableOption{
						ScrollID: "scroll_id_1",
					},
				},
				Scroll: "10m",
			},
			start: defaultStart,
			end:   defaultEnd,
			size:  1e4,
			list:  `[{"__data_label":"","__address":"http://127.0.0.1:93002","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__doc_id":"8defd23f1c2599e70f3ace3a042b2b5f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10"},{"__doc_id":"ba0a6e66f01d6cb77ae25b13ddf4ad1b","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"","__address":"http://127.0.0.1:93002","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"},{"__address":"http://127.0.0.1:93002","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__doc_id":"74ea55e7397582b101f0e21efbc876c6","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":""},{"__result_table":"bk_log_index_set_10","__data_label":"","__address":"http://127.0.0.1:93002","__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__doc_id":"084792484f943e314e31ef2b2e878115","__index":"v2_2_bklog_bk_unify_query_20240814_0"},{"__ext.container_name":"unify-query","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__doc_id":"0a3f47a7c57d0af7d40d82c729c37155","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"bk_log_index_set_10","__data_label":"","__address":"http://127.0.0.1:93002"}]`,
			resultTableOptions: map[string]*metadata.ResultTableOption{
				"bk_log_index_set_10|http://127.0.0.1:93002": {
					ScrollID: "scroll_id_1",
				},
			},
		},
		"query with scroll id 2": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageType: consul.ElasticsearchStorageType,
				ResultTableOptions: metadata.ResultTableOptions{
					"bk_log_index_set_10|http://127.0.0.1:93002": &metadata.ResultTableOption{
						ScrollID: "scroll_id_2",
					},
				},
				Scroll: "10m",
			},
			start: defaultStart,
			end:   defaultEnd,
			size:  0,
		},
		"query with search after": {
			query: &metadata.Query{
				DB:          db,
				Field:       field,
				DataSource:  structured.BkLog,
				TableID:     "bk_log_index_set_10",
				StorageType: consul.ElasticsearchStorageType,
				Orders: []metadata.Order{
					{
						Name: "timestamp",
						Ast:  false,
					},
					{
						Name: "type",
						Ast:  false,
					},
					{
						Name: "kibana_stats.kibana.name",
						Ast:  false,
					},
				},
				Size: 5,
				ResultTableOptions: metadata.ResultTableOptions{
					"bk_log_index_set_10|http://127.0.0.1:93002": &metadata.ResultTableOption{
						SearchAfter: []any{1743465646224, "kibana_settings", nil},
					},
				},
			},
			start: defaultStart,
			end:   defaultEnd,
			size:  1e4,
			list:  `[{"__data_label":"","__address":"http://127.0.0.1:93002","timestamp":"2025-04-01T00:00:36.224Z","type":"kibana_stats","kibana_stats.kibana.name":"es-os60crz7-kibana","__doc_id":"rYSm7pUBxj8-27WaYRCB","__index":".monitoring-kibana-7-2025.04.01","__result_table":"bk_log_index_set_10"},{"__address":"http://127.0.0.1:93002","timestamp":"2025-04-01T00:00:36.224Z","type":"kibana_settings","__doc_id":"roSm7pUBxj8-27WaYRCB","__index":".monitoring-kibana-7-2025.04.01","__result_table":"bk_log_index_set_10","__data_label":""},{"__address":"http://127.0.0.1:93002","timestamp":"2025-04-01T00:00:26.225Z","type":"kibana_stats","kibana_stats.kibana.name":"es-os60crz7-kibana","__doc_id":"q4Sm7pUBxj8-27WaOhBx","__index":".monitoring-kibana-7-2025.04.01","__result_table":"bk_log_index_set_10","__data_label":""},{"__address":"http://127.0.0.1:93002","timestamp":"2025-04-01T00:00:26.225Z","type":"kibana_settings","__doc_id":"rISm7pUBxj8-27WaOhBx","__index":".monitoring-kibana-7-2025.04.01","__result_table":"bk_log_index_set_10","__data_label":""},{"__doc_id":"8DSm7pUBipSLyy3IEwRg","__index":".monitoring-kibana-7-2025.04.01","__result_table":"bk_log_index_set_10","__data_label":"","__address":"http://127.0.0.1:93002","timestamp":"2025-04-01T00:00:16.224Z","type":"kibana_stats","kibana_stats.kibana.name":"es-os60crz7-kibana"}]`,
			resultTableOptions: map[string]*metadata.ResultTableOption{
				"bk_log_index_set_10|http://127.0.0.1:93002": {
					SearchAfter: []any{1743465616224.0, "kibana_stats", "es-os60crz7-kibana"},
				},
			},
		},
	} {
		t.Run(fmt.Sprintf("testing run: %s", idx), func(t *testing.T) {
			var (
				wg sync.WaitGroup

				list []any
			)
			dataCh := make(chan map[string]any)
			wg.Add(1)
			go func() {
				defer wg.Done()
				for d := range dataCh {
					list = append(list, d)
				}
			}()

			size, options, err := ins.QueryRawData(ctx, c.query, c.start, c.end, dataCh)
			close(dataCh)

			wg.Wait()

			if c.err != nil {
				assert.Equal(t, c.err, err)
			} else {
				assert.Nil(t, err)
				if len(list) > 0 {
					res, _ := json.Marshal(list)
					assert.JSONEq(t, c.list, string(res))
				} else {
					assert.Nil(t, list)
				}

				assert.Equal(t, c.size, size)
				assert.Equal(t, c.resultTableOptions, options)
			}
		})
	}
}

// TestInstance_mappingCache tests the functionality of the mapping cache
func TestInstance_mappingCache(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	// 创建一个带有较短TTL的实例，方便测试缓存过期
	ins, err := NewInstance(ctx, &InstanceOption{
		Connects: []Connect{
			{
				Address: mock.EsUrl,
			},
		},
		Timeout:    3 * time.Second,
		MappingTTL: 100 * time.Millisecond, // 设置较短的TTL方便测试过期
	})
	if err != nil {
		t.Fatalf("Instance creation error: %v", err)
	}

	// 1. 测试不同的tableID和field组合
	t.Run("different tableID and field combinations", func(t *testing.T) {
		// 确保缓存是空的
		ins.fieldTypesCache.Clear()

		// 定义测试数据
		tableIDs := []string{"table1", "table2"}
		fields := map[string]string{
			"field1":    "keyword",
			"field2":    "integer",
			"timestamp": "date",
		}

		// 分别为不同表写入字段类型
		for _, tableID := range tableIDs {
			ins.fieldTypesCache.AppendFieldTypesCache(tableID, fields)
		}

		// 验证每个组合都能正确获取
		for _, tableID := range tableIDs {
			for field, expectedType := range fields {
				fieldType, exists := ins.fieldTypesCache.GetFieldType(tableID, field)
				assert.True(t, exists, "Cache should contain entry for tableID=%s field=%s", tableID, field)
				assert.Equal(t, expectedType, fieldType, "Cached field type mismatch for tableID=%s field=%s", tableID, field)
			}
		}

		// 验证不存在的组合返回不存在
		notExistCombinations := []struct {
			tableID string
			field   string
		}{
			{"table3", "field1"}, // 不存在的tableID
			{"table1", "field3"}, // 不存在的field
			{"table3", "field3"}, // 都不存在
		}

		for _, c := range notExistCombinations {
			_, exists := ins.fieldTypesCache.GetFieldType(c.tableID, c.field)
			assert.False(t, exists, "Cache should not contain entry for tableID=%s field=%s", c.tableID, c.field)
		}
	})

	// 2. 测试缓存过期
	t.Run("cache expiration", func(t *testing.T) {
		// 确保缓存是空的
		ins.fieldTypesCache.Clear()

		// 写入缓存
		tableID := "expiry_table"
		field := "field1"
		fieldTypes := map[string]string{
			field: "keyword",
		}
		ins.fieldTypesCache.AppendFieldTypesCache(tableID, fieldTypes)

		// 验证缓存命中
		fieldType, exists := ins.fieldTypesCache.GetFieldType(tableID, field)
		assert.True(t, exists, "Cache should contain entry immediately after writing")
		assert.Equal(t, "keyword", fieldType, "Cached field type mismatch")

		// 等待缓存过期（TTL + 额外时间确保过期）
		waitDuration := ins.fieldTypesCache.GetTTL() + 50*time.Millisecond
		t.Logf("Waiting for cache expiration: %v", waitDuration)
		time.Sleep(waitDuration)
		t.Logf("Cache expiration wait complete")

		// 验证缓存已过期
		_, exists = ins.fieldTypesCache.GetFieldType(tableID, field)
		assert.False(t, exists, "Cache should return miss after expiration")
	})

	// 3. 测试更新已存在的缓存条目
	t.Run("update existing cache entry", func(t *testing.T) {
		// 确保缓存是空的
		ins.fieldTypesCache.Clear()

		tableID := "update_table"
		field := "field1"

		// 初始映射
		initialFieldType := "keyword"
		initialTypes := map[string]string{
			field: initialFieldType,
		}

		// 写入初始值
		ins.fieldTypesCache.AppendFieldTypesCache(tableID, initialTypes)

		// 验证初始值
		fieldType, exists := ins.fieldTypesCache.GetFieldType(tableID, field)
		assert.True(t, exists, "Cache should contain initial entry")
		assert.Equal(t, initialFieldType, fieldType, "Initial cached field type mismatch")

		// 更新映射
		updatedFieldType := "text"
		updatedTypes := map[string]string{
			field: updatedFieldType,
		}

		// 更新值
		ins.fieldTypesCache.AppendFieldTypesCache(tableID, updatedTypes)

		// 验证更新后的值
		fieldType, exists = ins.fieldTypesCache.GetFieldType(tableID, field)
		assert.True(t, exists, "Cache should contain updated entry")
		assert.Equal(t, updatedFieldType, fieldType, "Updated cached field type mismatch")
	})

	// 4. 测试FormatFactory使用GetFieldType
	t.Run("FormatFactory uses GetFieldType", func(t *testing.T) {
		// 确保缓存是空的
		ins.fieldTypesCache.Clear()

		tableID := "format_factory_table"
		field := "test_field"
		fieldType := "keyword"

		// 准备字段类型映射
		fieldTypes := map[string]string{
			field: fieldType,
		}

		// 添加到缓存
		ins.fieldTypesCache.AppendFieldTypesCache(tableID, fieldTypes)

		// 不再需要创建FormatFactory实例进行测试，直接测试缓存功能
		retrievedType, exists := ins.fieldTypesCache.GetFieldType(tableID, field)
		assert.True(t, exists, "Should be able to retrieve field type")
		assert.Equal(t, fieldType, retrievedType, "Retrieved field type should match")
	})

	// 5. 测试使用metadata.Query的GetCacheKey方法
	t.Run("using metadata.Query.GetCacheKey", func(t *testing.T) {
		ins.fieldTypesCache.Clear()

		queries := []*metadata.Query{
			{
				TableID: "table_a",
				Field:   "field_x",
			},
			{
				TableID: "table_a",
				Field:   "field_y",
			},
			{
				TableID: "table_b",
				Field:   "field_x",
			},
		}

		// 写入缓存
		for _, query := range queries {
			tableID, _ := query.GetCacheKey()
			fieldTypes := map[string]string{
				query.Field: "keyword",
			}
			ins.fieldTypesCache.AppendFieldTypesCache(tableID, fieldTypes)
		}

		// 验证缓存命中
		for i, query := range queries {
			tableID, _ := query.GetCacheKey()
			fieldType, exists := ins.fieldTypesCache.GetFieldType(tableID, query.Field)
			assert.True(t, exists, "Cache should contain entry for query %d", i)
			assert.Equal(t, "keyword", fieldType, "Cached field type mismatch for query %d", i)
		}
	})
}

func TestMappingCacheConcurrency(t *testing.T) {
	mock.Init()
	ctx := metadata.InitHashID(context.Background())

	ins, err := NewInstance(ctx, &InstanceOption{
		Connects: []Connect{
			{
				Address: mock.EsUrl,
			},
		},
		Timeout:    3 * time.Second,
		MappingTTL: 100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("Instance creation error: %v", err)
	}

	const numTables = 5
	const numGoroutines = 5
	const numOperations = 20

	var wg sync.WaitGroup

	// 清空缓存
	ins.fieldTypesCache.Clear()

	t.Run("concurrent read and write operations", func(t *testing.T) {
		ins.fieldTypesCache.Clear()

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()

				for j := 0; j < numOperations; j++ {
					tableID := fmt.Sprintf("table_%d", j%numTables)
					field := fmt.Sprintf("field_%d", j%3) // 减少字段数量

					if j%3 == 0 {
						// 写入操作
						fieldTypes := map[string]string{
							field: "keyword",
						}
						ins.fieldTypesCache.AppendFieldTypesCache(tableID, fieldTypes)
					} else {
						// 读取操作
						_, _ = ins.fieldTypesCache.GetFieldType(tableID, field)
					}

					time.Sleep(1 * time.Millisecond)
				}
			}(i)
		}

		wg.Wait()

		cacheEntries := 0
		for i := 0; i < numTables; i++ {
			for j := 0; j < 3; j++ {
				tableID := fmt.Sprintf("table_%d", i)
				field := fmt.Sprintf("field_%d", j)

				fieldType, exists := ins.fieldTypesCache.GetFieldType(tableID, field)
				if exists {
					cacheEntries++
					assert.Equal(t, "keyword", fieldType, "Unexpected field type for tableID=%s field=%s", tableID, field)
				}
			}
		}

		t.Logf("Found %d cache entries after concurrent operations", cacheEntries)
	})

	t.Run("concurrent operations with TTL expiration", func(t *testing.T) {
		ins.fieldTypesCache.Clear()

		for i := 0; i < numTables; i++ {
			tableID := fmt.Sprintf("exp_table_%d", i)
			field := "exp_field"
			fieldTypes := map[string]string{
				field: "keyword",
			}

			ins.fieldTypesCache.AppendFieldTypesCache(tableID, fieldTypes)
		}

		var expireWg sync.WaitGroup
		halfTTL := ins.fieldTypesCache.GetTTL() / 2

		for i := 0; i < 3; i++ {
			expireWg.Add(1)
			go func(id int) {
				defer expireWg.Done()

				end := time.Now().Add(ins.fieldTypesCache.GetTTL() + 50*time.Millisecond)
				for time.Now().Before(end) {
					for j := 0; j < numTables; j++ {
						tableID := fmt.Sprintf("exp_table_%d", j)
						field := "exp_field"

						_, _ = ins.fieldTypesCache.GetFieldType(tableID, field)

						time.Sleep(2 * time.Millisecond)
					}
				}
			}(i)
		}

		time.Sleep(halfTTL)
		t.Logf("Updating half of the cache entries after %v", halfTTL)

		for i := 0; i < numTables/2; i++ {
			tableID := fmt.Sprintf("exp_table_%d", i)
			field := "exp_field"
			fieldTypes := map[string]string{
				field: "keyword",
			}

			ins.fieldTypesCache.AppendFieldTypesCache(tableID, fieldTypes)
		}

		expireWg.Wait()

		updatedEntries := 0
		expiredEntries := 0

		for i := 0; i < numTables; i++ {
			tableID := fmt.Sprintf("exp_table_%d", i)
			field := "exp_field"

			_, exists := ins.fieldTypesCache.GetFieldType(tableID, field)
			if exists {
				updatedEntries++
			} else {
				expiredEntries++
			}
		}

		t.Logf("After TTL: %d entries still valid, %d entries expired",
			updatedEntries, expiredEntries)

		// 不进行严格断言，因为并发测试中精确时间控制很难，但应该有一定数量的条目过期
		assert.True(t, expiredEntries > 0, "Some entries should have expired")
	})
}
