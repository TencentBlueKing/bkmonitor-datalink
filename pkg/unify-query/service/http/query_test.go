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
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/prometheus/promql/parser"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/featureFlag"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/promql"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestQueryTsWithEs(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	spaceUid := influxdb.SpaceUid
	tableID := influxdb.ResultTableEs

	mock.Init()
	promql.MockEngine()

	defaultStart := time.UnixMilli(1717027200000)
	defaultEnd := time.UnixMilli(1717027500000)

	for i, c := range map[string]struct {
		queryTs *structured.QueryTs
		result  string
	}{
		"查询 10 条原始数据，按照字段正向排序": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						Limit:         10,
						From:          0,
						ReferenceName: "a",
					},
				},
				OrderBy: structured.OrderBy{
					"_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     false,
				SpaceUid:    spaceUid,
			},
		},
		"根据维度 __ext.container_name 进行 count 聚合，同时用值正向排序": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						Limit:         5,
						From:          0,
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function: "count_over_time",
							Window:   "30s",
						},
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "sum",
								Dimensions: []string{"__ext.container_name"},
							},
							{
								Method: "topk",
								VArgsList: []interface{}{
									5,
								},
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"gseIndex",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     false,
				SpaceUid:    spaceUid,
				Step:        "30s",
			},
		},
	} {
		t.Run(fmt.Sprintf("%s", i), func(t *testing.T) {
			metadata.SetUser(ctx, "username:test", spaceUid, "true")

			res, err := queryTsWithPromEngine(ctx, c.queryTs)
			if err != nil {
				log.Errorf(ctx, err.Error())
				return
			}
			data := res.(*PromData)
			if data.Status != nil && data.Status.Code != "" {
				fmt.Println("code: ", data.Status.Code)
				fmt.Println("message: ", data.Status.Message)
				return
			}

			log.Infof(ctx, fmt.Sprintf("%+v", data.Tables))
		})
	}
}

