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
	"testing"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/function"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
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
		"query with prefix and suffix": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "key-1",
						Value:         []string{"val-1"},
						Operator:      structured.ConditionEqual,
						IsPrefix:      true,
					},
					{
						DimensionName: "key-2",
						Value:         []string{"val-2"},
						Operator:      structured.ConditionEqual,
						IsSuffix:      true,
					},
					{
						DimensionName: "key-3",
						Value:         []string{"val-3"},
						Operator:      structured.ConditionEqual,
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"match_phrase_prefix":{"key-1":{"query":"val-1"}}},{"match_phrase":{"key-2":{"query":"val-2"}}},{"match_phrase":{"key-3":{"query":"val-3"}}}]}}}`,
		},
		"nested query": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "nested1.key",
						Value:         []string{"val*-1", "val\\*-2"},
						Operator:      structured.ConditionContains,
					},
					{
						DimensionName: "nested1.key",
						Value:         []string{"val-3"},
						Operator:      structured.ConditionEqual,
					},
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionExisted,
					},
				},
			},
			expected: `{"query":{"nested":{"query":{"bool":{"must":[{"bool":{"should":[{"wildcard":{"nested1.key":{"value":"*val\\*-1*"}}},{"wildcard":{"nested1.key":{"value":"*val\\*-2*"}}}]}},{"match_phrase":{"nested1.key":{"query":"val-3"}}},{"exists":{"field":"nested1.key"}}]}},"path":"nested1"}}}`,
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
		"nested not existed query": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionNotExisted,
					},
					{
						DimensionName: "nested1.name",
						Operator:      structured.ConditionExisted,
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"bool":{"must_not":{"nested":{"path":"nested1","query":{"exists":{"field":"nested1.key"}}}}}},{"nested":{"path":"nested1","query":{"exists":{"field":"nested1.name"}}}}]}}}`,
		},
		"combine nested and normal query in one group": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "keyword",
						Value:         []string{"test"},
						Operator:      structured.ConditionEqual,
					},
					{
						DimensionName: "nested1.key",
						Value:         []string{"val-1"},
						Operator:      structured.ConditionEqual,
					},
				},
				{
					{
						DimensionName: "text",
						Value:         []string{"test"},
						Operator:      structured.ConditionContains,
					},
				},
			},
			expected: `{"query":{"bool":{"should":[{"bool":{"must":[{"match_phrase":{"keyword":{"query":"test"}}},{"nested":{"query":{"match_phrase":{"nested1.key":{"query":"val-1"}}},"path":"nested1"}}]}},{"match_phrase":{"text":{"query":"test"}}}]}}}`,
		},
		"multiple nested fields in same condition group": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "nested1.name",
						Value:         []string{"test-user"},
						Operator:      structured.ConditionEqual,
					},
					{
						DimensionName: "nested2.city",
						Value:         []string{"Shanghai"},
						Operator:      structured.ConditionEqual,
					},
					{
						DimensionName: "keyword",
						Value:         []string{"normal-field"},
						Operator:      structured.ConditionEqual,
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"match_phrase":{"keyword":{"query":"normal-field"}}},{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.name":{"query":"test-user"}}}}},{"nested":{"path":"nested2","query":{"match_phrase":{"nested2.city":{"query":"Shanghai"}}}}}]}}}`,
		},
		"nested fields in different condition groups": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "nested1.name",
						Value:         []string{"test-user"},
						Operator:      structured.ConditionEqual,
					},
					{
						DimensionName: "keyword",
						Value:         []string{"group1"},
						Operator:      structured.ConditionEqual,
					},
				},
				{
					{
						DimensionName: "nested2.city",
						Value:         []string{"Shanghai"},
						Operator:      structured.ConditionEqual,
					},
					{
						DimensionName: "text",
						Value:         []string{"group2"},
						Operator:      structured.ConditionContains,
					},
				},
			},
			expected: `{"query":{"bool":{"should":[{"bool":{"must":[{"match_phrase":{"keyword":{"query":"group1"}}},{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.name":{"query":"test-user"}}}}}]}},{"bool":{"must":[{"match_phrase":{"text":{"query":"group2"}}},{"nested":{"path":"nested2","query":{"match_phrase":{"nested2.city":{"query":"Shanghai"}}}}}]}}]}}}`,
		},
		"nested fields with different levels": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "nested3.nestedChild.key",
						Value:         []string{"value"},
						Operator:      structured.ConditionEqual,
					},
					{
						DimensionName: "nested3.annotations",
						Operator:      structured.ConditionExisted,
					},
					{
						DimensionName: "keyword",
						Value:         []string{"test"},
						Operator:      structured.ConditionEqual,
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"match_phrase":{"keyword":{"query":"test"}}},{"nested":{"path":"nested3","query":{"exists":{"field":"nested3.annotations"}}}},{"nested":{"query":{"match_phrase":{"nested3.nestedChild.key":{"query":"value"}}},"path":"nested3.nestedChild"}}]}}}`,
		},
		"mixed nested and normal queries with different operators": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "nested1.age",
						Value:         []string{"18"},
						Operator:      structured.ConditionGte,
					},
					{
						DimensionName: "nested1.active",
						Operator:      structured.ConditionExisted,
					},
					{
						DimensionName: "keyword",
						Value:         []string{"value1", "value2"},
						Operator:      structured.ConditionNotEqual,
					},
					{
						DimensionName: "text",
						Value:         []string{"partial"},
						Operator:      structured.ConditionContains,
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"bool":{"must_not":[{"match_phrase":{"keyword":{"query":"value1"}}},{"match_phrase":{"keyword":{"query":"value2"}}}]}},{"match_phrase":{"text":{"query":"partial"}}},{"nested":{"path":"nested1","query":{"bool":{"must":[{"range":{"nested1.age":{"from":"18","include_lower":true,"include_upper":true,"to":null}}},{"exists":{"field":"nested1.active"}}]}}}}]}}}`,
		},
		"nested + must_not": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "nested1.age",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{""},
					},
				},
			},
			expected: `{
  "query" : {
    "nested" : {
      "path" : "nested1",
      "query" : {
        "exists" : {
          "field" : "nested1.age"
        }
      }
    }
  }
}`,
		},

		"nested_must_not_query_empty_value": {
			conditions: metadata.AllConditions{
				[]metadata.ConditionField{
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{""},
					},
				},
			},
			expected: `{"query":{"bool":{"must_not":{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.key":{"query":""}}}}}}}}`,
		},
		"nested_must_not_query_not_empty_value": {
			conditions: metadata.AllConditions{
				[]metadata.ConditionField{
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{"11"},
					},
				},
			},
			expected: `{"query":{"bool":{"must_not":{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.key":{"query":"11"}}}}}}}}`,
		},
		"nested_must_not_query_mix": {
			conditions: metadata.AllConditions{
				[]metadata.ConditionField{
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{"11"},
					},
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{""},
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"bool":{"must_not":{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.key":{"query":"11"}}}}}}},{"bool":{"must_not":{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.key":{"query":""}}}}}}}]}}}`,
		},
		"nested_must_not_query_type_mix": {
			conditions: metadata.AllConditions{
				[]metadata.ConditionField{
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{"11"},
					},
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{""},
					},
					{
						DimensionName: "nested1.active",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{""},
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"bool":{"must_not":{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.key":{"query":"11"}}}}}}},{"bool":{"must_not":{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.key":{"query":""}}}}}}},{"nested":{"path":"nested1","query":{"exists":{"field":"nested1.active"}}}}]}}}`,
		},
		"nested_must_not_query_type_mix_2": {
			conditions: metadata.AllConditions{
				[]metadata.ConditionField{
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{"11"},
					},
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionEqual,
						Value:         []string{"22"},
					},
					{
						DimensionName: "nested1.key",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{""},
					},
				},
			},
			expected: `{"query":{"bool":{"must":[{"bool":{"must_not":{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.key":{"query":"11"}}}}}}},{"bool":{"must_not":{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.key":{"query":""}}}}}}},{"nested":{"path":"nested1","query":{"match_phrase":{"nested1.key":{"query":"22"}}}}}]}}}`,
		},
		"nested_must_not_query_key_is_not_keyword_or_text": {
			conditions: metadata.AllConditions{
				[]metadata.ConditionField{
					{
						DimensionName: "nested1.active",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{""},
					},
				},
			},
			expected: `{"query":{"nested":{"path":"nested1","query":{"exists":{"field":"nested1.active"}}}}}`,
		},
		"empty key with prefix": {
			conditions: metadata.AllConditions{
				[]metadata.ConditionField{
					{
						DimensionName: "",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{"test"},
						IsPrefix:      true,
					},
				},
			},
			expected: `{"query":{"bool":{"must_not":{"multi_match":{"fields":["*","__*"],"lenient":true,"query":"test","type":"phrase_prefix"}}}}}`,
		},
		"* with prefix use": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "*",
						Operator:      structured.ConditionNotEqual,
						Value:         []string{"test"},
						IsPrefix:      true,
					},
				},
			},
			expected: `{"query":{"bool":{"must_not":{"multi_match":{"fields":["*","__*"],"lenient":true,"query":"test","type":"phrase_prefix"}}}}}`,
		},
		"dtEventTimeStamp's value is nano unix": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "dtEventTimeStamp",
						Operator:      structured.ConditionLte,
						Value:         []string{"1754466569000000002"},
					},
				},
			},
			expected: `{"query":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":null,"include_lower":true,"include_upper":true,"to":"1754466569000"}}}}`,
		},
		"dtEventTimeStamp's value is milli unix": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "dtEventTimeStamp",
						Operator:      structured.ConditionLte,
						Value:         []string{"1754466569000"},
					},
				},
			},
			expected: `{"query":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":null,"include_lower":true,"include_upper":true,"to":"1754466569000"}}}}`,
		},
		"dtEventTimeStamp's value is unix": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "dtEventTimeStamp",
						Operator:      structured.ConditionLte,
						Value:         []string{"1754466569"},
					},
				},
			},
			expected: `{"query":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":null,"include_lower":true,"include_upper":true,"to":"1754466569000"}}}}`,
		},
		"dtEventTimeStamp's value is error unix": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "dtEventTimeStamp",
						Operator:      structured.ConditionLte,
						Value:         []string{"175446656a"},
					},
				},
			},
			expected: `{"query":{"range":{"dtEventTimeStamp":{"from":null,"include_lower":true,"include_upper":true,"to":"175446656a"}}}}`,
		},
		"dtEventTimeStamp's value is string": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "dtEventTimeStamp",
						Operator:      structured.ConditionLte,
						Value:         []string{"2025-08-06T07:49:29.000000001Z"},
					},
				},
			},
			expected: `{"query":{"range":{"dtEventTimeStamp":{"format":"epoch_millis","from":null,"include_lower":true,"include_upper":true,"to":"1754466569000"}}}}`,
		},
		"dtEventTimeNanoStamp's value is string": {
			conditions: metadata.AllConditions{
				{
					{
						DimensionName: "dtEventTimeNanoStamp",
						Operator:      structured.ConditionLte,
						Value:         []string{"2025-08-06T07:49:29.000000000Z"},
					},
				},
			},
			expected: `{"query":{"range":{"dtEventTimeNanoStamp":{"format":"strict_date_optional_time_nanos","from":null,"include_lower":true,"include_upper":true,"to":"2025-08-06T07:49:29.000000000Z"}}}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			mappings := []map[string]any{
				{
					"properties": map[string]any{
						"dtEventTimeStamp": map[string]any{
							"type":           "date",
							"include_in_all": false,
							"format":         "epoch_millis",
						},
						"dtEventTimeNanoStamp": map[string]any{
							"type":   "date_nanos",
							"format": "strict_date_optional_time_nanos",
						},
						"nested1": map[string]any{
							"type": "nested",
							"properties": map[string]any{
								"key": map[string]any{
									"type": "keyword",
								},
								"name": map[string]any{
									"type": "keyword",
								},
								"age": map[string]any{
									"type": "long",
								},
								"active": map[string]any{
									"type": "boolean",
								},
							},
						},
						"nested2": map[string]any{
							"type": "nested",
							"properties": map[string]any{
								"city": map[string]any{
									"type": "keyword",
								},
								"street": map[string]any{
									"type": "keyword",
								},
							},
						},
						"nested3": map[string]any{
							"type": "nested",
							"properties": map[string]any{
								"annotations": map[string]any{
									"type": "keyword",
								},
								"nestedChild": map[string]any{
									"type": "nested",
									"properties": map[string]any{
										"key": map[string]any{
											"type": "keyword",
										},
									},
								},
							},
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

			iof := NewIndexOptionFormat(nil)
			for _, mapping := range mappings {
				iof.Parse(nil, mapping)
			}

			fact := NewFormatFactory(ctx).WithFieldMap(iof.FieldsMap())
			ss := elastic.NewSearchSource()
			query, err := fact.Query(c.conditions)
			assert.Nil(t, err)
			ss.Query(query)

			body, _ := ss.Source()
			bodyJson, _ := json.Marshal(body)
			bodyString := string(bodyJson)
			assert.NotEmpty(t, c.expected)
			assert.JSONEq(t, c.expected, bodyString)
		})
	}
}

func TestFormatFactory_WithMapping(t *testing.T) {
	testCases := []struct {
		name     string
		settings map[string]any
		mappings []map[string]any
		expected string
	}{
		{
			name: "test normal mappings",
			mappings: []map[string]any{
				{
					"properties": map[string]any{
						"nested1": map[string]any{
							"type": "nested",
							"properties": map[string]any{
								"key": map[string]any{
									"type": "keyword",
								},
							},
						},
						"keyword": map[string]any{
							"type": "keyword",
						},
					},
				},
			},
			expected: `{"keyword":{"alias_name":"","field_name":"keyword","field_type":"keyword","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"origin_field":"keyword","tokenize_on_chars":[]},"nested1":{"alias_name":"","field_name":"nested1","field_type":"nested","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"origin_field":"nested1","tokenize_on_chars":[]},"nested1.key":{"alias_name":"","field_name":"nested1.key","field_type":"keyword","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"origin_field":"nested1","tokenize_on_chars":[]}}`,
		},
		{
			name: "test old es version mapping",
			mappings: []map[string]any{
				{
					"es_type": map[string]any{
						"properties": map[string]any{
							"nested1": map[string]any{
								"type": "nested",
								"properties": map[string]any{
									"key": map[string]any{
										"type": "keyword",
									},
								},
							},
							"keyword": map[string]any{
								"type": "keyword",
							},
						},
					},
				},
			},
			expected: `{"keyword":{"alias_name":"","field_name":"keyword","field_type":"keyword","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"origin_field":"keyword","tokenize_on_chars":[]},"nested1":{"alias_name":"","field_name":"nested1","field_type":"nested","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"origin_field":"nested1","tokenize_on_chars":[]},"nested1.key":{"alias_name":"","field_name":"nested1.key","field_type":"keyword","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"origin_field":"nested1","tokenize_on_chars":[]}}`,
		},
		{
			name: "analyzer",
			settings: map[string]any{
				"analysis": map[string]any{
					"analyzer": map[string]any{
						"my_custom_analyzer": map[string]any{
							"type":      "custom",
							"tokenizer": "my_char_group_tokenizer",
							"filter":    []string{"lowercase"},
						},
						"my_custom_analyzer_1": map[string]any{
							"type":      "custom",
							"tokenizer": "my_char_group_tokenizer_1",
							"filter":    []string{"lowercase"},
						},
					},
					"tokenizer": map[string]any{
						"my_char_group_tokenizer": map[string]any{
							"type":              "char_group",
							"tokenize_on_chars": []string{"-", "\n", " "},
							"max_token_length":  512,
						},
						"my_char_group_tokenizer_1": map[string]any{
							"type":              "char_group",
							"tokenize_on_chars": []string{"-"},
							"max_token_length":  512,
						},
					},
				},
			},
			mappings: []map[string]any{
				{
					"properties": map[string]any{
						"log_message": map[string]any{
							"type":     "text",
							"analyzer": "my_custom_analyzer",
							"fields": map[string]any{
								"raw": map[string]any{
									"type": "keyword",
								},
							},
						},
						"value": map[string]any{
							"type": "double",
						},
						"event": map[string]any{
							"type": "nested",
						},
					},
				},
				{
					"properties": map[string]any{
						"log_message": map[string]any{
							"type":     "text",
							"analyzer": "my_custom_analyzer",
							"fields": map[string]any{
								"raw": map[string]any{
									"type": "keyword",
								},
							},
						},
						"value": map[string]any{
							"type": "text",
						},
						"event": map[string]any{
							"type": "nested",
						},
						"event.name": map[string]any{
							"type":       "text",
							"doc_values": true,
							"normalizer": true,
							"analyzer":   "my_custom_analyzer_1",
						},
					},
				},
			},
			expected: `{"event":{"alias_name":"","field_name":"event","field_type":"nested","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"origin_field":"event","tokenize_on_chars":[]},"event.name":{"alias_name":"","field_name":"event.name","field_type":"text","is_agg":true,"is_analyzed":true,"is_case_sensitive":true,"origin_field":"event","tokenize_on_chars":["-"]},"log_message":{"alias_name":"","field_name":"log_message","field_type":"text","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"origin_field":"log_message","tokenize_on_chars":["-","\n"," "]},"value":{"alias_name":"","field_name":"value","field_type":"double","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"origin_field":"value","tokenize_on_chars":[]}}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			iof := NewIndexOptionFormat(nil)
			for _, mapping := range tc.mappings {
				iof.Parse(tc.settings, mapping)
			}

			actual, _ := json.Marshal(iof.FieldsMap())
			assert.JSONEq(t, tc.expected, string(actual))
		})
	}
}

func TestFormatFactory_RangeQueryAndAggregates(t *testing.T) {
	start := time.Unix(1721024820, 0)
	end := time.Unix(1721046420, 0)
	timeFormat := function.Second

	for name, c := range map[string]struct {
		timeField  metadata.TimeField
		aggregates metadata.Aggregates
		expected   string
	}{
		"second date field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: function.Second,
			},
			expected: `{"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"second time field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: function.Second,
			},
			expected: `{"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"int time field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeInt,
				Unit: function.Second,
			},
			expected: `{"query":{"range":{"time":{"from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"aggregate 1d": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: function.Second,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Hour * 24,
					TimeZone:   "Asia/Shanghai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","interval":"1d","min_doc_count":0,"time_zone":"Asia/Shanghai"}}},"terms":{"field":"gseIndex","missing":" "}}},"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"aggregate 1h": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: function.Second,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Hour,
					TimeZone:   "Asia/Shanghai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","interval":"1h","min_doc_count":0,"time_zone":"Asia/Shanghai"}}},"terms":{"field":"gseIndex","missing":" "}}},"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"aggregate 1h2m": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: function.Second,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Hour + 2*time.Minute,
					TimeZone:   "Asia/Shanghai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","interval":"62m","min_doc_count":0,"time_zone":"Asia/Shanghai"}}},"terms":{"field":"gseIndex","missing":" "}}},"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"aggregate 1h12s": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: function.Second,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Hour + 12*time.Second,
					TimeZone:   "Asia/Shanghai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","interval":"3612s","min_doc_count":0,"time_zone":"Asia/Shanghai"}}},"terms":{"field":"gseIndex","missing":" "}}},"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"aggregate second time field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: function.Second,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/Shanghai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","interval":"1m","min_doc_count":0,"time_zone":"Asia/Shanghai"}}},"terms":{"field":"gseIndex","missing":" "}}},"query":{"range":{"time":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"aggregate second int field": {
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeInt,
				Unit: function.Second,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/Shanghai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"time":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420,"min":1721024820},"field":"time","interval":"60ms","min_doc_count":0}}},"terms":{"field":"gseIndex","missing":" "}}},"query":{"range":{"time":{"from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
		"aggregate millisecond int field": {
			timeField: metadata.TimeField{
				Name: "dtEventTime",
				Type: TimeFieldTypeInt,
				Unit: function.Millisecond,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/Shanghai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"dtEventTime":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420000,"min":1721024820000},"field":"dtEventTime","interval":"1m","min_doc_count":0}}},"terms":{"field":"gseIndex","missing":" "}}},"query":{"range":{"dtEventTime":{"from":1721024820000,"include_lower":true,"include_upper":true,"to":1721046420000}}}}`,
		},
		"aggregate millisecond time field": {
			timeField: metadata.TimeField{
				Name: "dtEventTime",
				Type: TimeFieldTypeTime,
				Unit: function.Millisecond,
			},
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"gseIndex"},
					Window:     time.Minute,
					TimeZone:   "Asia/Shanghai",
				},
			},
			expected: `{"aggregations":{"gseIndex":{"aggregations":{"dtEventTime":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"date_histogram":{"extended_bounds":{"max":1721046420000,"min":1721024820000},"field":"dtEventTime","interval":"1m","min_doc_count":0,"time_zone":"Asia/Shanghai"}}},"terms":{"field":"gseIndex","missing":" "}}},"query":{"range":{"dtEventTime":{"format":"epoch_second","from":1721024820,"include_lower":true,"include_upper":true,"to":1721046420}}}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			fact := NewFormatFactory(ctx).
				WithQuery("value", c.timeField, start, end, timeFormat, 0).
				WithTransform(metadata.GetFieldFormat(ctx).EncodeFunc(), metadata.GetFieldFormat(ctx).DecodeFunc())

			ss := elastic.NewSearchSource()
			rangeQuery, err := fact.RangeQuery()
			assert.Nil(t, err)
			ss.Query(rangeQuery)
			if len(c.aggregates) > 0 {
				aggName, agg, aggErr := fact.EsAgg(c.aggregates)
				assert.Nil(t, aggErr)
				if aggErr == nil {
					ss.Aggregation(aggName, agg)
				}
			}

			body, _ := ss.Source()
			bodyJson, _ := json.Marshal(body)
			bodyString := string(bodyJson)
			assert.JSONEq(t, c.expected, bodyString)
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
			expected: `{"timeseries":[{"labels":[{"name":"database_name","value":"dbRaphael"}],"samples":[{"value":649154281472,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"bak_cbs_dbRaphael_new_20220117170335_100630794"}],"samples":[{"value":917192704,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"dbRaphael_new"}],"samples":[{"value":917192704,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"time_controller_prod"}],"samples":[{"value":1905320722432,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_time"}],"samples":[{"value":158793728,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_gamerapp"}],"samples":[{"value":165445632,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"analysis"}],"samples":[{"value":109520945152,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"scrm"}],"samples":[{"value":248476893184,"timestamp":1733068800000},{"value":248476893184,"timestamp":1733155200000},{"value":248476893184,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"dbRaphaelBiz"}],"samples":[{"value":64331317248,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"boss"}],"samples":[{"value":98140160,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_gamematrix_web_manage_prod"}],"samples":[{"value":3144843264,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"opcg_backend_dev"}],"samples":[{"value":27525120,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"gmve_configcenter"}],"samples":[{"value":152535040,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"opcg_backend_pro"}],"samples":[{"value":3726671872,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_vdesk_task"}],"samples":[{"value":199426048,"timestamp":1733068800000},{"value":199426048,"timestamp":1733155200000},{"value":199426048,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_vdesk_config"}],"samples":[{"value":620756992,"timestamp":1733068800000},{"value":620756992,"timestamp":1733155200000},{"value":620756992,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_vdesk_ticket"}],"samples":[{"value":5230510080,"timestamp":1733068800000},{"value":5238898688,"timestamp":1733155200000},{"value":5251481600,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_mc"}],"samples":[{"value":130160410624,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"grafana"}],"samples":[{"value":61587456,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_vdesk_customer"}],"samples":[{"value":10132996096,"timestamp":1733068800000},{"value":10132996096,"timestamp":1733155200000},{"value":10132996096,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_xinyue_robot"}],"samples":[{"value":3490217984,"timestamp":1733068800000},{"value":3490217984,"timestamp":1733155200000},{"value":3490217984,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_cloudgames"}],"samples":[{"value":17596563456,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_scpal"}],"samples":[{"value":10747904,"timestamp":1733068800000},{"value":10747904,"timestamp":1733155200000},{"value":10747904,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_activity"}],"samples":[{"value":2097152,"timestamp":1733068800000},{"value":2097152,"timestamp":1733155200000},{"value":2097152,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_vdesk_im"}],"samples":[{"value":818462720,"timestamp":1733068800000},{"value":830914560,"timestamp":1733155200000},{"value":830914560,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"opcg_cluster"}],"samples":[{"value":20987904,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"opcg_cluster_test"}],"samples":[{"value":33882112,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"offline"}],"samples":[{"value":70555746304,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_backup"}],"samples":[{"value":20854243328,"timestamp":1733068800000},{"value":20359315456,"timestamp":1733155200000},{"value":20455784448,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_live_act"}],"samples":[{"value":1343488,"timestamp":1733068800000},{"value":1343488,"timestamp":1733155200000},{"value":1343488,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_svip_data_flow"}],"samples":[{"value":420724736,"timestamp":1733068800000},{"value":420724736,"timestamp":1733155200000},{"value":420724736,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_h5_backend"}],"samples":[{"value":145408000,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"sr_server"}],"samples":[{"value":17973248,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_mp"}],"samples":[{"value":1064960,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"games_launcher"}],"samples":[{"value":31309824,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_external_idata"}],"samples":[{"value":4674715648,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_robot_platform"}],"samples":[{"value":118902964224,"timestamp":1733068800000},{"value":118902964224,"timestamp":1733155200000},{"value":118902964224,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_self_service"}],"samples":[{"value":52723712,"timestamp":1733068800000},{"value":52723712,"timestamp":1733155200000},{"value":52723712,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"test_tv_backend_db"}],"samples":[{"value":1327104,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_ocpauth"}],"samples":[{"value":1589248,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"gmve_configcenter_cq4"}],"samples":[{"value":83312640,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"gmve_configcenter_cq5"}],"samples":[{"value":91766784,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"gmve_configcenter_nj6"}],"samples":[{"value":83378176,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"gmve_configcenter_sz3"}],"samples":[{"value":108412928,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"gmve_configcenter_tj4"}],"samples":[{"value":48627712,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"gmve_configcenter_tj7"}],"samples":[{"value":79052800,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"deploy_gmve_configcenter"}],"samples":[{"value":1556480,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"opcg_db_hub"}],"samples":[{"value":2855911424,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"paladin"}],"samples":[{"value":11010048,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"tmp_gmve_configcenter"}],"samples":[{"value":10944512,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"cloudgame"}],"samples":[{"value":83951616,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"dev_gmve_configcenter"}],"samples":[{"value":12713984,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"qa1_gmve_configcenter"}],"samples":[{"value":43220992,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"rc_gmve_configcenter"}],"samples":[{"value":25280512,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"dbXyplusService"}],"samples":[{"value":110100480,"timestamp":1733068800000},{"value":110100480,"timestamp":1733155200000},{"value":110100480,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_live_vote"}],"samples":[{"value":458752,"timestamp":1733068800000},{"value":458752,"timestamp":1733155200000},{"value":458752,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_team"}],"samples":[{"value":950272,"timestamp":1733068800000},{"value":950272,"timestamp":1733155200000},{"value":950272,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_vdesk_approval"}],"samples":[{"value":11845632,"timestamp":1733068800000},{"value":11845632,"timestamp":1733155200000},{"value":11845632,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_vdesk_satisfaction"}],"samples":[{"value":376832,"timestamp":1733068800000},{"value":376832,"timestamp":1733155200000},{"value":376832,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_vdesk_voice"}],"samples":[{"value":1343488,"timestamp":1733068800000},{"value":1343488,"timestamp":1733155200000},{"value":1343488,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_xinyue_robot_act"}],"samples":[{"value":20178796544,"timestamp":1733068800000},{"value":20178796544,"timestamp":1733155200000},{"value":20178796544,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"qa2_gmve_configcenter"}],"samples":[{"value":104644608,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"test"}],"samples":[{"value":32702464,"timestamp":1733068800000},{"value":32702464,"timestamp":1733155200000},{"value":32702464,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"sr_server_qa2"}],"samples":[{"value":30343168,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"zhiqiangli_test"}],"samples":[{"value":819200,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"sr_server_rc2"}],"samples":[{"value":1163264,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_danmu"}],"samples":[{"value":1835008,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_svip_station"}],"samples":[{"value":278528,"timestamp":1733068800000},{"value":278528,"timestamp":1733155200000},{"value":278528,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"gmve_license_center"}],"samples":[{"value":344064,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"data"}],"samples":[{"value":46137344,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_inner_idata"}],"samples":[{"value":32751616,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"opcgvirt_gmatrix"}],"samples":[{"value":170098688,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"digitalgw"}],"samples":[{"value":1333788672,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_game"}],"samples":[{"value":98304,"timestamp":1733068800000},{"value":98304,"timestamp":1733155200000},{"value":98304,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_hostPasswd"}],"samples":[{"value":77709312,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_robot_oper_closedloop"}],"samples":[{"value":104562688,"timestamp":1733068800000},{"value":104562688,"timestamp":1733155200000},{"value":104562688,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_user"}],"samples":[{"value":163840,"timestamp":1733068800000},{"value":163840,"timestamp":1733155200000},{"value":163840,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_vdesk_llm"}],"samples":[{"value":98304,"timestamp":1733068800000},{"value":98304,"timestamp":1733155200000},{"value":98304,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"db_xinyue_robot_log"}],"samples":[{"value":20356399104,"timestamp":1733068800000},{"value":20356399104,"timestamp":1733155200000},{"value":20356399104,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"dbtest"}],"samples":[{"value":114688,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"games@002dlauncher@002danalysis"}],"samples":[{"value":311296,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"gmve_license"}],"samples":[{"value":294912,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"greatwall"}],"samples":[{"value":1310720,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"codeaxis"}],"samples":[{"value":9584640,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"dev_digitalgw"}],"samples":[{"value":507904,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"dev_dunhuang"}],"samples":[{"value":294912,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"dev_gmve_recorder"}],"samples":[{"value":458752,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"dunhuang"}],"samples":[{"value":4483710976,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"sr_server_test"}],"samples":[{"value":5652480,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"tdw_export"}],"samples":[{"value":311296,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"test_gmve_recorder"}],"samples":[{"value":557056,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"tiyan_gmve_recorder"}],"samples":[{"value":229376,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"bak_cbs_cloudgame_201911131726"}],"samples":[{"value":147456,"timestamp":1733241600000}],"exemplars":null,"histograms":null},{"labels":[{"name":"database_name","value":"test_db"}],"samples":[{"value":114688,"timestamp":1733241600000}],"exemplars":null,"histograms":null}]}`,
		},
	}

	metadata.InitMetadata()
	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			fact := NewFormatFactory(ctx).
				WithQuery("", metadata.TimeField{
					Name: DefaultTimeFieldName,
					Type: DefaultTimeFieldType,
					Unit: DefaultTimeFieldUnit,
				}, time.Time{}, time.Time{}, "", 0)

			_, _, err := fact.EsAgg(c.aggregates)
			assert.NoError(t, err)

			var sr *elastic.SearchResult
			err = json.Unmarshal([]byte(c.res), &sr)
			assert.NoError(t, err)

			ts, err := fact.AggDataFormat(sr.Aggregations, nil)
			assert.NoError(t, err)

			outTs, err := json.Marshal(ts)
			assert.NoError(t, err)
			assert.JSONEq(t, string(outTs), c.expected)
		})
	}
}

