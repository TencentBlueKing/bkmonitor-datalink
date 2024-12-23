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
	"encoding/json"
	"testing"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestFormatFactory_Query(t *testing.T) {
	for name, c := range map[string]struct {
		conditions metadata.AllConditions
		expected   string
	}{
		"query 1": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "key",
						Value:         []string{"val-1"},
						Operator:      structured.ConditionEqual,
					},
				},
			},
			expected: `{"query":{"match_phrase":{"key":{"query":"val-1"}}}}`,
		},
		"query 2": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "key",
						Value:         []string{"val-1", "val-2"},
						Operator:      structured.ConditionEqual,
					},
				},
			},
			expected: `{"query":{"bool":{"should":[{"match_phrase":{"key":{"query":"val-1"}}},{"match_phrase":{"key":{"query":"val-2"}}}]}}}`,
		},
		"query 3": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "key",
						Value:         []string{"val-1", "val-2"},
						Operator:      structured.ConditionNotEqual,
					},
				},
			},
			expected: `{"query":{"bool":{"must_not":[{"match_phrase":{"key":{"query":"val-1"}}},{"match_phrase":{"key":{"query":"val-2"}}}]}}}`,
		},
		"query 4": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "key",
						Value:         []string{"val-1", "val-2"},
						Operator:      structured.ConditionContains,
					},
					{
						DimensionName: "key",
						Value:         []string{"val-3", "val-4"},
						Operator:      structured.ConditionContains,
					},
				},
				{
					{
						DimensionName: "key",
						Value:         []string{"val-1", "val-2"},
						Operator:      structured.ConditionNotEqual,
					},
					{
						DimensionName: "key",
						Value:         []string{"9"},
						Operator:      structured.ConditionGte,
					},
				},
			},
			expected: `{"query":{"bool":{"should":[{"bool":{"must":[{"bool":{"should":[{"wildcard":{"key":{"value":"val-1"}}},{"wildcard":{"key":{"value":"val-2"}}}]}},{"bool":{"should":[{"wildcard":{"key":{"value":"val-3"}}},{"wildcard":{"key":{"value":"val-4"}}}]}}]}},{"bool":{"must":[{"bool":{"must_not":[{"match_phrase":{"key":{"query":"val-1"}}},{"match_phrase":{"key":{"query":"val-2"}}}]}},{"range":{"key":{"from":"9","include_lower":true,"include_upper":true,"to":null}}}]}}]}}}`,
		},
		"query 5": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "key-1",
						Value:         []string{"val-1"},
						Operator:      structured.ConditionEqual,
					},
					{
						DimensionName: "key-2",
						Value:         []string{"val-2"},
						Operator:      structured.ConditionEqual,
					},
					{
						DimensionName: "key-3",
						Value:         []string{"val-3"},
						Operator:      structured.ConditionEqual,
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"match_phrase":{"key-1":{"query":"val-1"}}},{"match_phrase":{"key-2":{"query":"val-2"}}},{"match_phrase":{"key-3":{"query":"val-3"}}}]}}}`,
		},
		"nested query": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "nested.key",
						Value:         []string{"val-1", "val-2"},
						Operator:      structured.ConditionContains,
					},
					{
						DimensionName: "nested.key",
						Value:         []string{"val-3"},
						Operator:      structured.ConditionEqual,
					},
					{
						DimensionName: "nested.key",
						Operator:      structured.ConditionExisted,
					},
				},
			},
			expected: `{"query":{"nested":{"path":"nested","query":{"bool":{"must":[{"bool":{"should":[{"wildcard":{"nested.key":{"value":"val-1"}}},{"wildcard":{"nested.key":{"value":"val-2"}}}]}},{"match_phrase":{"nested.key":{"query":"val-3"}}},{"exists":{"field":"nested.key"}}]}}}}}`,
		},
		"keyword and text check wildcard": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "keyword",
						Value:         []string{"keyword_not_wildcard"},
						Operator:      structured.ConditionContains,
					},
					{
						DimensionName: "keyword",
						Value:         []string{"keyword_is_wildcard"},
						Operator:      structured.ConditionContains,
						IsWildcard:    true,
					},
					{
						DimensionName: "text",
						Value:         []string{"text_not_wildcard"},
						Operator:      structured.ConditionContains,
					},
					{
						DimensionName: "text",
						Value:         []string{"text_is_wildcard"},
						Operator:      structured.ConditionContains,
						IsWildcard:    true,
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"wildcard":{"keyword":{"value":"*keyword_not_wildcard*"}}},{"wildcard":{"keyword":{"value":"*keyword_is_wildcard*"}}},{"match_phrase":{"text":{"query":"text_not_wildcard"}}},{"wildcard":{"text":{"value":"text_is_wildcard"}}}]}}}`,
		},
		"existed and not existed query": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "key-1",
						Value:         []string{"val-1", "val-2"},
						Operator:      structured.ConditionNotExisted,
					},
					{
						DimensionName: "key-2",
						Value:         []string{"val-3"},
						Operator:      structured.ConditionExisted,
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"bool":{"must_not":{"exists":{"field":"key-1"}}}},{"exists":{"field":"key-2"}}]}}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			mappings := []map[string]any{
				{
					"properties": map[string]any{
						"nested": map[string]any{
							"type": "nested",
						},
						"keyword": map[string]any{
							"type": "keyword",
						},
						"text": map[string]any{
							"type": "text",
						},
					},
				},
			}
			fact := NewFormatFactory(ctx).WithMappings(mappings...)
			ss := elastic.NewSearchSource()
			query, err := fact.Query(c.conditions)
			assert.Nil(t, err)
			if err == nil {
				ss.Query(query)

				body, _ := ss.Source()
				bodyJson, _ := json.Marshal(body)
				bodyString := string(bodyJson)
				assert.Equal(t, c.expected, bodyString)
			}

		})
	}
}