func TestQueryReference(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	spaceUid := influxdb.SpaceUid
	tableID := influxdb.ResultTableEs

	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(ctx)

	mock.Init()
	defaultStart := time.UnixMilli(1741154079123)
	defaultEnd := time.UnixMilli(1741155879987)

	mock.Es.Set(map[string]any{
		`{"aggregations":{"_value":{"value_count":{"field":"gseIndex"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1741154079123,"include_lower":true,"include_upper":true,"to":1741155879987}}}}},"size":0}`:                                                                                                                                                                                                  `{"took":626,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":182355}}}`,
		`{"aggregations":{"_value":{"value_count":{"field":"gseIndex"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:                                                                                                                                                                                                        `{"took":171,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":182486}}}`,
		`{"aggregations":{"__ext.container_name":{"aggregations":{"_value":{"value_count":{"field":"gseIndex"}}},"terms":{"field":"__ext.container_name","missing":" ","order":[{"_value":"asc"}],"size":5}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1741154079123,"include_lower":true,"include_upper":true,"to":1741155879987}}}}},"size":0}`:                                                             `{"took":860,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"__ext.container_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"unify-query","doc_count":182355,"_value":{"value":182355}}]}}}`,
		`{"aggregations":{"__ext.container_name":{"aggregations":{"_value":{"value_count":{"field":"gseIndex"}}},"terms":{"field":"__ext.container_name","missing":" ","order":[{"_value":"desc"}],"size":5}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:                                                                  `{"took":885,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"__ext.container_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"unify-query","doc_count":182486,"_value":{"value":182486}}]}}}`,
		`{"aggregations":{"_value":{"value_count":{"field":"__ext.container_name"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:                                                                                                                                                                                            `{"took":283,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":182486}}}`,
		`{"aggregations":{"_value":{"value_count":{"field":"__ext.io_kubernetes_pod"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:                                                                                                                                                                                         `{"took":167,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":182486}}}`,
		`{"aggregations":{"_value":{"cardinality":{"field":"__ext.io_kubernetes_pod"}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:                                                                                                                                                                                         `{"took":1595,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"_value":{"value":4}}}`,
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"value_count":{"field":"__ext.io_kubernetes_pod"}}},"date_histogram":{"extended_bounds":{"max":1741155879000,"min":1741154079000},"field":"dtEventTimeStamp","interval":"1m","min_doc_count":0}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1741154079,"include_lower":true,"include_upper":true,"to":1741155879}}}}},"size":0}`:       `{"took":529,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"buckets":[{"key_as_string":"1741154040000","key":1741154040000,"doc_count":3408,"_value":{"value":3408}},{"key_as_string":"1741154100000","key":1741154100000,"doc_count":4444,"_value":{"value":4444}},{"key_as_string":"1741154160000","key":1741154160000,"doc_count":4577,"_value":{"value":4577}},{"key_as_string":"1741154220000","key":1741154220000,"doc_count":4668,"_value":{"value":4668}},{"key_as_string":"1741154280000","key":1741154280000,"doc_count":5642,"_value":{"value":5642}},{"key_as_string":"1741154340000","key":1741154340000,"doc_count":4860,"_value":{"value":4860}},{"key_as_string":"1741154400000","key":1741154400000,"doc_count":35988,"_value":{"value":35988}},{"key_as_string":"1741154460000","key":1741154460000,"doc_count":7098,"_value":{"value":7098}},{"key_as_string":"1741154520000","key":1741154520000,"doc_count":5287,"_value":{"value":5287}},{"key_as_string":"1741154580000","key":1741154580000,"doc_count":5422,"_value":{"value":5422}},{"key_as_string":"1741154640000","key":1741154640000,"doc_count":4906,"_value":{"value":4906}},{"key_as_string":"1741154700000","key":1741154700000,"doc_count":4447,"_value":{"value":4447}},{"key_as_string":"1741154760000","key":1741154760000,"doc_count":4713,"_value":{"value":4713}},{"key_as_string":"1741154820000","key":1741154820000,"doc_count":4621,"_value":{"value":4621}},{"key_as_string":"1741154880000","key":1741154880000,"doc_count":4417,"_value":{"value":4417}},{"key_as_string":"1741154940000","key":1741154940000,"doc_count":5092,"_value":{"value":5092}},{"key_as_string":"1741155000000","key":1741155000000,"doc_count":4805,"_value":{"value":4805}},{"key_as_string":"1741155060000","key":1741155060000,"doc_count":5545,"_value":{"value":5545}},{"key_as_string":"1741155120000","key":1741155120000,"doc_count":4614,"_value":{"value":4614}},{"key_as_string":"1741155180000","key":1741155180000,"doc_count":5121,"_value":{"value":5121}},{"key_as_string":"1741155240000","key":1741155240000,"doc_count":4854,"_value":{"value":4854}},{"key_as_string":"1741155300000","key":1741155300000,"doc_count":5343,"_value":{"value":5343}},{"key_as_string":"1741155360000","key":1741155360000,"doc_count":4789,"_value":{"value":4789}},{"key_as_string":"1741155420000","key":1741155420000,"doc_count":4755,"_value":{"value":4755}},{"key_as_string":"1741155480000","key":1741155480000,"doc_count":5115,"_value":{"value":5115}},{"key_as_string":"1741155540000","key":1741155540000,"doc_count":4588,"_value":{"value":4588}},{"key_as_string":"1741155600000","key":1741155600000,"doc_count":6474,"_value":{"value":6474}},{"key_as_string":"1741155660000","key":1741155660000,"doc_count":5416,"_value":{"value":5416}},{"key_as_string":"1741155720000","key":1741155720000,"doc_count":5128,"_value":{"value":5128}},{"key_as_string":"1741155780000","key":1741155780000,"doc_count":5050,"_value":{"value":5050}},{"key_as_string":"1741155840000","key":1741155840000,"doc_count":1299,"_value":{"value":1299}}]}}}`,
		`{"aggregations":{"dtEventTimeStamp":{"aggregations":{"_value":{"value_count":{"field":"__ext.io_kubernetes_pod"}}},"date_histogram":{"extended_bounds":{"max":1741155879987,"min":1741154079123},"field":"dtEventTimeStamp","interval":"1m","min_doc_count":0}}},"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1741154079123,"include_lower":true,"include_upper":true,"to":1741155879987}}}}},"size":0}`: `{"took":759,"timed_out":false,"_shards":{"total":2,"successful":2,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"dtEventTimeStamp":{"buckets":[{"key_as_string":"1741154040000","key":1741154040000,"doc_count":3277,"_value":{"value":3277}},{"key_as_string":"1741154100000","key":1741154100000,"doc_count":4444,"_value":{"value":4444}},{"key_as_string":"1741154160000","key":1741154160000,"doc_count":4577,"_value":{"value":4577}},{"key_as_string":"1741154220000","key":1741154220000,"doc_count":4668,"_value":{"value":4668}},{"key_as_string":"1741154280000","key":1741154280000,"doc_count":5642,"_value":{"value":5642}},{"key_as_string":"1741154340000","key":1741154340000,"doc_count":4860,"_value":{"value":4860}},{"key_as_string":"1741154400000","key":1741154400000,"doc_count":35988,"_value":{"value":35988}},{"key_as_string":"1741154460000","key":1741154460000,"doc_count":7098,"_value":{"value":7098}},{"key_as_string":"1741154520000","key":1741154520000,"doc_count":5287,"_value":{"value":5287}},{"key_as_string":"1741154580000","key":1741154580000,"doc_count":5422,"_value":{"value":5422}},{"key_as_string":"1741154640000","key":1741154640000,"doc_count":4906,"_value":{"value":4906}},{"key_as_string":"1741154700000","key":1741154700000,"doc_count":4447,"_value":{"value":4447}},{"key_as_string":"1741154760000","key":1741154760000,"doc_count":4713,"_value":{"value":4713}},{"key_as_string":"1741154820000","key":1741154820000,"doc_count":4621,"_value":{"value":4621}},{"key_as_string":"1741154880000","key":1741154880000,"doc_count":4417,"_value":{"value":4417}},{"key_as_string":"1741154940000","key":1741154940000,"doc_count":5092,"_value":{"value":5092}},{"key_as_string":"1741155000000","key":1741155000000,"doc_count":4805,"_value":{"value":4805}},{"key_as_string":"1741155060000","key":1741155060000,"doc_count":5545,"_value":{"value":5545}},{"key_as_string":"1741155120000","key":1741155120000,"doc_count":4614,"_value":{"value":4614}},{"key_as_string":"1741155180000","key":1741155180000,"doc_count":5121,"_value":{"value":5121}},{"key_as_string":"1741155240000","key":1741155240000,"doc_count":4854,"_value":{"value":4854}},{"key_as_string":"1741155300000","key":1741155300000,"doc_count":5343,"_value":{"value":5343}},{"key_as_string":"1741155360000","key":1741155360000,"doc_count":4789,"_value":{"value":4789}},{"key_as_string":"1741155420000","key":1741155420000,"doc_count":4755,"_value":{"value":4755}},{"key_as_string":"1741155480000","key":1741155480000,"doc_count":5115,"_value":{"value":5115}},{"key_as_string":"1741155540000","key":1741155540000,"doc_count":4588,"_value":{"value":4588}},{"key_as_string":"1741155600000","key":1741155600000,"doc_count":6474,"_value":{"value":6474}},{"key_as_string":"1741155660000","key":1741155660000,"doc_count":5416,"_value":{"value":5416}},{"key_as_string":"1741155720000","key":1741155720000,"doc_count":5128,"_value":{"value":5128}},{"key_as_string":"1741155780000","key":1741155780000,"doc_count":5050,"_value":{"value":5050}},{"key_as_string":"1741155840000","key":1741155840000,"doc_count":1299,"_value":{"value":1299}}]}}}`,
	})

	for i, c := range map[string]struct {
		queryTs *structured.QueryTs
		result  string
	}{
		"统计数量，毫秒查询": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.UnixMilli(), 10),
				End:         strconv.FormatInt(defaultEnd.UnixMilli(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079123,182355]]}]`,
		},
		"统计数量": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079000,182486]]}]`,
		},
		"根据维度 __ext.container_name 进行 sum 聚合，同时用值正向排序": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						Limit:         5,
						From:          0,
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "count",
								Dimensions: []string{"__ext.container_name"},
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.UnixMilli(), 10),
				End:         strconv.FormatInt(defaultEnd.UnixMilli(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__ext.container_name"],"group_values":["unify-query"],"values":[[1741154079123,182355]]}]`,
		},
		"根据维度 __ext.container_name 进行 count 聚合，同时用值倒序": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "gseIndex",
						Limit:         5,
						From:          0,
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method:     "count",
								Dimensions: []string{"__ext.container_name"},
							},
						},
					},
				},
				OrderBy: structured.OrderBy{
					"-_value",
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["__ext.container_name"],"group_values":["unify-query"],"values":[[1741154079000,182486]]}]`,
		},
		"统计 __ext.container_name 和 __ext.io_kubernetes_pod 不为空的文档数量": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.container_name",
						ReferenceName: "a",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "__ext.io_kubernetes_pod",
									Operator:      "ncontains",
									Value:         []string{""},
								},
								{
									DimensionName: "__ext.container_name",
									Operator:      "ncontains",
									Value:         []string{""},
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079000,182486]]}]`,
		},
		"a + b": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.io_kubernetes_pod",
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
						},
					},
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.io_kubernetes_pod",
						ReferenceName: "b",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
						},
					},
				},
				MetricMerge: "a + b",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079000,364972]]}]`,
		},
		"__ext.io_kubernetes_pod 统计去重数量": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.io_kubernetes_pod",
						ReferenceName: "a",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "cardinality",
							},
						},
					},
				},
				MetricMerge: "a",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     true,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079000,4]]}]`,
		},
		"__ext.io_kubernetes_pod 统计数量": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.io_kubernetes_pod",
						ReferenceName: "b",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
							{
								Method: "date_histogram",
								Window: "1m",
							},
						},
					},
				},
				MetricMerge: "b",
				Start:       strconv.FormatInt(defaultStart.Unix(), 10),
				End:         strconv.FormatInt(defaultEnd.Unix(), 10),
				Instant:     false,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079000,3408],[1741154139000,4444],[1741154199000,4577],[1741154259000,4668],[1741154319000,5642],[1741154379000,4860],[1741154439000,35988],[1741154499000,7098],[1741154559000,5287],[1741154619000,5422],[1741154679000,4906],[1741154739000,4447],[1741154799000,4713],[1741154859000,4621],[1741154919000,4417],[1741154979000,5092],[1741155039000,4805],[1741155099000,5545],[1741155159000,4614],[1741155219000,5121],[1741155279000,4854],[1741155339000,5343],[1741155399000,4789],[1741155459000,4755],[1741155519000,5115],[1741155579000,4588],[1741155639000,6474],[1741155699000,5416],[1741155759000,5128],[1741155819000,5050],[1741155879000,1299]]}]`,
		},
		"__ext.io_kubernetes_pod 统计数量，毫秒": {
			queryTs: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    structured.BkLog,
						TableID:       structured.TableID(tableID),
						FieldName:     "__ext.io_kubernetes_pod",
						ReferenceName: "b",
						AggregateMethodList: structured.AggregateMethodList{
							{
								Method: "count",
							},
							{
								Method: "date_histogram",
								Window: "1m",
							},
						},
					},
				},
				MetricMerge: "b",
				Start:       strconv.FormatInt(defaultStart.UnixMilli(), 10),
				End:         strconv.FormatInt(defaultEnd.UnixMilli(), 10),
				Instant:     false,
				SpaceUid:    spaceUid,
			},
			result: `[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1741154079123,3277],[1741154139123,4444],[1741154199123,4577],[1741154259123,4668],[1741154319123,5642],[1741154379123,4860],[1741154439123,35988],[1741154499123,7098],[1741154559123,5287],[1741154619123,5422],[1741154679123,4906],[1741154739123,4447],[1741154799123,4713],[1741154859123,4621],[1741154919123,4417],[1741154979123,5092],[1741155039123,4805],[1741155099123,5545],[1741155159123,4614],[1741155219123,5121],[1741155279123,4854],[1741155339123,5343],[1741155399123,4789],[1741155459123,4755],[1741155519123,5115],[1741155579123,4588],[1741155639123,6474],[1741155699123,5416],[1741155759123,5128],[1741155819123,5050],[1741155879123,1299]]}]`,
		},
	} {
		t.Run(fmt.Sprintf("%s", i), func(t *testing.T) {
			metadata.SetUser(ctx, "username:test", spaceUid, "true")

			data, err := queryReferenceWithPromEngine(ctx, c.queryTs)
			assert.Nil(t, err)

			if err != nil {
				return
			}

			if data.Status != nil && data.Status.Code != "" {
				fmt.Println("code: ", data.Status.Code)
				fmt.Println("message: ", data.Status.Message)
				return
			}

			actual, _ := json.Marshal(data.Tables)
			assert.Equal(t, c.result, string(actual))
		})
	}
}

func TestQueryTs(t *testing.T) {

	ctx := metadata.InitHashID(context.Background())
	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	mock.InfluxDB.Set(map[string]any{
		`SELECT mean("usage") AS _value, "time" AS _time FROM cpu_summary WHERE time > 1677081540000000000 and time < 1677085659999000000 AND (bk_biz_id='2') GROUP BY time(1m0s) LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{},
							Columns: []string{
								influxdb.TimeColumnName,
								influxdb.ResultColumnName,
							},
							Values: [][]any{
								{
									1677081600000000000, 30,
								},
								{
									1677081660000000000, 21,
								},
								{
									1677081720000000000, 1,
								},
								{
									1677081780000000000, 7,
								},
								{
									1677081840000000000, 4,
								},
								{
									1677081900000000000, 2,
								},
								{
									1677081960000000000, 100,
								},
								{
									1677082020000000000, 94,
								},
								{
									1677082080000000000, 34,
								},
							},
						},
					},
				},
			},
		},
		`SELECT "usage" AS _value, *::tag, "time" AS _time FROM cpu_summary WHERE time > 1677081359999000000 and time < 1677085659999000000 AND ((notice_way='weixin' and status='failed') and bk_biz_id='2') LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{},
							Columns: []string{
								influxdb.ResultColumnName,
								"job",
								"notice_way",
								"status",
								influxdb.TimeColumnName,
							},
							Values: [][]any{
								{
									30,
									"SLI",
									"weixin",
									"failed",
									1677081600000000000,
								},
								{
									21,
									"SLI",
									"weixin",
									"failed",
									1677081660000000000,
								},
								{
									1,
									"SLI",
									"weixin",
									"failed",
									1677081720000000000,
								},
								{
									7,
									"SLI",
									"weixin",
									"failed",
									1677081780000000000,
								},
								{
									4,
									"SLI",
									"weixin",
									"failed",
									1677081840000000000,
								},
								{
									2,
									"SLI",
									"weixin",
									"failed",
									1677081900000000000,
								},
								{
									100,
									"SLI",
									"weixin",
									"failed",
									1677081960000000000,
								},
								{
									94,
									"SLI",
									"weixin",
									"failed",
									1677082020000000000,
								},
								{
									34,
									"SLI",
									"weixin",
									"failed",
									1677082080000000000,
								},
							},
						},
					},
				},
			},
		},
		`SELECT count("usage") AS _value, "time" AS _time FROM cpu_summary WHERE time > 1677081540000000000 and time < 1677085659999000000 AND (bk_biz_id='2') GROUP BY "status", time(1m0s) LIMIT 100000005 SLIMIT 100005 TZ('UTC')`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{
								"status": "failed",
							},
							Columns: []string{
								influxdb.TimeColumnName,
								influxdb.ResultColumnName,
							},
							Values: [][]any{
								{
									1677081600000000000, 30,
								},
								{
									1677081660000000000, 21,
								},
								{
									1677081720000000000, 1,
								},
								{
									1677081780000000000, 7,
								},
								{
									1677081840000000000, 4,
								},
								{
									1677081900000000000, 2,
								},
								{
									1677081960000000000, 100,
								},
								{
									1677082020000000000, 94,
								},
								{
									1677082080000000000, 34,
								},
							},
						},
					},
				},
			},
		},
	})

	testCases := map[string]struct {
		query  string
		result string
	}{
		"test query": {
			query:  `{"query_list":[{"data_source":"","table_id":"system.cpu_summary","field_name":"usage","field_list":null,"function":[{"method":"mean","without":false,"dimensions":[],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"avg_over_time","window":"60s","position":0,"vargs_list":null},"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":[],"group_values":[],"values":[[1677081600000,30],[1677081660000,21],[1677081720000,1],[1677081780000,7],[1677081840000,4],[1677081900000,2],[1677081960000,100],[1677082020000,94],[1677082080000,34]]}]}`,
		},
		"test lost sample in increase": {
			query:  `{"query_list":[{"data_source":"bkmonitor","table_id":"system.cpu_summary","field_name":"usage","field_list":null,"function":null,"time_aggregation":{"function":"increase","window":"5m0s","position":0,"vargs_list":null},"reference_name":"a","dimensions":null,"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"notice_way","value":["weixin"],"op":"eq"},{"field_name":"status","value":["failed"],"op":"eq"}],"condition_list":["and"]},"keep_columns":null}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["job","notice_way","status"],"group_values":["SLI","weixin","failed"],"values":[[1677081660000,52.499649999999995],[1677081720000,38.49981666666667],[1677081780000,46.66666666666667],[1677081840000,40],[1677081900000,16.25],[1677081960000,137.5],[1677082020000,247.5],[1677082080000,285],[1677082140000,263.6679222222223],[1677082200000,160.00106666666667],[1677082260000,51.00056666666667]]}]}`,
		},
		"test query support fuzzy __name__ with count": {
			query:  `{"query_list":[{"data_source":"","table_id":"system.cpu_summary","field_name":".*","is_regexp":true,"field_list":null,"function":[{"method":"sum","without":false,"dimensions":["status"],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0,"vargs_list":null},"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[],"condition_list":[]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"60s"}`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["status"],"group_values":["failed"],"values":[[1677081600000,30],[1677081660000,21],[1677081720000,1],[1677081780000,7],[1677081840000,4],[1677081900000,2],[1677081960000,100],[1677082020000,94],[1677082080000,34]]}]}`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			metadata.SetUser(ctx, "", influxdb.SpaceUid, "")

			body := []byte(c.query)
			query := &structured.QueryTs{}
			err := json.Unmarshal(body, query)
			assert.Nil(t, err)

			res, err := queryTsWithPromEngine(ctx, query)
			assert.Nil(t, err)
			out, err := json.Marshal(res)
			assert.Nil(t, err)
			actual := string(out)
			fmt.Printf("ActualResult: %v\n", actual)
			assert.Equal(t, c.result, actual)
		})
	}
}

func TestQueryRawWithInstance(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	spaceUid := influxdb.SpaceUid
	tableID := influxdb.ResultTableBkBaseEs

	mock.Init()
	influxdb.MockSpaceRouter(ctx)
	promql.MockEngine()

	start := "1723594000"
	end := "1723595000"

	mock.Es.Set(map[string]any{
		`{"_source":{"includes":["__ext.container_id","dtEventTimeStamp"]},"from":1,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10}`:       `{"took":301,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c726c895a380ba1a9df04ba4a977b29b","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"fa209967d4a8c5d21b3e4f67d2cd579e","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"dc888e9a3789976aa11483626fc61a4f","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c2dae031f095fa4b9deccf81964c7837","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"8a916e558c71d4226f1d7f3279cf0fdd","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"f6950fef394e813999d7316cdbf0de4d","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"328d487e284703b1d0bb8017dba46124","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"cb790ecb36bbaf02f6f0eb80ac2fd65c","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"bd8a8ef60e94ade63c55c8773170d458","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c8401bb4ec021b038cb374593b8adce3","_score":0.0,"_source":{"dtEventTimeStamp":"1723594161000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}}]}}`,
		`{"_source":{"includes":["__ext.io_kubernetes_pod","dtEventTimeStamp"]},"from":20,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":10}`: `{"took":468,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"e058129ae18bff87c95e83f24584e654","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c124dae69af9b86a7128ee4281820158","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c7f73abf7e865a4b4d7fc608387d01cf","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"39c3ec662881e44bf26d2a6bfc0e35c3","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"58e03ce0b9754bf0657d49a5513adcb5","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"43a36f412886bf30b0746562513638d3","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"218ceafd04f89b39cda7954e51f4a48a","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"8d9abe9b782fe3a1272c93f0af6b39e1","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"0826407be7f04f19086774ed68eac8dd","_score":0.0,"_source":{"dtEventTimeStamp":"1723594224000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"d56b4120194eb37f53410780da777d43","_score":0.0,"_source":{"dtEventTimeStamp":"1723594224000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94"}}}]}}`,
		`{"_source":{"includes":["__ext.container_id","dtEventTimeStamp"]},"from":1,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723594000,"include_lower":true,"include_upper":true,"to":1723595000}}}}},"size":1}`:        `{"took":17,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"4f3a5e9c167097c9658e88b2f32364b2","_score":0.0,"_source":{"dtEventTimeStamp":"1723594209000","__ext":{"container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f"}}}]}}`,
		`{"_source":{"includes":["__ext.container_id","dtEventTimeStamp"]},"from":1,"query":{"bool":{"filter":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":1723594000123,"include_lower":true,"include_upper":true,"to":1723595000234}}}}},"size":10}`: `{"took":468,"timed_out":false,"_shards":{"total":3,"successful":3,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":0.0,"hits":[{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"e058129ae18bff87c95e83f24584e654","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c124dae69af9b86a7128ee4281820158","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"c7f73abf7e865a4b4d7fc608387d01cf","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"39c3ec662881e44bf26d2a6bfc0e35c3","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"58e03ce0b9754bf0657d49a5513adcb5","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"43a36f412886bf30b0746562513638d3","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"218ceafd04f89b39cda7954e51f4a48a","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"8d9abe9b782fe3a1272c93f0af6b39e1","_score":0.0,"_source":{"dtEventTimeStamp":"1723594211000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"0826407be7f04f19086774ed68eac8dd","_score":0.0,"_source":{"dtEventTimeStamp":"1723594224000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94"}}},{"_index":"v2_2_bklog_bk_unify_query_20240814_0","_type":"_doc","_id":"d56b4120194eb37f53410780da777d43","_score":0.0,"_source":{"dtEventTimeStamp":"1723594224000","__ext":{"io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94"}}}]}}`,
	})

	tcs := map[string]struct {
		queryTs  *structured.QueryTs
		total    int64
		expected string
	}{
		"query with EpochMillis": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(tableID),
						From:        1,
						Limit:       10,
						KeepColumns: []string{"__ext.container_id", "dtEventTimeStamp"},
					},
				},
				Start: "1723594000123",
				End:   "1723595000234",
			},
			total:    1e4,
			expected: `[{"__data_label":"bkbase_es","__doc_id":"0826407be7f04f19086774ed68eac8dd","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594224000","dtEventTimeStamp":"1723594224000"},{"__data_label":"bkbase_es","__doc_id":"218ceafd04f89b39cda7954e51f4a48a","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"39c3ec662881e44bf26d2a6bfc0e35c3","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"43a36f412886bf30b0746562513638d3","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"58e03ce0b9754bf0657d49a5513adcb5","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"8d9abe9b782fe3a1272c93f0af6b39e1","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"c124dae69af9b86a7128ee4281820158","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"c7f73abf7e865a4b4d7fc608387d01cf","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"d56b4120194eb37f53410780da777d43","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594224000","dtEventTimeStamp":"1723594224000"},{"__data_label":"bkbase_es","__doc_id":"e058129ae18bff87c95e83f24584e654","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"}]`,
		},
		"query_bk_base_es_with_raw": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(tableID),
						From:        1,
						Limit:       10,
						KeepColumns: []string{"__ext.container_id", "dtEventTimeStamp"},
					},
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(tableID),
						From:        20,
						Limit:       10,
						KeepColumns: []string{"__ext.io_kubernetes_pod", "dtEventTimeStamp"},
					},
				},
				Start: start,
				End:   end,
			},
			total:    2e4,
			expected: `[{"__data_label":"bkbase_es","__doc_id":"0826407be7f04f19086774ed68eac8dd","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594224000","dtEventTimeStamp":"1723594224000"},{"__data_label":"bkbase_es","__doc_id":"218ceafd04f89b39cda7954e51f4a48a","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"328d487e284703b1d0bb8017dba46124","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"},{"__data_label":"bkbase_es","__doc_id":"39c3ec662881e44bf26d2a6bfc0e35c3","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"43a36f412886bf30b0746562513638d3","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"58e03ce0b9754bf0657d49a5513adcb5","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"8a916e558c71d4226f1d7f3279cf0fdd","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"},{"__data_label":"bkbase_es","__doc_id":"8d9abe9b782fe3a1272c93f0af6b39e1","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"bd8a8ef60e94ade63c55c8773170d458","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"},{"__data_label":"bkbase_es","__doc_id":"c124dae69af9b86a7128ee4281820158","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"c2dae031f095fa4b9deccf81964c7837","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"},{"__data_label":"bkbase_es","__doc_id":"c726c895a380ba1a9df04ba4a977b29b","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"},{"__data_label":"bkbase_es","__doc_id":"c7f73abf7e865a4b4d7fc608387d01cf","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"c8401bb4ec021b038cb374593b8adce3","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"},{"__data_label":"bkbase_es","__doc_id":"cb790ecb36bbaf02f6f0eb80ac2fd65c","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"},{"__data_label":"bkbase_es","__doc_id":"d56b4120194eb37f53410780da777d43","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-llp94","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594224000","dtEventTimeStamp":"1723594224000"},{"__data_label":"bkbase_es","__doc_id":"dc888e9a3789976aa11483626fc61a4f","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"},{"__data_label":"bkbase_es","__doc_id":"e058129ae18bff87c95e83f24584e654","__ext.io_kubernetes_pod":"bkmonitor-unify-query-64bd4f5df4-599f9","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594211000","dtEventTimeStamp":"1723594211000"},{"__data_label":"bkbase_es","__doc_id":"f6950fef394e813999d7316cdbf0de4d","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"},{"__data_label":"bkbase_es","__doc_id":"fa209967d4a8c5d21b3e4f67d2cd579e","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594161000","dtEventTimeStamp":"1723594161000"}]`,
		},
		"query_bk_base_es_with_errors": {
			queryTs: &structured.QueryTs{
				SpaceUid: spaceUid,
				QueryList: []*structured.Query{
					{
						DataSource:  structured.BkLog,
						TableID:     structured.TableID(tableID),
						From:        1,
						Limit:       1,
						KeepColumns: []string{"__ext.container_id", "dtEventTimeStamp"},
					},
				},
				Start: start,
				End:   end,
			},
			total:    1e4,
			expected: `[{"__data_label":"bkbase_es","__doc_id":"4f3a5e9c167097c9658e88b2f32364b2","__ext.container_id":"77bd897e66402eb66ee97a1f832fb55b2114d83dc369f01e36ce4cec8483786f","__index":"v2_2_bklog_bk_unify_query_20240814_0","__result_table":"result_table.bk_base_es","_time":"1723594209000","dtEventTimeStamp":"1723594209000"}]`,
		},
	}

	for name, c := range tcs {
		t.Run(name, func(t *testing.T) {
			total, list, err := queryRawWithInstance(ctx, c.queryTs)
			assert.Nil(t, err)
			if err != nil {
				return
			}

			sort.SliceStable(list, func(i, j int) bool {
				a := list[i]["_time"].(string) < list[j]["_time"].(string)
				b := list[i]["__doc_id"].(string) < list[j]["__doc_id"].(string)

				if a {
					return a
				} else {
					return b
				}
			})

			assert.Equal(t, c.total, total)
			actual, _ := json.Marshal(list)
			assert.JSONEq(t, c.expected, string(actual))
		})
	}
}

