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

	"github.com/olivere/elastic/v7"
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
			expected: `{"query":{"bool":{"should":[{"bool":{"must":[{"bool":{"should":[{"wildcard":{"key":{"value":"*val-1*"}}},{"wildcard":{"key":{"value":"*val-2*"}}}]}},{"bool":{"should":[{"wildcard":{"key":{"value":"*val-3*"}}},{"wildcard":{"key":{"value":"*val-4*"}}}]}}]}},{"bool":{"must":[{"bool":{"must_not":[{"match_phrase":{"key":{"query":"val-1"}}},{"match_phrase":{"key":{"query":"val-2"}}}]}},{"range":{"key":{"from":"9","include_lower":true,"include_upper":true,"to":null}}}]}}]}}}`,
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
			expected: `{"query":{"nested":{"path":"nested","query":{"bool":{"must":[{"bool":{"should":[{"wildcard":{"nested.key":{"value":"*val-1*"}}},{"wildcard":{"nested.key":{"value":"*val-2*"}}}]}},{"match_phrase":{"nested.key":{"query":"val-3"}}},{"exists":{"field":"nested.key"}}]}}}}}`,
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
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","fixed_interval":"1m","min_doc_count":0,"time_zone":"Asia/ShangHai"}}},"terms":{"field":"gseIndex"}}},"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
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
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","fixed_interval":"1m","min_doc_count":0}}},"terms":{"field":"gseIndex"}}},"query":{"range":{"time":{"from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
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
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"dtEventTime":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420000,"min":1721024820000},"field":"dtEventTime","fixed_interval":"1m","min_doc_count":0}}},"terms":{"field":"gseIndex"}}},"query":{"range":{"dtEventTime":{"from":1721024820000,"include_lower":true,"include_upper":true,"to":1721046420000}}}}`,
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
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"dtEventTime":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420000,"min":1721024820000},"field":"dtEventTime","fixed_interval":"1m","min_doc_count":0,"time_zone":"Asia/ShangHai"}}},"terms":{"field":"gseIndex"}}},"query":{"range":{"dtEventTime":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
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