func TestFormatFactory_RangeQueryAndAggregates(t *testing.T) {
	var start int64 = 1721024820
	var end int64 = 1721046420

	for name, c := range map[string]struct {
		timeField  metadata.TimeField
		aggregates metadata.Aggregates
		expected   string
	}{
		"second date field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: Second,
			},
			expected: `{"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"second time field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: Second,
			},
			expected: `{"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"int time field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeInt,
				Unit: Second,
			},
			expected: `{"query":{"range":{"time":{"from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"aggregate second time field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: Second,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/ShangHai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","interval":"1m","min_doc_count":0,"time_zone":"Asia/ShangHai"}}},"terms":{"field":"gseIndex"}}},"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"aggregate second int field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeInt,
				Unit: Second,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/ShangHai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","interval":"1m","min_doc_count":0}}},"terms":{"field":"gseIndex"}}},"query":{"range":{"time":{"from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"aggregate millisecond int field": {
			timeField: metadata.TimeField{
				Name: "dtEventTime",
				Type: TimeFieldTypeInt,
				Unit: Millisecond,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/ShangHai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"dtEventTime":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420000,"min":1721024820000},"field":"dtEventTime","interval":"1m","min_doc_count":0}}},"terms":{"field":"gseIndex"}}},"query":{"range":{"dtEventTime":{"from":1721024820000,"include_lower":true,"include_upper":true,"to":1721046420000}}}}`,
		},
		"aggregate millisecond time field": {
			timeField: metadata.TimeField{
				Name: "dtEventTime",
				Type: TimeFieldTypeTime,
				Unit: Millisecond,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/ShangHai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"dtEventTime":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420000,"min":1721024820000},"field":"dtEventTime","interval":"1m","min_doc_count":0,"time_zone":"Asia/ShangHai"}}},"terms":{"field":"gseIndex"}}},"query":{"range":{"dtEventTime":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			fact := NewFormatFactory(ctx).
				WithQuery("value", c.timeField, start, end, 0, 0).
				WithTransform(metadata.GetPromDataFormat(ctx).EncodeFunc(), metadata.GetPromDataFormat(ctx).DecodeFunc())

			ss := elastic.NewSearchSource()
			rangeQuery, err := fact.RangeQuery()
			assert.Nil(t, err)
			if err == nil {
				ss.Query(rangeQuery)
				if len(c.aggregates) > 0 {
					aggName, agg, aggErr := fact.EsAgg(c.aggregates)
					assert.Nil(t, aggErr)
					if aggErr == nil {
						ss.Aggregation(aggName, agg)
					}
				}
			}

			body, _ := ss.Source()
			bodyJson, _ := json.Marshal(body)
			bodyString := string(bodyJson)
			assert.Equal(t, c.expected, bodyString)
		})
	}
}