// TestQueryExemplar comment lint rebel
func TestQueryExemplar(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	promql.MockEngine()
	influxdb.MockSpaceRouter(ctx)

	body := []byte(`{"query_list":[{"data_source":"","table_id":"system.cpu_summary","field_name":"usage","field_list":["bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"function":null,"time_aggregation":{"function":"","window":"","position":0,"vargs_list":null},"reference_name":"","dimensions":null,"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"bk_obj_id","value":["module"],"op":"contains"},{"field_name":"ip","value":["127.0.0.2"],"op":"contains"},{"field_name":"bk_inst_id","value":["14261"],"op":"contains"},{"field_name":"bk_biz_id","value":["7"],"op":"contains"}],"condition_list":["and","and","and"]},"keep_columns":null}],"metric_merge":"","result_columns":null,"start_time":"1677081600","end_time":"1677085600","step":"","down_sample_range":"1m"}`)

	query := &structured.QueryTs{}
	err := json.Unmarshal(body, query)
	assert.Nil(t, err)

	metadata.SetUser(ctx, "", influxdb.SpaceUid, "")

	mock.InfluxDB.Set(map[string]any{
		`select usage as _value, time as _time, bk_trace_id, bk_span_id, bk_trace_value, bk_trace_timestamp from cpu_summary where time > 1677081600000000000 and time < 1677085600000000000 and (bk_obj_id='module' and (ip='127.0.0.2' and (bk_inst_id='14261' and bk_biz_id='7'))) and bk_biz_id='2' and (bk_span_id != '' or bk_trace_id != '')  limit 100000005 slimit 100005`: &decoder.Response{
			Results: []decoder.Result{
				{
					Series: []*decoder.Row{
						{
							Name: "",
							Tags: map[string]string{},
							Columns: []string{
								influxdb.ResultColumnName,
								influxdb.TimeColumnName,
								"bk_trace_id",
								"bk_span_id",
								"bk_trace_value",
								"bk_trace_timestamp",
							},
							Values: [][]any{
								{
									30,
									1677081600000000000,
									"b9cc0e45d58a70b61e8db6fffb5e3376",
									"3d2a373cbeefa1f8",
									1,
									1680157900669,
								},
								{
									21,
									1677081660000000000,
									"fe45f0eccdce3e643a77504f6e6bd87a",
									"c72dcc8fac9bcead",
									1,
									1682121442937,
								},
								{
									1,
									1677081720000000000,
									"771073eb573336a6d3365022a512d6d8",
									"fca46f1c065452e8",
									1,
									1682150008969,
								},
							},
						},
					},
				},
			},
		},
	})

	res, err := queryExemplar(ctx, query)
	assert.Nil(t, err)
	out, err := json.Marshal(res)
	assert.Nil(t, err)
	actual := string(out)
	assert.Equal(t, `{"series":[{"name":"_result0","metric_name":"usage","columns":["_value","_time","bk_trace_id","bk_span_id","bk_trace_value","bk_trace_timestamp"],"types":["float","float","string","string","float","float"],"group_keys":[],"group_values":[],"values":[[30,1677081600000000000,"b9cc0e45d58a70b61e8db6fffb5e3376","3d2a373cbeefa1f8",1,1680157900669],[21,1677081660000000000,"fe45f0eccdce3e643a77504f6e6bd87a","c72dcc8fac9bcead",1,1682121442937],[1,1677081720000000000,"771073eb573336a6d3365022a512d6d8","fca46f1c065452e8",1,1682150008969]]}]}`, actual)
}