func TestToFixInterval(t *testing.T) {
	tests := []struct {
		name      string
		timeUnit  string
		window    time.Duration
		want      string
		wantError bool
	}{
		{
			name:     "second unit should error",
			timeUnit: function.Second,
			window:   time.Second,
			want:     "1ms",
		},
		{
			name:      "window less than 1 should error",
			timeUnit:  function.Millisecond,
			window:    time.Microsecond, // 0.001ms
			wantError: true,
		},
		{
			name:     "microsecond unit conversion",
			timeUnit: function.Microsecond,
			window:   time.Millisecond, // 1ms = 1000us
			want:     "1s",
		},
		{
			name:     "nanosecond unit conversion",
			timeUnit: function.Nanosecond,
			window:   time.Millisecond, // 1ms = 1000000ns
			want:     "1000s",
		},
		{
			name:     "microsecond unit no conversion",
			timeUnit: function.Microsecond,
			window:   time.Minute,
			want:     "1000m",
		},
		{
			name:     "microsecond unit no conversion",
			timeUnit: function.Microsecond,
			window:   time.Hour * 6,
			want:     "250d", // 250d = 6000h
		},
		{
			name:     "nanosecond unit no conversion",
			timeUnit: function.Nanosecond,
			window:   time.Minute,
			want:     "1000000m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &FormatFactory{
				timeField: metadata.TimeField{
					Unit: tt.timeUnit,
				},
			}

			got, err := f.toFixInterval(tt.window)
			if (err != nil) != tt.wantError {
				t.Errorf("toFixInterval() error = %v, wantError %v", err, tt.wantError)
				return
			}
			if !tt.wantError && got != tt.want {
				t.Errorf("toFixInterval() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuildQuery(t *testing.T) {
	start := time.Unix(1721024820, 0)
	end := time.Unix(1721046420, 0)
	timeFormat := function.Second

	for name, c := range map[string]struct {
		query     *metadata.Query
		timeField metadata.TimeField
		expected  string
		err       error
	}{
		"collapse with other aggregations": {
			query: &metadata.Query{
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"gseIndex"},
						Window:     time.Hour,
						TimeZone:   "Asia/Shanghai",
					},
				},
				Collapse: &metadata.Collapse{
					Field: "gseIndex",
				},
			},
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: function.Second,
			},

			expected: `{
	"aggregations": {
		"gseIndex": {
			"aggregations": {
				"time": {
					"aggregations": {
						"_value": {
							"value_count": {
								"field": "value"
							}
						}
					},
					"date_histogram": {
						"extended_bounds": {
							"max": 1721046420,
							"min": 1721024820
						},
						"field": "time",
						"interval": "1h",
						"min_doc_count": 0,
						"time_zone": "Asia/Shanghai"
					}
				}
			},
			"terms": {
				"field": "gseIndex",
				"missing": " "
			}
		}
	},
	"collapse": {
		"field": "gseIndex"
	},
	"query": {
		"bool": {
			"filter": {
				"range": {
					"time": {
						"from": 1721024820,
						"include_lower": true,
						"include_upper": true,
						"to": 1721046420
					}
				}
			}
		}
	},
	"size": 0
}`,
		},
		"collapse with other aggregations 2d": {
			query: &metadata.Query{
				Aggregates: metadata.Aggregates{
					{
						Name:       "count",
						Dimensions: []string{"gseIndex"},
						Window:     time.Hour * 24 * 2,
						TimeZone:   "Asia/Shanghai",
					},
				},
				Collapse: &metadata.Collapse{
					Field: "gseIndex",
				},
			},
			timeField: metadata.TimeField{
				Name: "time",
				Type: TimeFieldTypeTime,
				Unit: function.Second,
			},

			expected: `{
	"aggregations": {
		"gseIndex": {
			"aggregations": {
				"time": {
					"aggregations": {
						"_value": {
							"value_count": {
								"field": "value"
							}
						}
					},
					"date_histogram": {
						"extended_bounds": {
							"max": 1721046420,
							"min": 1721024820
						},
						"field": "time",
						"interval": "2d",
						"min_doc_count": 0,
						"time_zone": "Asia/Shanghai"
					}
				}
			},
			"terms": {
				"field": "gseIndex",
				"missing": " "
			}
		}
	},
	"collapse": {
		"field": "gseIndex"
	},
	"query": {
		"bool": {
			"filter": {
				"range": {
					"time": {
						"from": 1721024820,
						"include_lower": true,
						"include_upper": true,
						"to": 1721046420
					}
				}
			}
		}
	},
	"size": 0
}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			fact := NewFormatFactory(ctx).
				WithQuery("value", c.timeField, start, end, timeFormat, 0).
				WithTransform(metadata.GetFieldFormat(ctx).EncodeFunc(), metadata.GetFieldFormat(ctx).DecodeFunc())

			filterQueries := []elastic.Query{
				elastic.NewRangeQuery(c.timeField.Name).
					From(start.Unix()).
					To(end.Unix()).
					IncludeLower(true).
					IncludeUpper(true),
			}

			ss := elastic.NewSearchSource()
			esQuery := elastic.NewBoolQuery().Filter(filterQueries...)
			ss.Query(esQuery).Size(c.query.Size)
			if c.query.Collapse != nil {
				ss.Collapse(elastic.NewCollapseBuilder(c.query.Collapse.Field))
			}

			name, agg, err := fact.EsAgg(c.query.Aggregates)
			if err != nil {
				assert.Equal(t, c.err, err)
				return
			}
			ss.Aggregation(name, agg)
			ss.Size(0)

			body, err := ss.Source()
			assert.Nil(t, err)

			bodyJson, err := json.Marshal(body)
			assert.Nil(t, err)

			bodyString := string(bodyJson)
			assert.JSONEq(t, c.expected, bodyString)
		})
	}
}

func TestFactory_Agg(t *testing.T) {
	testCases := map[string]struct {
		aggInfoList []any
		expected    string
	}{
		"test-1": {
			aggInfoList: []any{
				ValueAgg{
					FieldName: "value", Name: FieldValue, FuncType: Count,
				},
				ReverNested{},
			},
			expected: `{"aggregations":{"_value":{"value_count":{"field":"value"}}}}`,
		},
		"test-2": {
			aggInfoList: []any{
				ValueAgg{
					FieldName: "value", Name: FieldValue, FuncType: Count,
				},
				ReverNested{},
				TermAgg{
					Name: "name",
				},
				ReverNested{},
			},
			expected: `{"aggregations":{"name":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"terms":{"field":"name","missing":" "}}}}`,
		},
		"test-3": {
			aggInfoList: []any{
				ValueAgg{
					FieldName: "value", Name: FieldValue, FuncType: Count,
				},
				ReverNested{
					Name: DefaultReverseAggName,
				},
				TermAgg{
					Name: "events.name",
				},
				NestedAgg{
					Name: "events",
				},
			},
			expected: `{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"reverse_nested":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"reverse_nested":{}}},"terms":{"field":"events.name","missing":" "}}},"nested":{"path":"events"}}}}`,
		},
		"test-4": {
			aggInfoList: []any{
				ValueAgg{
					FieldName: "value", Name: "events.name", FuncType: Count,
				},
				NestedAgg{
					Name: "events",
				},
				TermAgg{
					Name: "events.name",
				},
				NestedAgg{
					Name: "events",
				},
			},
			expected: `{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"terms":{"field":"events.name","missing":" "}}},"nested":{"path":"events"}}}}`,
		},
		"test-5": {
			aggInfoList: []any{
				ValueAgg{
					FieldName: "value", Name: "events.name", FuncType: Count,
				},
				NestedAgg{
					Name: "events",
				},
				TermAgg{
					Name: "events.name",
				},
				NestedAgg{
					Name: "events",
				},
				TermAgg{
					Name: "city",
				},
				ReverNested{
					Name: DefaultReverseAggName,
				},
				TermAgg{
					Name: "country",
				},
				ReverNested{
					Name: DefaultReverseAggName,
				},
			},
			expected: `{"aggregations":{"country":{"aggregations":{"city":{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"_value":{"value_count":{"field":"value"}}},"terms":{"field":"events.name","missing":" "}}},"nested":{"path":"events"}}},"terms":{"field":"city","missing":" "}}},"terms":{"field":"country","missing":" "}}}}`,
		},
		"test-6": {
			aggInfoList: []any{
				ValueAgg{
					FieldName: "events.name", Name: "_value", FuncType: Count,
				},
				NestedAgg{
					Name: "events",
				},
				TermAgg{
					Name: "town",
				},
				ReverNested{
					Name: DefaultReverseAggName,
				},
				TermAgg{
					Name: "events.name",
				},
				NestedAgg{
					Name: "events",
				},
				TermAgg{
					Name: "city",
				},
				ReverNested{
					Name: DefaultReverseAggName,
				},
				TermAgg{
					Name: "country",
				},
				ReverNested{
					Name: DefaultReverseAggName,
				},
			},
			expected: `{"aggregations":{"country":{"aggregations":{"city":{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"reverse_nested":{"aggregations":{"town":{"aggregations":{"events":{"aggregations":{"_value":{"value_count":{"field":"events.name"}}},"nested":{"path":"events"}}},"terms":{"field":"town","missing":" "}}},"reverse_nested":{}}},"terms":{"field":"events.name","missing":" "}}},"nested":{"path":"events"}}},"terms":{"field":"city","missing":" "}}},"terms":{"field":"country","missing":" "}}}}`,
		},
	}
	commonMapping := []map[string]any{
		{
			"properties": map[string]any{
				"name": map[string]any{
					"type": "keyword",
				},
				"age": map[string]any{
					"type": "integer",
				},
				"events": map[string]any{
					"type": "nested",
					"properties": map[string]any{
						"name": map[string]any{
							"type": "keyword",
						},
					},
				},
			},
		},
	}

	iof := NewIndexOptionFormat(nil)
	for _, mapping := range commonMapping {
		iof.Parse(nil, mapping)
	}

	for idx, c := range testCases {
		t.Run(idx, func(t *testing.T) {
			mock.Init()
			ctx := metadata.InitHashID(context.Background())
			fact := NewFormatFactory(ctx).
				WithFieldMap(iof.FieldsMap()).
				WithTransform(metadata.GetFieldFormat(ctx).EncodeFunc(), metadata.GetFieldFormat(ctx).DecodeFunc())
			fact.valueField = "value"
			fact.aggInfoList = c.aggInfoList
			fact.resetAggInfoListWithNested()
			name, agg, err := fact.Agg()
			assert.Nil(t, err)

			sourceAgg := elastic.NewSearchSource().Aggregation(name, agg)
			source, _ := sourceAgg.Source()
			actual, _ := json.Marshal(source)

			fmt.Println(string(actual))
			t.Logf(`%s`, string(actual))
			assert.JSONEq(t, c.expected, string(actual))
		})
	}
}

func TestFormatFactory_AggregateCases(t *testing.T) {
	commonMapping := []map[string]any{
		{
			"properties": map[string]any{
				"name": map[string]any{
					"type": "keyword",
				},
				"age": map[string]any{
					"type": "integer",
				},
				"events": map[string]any{
					"type": "nested",
					"properties": map[string]any{
						"name": map[string]any{
							"type": "keyword",
						},
					},
				},
			},
		},
	}

	iof := NewIndexOptionFormat(nil)
	for _, mapping := range commonMapping {
		iof.Parse(nil, mapping)
	}

	for name, c := range map[string]struct {
		aggregates  metadata.Aggregates
		valueField  string
		expected    string
		shouldError bool
	}{
		"dims and value both nested": {
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"events.name"},
				},
			},
			valueField: "events.name",
			expected:   `{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"_value":{"value_count":{"field":"events.name"}}},"terms":{"field":"events.name","missing":" "}}},"nested":{"path":"events"}}},"size":0}`,
		},
		"value nested but dim nonnested": {
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"name"},
				},
			},
			valueField: "events.name",
			expected:   `{"aggregations":{"name":{"aggregations":{"events":{"aggregations":{"_value":{"value_count":{"field":"events.name"}}},"nested":{"path":"events"}}},"terms":{"field":"name","missing":" "}}},"size":0}`,
		},
		"dims nested but value nonnested": {
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"events.name"},
				},
			},
			valueField: "name",
			expected:   `{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"reverse_nested":{"aggregations":{"_value":{"value_count":{"field":"name"}}},"reverse_nested":{}}},"terms":{"field":"events.name","missing":" "}}},"nested":{"path":"events"}}},"size":0}`,
		},
		"dims and value both nonnested": {
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"name"},
				},
			},
			valueField: "name",
			expected:   `{"aggregations":{"name":{"aggregations":{"_value":{"value_count":{"field":"name"}}},"terms":{"field":"name","missing":" "}}},"size":0}`,
		},
		"dims seq: nested-> nonnested value: nonnested": {
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"events.name", "name"},
				},
			},
			valueField: "name",
			expected:   `{"aggregations":{"name":{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"reverse_nested":{"aggregations":{"_value":{"value_count":{"field":"name"}}},"reverse_nested":{}}},"terms":{"field":"events.name","missing":" "}}},"nested":{"path":"events"}}},"terms":{"field":"name","missing":" "}}},"size":0}`,
		},
		"dims seq: nonnested-> nested value: nonnested": {
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"name", "events.name"},
				},
			},
			valueField: "name",
			expected:   `{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"reverse_nested":{"aggregations":{"name":{"aggregations":{"_value":{"value_count":{"field":"name"}}},"terms":{"field":"name","missing":" "}}},"reverse_nested":{}}},"terms":{"field":"events.name","missing":" "}}},"nested":{"path":"events"}}},"size":0}`,
		},
		"dims seq: nonnested -> nested -> nonnested value: nested": {
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"name", "events.name", "age"},
				},
			},
			valueField: "events.name",
			expected:   `{"aggregations":{"age":{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"reverse_nested":{"aggregations":{"name":{"aggregations":{"events":{"aggregations":{"_value":{"value_count":{"field":"events.name"}}},"nested":{"path":"events"}}},"terms":{"field":"name","missing":" "}}},"reverse_nested":{}}},"terms":{"field":"events.name","missing":" "}}},"nested":{"path":"events"}}},"terms":{"field":"age"}}},"size":0}`,
		},
		"dims seq: nested -> nonnested -> nested value: nested": {
			aggregates: metadata.Aggregates{
				{
					Name:       "count",
					Dimensions: []string{"events.name", "name", "age"},
				},
			},
			valueField: "events.name",
			expected:   `{"aggregations":{"age":{"aggregations":{"name":{"aggregations":{"events":{"aggregations":{"events.name":{"aggregations":{"_value":{"value_count":{"field":"events.name"}}},"terms":{"field":"events.name","missing":" "}}},"nested":{"path":"events"}}},"terms":{"field":"name","missing":" "}}},"terms":{"field":"age"}}},"size":0}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := metadata.InitHashID(context.Background())
			fact := NewFormatFactory(ctx).
				WithQuery("", metadata.TimeField{
					Name: DefaultTimeFieldName,
					Type: DefaultTimeFieldType,
					Unit: DefaultTimeFieldUnit,
				}, time.Time{}, time.Time{}, "", 0).
				WithFieldMap(iof.FieldsMap()).
				WithTransform(metadata.GetFieldFormat(ctx).EncodeFunc(), metadata.GetFieldFormat(ctx).DecodeFunc())
			fact.valueField = c.valueField
			ss := elastic.NewSearchSource()
			aggName, agg, aggErr := fact.EsAgg(c.aggregates)
			if c.shouldError {
				assert.Error(t, aggErr)
				return
			}

			assert.NoError(t, aggErr)
			if agg != nil {
				ss.Aggregation(aggName, agg)
			}
			ss.Size(0)
			body, _ := ss.Source()
			bodyJson, _ := json.Marshal(body)
			bodyString := string(bodyJson)
			t.Logf(`Body: %s`, bodyString)
			assert.JSONEq(t, c.expected, bodyString)
		})
	}
}