func TestFormatFactory_AggDataFormat(t *testing.T) {
	testCases := map[string]struct {
		res        string
		aggregates metadata.Aggregates
		expected   string
	}{
		"max value is null": {
			aggregates: metadata.Aggregates{
				{
					Name:       "max",
					Dimensions: []string{"database_name"},
					Window:     time.Hour * 24,
				},
			},
			res:      `{"took":12,"timed_out":false,"_shards":{"total":9,"successful":9,"skipped":0,"failed":0},"hits":{"total":{"value":10000,"relation":"gte"},"max_score":null,"hits":[]},"aggregations":{"database_name":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"dbRaphael","doc_count":31681,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":31681,"_value":{"value":6.49154281472E11}}]}},{"key":"bak_cbs_dbRaphael_new_20220117170335_100630794","doc_count":7599,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":7599,"_value":{"value":9.17192704E8}}]}},{"key":"dbRaphael_new","doc_count":7599,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":7599,"_value":{"value":9.17192704E8}}]}},{"key":"time_controller_prod","doc_count":1834,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1834,"_value":{"value":1.905320722432E12}}]}},{"key":"db_time","doc_count":1288,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1288,"_value":{"value":1.58793728E8}}]}},{"key":"db_gamerapp","doc_count":815,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":815,"_value":{"value":1.65445632E8}}]}},{"key":"analysis","doc_count":782,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":782,"_value":{"value":1.09520945152E11}}]}},{"key":"scrm","doc_count":753,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":251,"_value":{"value":2.48476893184E11}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":251,"_value":{"value":2.48476893184E11}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":251,"_value":{"value":2.48476893184E11}}]}},{"key":"dbRaphaelBiz","doc_count":222,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":222,"_value":{"value":6.4331317248E10}}]}},{"key":"boss","doc_count":130,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":130,"_value":{"value":9.814016E7}}]}},{"key":"db_gamematrix_web_manage_prod","doc_count":122,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":122,"_value":{"value":3.144843264E9}}]}},{"key":"opcg_backend_dev","doc_count":108,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":108,"_value":{"value":2.752512E7}}]}},{"key":"gmve_configcenter","doc_count":107,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":107,"_value":{"value":1.5253504E8}}]}},{"key":"opcg_backend_pro","doc_count":106,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":106,"_value":{"value":3.726671872E9}}]}},{"key":"db_vdesk_task","doc_count":99,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":33,"_value":{"value":1.99426048E8}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":33,"_value":{"value":1.99426048E8}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":33,"_value":{"value":1.99426048E8}}]}},{"key":"db_vdesk_config","doc_count":87,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":29,"_value":{"value":6.20756992E8}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":29,"_value":{"value":6.20756992E8}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":29,"_value":{"value":6.20756992E8}}]}},{"key":"db_vdesk_ticket","doc_count":87,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":29,"_value":{"value":5.23051008E9}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":29,"_value":{"value":5.238898688E9}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":29,"_value":{"value":5.2514816E9}}]}},{"key":"db_mc","doc_count":86,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":86,"_value":{"value":1.30160410624E11}}]}},{"key":"grafana","doc_count":77,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":77,"_value":{"value":6.1587456E7}}]}},{"key":"db_vdesk_customer","doc_count":57,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":19,"_value":{"value":1.0132996096E10}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":19,"_value":{"value":1.0132996096E10}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":19,"_value":{"value":1.0132996096E10}}]}},{"key":"db_xinyue_robot","doc_count":57,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":19,"_value":{"value":3.490217984E9}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":19,"_value":{"value":3.490217984E9}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":19,"_value":{"value":3.490217984E9}}]}},{"key":"db_cloudgames","doc_count":38,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":38,"_value":{"value":1.7596563456E10}}]}},{"key":"db_scpal","doc_count":33,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":11,"_value":{"value":1.0747904E7}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":11,"_value":{"value":1.0747904E7}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":11,"_value":{"value":1.0747904E7}}]}},{"key":"db_activity","doc_count":30,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":10,"_value":{"value":2097152.0}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":10,"_value":{"value":2097152.0}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":10,"_value":{"value":2097152.0}}]}},{"key":"db_vdesk_im","doc_count":30,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":10,"_value":{"value":8.1846272E8}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":10,"_value":{"value":8.3091456E8}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":10,"_value":{"value":8.3091456E8}}]}},{"key":"opcg_cluster","doc_count":27,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":27,"_value":{"value":2.0987904E7}}]}},{"key":"opcg_cluster_test","doc_count":27,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":27,"_value":{"value":3.3882112E7}}]}},{"key":"offline","doc_count":26,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":26,"_value":{"value":7.0555746304E10}}]}},{"key":"db_backup","doc_count":24,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":8,"_value":{"value":2.0854243328E10}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":8,"_value":{"value":2.0359315456E10}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":8,"_value":{"value":2.0455784448E10}}]}},{"key":"db_live_act","doc_count":21,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":7,"_value":{"value":1343488.0}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":7,"_value":{"value":1343488.0}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":7,"_value":{"value":1343488.0}}]}},{"key":"db_svip_data_flow","doc_count":21,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":7,"_value":{"value":4.20724736E8}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":7,"_value":{"value":4.20724736E8}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":7,"_value":{"value":4.20724736E8}}]}},{"key":"db_h5_backend","doc_count":18,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":18,"_value":{"value":1.45408E8}}]}},{"key":"sr_server","doc_count":17,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":17,"_value":{"value":1.7973248E7}}]}},{"key":"db_mp","doc_count":16,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":16,"_value":{"value":1064960.0}}]}},{"key":"games_launcher","doc_count":16,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":16,"_value":{"value":3.1309824E7}}]}},{"key":"db_external_idata","doc_count":15,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":15,"_value":{"value":4.674715648E9}}]}},{"key":"db_robot_platform","doc_count":15,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":5,"_value":{"value":1.18902964224E11}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":5,"_value":{"value":1.18902964224E11}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":5,"_value":{"value":1.18902964224E11}}]}},{"key":"db_self_service","doc_count":15,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":5,"_value":{"value":5.2723712E7}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":5,"_value":{"value":5.2723712E7}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":5,"_value":{"value":5.2723712E7}}]}},{"key":"test_tv_backend_db","doc_count":13,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":13,"_value":{"value":1327104.0}}]}},{"key":"db_ocpauth","doc_count":12,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":12,"_value":{"value":1589248.0}}]}},{"key":"gmve_configcenter_cq4","doc_count":12,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":12,"_value":{"value":8.331264E7}}]}},{"key":"gmve_configcenter_cq5","doc_count":12,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":12,"_value":{"value":9.1766784E7}}]}},{"key":"gmve_configcenter_nj6","doc_count":12,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":12,"_value":{"value":8.3378176E7}}]}},{"key":"gmve_configcenter_sz3","doc_count":12,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":12,"_value":{"value":1.08412928E8}}]}},{"key":"gmve_configcenter_tj4","doc_count":12,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":12,"_value":{"value":4.8627712E7}}]}},{"key":"gmve_configcenter_tj7","doc_count":12,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":12,"_value":{"value":7.90528E7}}]}},{"key":"deploy_gmve_configcenter","doc_count":11,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":11,"_value":{"value":1556480.0}}]}},{"key":"opcg_db_hub","doc_count":11,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":11,"_value":{"value":2.855911424E9}}]}},{"key":"paladin","doc_count":11,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":11,"_value":{"value":1.1010048E7}}]}},{"key":"tmp_gmve_configcenter","doc_count":11,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":11,"_value":{"value":1.0944512E7}}]}},{"key":"cloudgame","doc_count":10,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":10,"_value":{"value":8.3951616E7}}]}},{"key":"dev_gmve_configcenter","doc_count":10,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":10,"_value":{"value":1.2713984E7}}]}},{"key":"qa1_gmve_configcenter","doc_count":10,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":10,"_value":{"value":4.3220992E7}}]}},{"key":"rc_gmve_configcenter","doc_count":10,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":10,"_value":{"value":2.5280512E7}}]}},{"key":"dbXyplusService","doc_count":9,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":3,"_value":{"value":1.1010048E8}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":3,"_value":{"value":1.1010048E8}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":1.1010048E8}}]}},{"key":"db_live_vote","doc_count":9,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":3,"_value":{"value":458752.0}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":3,"_value":{"value":458752.0}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":458752.0}}]}},{"key":"db_team","doc_count":9,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":3,"_value":{"value":950272.0}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":3,"_value":{"value":950272.0}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":950272.0}}]}},{"key":"db_vdesk_approval","doc_count":9,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":3,"_value":{"value":1.1845632E7}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":3,"_value":{"value":1.1845632E7}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":1.1845632E7}}]}},{"key":"db_vdesk_satisfaction","doc_count":9,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":3,"_value":{"value":376832.0}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":3,"_value":{"value":376832.0}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":376832.0}}]}},{"key":"db_vdesk_voice","doc_count":9,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":3,"_value":{"value":1343488.0}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":3,"_value":{"value":1343488.0}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":1343488.0}}]}},{"key":"db_xinyue_robot_act","doc_count":9,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":3,"_value":{"value":2.0178796544E10}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":3,"_value":{"value":2.0178796544E10}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":2.0178796544E10}}]}},{"key":"qa2_gmve_configcenter","doc_count":9,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":9,"_value":{"value":1.04644608E8}}]}},{"key":"test","doc_count":9,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":3,"_value":{"value":3.2702464E7}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":3,"_value":{"value":3.2702464E7}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":3.2702464E7}}]}},{"key":"sr_server_qa2","doc_count":8,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":8,"_value":{"value":3.0343168E7}}]}},{"key":"zhiqiangli_test","doc_count":8,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":8,"_value":{"value":819200.0}}]}},{"key":"sr_server_rc2","doc_count":7,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":7,"_value":{"value":1163264.0}}]}},{"key":"db_danmu","doc_count":6,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":6,"_value":{"value":1835008.0}}]}},{"key":"db_svip_station","doc_count":6,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":2,"_value":{"value":278528.0}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":2,"_value":{"value":278528.0}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":278528.0}}]}},{"key":"gmve_license_center","doc_count":6,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":6,"_value":{"value":344064.0}}]}},{"key":"data","doc_count":5,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":5,"_value":{"value":4.6137344E7}}]}},{"key":"db_inner_idata","doc_count":5,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":5,"_value":{"value":3.2751616E7}}]}},{"key":"opcgvirt_gmatrix","doc_count":5,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":5,"_value":{"value":1.70098688E8}}]}},{"key":"digitalgw","doc_count":4,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":4,"_value":{"value":1.333788672E9}}]}},{"key":"db_game","doc_count":3,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":1,"_value":{"value":98304.0}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":1,"_value":{"value":98304.0}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1,"_value":{"value":98304.0}}]}},{"key":"db_hostPasswd","doc_count":3,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":7.7709312E7}}]}},{"key":"db_robot_oper_closedloop","doc_count":3,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":1,"_value":{"value":1.04562688E8}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":1,"_value":{"value":1.04562688E8}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1,"_value":{"value":1.04562688E8}}]}},{"key":"db_user","doc_count":3,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":1,"_value":{"value":163840.0}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":1,"_value":{"value":163840.0}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1,"_value":{"value":163840.0}}]}},{"key":"db_vdesk_llm","doc_count":3,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":1,"_value":{"value":98304.0}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":1,"_value":{"value":98304.0}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1,"_value":{"value":98304.0}}]}},{"key":"db_xinyue_robot_log","doc_count":3,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":1,"_value":{"value":2.0356399104E10}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":1,"_value":{"value":2.0356399104E10}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1,"_value":{"value":2.0356399104E10}}]}},{"key":"dbtest","doc_count":3,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":114688.0}}]}},{"key":"games@002dlauncher@002danalysis","doc_count":3,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":311296.0}}]}},{"key":"gmve_license","doc_count":3,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":294912.0}}]}},{"key":"greatwall","doc_count":3,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":3,"_value":{"value":1310720.0}}]}},{"key":"codeaxis","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":9584640.0}}]}},{"key":"dev_digitalgw","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":507904.0}}]}},{"key":"dev_dunhuang","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":294912.0}}]}},{"key":"dev_gmve_recorder","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":458752.0}}]}},{"key":"dunhuang","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":4.483710976E9}}]}},{"key":"sr_server_test","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":5652480.0}}]}},{"key":"tdw_export","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":311296.0}}]}},{"key":"test_gmve_recorder","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":557056.0}}]}},{"key":"tiyan_gmve_recorder","doc_count":2,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":2,"_value":{"value":229376.0}}]}},{"key":"bak_cbs_cloudgame_201911131726","doc_count":1,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1,"_value":{"value":147456.0}}]}},{"key":"test_db","doc_count":1,"dtEventTimeStamp":{"buckets":[{"key_as_string":"1733068800000","key":1733068800000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733155200000","key":1733155200000,"doc_count":0,"_value":{"value":null}},{"key_as_string":"1733241600000","key":1733241600000,"doc_count":1,"_value":{"value":114688.0}}]}}]}}}`,
			expected: `{"name:\"database_name\" value:\"analysis\" ":{"labels":[{"name":"database_name","value":"analysis"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":109520945152,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"bak_cbs_cloudgame_201911131726\" ":{"labels":[{"name":"database_name","value":"bak_cbs_cloudgame_201911131726"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":147456,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"bak_cbs_dbRaphael_new_20220117170335_100630794\" ":{"labels":[{"name":"database_name","value":"bak_cbs_dbRaphael_new_20220117170335_100630794"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":917192704,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"boss\" ":{"labels":[{"name":"database_name","value":"boss"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":98140160,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"cloudgame\" ":{"labels":[{"name":"database_name","value":"cloudgame"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":83951616,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"codeaxis\" ":{"labels":[{"name":"database_name","value":"codeaxis"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":9584640,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"data\" ":{"labels":[{"name":"database_name","value":"data"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":46137344,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"dbRaphael\" ":{"labels":[{"name":"database_name","value":"dbRaphael"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":649154281472,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"dbRaphaelBiz\" ":{"labels":[{"name":"database_name","value":"dbRaphaelBiz"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":64331317248,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"dbRaphael_new\" ":{"labels":[{"name":"database_name","value":"dbRaphael_new"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":917192704,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"dbXyplusService\" ":{"labels":[{"name":"database_name","value":"dbXyplusService"}],"samples":[{"value":110100480,"timestamp":1733068800000},{"value":110100480,"timestamp":1733155200000},{"value":110100480,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_activity\" ":{"labels":[{"name":"database_name","value":"db_activity"}],"samples":[{"value":2097152,"timestamp":1733068800000},{"value":2097152,"timestamp":1733155200000},{"value":2097152,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_backup\" ":{"labels":[{"name":"database_name","value":"db_backup"}],"samples":[{"value":20854243328,"timestamp":1733068800000},{"value":20359315456,"timestamp":1733155200000},{"value":20455784448,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_cloudgames\" ":{"labels":[{"name":"database_name","value":"db_cloudgames"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":17596563456,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_danmu\" ":{"labels":[{"name":"database_name","value":"db_danmu"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":1835008,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_external_idata\" ":{"labels":[{"name":"database_name","value":"db_external_idata"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":4674715648,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_game\" ":{"labels":[{"name":"database_name","value":"db_game"}],"samples":[{"value":98304,"timestamp":1733068800000},{"value":98304,"timestamp":1733155200000},{"value":98304,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_gamematrix_web_manage_prod\" ":{"labels":[{"name":"database_name","value":"db_gamematrix_web_manage_prod"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":3144843264,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_gamerapp\" ":{"labels":[{"name":"database_name","value":"db_gamerapp"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":165445632,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_h5_backend\" ":{"labels":[{"name":"database_name","value":"db_h5_backend"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":145408000,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_hostPasswd\" ":{"labels":[{"name":"database_name","value":"db_hostPasswd"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":77709312,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_inner_idata\" ":{"labels":[{"name":"database_name","value":"db_inner_idata"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":32751616,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_live_act\" ":{"labels":[{"name":"database_name","value":"db_live_act"}],"samples":[{"value":1343488,"timestamp":1733068800000},{"value":1343488,"timestamp":1733155200000},{"value":1343488,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_live_vote\" ":{"labels":[{"name":"database_name","value":"db_live_vote"}],"samples":[{"value":458752,"timestamp":1733068800000},{"value":458752,"timestamp":1733155200000},{"value":458752,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_mc\" ":{"labels":[{"name":"database_name","value":"db_mc"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":130160410624,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_mp\" ":{"labels":[{"name":"database_name","value":"db_mp"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":1064960,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_ocpauth\" ":{"labels":[{"name":"database_name","value":"db_ocpauth"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":1589248,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_robot_oper_closedloop\" ":{"labels":[{"name":"database_name","value":"db_robot_oper_closedloop"}],"samples":[{"value":104562688,"timestamp":1733068800000},{"value":104562688,"timestamp":1733155200000},{"value":104562688,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_robot_platform\" ":{"labels":[{"name":"database_name","value":"db_robot_platform"}],"samples":[{"value":118902964224,"timestamp":1733068800000},{"value":118902964224,"timestamp":1733155200000},{"value":118902964224,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_scpal\" ":{"labels":[{"name":"database_name","value":"db_scpal"}],"samples":[{"value":10747904,"timestamp":1733068800000},{"value":10747904,"timestamp":1733155200000},{"value":10747904,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_self_service\" ":{"labels":[{"name":"database_name","value":"db_self_service"}],"samples":[{"value":52723712,"timestamp":1733068800000},{"value":52723712,"timestamp":1733155200000},{"value":52723712,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_svip_data_flow\" ":{"labels":[{"name":"database_name","value":"db_svip_data_flow"}],"samples":[{"value":420724736,"timestamp":1733068800000},{"value":420724736,"timestamp":1733155200000},{"value":420724736,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_svip_station\" ":{"labels":[{"name":"database_name","value":"db_svip_station"}],"samples":[{"value":278528,"timestamp":1733068800000},{"value":278528,"timestamp":1733155200000},{"value":278528,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_team\" ":{"labels":[{"name":"database_name","value":"db_team"}],"samples":[{"value":950272,"timestamp":1733068800000},{"value":950272,"timestamp":1733155200000},{"value":950272,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_time\" ":{"labels":[{"name":"database_name","value":"db_time"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":158793728,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_user\" ":{"labels":[{"name":"database_name","value":"db_user"}],"samples":[{"value":163840,"timestamp":1733068800000},{"value":163840,"timestamp":1733155200000},{"value":163840,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_vdesk_approval\" ":{"labels":[{"name":"database_name","value":"db_vdesk_approval"}],"samples":[{"value":11845632,"timestamp":1733068800000},{"value":11845632,"timestamp":1733155200000},{"value":11845632,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_vdesk_config\" ":{"labels":[{"name":"database_name","value":"db_vdesk_config"}],"samples":[{"value":620756992,"timestamp":1733068800000},{"value":620756992,"timestamp":1733155200000},{"value":620756992,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_vdesk_customer\" ":{"labels":[{"name":"database_name","value":"db_vdesk_customer"}],"samples":[{"value":10132996096,"timestamp":1733068800000},{"value":10132996096,"timestamp":1733155200000},{"value":10132996096,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_vdesk_im\" ":{"labels":[{"name":"database_name","value":"db_vdesk_im"}],"samples":[{"value":818462720,"timestamp":1733068800000},{"value":830914560,"timestamp":1733155200000},{"value":830914560,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_vdesk_llm\" ":{"labels":[{"name":"database_name","value":"db_vdesk_llm"}],"samples":[{"value":98304,"timestamp":1733068800000},{"value":98304,"timestamp":1733155200000},{"value":98304,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_vdesk_satisfaction\" ":{"labels":[{"name":"database_name","value":"db_vdesk_satisfaction"}],"samples":[{"value":376832,"timestamp":1733068800000},{"value":376832,"timestamp":1733155200000},{"value":376832,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_vdesk_task\" ":{"labels":[{"name":"database_name","value":"db_vdesk_task"}],"samples":[{"value":199426048,"timestamp":1733068800000},{"value":199426048,"timestamp":1733155200000},{"value":199426048,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_vdesk_ticket\" ":{"labels":[{"name":"database_name","value":"db_vdesk_ticket"}],"samples":[{"value":5230510080,"timestamp":1733068800000},{"value":5238898688,"timestamp":1733155200000},{"value":5251481600,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_vdesk_voice\" ":{"labels":[{"name":"database_name","value":"db_vdesk_voice"}],"samples":[{"value":1343488,"timestamp":1733068800000},{"value":1343488,"timestamp":1733155200000},{"value":1343488,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_xinyue_robot\" ":{"labels":[{"name":"database_name","value":"db_xinyue_robot"}],"samples":[{"value":3490217984,"timestamp":1733068800000},{"value":3490217984,"timestamp":1733155200000},{"value":3490217984,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_xinyue_robot_act\" ":{"labels":[{"name":"database_name","value":"db_xinyue_robot_act"}],"samples":[{"value":20178796544,"timestamp":1733068800000},{"value":20178796544,"timestamp":1733155200000},{"value":20178796544,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"db_xinyue_robot_log\" ":{"labels":[{"name":"database_name","value":"db_xinyue_robot_log"}],"samples":[{"value":20356399104,"timestamp":1733068800000},{"value":20356399104,"timestamp":1733155200000},{"value":20356399104,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"dbtest\" ":{"labels":[{"name":"database_name","value":"dbtest"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":114688,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"deploy_gmve_configcenter\" ":{"labels":[{"name":"database_name","value":"deploy_gmve_configcenter"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":1556480,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"dev_digitalgw\" ":{"labels":[{"name":"database_name","value":"dev_digitalgw"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":507904,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"dev_dunhuang\" ":{"labels":[{"name":"database_name","value":"dev_dunhuang"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":294912,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"dev_gmve_configcenter\" ":{"labels":[{"name":"database_name","value":"dev_gmve_configcenter"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":12713984,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"dev_gmve_recorder\" ":{"labels":[{"name":"database_name","value":"dev_gmve_recorder"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":458752,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"digitalgw\" ":{"labels":[{"name":"database_name","value":"digitalgw"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":1333788672,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"dunhuang\" ":{"labels":[{"name":"database_name","value":"dunhuang"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":4483710976,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"games@002dlauncher@002danalysis\" ":{"labels":[{"name":"database_name","value":"games@002dlauncher@002danalysis"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":311296,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"games_launcher\" ":{"labels":[{"name":"database_name","value":"games_launcher"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":31309824,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"gmve_configcenter\" ":{"labels":[{"name":"database_name","value":"gmve_configcenter"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":152535040,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"gmve_configcenter_cq4\" ":{"labels":[{"name":"database_name","value":"gmve_configcenter_cq4"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":83312640,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"gmve_configcenter_cq5\" ":{"labels":[{"name":"database_name","value":"gmve_configcenter_cq5"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":91766784,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"gmve_configcenter_nj6\" ":{"labels":[{"name":"database_name","value":"gmve_configcenter_nj6"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":83378176,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"gmve_configcenter_sz3\" ":{"labels":[{"name":"database_name","value":"gmve_configcenter_sz3"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":108412928,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"gmve_configcenter_tj4\" ":{"labels":[{"name":"database_name","value":"gmve_configcenter_tj4"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":48627712,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"gmve_configcenter_tj7\" ":{"labels":[{"name":"database_name","value":"gmve_configcenter_tj7"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":79052800,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"gmve_license\" ":{"labels":[{"name":"database_name","value":"gmve_license"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":294912,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"gmve_license_center\" ":{"labels":[{"name":"database_name","value":"gmve_license_center"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":344064,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"grafana\" ":{"labels":[{"name":"database_name","value":"grafana"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":61587456,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"greatwall\" ":{"labels":[{"name":"database_name","value":"greatwall"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":1310720,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"offline\" ":{"labels":[{"name":"database_name","value":"offline"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":70555746304,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"opcg_backend_dev\" ":{"labels":[{"name":"database_name","value":"opcg_backend_dev"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":27525120,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"opcg_backend_pro\" ":{"labels":[{"name":"database_name","value":"opcg_backend_pro"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":3726671872,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"opcg_cluster\" ":{"labels":[{"name":"database_name","value":"opcg_cluster"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":20987904,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"opcg_cluster_test\" ":{"labels":[{"name":"database_name","value":"opcg_cluster_test"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":33882112,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"opcg_db_hub\" ":{"labels":[{"name":"database_name","value":"opcg_db_hub"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":2855911424,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"opcgvirt_gmatrix\" ":{"labels":[{"name":"database_name","value":"opcgvirt_gmatrix"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":170098688,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"paladin\" ":{"labels":[{"name":"database_name","value":"paladin"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":11010048,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"qa1_gmve_configcenter\" ":{"labels":[{"name":"database_name","value":"qa1_gmve_configcenter"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":43220992,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"qa2_gmve_configcenter\" ":{"labels":[{"name":"database_name","value":"qa2_gmve_configcenter"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":104644608,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"rc_gmve_configcenter\" ":{"labels":[{"name":"database_name","value":"rc_gmve_configcenter"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":25280512,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"scrm\" ":{"labels":[{"name":"database_name","value":"scrm"}],"samples":[{"value":248476893184,"timestamp":1733068800000},{"value":248476893184,"timestamp":1733155200000},{"value":248476893184,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"sr_server\" ":{"labels":[{"name":"database_name","value":"sr_server"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":17973248,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"sr_server_qa2\" ":{"labels":[{"name":"database_name","value":"sr_server_qa2"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":30343168,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"sr_server_rc2\" ":{"labels":[{"name":"database_name","value":"sr_server_rc2"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":1163264,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"sr_server_test\" ":{"labels":[{"name":"database_name","value":"sr_server_test"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":5652480,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"tdw_export\" ":{"labels":[{"name":"database_name","value":"tdw_export"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":311296,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"test\" ":{"labels":[{"name":"database_name","value":"test"}],"samples":[{"value":32702464,"timestamp":1733068800000},{"value":32702464,"timestamp":1733155200000},{"value":32702464,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"test_db\" ":{"labels":[{"name":"database_name","value":"test_db"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":114688,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"test_gmve_recorder\" ":{"labels":[{"name":"database_name","value":"test_gmve_recorder"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":557056,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"test_tv_backend_db\" ":{"labels":[{"name":"database_name","value":"test_tv_backend_db"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":1327104,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"time_controller_prod\" ":{"labels":[{"name":"database_name","value":"time_controller_prod"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":1905320722432,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"tiyan_gmve_recorder\" ":{"labels":[{"name":"database_name","value":"tiyan_gmve_recorder"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":229376,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"tmp_gmve_configcenter\" ":{"labels":[{"name":"database_name","value":"tmp_gmve_configcenter"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":10944512,"timestamp":1733241600000}],"exemplars":null,"histograms":null},"name:\"database_name\" value:\"zhiqiangli_test\" ":{"labels":[{"name":"database_name","value":"zhiqiangli_test"}],"samples":[{"timestamp":1733068800000},{"timestamp":1733155200000},{"value":819200,"timestamp":1733241600000}],"exemplars":null,"histograms":null}}`,
		},
	}

	metadata.InitMetadata()
	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			fact := NewFormatFactory(ctx).
				WithQuery("", metadata.TimeField{
					Name:     DefaultTimeFieldName,
					Type:     DefaultTimeFieldType,
					Unit:     DefaultTimeFieldUnit,
					UnitRate: 0,
				}, 0, 0, 0, 0)

			_, _, err := fact.EsAgg(c.aggregates)
			assert.NoError(t, err)

			var sr *elastic.SearchResult
			err = json.Unmarshal([]byte(c.res), &sr)
			assert.NoError(t, err)

			ts, err := fact.AggDataFormat(sr.Aggregations, nil)
			assert.NoError(t, err)

			outTs, err := json.Marshal(ts)
			assert.NoError(t, err)

			assert.Equal(t, string(outTs), c.expected)
		})
	}
}