func TestVmQueryParams(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	promql.MockEngine()

	testCases := []struct {
		username string
		spaceUid string
		query    string
		promql   string
		start    string
		end      string
		step     string
		params   string
		error    error
	}{
		{
			username: "vm-query",
			spaceUid: consul.VictoriaMetricsStorageType,
			query:    `{"query_list":[{"field_name":"bk_split_measurement","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"increase","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bk_biz_id","value":["100801"],"op":"eq"}],"condition_list":["and", "and"]}},{"field_name":"bk_split_measurement","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"delta","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (bcs_cluster_id, namespace) (increase(a[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (delta(b[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_list":["victoria_metrics"],"metric_filter_condition":{"a":"filter=\"bk_split_measurement\", bcs_cluster_id=~\"cls-2\", bcs_cluster_id=~\"cls-2\", bk_biz_id=\"100801\", result_table_id=\"victoria_metrics\", __name__=\"bk_split_measurement_value\"","b":"filter=\"bk_split_measurement\", result_table_id=\"victoria_metrics\", __name__=\"bk_split_measurement_value\""}}`,
		},
		{
			username: "vm-query-or",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"container_cpu_usage_seconds_total","field_list":null,"function":[{"method":"sum","without":false,"dimensions":[],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"count_over_time","window":"60s","position":0,"vargs_list":null},"reference_name":"a","dimensions":[],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"bk_biz_id","value":["7"],"op":"contains"},{"field_name":"ip","value":["127.0.0.1","127.0.0.2"],"op":"contains"},{"field_name":"ip","value":["[a-z]","[A-Z]"],"op":"req"},{"field_name":"api","value":["/metrics"],"op":"ncontains"},{"field_name":"bk_biz_id","value":["7"],"op":"contains"},{"field_name":"api","value":["/metrics"],"op":"contains"}],"condition_list":["and","and","and","or","and"]},"keep_columns":["_time","a"]}],"metric_merge":"a","result_columns":null,"start_time":"1697458200","end_time":"1697461800","step":"60s","down_sample_range":"3s","timezone":"Asia/Shanghai","look_back_delta":"","instant":false}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum(count_over_time(a[1m] offset -59s999ms))","start":1697458200,"end":1697461800,"step":60},"result_table_list":["100147_bcs_prom_computation_result_table_25428","100147_bcs_prom_computation_result_table_25429"],"metric_filter_condition":{"a":"bcs_cluster_id=\"BCS-K8S-25428\", bk_biz_id=\"7\", ip=~\"^(127\\\\.0\\\\.0\\\\.1|127\\\\.0\\\\.0\\\\.2)$\", ip=~\"[a-z]|[A-Z]\", api!=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25428\", bk_biz_id=\"7\", api=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25430\", bk_biz_id=\"7\", ip=~\"^(127\\\\.0\\\\.0\\\\.1|127\\\\.0\\\\.0\\\\.2)$\", ip=~\"[a-z]|[A-Z]\", api!=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25430\", bk_biz_id=\"7\", api=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25429\", bk_biz_id=\"7\", ip=~\"^(127\\\\.0\\\\.0\\\\.1|127\\\\.0\\\\.0\\\\.2)$\", ip=~\"[a-z]|[A-Z]\", api!=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25429\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25429\", bk_biz_id=\"7\", api=\"/metrics\", result_table_id=\"100147_bcs_prom_computation_result_table_25429\", __name__=\"container_cpu_usage_seconds_total_value\""}}`,
		},
		{
			username: "vm-query-or-for-internal",
			spaceUid: "vm-query",
			promql:   `{"promql":"sum by(job, metric_name) (delta(label_replace({__name__=~\"container_cpu_.+_total\", __name__ !~ \".+_size_count\", __name__ !~ \".+_process_time_count\", job=\"metric-social-friends-forever\"}, \"metric_name\", \"$1\", \"__name__\", \"ffs_rest_(.*)_count\")[2m:]))","start":"1698147600","end":"1698151200","step":"60s","bk_biz_ids":null,"timezone":"Asia/Shanghai","look_back_delta":"","instant":false}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (job, metric_name) (delta(label_replace({__name__=~\"a\"} offset -59s999ms, \"metric_name\", \"$1\", \"__name__\", \"ffs_rest_(.*)_count_value\")[2m:]))","start":1698147600,"end":1698151200,"step":60},"result_table_list":["100147_bcs_prom_computation_result_table_25428","100147_bcs_prom_computation_result_table_25429"],"metric_filter_condition":{"a":"bcs_cluster_id=\"BCS-K8S-25428\", __name__!~\".+_size_count_value\", __name__!~\".+_process_time_count_value\", job=\"metric-social-friends-forever\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=~\"container_cpu_.+_total_value\" or bcs_cluster_id=\"BCS-K8S-25430\", __name__!~\".+_size_count_value\", __name__!~\".+_process_time_count_value\", job=\"metric-social-friends-forever\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=~\"container_cpu_.+_total_value\" or bcs_cluster_id=\"BCS-K8S-25429\", __name__!~\".+_size_count_value\", __name__!~\".+_process_time_count_value\", job=\"metric-social-friends-forever\", result_table_id=\"100147_bcs_prom_computation_result_table_25429\", __name__=~\"container_cpu_.+_total_value\""}}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"container_cpu_usage_seconds_total","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bk_biz_id","value":["100801"],"op":"eq"}],"condition_list":["or", "and"]}},{"field_name":"container_cpu_usage_seconds_total","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(a[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(b[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_list":["100147_bcs_prom_computation_result_table_25428","100147_bcs_prom_computation_result_table_25429"],"metric_filter_condition":{"b":"bcs_cluster_id=\"BCS-K8S-25429\", result_table_id=\"100147_bcs_prom_computation_result_table_25429\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25428\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25430\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\""}}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bcs_cluster_id","value":["cls-2"],"op":"req"},{"field_name":"bk_biz_id","value":["100801"],"op":"eq"}],"condition_list":["and","and"]}},{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(a[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(b[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_list":["vm_rt"],"metric_filter_condition":{"a":"bcs_cluster_id=\"cls\", bcs_cluster_id=~\"cls-2\", bcs_cluster_id=~\"cls-2\", bk_biz_id=\"100801\", result_table_id=\"vm_rt\", __name__=\"metric_value\"","b":"bcs_cluster_id=\"cls\", result_table_id=\"vm_rt\", __name__=\"metric_value\""}}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"namespace","value":["ns"],"op":"contains"}],"condition_list":[]}},{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time(a[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(b[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_list":["vm_rt"],"metric_filter_condition":{"a":"bcs_cluster_id=\"cls\", namespace=\"ns\", result_table_id=\"vm_rt\", __name__=\"metric_value\"","b":"bcs_cluster_id=\"cls\", result_table_id=\"vm_rt\", __name__=\"metric_value\""}}`,
		},
		{
			username: "vm-query-fuzzy-name",
			spaceUid: "vm-query",
			query:    `{"query_list":[{"field_name":"me.*","is_regexp":true,"function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"sum_over_time","window":"1m0s"},"reference_name":"a","conditions":{"field_list":[{"field_name":"namespace","value":["ns"],"op":"contains"}],"condition_list":[]}},{"field_name":"metric","function":[{"method":"sum","dimensions":["bcs_cluster_id","namespace"]}],"time_aggregation":{"function":"count_over_time","window":"1m0s"},"reference_name":"b"}],"metric_merge":"a / b","start_time":"0","end_time":"600","step":"60s"}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"sum by (bcs_cluster_id, namespace) (sum_over_time({__name__=~\"a\"}[1m] offset -59s999ms)) / sum by (bcs_cluster_id, namespace) (count_over_time(b[1m] offset -59s999ms))","start":0,"end":600,"step":60},"result_table_list":["vm_rt"],"metric_filter_condition":{"a":"bcs_cluster_id=\"cls\", namespace=\"ns\", result_table_id=\"vm_rt\", __name__=~\"me.*_value\"","b":"bcs_cluster_id=\"cls\", result_table_id=\"vm_rt\", __name__=\"metric_value\""}}`,
		},
		{
			username: "vm-query",
			spaceUid: "vm-query",
			promql:   `{"promql":"max_over_time((increase(container_cpu_usage_seconds_total{}[10m]) \u003e 0)[1h:])","start":"1720765200","end":"1720786800","step":"10m","bk_biz_ids":null,"timezone":"Asia/Shanghai","look_back_delta":"","instant":false}`,
			params:   `{"influx_compatible":true,"use_native_or":true,"api_type":"query_range","cluster_name":"","api_params":{"query":"max_over_time((increase(a[10m] offset -9m59s999ms) \u003e 0)[1h:])","start":1720765200,"end":1720786800,"step":600},"result_table_list":["100147_bcs_prom_computation_result_table_25428","100147_bcs_prom_computation_result_table_25429"],"metric_filter_condition":{"a":"bcs_cluster_id=\"BCS-K8S-25428\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25430\", result_table_id=\"100147_bcs_prom_computation_result_table_25428\", __name__=\"container_cpu_usage_seconds_total_value\" or bcs_cluster_id=\"BCS-K8S-25429\", result_table_id=\"100147_bcs_prom_computation_result_table_25429\", __name__=\"container_cpu_usage_seconds_total_value\""}}`,
		},
	}

	for i, c := range testCases {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			var (
				query *structured.QueryTs
				err   error
			)
			ctx := metadata.InitHashID(ctx)
			metadata.SetUser(ctx, fmt.Sprintf("username:%s", c.username), c.spaceUid, "")

			if c.promql != "" {
				var queryPromQL *structured.QueryPromQL
				err = json.Unmarshal([]byte(c.promql), &queryPromQL)
				assert.Nil(t, err)
				query, err = promQLToStruct(ctx, queryPromQL)
			} else {
				err = json.Unmarshal([]byte(c.query), &query)
			}

			query.SpaceUid = c.spaceUid
			assert.Nil(t, err)
			_, err = queryTsWithPromEngine(ctx, query)
			if c.error != nil {
				assert.Contains(t, err.Error(), c.error.Error())
			} else {
				var vmParams map[string]string
				if vmParams != nil {
					assert.Equal(t, c.params, vmParams["sql"])
				}
			}
		})
	}
}

func TestStructAndPromQLConvert(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	promql.MockEngine()

	testCase := map[string]struct {
		queryStruct bool
		query       *structured.QueryTs
		promql      *structured.QueryPromQL
		err         error
	}{
		"query struct with or": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "custom",
						TableID:    "dataLabel",
						FieldName:  "metric",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function: "sum_over_time",
							Window:   "1m0s",
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
							},
							ConditionList: []string{
								"or",
							},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
				Start:       "1691132705",
				End:         "1691136305",
				Step:        "1m",
			},
			err: fmt.Errorf("or 过滤条件无法直接转换为 promql 语句，请使用结构化查询"),
		},
		"query struct with and": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "custom",
						TableID:    "dataLabel",
						FieldName:  "metric",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time(custom:dataLabel:metric{bcs_cluster_id=~"cls-2",bcs_cluster_id=~"cls-2"}[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"promql struct with and": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "custom",
						TableID:    "dataLabel",
						FieldName:  "metric",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2",
									},
									Operator: "req",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time(custom:dataLabel:metric{bcs_cluster_id=~"cls-2",bcs_cluster_id=~"cls-2"}[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"promql struct 1": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkMonitor,
						FieldName:  "container_cpu_usage_seconds_total",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2|cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bk_biz_id",
									Value: []string{
										"2",
									},
									Operator: "eq",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						ReferenceName: "a",
					},
					{
						DataSource: structured.BkMonitor,
						FieldName:  "container_cpu_usage_seconds_total",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "count_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: "a / on (bcs_cluster_id) group_left () b",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time(bkmonitor:container_cpu_usage_seconds_total{bcs_cluster_id=~"cls-2|cls-2",bk_biz_id="2"}[1m])) / on (bcs_cluster_id) group_left () sum by (bcs_cluster_id, result_table_id) (count_over_time(bkmonitor:container_cpu_usage_seconds_total[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"query struct 1": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkMonitor,
						FieldName:  "container_cpu_usage_seconds_total",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2|cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bk_biz_id",
									Value: []string{
										"2",
									},
									Operator: "eq",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						ReferenceName: "a",
					},
					{
						DataSource: structured.BkMonitor,
						FieldName:  "container_cpu_usage_seconds_total",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "count_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: "a / on (bcs_cluster_id) group_left () b",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time(bkmonitor:container_cpu_usage_seconds_total{bcs_cluster_id=~"cls-2|cls-2",bk_biz_id="2"}[1m])) / on (bcs_cluster_id) group_left () sum by (bcs_cluster_id, result_table_id) (count_over_time(bkmonitor:container_cpu_usage_seconds_total[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"query struct with __name__ ": {
			queryStruct: false,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkMonitor,
						TableID:    "table_id",
						FieldName:  ".*",
						IsRegexp:   true,
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						ReferenceName: "a",
						Dimensions:    nil,
						Limit:         0,
						Timestamp:     nil,
						StartOrEnd:    0,
						VectorOffset:  0,
						Offset:        "",
						OffsetForward: false,
						Slimit:        0,
						Soffset:       0,
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2|cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bk_biz_id",
									Value: []string{
										"2",
									},
									Operator: "eq",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						KeepColumns:         nil,
						AlignInfluxdbResult: false,
						Start:               "",
						End:                 "",
						Step:                "",
						Timezone:            "",
					},
					{
						DataSource: structured.BkMonitor,
						TableID:    "table_id",
						FieldName:  ".*",
						IsRegexp:   true,
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "count_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: "a / on (bcs_cluster_id) group_left () b",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time({__name__=~"bkmonitor:table_id:.*",bcs_cluster_id=~"cls-2|cls-2",bk_biz_id="2"}[1m])) / on (bcs_cluster_id) group_left () sum by (bcs_cluster_id, result_table_id) (count_over_time({__name__=~"bkmonitor:table_id:.*"}[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"promql struct with __name__ ": {
			queryStruct: true,
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: structured.BkMonitor,
						TableID:    "table_id",
						FieldName:  ".*",
						IsRegexp:   true,
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						ReferenceName: "a",
						Dimensions:    nil,
						Limit:         0,
						Timestamp:     nil,
						StartOrEnd:    0,
						VectorOffset:  0,
						Offset:        "",
						OffsetForward: false,
						Slimit:        0,
						Soffset:       0,
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "bcs_cluster_id",
									Value: []string{
										"cls-2|cls-2",
									},
									Operator: "req",
								},
								{
									DimensionName: "bk_biz_id",
									Value: []string{
										"2",
									},
									Operator: "eq",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						KeepColumns:         nil,
						AlignInfluxdbResult: false,
						Start:               "",
						End:                 "",
						Step:                "",
						Timezone:            "",
					},
					{
						DataSource: structured.BkMonitor,
						TableID:    "table_id",
						FieldName:  ".*",
						IsRegexp:   true,
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"bcs_cluster_id",
									"result_table_id",
								},
							},
						},
						TimeAggregation: structured.TimeAggregation{
							Function: "count_over_time",
							Window:   "1m0s",
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: "a / on (bcs_cluster_id) group_left () b",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `1m`,
			},
			promql: &structured.QueryPromQL{
				PromQL: `sum by (bcs_cluster_id, result_table_id) (sum_over_time({__name__=~"bkmonitor:table_id:.*",bcs_cluster_id=~"cls-2|cls-2",bk_biz_id="2"}[1m])) / on (bcs_cluster_id) group_left () sum by (bcs_cluster_id, result_table_id) (count_over_time({__name__=~"bkmonitor:table_id:.*"}[1m]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `1m`,
			},
		},
		"promql to struct with 1m": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `count_over_time(bkmonitor:metric[1m] @ start() offset -29s999ms)`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `30s`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						AlignInfluxdbResult: true,
						DataSource:          `bkmonitor`,
						FieldName:           `metric`,
						StartOrEnd:          parser.START,
						//Offset:              "59s999ms",
						OffsetForward: true,
						TimeAggregation: structured.TimeAggregation{
							Function:  "count_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: `a`,
						Step:          `30s`,
					},
				},
				MetricMerge: "a",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `30s`,
			},
		},
		"promql to struct with delta label_replace 1m:2m": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `sum by (job, metric_name) (delta(label_replace({__name__=~"bkmonitor:container_cpu_.+_total",job="metric-social-friends-forever"} @ start() offset -29s999ms, "metric_name", "$1", "__name__", "ffs_rest_(.*)_count")[2m:]))`,
				Start:  `1691132705`,
				End:    `1691136305`,
				Step:   `30s`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:          `bkmonitor`,
						FieldName:           `container_cpu_.+_total`,
						IsRegexp:            true,
						StartOrEnd:          parser.START,
						AlignInfluxdbResult: true,
						TimeAggregation: structured.TimeAggregation{
							Function:   "delta",
							Window:     "2m0s",
							NodeIndex:  3,
							IsSubQuery: true,
							Step:       "0s",
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "job",
									Operator:      "eq",
									Value: []string{
										"metric-social-friends-forever",
									},
								},
								//{
								//	DimensionName: "__name__",
								//	Operator:      "nreq",
								//	Value: []string{
								//		".+_size_count",
								//	},
								//},
								//{
								//	DimensionName: "__name__",
								//	Operator:      "nreq",
								//	Value: []string{
								//		".+_process_time_count",
								//	},
								//},
							},
							ConditionList: []string{},
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "label_replace",
								VArgsList: []interface{}{
									"metric_name",
									"$1",
									"__name__",
									"ffs_rest_(.*)_count",
								},
							},
							{
								Method: "sum",
								Dimensions: []string{
									"job",
									"metric_name",
								},
							},
						},
						ReferenceName: `a`,
						Offset:        "0s",
					},
				},
				MetricMerge: "a",
				Start:       `1691132705`,
				End:         `1691136305`,
				Step:        `30s`,
			},
		},
		"promql to struct with topk": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `topk(1, bkmonitor:metric)`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "metric",
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "topk",
								VArgsList: []interface{}{
									1,
								},
							},
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
			},
		},
		"promql to struct with delta(metric[1m])`": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `delta(bkmonitor:metric[1m])`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "metric",
						TimeAggregation: structured.TimeAggregation{
							Function:  "delta",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
			},
		},
		"promq to struct with metric @end()`": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `bkmonitor:metric @ end()`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "metric",
						StartOrEnd: parser.END,
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
			},
		},
		"promql to struct with condition contains`": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `bkmonitor:metric{dim_contains=~"^(val-1|val-2|val-3)$",dim_req=~"val-1|val-2|val-3"} @ end()`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "metric",
						StartOrEnd: parser.END,
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "dim_contains",
									Value: []string{
										"val-1",
										"val-2",
										"val-3",
									},
									Operator: "contains",
								},
								{
									DimensionName: "dim_req",
									Value: []string{
										"val-1",
										"val-2",
										"val-3",
									},
									Operator: "req",
								},
							},
							ConditionList: []string{
								"and",
							},
						},
						ReferenceName: "a",
					},
				},
				MetricMerge: "a",
			},
		},
		"quantile and quantile_over_time": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `quantile(0.9, quantile_over_time(0.9, bkmonitor:metric[1m]))`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "metric",
						Conditions: structured.Conditions{
							FieldList:     []structured.ConditionField{},
							ConditionList: []string{},
						},
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:  "quantile_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
							VargsList: []interface{}{
								0.9,
							},
							Position: 1,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "quantile",
								VArgsList: []interface{}{
									0.9,
								},
							},
						},
					},
				},
				MetricMerge: "a",
			},
		},
		"nodeIndex 3 with sum": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `increase(sum by (deployment_environment, result_table_id) (bkmonitor:5000575_bkapm_metric_tgf_server_gs_cn_idctest:__default__:trace_additional_duration_count{deployment_environment="g-5"})[2m:])`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						TableID:    "5000575_bkapm_metric_tgf_server_gs_cn_idctest.__default__",
						FieldName:  "trace_additional_duration_count",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "deployment_environment",
									Value:         []string{"g-5"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{},
						},
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:   "increase",
							Window:     "2m0s",
							NodeIndex:  3,
							IsSubQuery: true,
							Step:       "0s",
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"deployment_environment", "result_table_id",
								},
							},
						},
						Offset: "0s",
					},
				},
				MetricMerge: "a",
			},
		},
		"nodeIndex 2 with sum": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `sum by (deployment_environment, result_table_id) (increase(bkmonitor:5000575_bkapm_metric_tgf_server_gs_cn_idctest:__default__:trace_additional_duration_count{deployment_environment="g-5"}[2m]))`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						TableID:    "5000575_bkapm_metric_tgf_server_gs_cn_idctest.__default__",
						FieldName:  "trace_additional_duration_count",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "deployment_environment",
									Value:         []string{"g-5"},
									Operator:      "eq",
								},
							},
							ConditionList: []string{},
						},
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:  "increase",
							Window:    "2m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "sum",
								Dimensions: []string{
									"deployment_environment", "result_table_id",
								},
							},
						},
					},
				},
				MetricMerge: "a",
			},
		},
		"predict_linear": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `predict_linear(bkmonitor:metric[1h], 4*3600)`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    "bkmonitor",
						TableID:       "",
						FieldName:     "metric",
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:  "predict_linear",
							Window:    "1h0m0s",
							NodeIndex: 2,
							VargsList: []interface{}{4 * 3600},
						},
					},
				},
				MetricMerge: "a",
			},
		},
		"promql to struct with many time aggregate": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `min_over_time(increase(bkmonitor:metric[1m])[2m:])`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    "bkmonitor",
						TableID:       "",
						FieldName:     "metric",
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:  "increase",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "min_over_time",
								Window:     "2m0s",
								IsSubQuery: true,
								Step:       "0s",
							},
						},
						Offset: "0s",
					},
				},
				MetricMerge: "a",
			},
		},
		"promql to struct with many time aggregate and funciton": {
			queryStruct: true,
			promql: &structured.QueryPromQL{
				PromQL: `topk(5, floor(sum by (dim) (last_over_time(min_over_time(increase(label_replace(bkmonitor:metric, "name", "$0", "__name__", ".+")[1m:])[2m:])[3m:15s]))))`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource:    "bkmonitor",
						TableID:       "",
						FieldName:     "metric",
						ReferenceName: "a",
						TimeAggregation: structured.TimeAggregation{
							Function:   "increase",
							Window:     "1m0s",
							NodeIndex:  3,
							IsSubQuery: true,
							Step:       "0s",
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method: "label_replace",
								VArgsList: []interface{}{
									"name",
									"$0",
									"__name__",
									".+",
								},
							},
							{
								Method:     "min_over_time",
								Window:     "2m0s",
								IsSubQuery: true,
								Step:       "0s",
							},
							{
								Method:     "last_over_time",
								Window:     "3m0s",
								IsSubQuery: true,
								Step:       "15s",
							},
							{
								Method:     "sum",
								Dimensions: []string{"dim"},
							},
							{
								Method: "floor",
							},
							{
								Method: "topk",
								VArgsList: []interface{}{
									5,
								},
							},
						},
						Offset: "0s",
					},
				},
				MetricMerge: "a",
			},
		},
		"promql with match - 1": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL: `sum by (pod_name, bcs_cluster_id, namespace,instance) (rate(container_cpu_usage_seconds_total{namespace="ns-1"}[2m])) / on(bcs_cluster_id, namespace, pod_name) group_left() sum (sum_over_time(kube_pod_container_resource_limits_cpu_cores{namespace="ns-1"}[1m])) by (pod_name, bcs_cluster_id,namespace)`,
				Match:  `{pod_name="pod", bcs_cluster_id!="cls-1", namespace="ns-1", instance="ins-1"}`,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "container_cpu_usage_seconds_total",
						TimeAggregation: structured.TimeAggregation{
							Function:  "rate",
							Window:    "2m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"pod_name", "bcs_cluster_id", "namespace", "instance"},
							},
						},
						ReferenceName: "a",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
								{
									DimensionName: "bcs_cluster_id",
									Operator:      structured.ConditionNotEqual,
									Value:         []string{"cls-1"},
								},
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "instance",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ins-1"},
								},
							},
							ConditionList: []string{"and", "and", "and", "and"},
						},
					},
					{
						DataSource: "bkmonitor",
						FieldName:  "kube_pod_container_resource_limits_cpu_cores",
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"pod_name", "bcs_cluster_id", "namespace"},
							},
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
								{
									DimensionName: "bcs_cluster_id",
									Operator:      structured.ConditionNotEqual,
									Value:         []string{"cls-1"},
								},
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "instance",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ins-1"},
								},
							},
							ConditionList: []string{"and", "and", "and", "and"},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: `a / on(bcs_cluster_id, namespace, pod_name) group_left() b`,
			},
		},
		"promql with match and verify - 1": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL:             `sum by (pod_name, bcs_cluster_id, namespace,instance) (rate(container_cpu_usage_seconds_total{namespace="ns-1"}[2m])) / on(bcs_cluster_id, namespace, pod_name) group_left() sum (sum_over_time(kube_pod_container_resource_limits_cpu_cores{namespace="ns-1"}[1m])) by (pod_name, bcs_cluster_id,namespace)`,
				Match:              `{pod_name="pod", bcs_cluster_id!="cls-1", namespace="ns-1", instance="ins-1"}`,
				IsVerifyDimensions: true,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "container_cpu_usage_seconds_total",
						TimeAggregation: structured.TimeAggregation{
							Function:  "rate",
							Window:    "2m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"pod_name", "bcs_cluster_id", "namespace", "instance"},
							},
						},
						ReferenceName: "a",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
								{
									DimensionName: "bcs_cluster_id",
									Operator:      structured.ConditionNotEqual,
									Value:         []string{"cls-1"},
								},
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "instance",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ins-1"},
								},
							},
							ConditionList: []string{"and", "and", "and", "and"},
						},
					},
					{
						DataSource: "bkmonitor",
						FieldName:  "kube_pod_container_resource_limits_cpu_cores",
						TimeAggregation: structured.TimeAggregation{
							Function:  "sum_over_time",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"pod_name", "bcs_cluster_id", "namespace"},
							},
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
								{
									DimensionName: "bcs_cluster_id",
									Operator:      structured.ConditionNotEqual,
									Value:         []string{"cls-1"},
								},
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
							},
							ConditionList: []string{"and", "and", "and"},
						},
						ReferenceName: "b",
					},
				},
				MetricMerge: `a / on(bcs_cluster_id, namespace, pod_name) group_left() b`,
			},
		},
		"promql with match and verify - 2": {
			queryStruct: false,
			promql: &structured.QueryPromQL{
				PromQL:             `sum by (pod_name) (rate(container_cpu_usage_seconds_total{namespace="ns-1"}[2m])) / on(bcs_cluster_id, namespace, pod_name) group_left() kube_pod_container_resource_limits_cpu_cores{namespace="ns-1"} or sum by (bcs_cluster_id, namespace, pod_name, instance) (rate(container_cpu_usage_seconds_total{namespace="ns-1"}[1m]))`,
				Match:              `{pod_name="pod", bcs_cluster_id!="cls-1", namespace="ns-1", instance="ins-1"}`,
				IsVerifyDimensions: true,
			},
			query: &structured.QueryTs{
				QueryList: []*structured.Query{
					{
						DataSource: "bkmonitor",
						FieldName:  "container_cpu_usage_seconds_total",
						TimeAggregation: structured.TimeAggregation{
							Function:  "rate",
							Window:    "2m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"pod_name"},
							},
						},
						ReferenceName: "a",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
							},
							ConditionList: []string{"and"},
						},
					},
					{
						DataSource: "bkmonitor",
						FieldName:  "kube_pod_container_resource_limits_cpu_cores",
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
							},
						},
						ReferenceName: "b",
					},
					{
						DataSource: "bkmonitor",
						FieldName:  "container_cpu_usage_seconds_total",
						TimeAggregation: structured.TimeAggregation{
							Function:  "rate",
							Window:    "1m0s",
							NodeIndex: 2,
						},
						AggregateMethodList: []structured.AggregateMethod{
							{
								Method:     "sum",
								Dimensions: []string{"bcs_cluster_id", "namespace", "pod_name", "instance"},
							},
						},
						Conditions: structured.Conditions{
							FieldList: []structured.ConditionField{
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "pod_name",
									Operator:      structured.ConditionEqual,
									Value:         []string{"pod"},
								},
								{
									DimensionName: "bcs_cluster_id",
									Operator:      structured.ConditionNotEqual,
									Value:         []string{"cls-1"},
								},
								{
									DimensionName: "namespace",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ns-1"},
								},
								{
									DimensionName: "instance",
									Operator:      structured.ConditionEqual,
									Value:         []string{"ins-1"},
								},
							},
							ConditionList: []string{"and", "and", "and", "and"},
						},
						ReferenceName: "c",
					},
				},
				MetricMerge: `a / on(bcs_cluster_id, namespace, pod_name) group_left() b or c`,
			},
		},
	}

	for n, c := range testCase {
		t.Run(n, func(t *testing.T) {
			ctx, _ = context.WithCancel(ctx)
			if c.queryStruct {
				promql, err := structToPromQL(ctx, c.query)
				if c.err != nil {
					assert.Equal(t, c.err, err)
				} else {
					assert.Nil(t, err)
					if err == nil {
						equalWithJson(t, c.promql, promql)
					}
				}
			} else {
				query, err := promQLToStruct(ctx, c.promql)
				if c.err != nil {
					assert.Equal(t, c.err, err)
				} else {
					assert.Nil(t, err)
					if err == nil {
						equalWithJson(t, c.query, query)
					}
				}
			}
		})
	}
}

