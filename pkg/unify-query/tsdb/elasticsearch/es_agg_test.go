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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/consul"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
)

func TestES_AggregationOptimization(t *testing.T) {
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

	mock.Es.Set(map[string]any{
		`{"aggregations":{"status":{"aggregations":{"_value":{"value_count":{"field":"dtEventTimeStamp"}}},"terms":{"field":"status","include":["error","warning"],"missing":" "}}},"query":{"bool":{"filter":[{"bool":{"should":[{"match_phrase":{"status":{"query":"error"}}},{"match_phrase":{"status":{"query":"warning"}}}]}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}]}},"size":0}`:                                                                                                    `{"took":5,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1000,"relation":"eq"},"max_score":null,"hits":[]},"aggregations":{"status":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"error","doc_count":450,"_value":{"value":450}},{"key":"warning","doc_count":350,"_value":{"value":350}}]}}}`,
		`{"aggregations":{"level":{"aggregations":{"_value":{"value_count":{"field":"dtEventTimeStamp"}}},"terms":{"exclude":["debug","trace"],"field":"level","missing":" "}}},"query":{"bool":{"filter":[{"bool":{"must_not":[{"match_phrase":{"level":{"query":"debug"}}},{"match_phrase":{"level":{"query":"trace"}}}]}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}]}},"size":0}`:                                                                                                          `{"took":3,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1000,"relation":"eq"},"max_score":null,"hits":[]},"aggregations":{"level":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"info","doc_count":400,"_value":{"value":400}},{"key":"warn","doc_count":300,"_value":{"value":300}},{"key":"error","doc_count":200,"_value":{"value":200}}]}}}`,
		`{"aggregations":{"service":{"aggregations":{"_value":{"value_count":{"field":"dtEventTimeStamp"}}},"terms":{"exclude":["test"],"field":"service","include":["web","api"],"missing":" "}}},"query":{"bool":{"filter":[{"bool":{"must":[{"bool":{"should":[{"match_phrase":{"service":{"query":"web"}}},{"match_phrase":{"service":{"query":"api"}}}]}},{"bool":{"must_not":{"match_phrase":{"service":{"query":"test"}}}}}]}},{"range":{"dtEventTimeStamp":{"format":"epoch_second","from":1723593608,"include_lower":true,"include_upper":true,"to":1723679962}}}]}},"size":0}`: `{"took":4,"timed_out":false,"_shards":{"total":1,"successful":1,"skipped":0,"failed":0},"hits":{"total":{"value":1000,"relation":"eq"},"max_score":null,"hits":[]},"aggregations":{"service":{"doc_count_error_upper_bound":0,"sum_other_doc_count":0,"buckets":[{"key":"web","doc_count":500,"_value":{"value":500}},{"key":"api","doc_count":300,"_value":{"value":300}}]}}}`,
	})

	testCases := []struct {
		name     string
		query    *metadata.Query
		expected string
	}{
		{
			name: "ES聚合优化 - include参数",
			query: &metadata.Query{
				DB:          "es_index",
				Field:       "dtEventTimeStamp",
				DataSource:  structured.BkLog,
				TableID:     "es_index",
				StorageType: consul.ElasticsearchStorageType,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "status",
							Operator:      structured.ConditionEqual,
							Value:         []string{"error", "warning"},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       Count,
						Dimensions: []string{"status"},
					},
				},
			},
			expected: `[{"labels":[{"name":"__name__","value":"bklog:es_index:"},{"name":"status","value":"error"}],"samples":[{"value":450,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:es_index:"},{"name":"status","value":"warning"}],"samples":[{"value":350,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		{
			name: "ES聚合优化 - exclude参数",
			query: &metadata.Query{
				DB:          "es_index",
				Field:       "dtEventTimeStamp",
				DataSource:  structured.BkLog,
				TableID:     "es_index",
				StorageType: consul.ElasticsearchStorageType,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "level",
							Operator:      structured.ConditionNotEqual,
							Value:         []string{"debug", "trace"},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       Count,
						Dimensions: []string{"level"},
					},
				},
			},
			expected: `[{"labels":[{"name":"__name__","value":"bklog:es_index:"},{"name":"level","value":"error"}],"samples":[{"value":200,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:es_index:"},{"name":"level","value":"info"}],"samples":[{"value":400,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:es_index:"},{"name":"level","value":"warn"}],"samples":[{"value":300,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
		{
			name: "ES聚合优化 - 混合include和exclude",
			query: &metadata.Query{
				DB:          "es_index",
				Field:       "dtEventTimeStamp",
				DataSource:  structured.BkLog,
				TableID:     "es_index",
				StorageType: consul.ElasticsearchStorageType,
				AllConditions: metadata.AllConditions{
					{
						{
							DimensionName: "service",
							Operator:      structured.ConditionEqual,
							Value:         []string{"web", "api"},
						},
						{
							DimensionName: "service",
							Operator:      structured.ConditionNotEqual,
							Value:         []string{"test"},
						},
					},
				},
				Aggregates: metadata.Aggregates{
					{
						Name:       Count,
						Dimensions: []string{"service"},
					},
				},
			},
			expected: `[{"labels":[{"name":"__name__","value":"bklog:es_index:"},{"name":"service","value":"api"}],"samples":[{"value":300,"timestamp":1723593608000}],"exemplars":null,"histograms":null},{"labels":[{"name":"__name__","value":"bklog:es_index:"},{"name":"service","value":"web"}],"samples":[{"value":500,"timestamp":1723593608000}],"exemplars":null,"histograms":null}]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ss := ins.QuerySeriesSet(ctx, tc.query, defaultStart, defaultEnd)
			timeSeries, err := mock.SeriesSetToTimeSeries(ss)
			if err != nil {
				t.Errorf("Failed to convert SeriesSet to TimeSeries: %v", err)
				return
			}

			actual := timeSeries.String()
			assert.JSONEq(t, tc.expected, actual, "ES aggregation optimization should work correctly")

			t.Logf("Test case: %s", tc.name)
			t.Logf("Actual result: %s", actual)
		})
	}
}

func TestES_AggregationOptimization_Debug(t *testing.T) {
	ctx := context.Background()

	query := &metadata.Query{
		DB:          "es_index",
		Field:       "dtEventTimeStamp",
		DataSource:  structured.BkLog,
		TableID:     "es_index",
		StorageType: consul.ElasticsearchStorageType,
		AllConditions: metadata.AllConditions{
			{
				{
					DimensionName: "status",
					Operator:      structured.ConditionEqual,
					Value:         []string{"error", "warning"},
				},
			},
		},
		Aggregates: metadata.Aggregates{
			{
				Name:       Count,
				Dimensions: []string{"status"},
			},
		},
	}

	labelMap, err := buildLabelMapFromQuery(query)
	assert.NoError(t, err)
	assert.NotEmpty(t, labelMap)

	t.Logf("Generated LabelMap:")
	for key, entry := range labelMap {
		t.Logf("  %s: %v", key, entry.Values)
	}

	factory := NewFormatFactory(ctx).
		WithLabelMap(labelMap).
		WithMappings(map[string]any{
			"properties": map[string]any{
				"status": map[string]any{"type": "keyword"},
			},
		})

	name, agg, err := factory.EsAgg(query.Aggregates)
	assert.NoError(t, err)
	assert.NotNil(t, agg)
	assert.NotEmpty(t, name)

	t.Logf("Generated ES aggregation name: %s", name)
	t.Logf("ES aggregation type: %T", agg)
}