func equalWithJson(t *testing.T, a, b interface{}) {
	a1, a1Err := json.Marshal(a)
	assert.Nil(t, a1Err)

	b1, b1Err := json.Marshal(b)
	assert.Nil(t, b1Err)
	if a1Err == nil && b1Err == nil {
		assert.Equal(t, string(a1), string(b1))
	}
}

func TestQueryTs_ToQueryReference(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	influxdb.MockSpaceRouter(ctx)

	metadata.SetUser(ctx, "", influxdb.SpaceUid, "")
	jsonData := `{"query_list":[{"data_source":"","table_id":"","field_name":"container_cpu_usage_seconds_total","is_regexp":false,"field_list":null,"function":[{"method":"sum","without":false,"dimensions":["namespace"],"position":0,"args_list":null,"vargs_list":null}],"time_aggregation":{"function":"rate","window":"5m","node_index":0,"position":0,"vargs_list":[],"is_sub_query":false,"step":""},"reference_name":"a","dimensions":["namespace"],"limit":0,"timestamp":null,"start_or_end":0,"vector_offset":0,"offset":"","offset_forward":false,"slimit":0,"soffset":0,"conditions":{"field_list":[{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-blueking-gse-data-common"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-blueking-gse"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["flux-cd-deploy"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["kube-system"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bkmonitor-operator-bkop"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bkmonitor-operator"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-blueking-gse-data-jk"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["kyverno"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-bscp-prod"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-bkce-bcs-k8s-40980"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-costops-grey"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["ieg-bscp-test"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-system"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bkop-system"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bk-system"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-k8s-25186"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-k8s-25451"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-k8s-25326"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-k8s-25182"],"op":"contains"},{"field_name":"job","value":["kubelet"],"op":"contains"},{"field_name":"image","value":[""],"op":"ncontains"},{"field_name":"container_name","value":["POD"],"op":"ncontains"},{"field_name":"bcs_cluster_id","value":["BCS-K8S-00000"],"op":"contains"},{"field_name":"namespace","value":["bcs-k8s-25037"],"op":"contains"}],"condition_list":["and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and","or","and","and","and","and"]},"keep_columns":["_time","a","namespace"],"step":""}],"metric_merge":"a","result_columns":null,"start_time":"1702266900","end_time":"1702871700","step":"150s","down_sample_range":"5m","timezone":"Asia/Shanghai","look_back_delta":"","instant":false}`
	var query *structured.QueryTs
	err := json.Unmarshal([]byte(jsonData), &query)
	assert.Nil(t, err)

	queryReference, err := query.ToQueryReference(ctx)
	assert.Nil(t, err)

	vmExpand := queryReference.ToVmExpand(ctx)
	expectData := `job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-blueking-gse-data-common", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-blueking-gse", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="flux-cd-deploy", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="kube-system", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bkmonitor-operator-bkop", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bkmonitor-operator", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-blueking-gse-data-jk", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="kyverno", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-bscp-prod", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-bkce-bcs-k8s-40980", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-costops-grey", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="ieg-bscp-test", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-system", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bkop-system", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bk-system", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-k8s-25186", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-k8s-25451", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-k8s-25326", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-k8s-25182", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value" or job="kubelet", image!="", container_name!="POD", bcs_cluster_id="BCS-K8S-00000", namespace="bcs-k8s-25037", result_table_id="2_bcs_prom_computation_result_table", __name__="container_cpu_usage_seconds_total_value"`
	assert.Equal(t, expectData, vmExpand.MetricFilterCondition["a"])
	assert.Nil(t, err)
	assert.True(t, metadata.GetQueryParams(ctx).IsDirectQuery())
}

func TestQueryTsClusterMetrics(t *testing.T) {
	ctx := metadata.InitHashID(context.Background())

	mock.Init()
	promql.MockEngine()

	influxdb.MockSpaceRouter(ctx)

	testCases := map[string]struct {
		query  string
		result string
	}{
		"rangeCase": {
			query: `
                {
                    "space_uid": "influxdb",
                    "query_list": [
                        {
                            "data_source": "",
                            "table_id": "",
                            "field_name": "influxdb_shard_write_points_ok",
                            "field_list": null,
                            "function": [
                                {
                                    "method": "sum",
                                    "without": false,
                                    "dimensions": ["bkm_cluster"],
                                    "position": 0,
                                    "args_list": null,
                                    "vargs_list": null
                                }
                            ],
                            "time_aggregation": {
                                "function": "avg_over_time",
                                "window": "60s",
                                "position": 0,
                                "vargs_list": null
                            },
                            "reference_name": "a",
                            "dimensions": [],
                            "limit": 0,
                            "timestamp": null,
                            "start_or_end": 0,
                            "vector_offset": 0,
                            "offset": "",
                            "offset_forward": false,
                            "slimit": 0,
                            "soffset": 0,
                            "conditions": {
                                "field_list": [{"field_name": "bkm_cluster", "value": ["default"], "op": "eq"}],
                                "condition_list": []
                            },
                            "keep_columns": [
                                "_time",
                                "a"
                            ]
                        }
                    ],
                    "metric_merge": "a",
                    "result_columns": null,
                    "start_time": "1700901370",
                    "end_time": "1700905370",
                    "step": "60s",
					"instant": false
                }
			`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bkm_cluster"],"group_values":["default"],"values":[[1700903220000,1498687],[1700903340000,1499039.5]]}]}`,
		},
		"instanceCase": {
			query: `
                {
                    "space_uid": "influxdb",
                    "query_list": [
                        {
                            "data_source": "",
                            "table_id": "",
                            "field_name": "influxdb_shard_write_points_ok",
                            "field_list": null,
                            "reference_name": "a",
                            "dimensions": [],
                            "limit": 0,
                            "timestamp": null,
                            "start_or_end": 0,
                            "vector_offset": 0,
                            "offset": "",
                            "offset_forward": false,
                            "slimit": 0,
                            "soffset": 0,
                            "conditions": {
                                "field_list": [
									{"field_name": "bkm_cluster", "value": ["default"], "op": "eq"},
									{"field_name": "id", "value": ["43"], "op": "eq"},
									{"field_name": "database", "value": ["_internal"], "op": "eq"},
									{"field_name": "bkm_cluster", "value": ["default"], "op": "eq"},
									{"field_name": "id", "value": ["44"], "op": "eq"}
								],
                                "condition_list": ["and", "or", "and", "and"]
                            },
                            "keep_columns": [
                                "_time",
                                "a"
                            ]
                        }
                    ],
                    "metric_merge": "a",
                    "result_columns": null,
                    "end_time": "1700905370",
					"instant": true
                }
			`,
			result: `{"series":[{"name":"_result0","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bkm_cluster","database","engine","hostname","id","index_type","path","retention_policy","wal_path"],"group_values":["default","_internal","tsm1","influxdb-0","43","inmem","/var/lib/influxdb/data/_internal/monitor/43","monitor","/var/lib/influxdb/wal/_internal/monitor/43"],"values":[[1700903370000,0]]},{"name":"_result1","metric_name":"","columns":["_time","_value"],"types":["float","float"],"group_keys":["bkm_cluster","database","engine","hostname","id","index_type","path","retention_policy","wal_path"],"group_values":["default","_internal","tsm1","influxdb-0","44","inmem","/var/lib/influxdb/data/_internal/monitor/44","monitor","/var/lib/influxdb/wal/_internal/monitor/44"],"values":[[1700903370000,0]]}]}`,
		},
	}
	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			body := []byte(c.query)
			query := &structured.QueryTs{}
			err := json.Unmarshal(body, query)
			assert.Nil(t, err)

			res, err := QueryTsClusterMetrics(ctx, query)
			t.Logf("QueryTsClusterMetrics error: %+v", err)
			assert.Nil(t, err)
			out, err := json.Marshal(res)
			actual := string(out)
			assert.Nil(t, err)
			fmt.Printf("ActualResult: %v\n", actual)
			assert.Equal(t, c.result, actual)
		})
	}
}

func TestQueryTsToInstanceAndStmt(t *testing.T) {

	ctx := metadata.InitHashID(context.Background())

	spaceUid := influxdb.SpaceUid

	mock.Init()
	promql.MockEngine()

	testCases := map[string]struct {
		query        *structured.QueryTs
		promql       string
		stmt         string
		instanceType string
	}{
		"test_matcher_with_vm": {
			promql:       `datasource:result_table:vm:container_cpu_usage_seconds_total{}`,
			stmt:         `a`,
			instanceType: consul.VictoriaMetricsStorageType,
		},
		"test_matcher_with_influxdb": {
			promql:       `datasource:result_table:influxdb:cpu_summary{}`,
			stmt:         `a`,
			instanceType: consul.PrometheusStorageType,
		},
		"test_group_with_vm": {
			promql:       `sum(count_over_time(datasource:result_table:vm:container_cpu_usage_seconds_total{}[1m]))`,
			stmt:         `sum(count_over_time(a[1m] offset -59s999ms))`,
			instanceType: consul.VictoriaMetricsStorageType,
		},
		"test_group_with_influxdb": {
			promql:       `sum(count_over_time(datasource:result_table:influxdb:cpu_summary{}[1m]))`,
			stmt:         `sum(last_over_time(a[1m] offset -59s999ms))`,
			instanceType: consul.PrometheusStorageType,
		},
	}

	err := featureFlag.MockFeatureFlag(ctx, `{
	  	"must-vm-query": {
	  		"variations": {
	  			"true": true,
	  			"false": false
	  		},
	  		"targeting": [{
	  			"query": "tableID in [\"result_table.vm\"]",
	  			"percentage": {
	  				"true": 100,
	  				"false":0 
	  			}
	  		}],
	  		"defaultRule": {
	  			"variation": "false"
	  		}
	  	}
	  }`)
	if err != nil {
		log.Fatalf(ctx, err.Error())
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			if c.promql != "" {
				query, err := promQLToStruct(ctx, &structured.QueryPromQL{PromQL: c.promql})
				if err != nil {
					log.Fatalf(ctx, err.Error())
				}
				c.query = query
			}
			c.query.SpaceUid = spaceUid

			instance, stmt, err := queryTsToInstanceAndStmt(metadata.InitHashID(ctx), c.query)
			if err != nil {
				log.Fatalf(ctx, err.Error())
			}

			assert.Equal(t, c.stmt, stmt)
			if instance != nil {
				assert.Equal(t, c.instanceType, instance.InstanceType())
			}
		})
	}
}
