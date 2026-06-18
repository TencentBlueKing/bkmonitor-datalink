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
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIndexFormatFieldMap(t *testing.T) {
	testCases := []struct {
		name     string
		settings map[string]any
		mappings map[string]any

		fieldMap string
	}{
		{
			name: "test my_char_group_tokenizer",
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
			mappings: map[string]any{
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
					"case_sensitivity_test": map[string]any{
						"type":     "text",
						"analyzer": "my_custom_analyzer",
					},
					"value": map[string]any{
						"type": "double",
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
			fieldMap: `{"case_sensitivity_test":{"alias_name":"","field_name":"case_sensitivity_test","field_type":"text","origin_field":"case_sensitivity_test","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":["-","\n"," "]},"event":{"alias_name":"","field_name":"event","field_type":"nested","origin_field":"event","is_agg":false,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]},"event.name":{"alias_name":"","field_name":"event.name","field_type":"text","origin_field":"event","is_agg":true,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":["-"]},"log_message":{"alias_name":"","field_name":"log_message","field_type":"text","origin_field":"log_message","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":["-","\n"," "]},"value":{"alias_name":"","field_name":"value","field_type":"double","origin_field":"value","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"tokenize_on_chars":[]}}`,
		},
		{
			name: "nested_index_structure",
			settings: map[string]any{
				"index": map[string]any{
					"analysis": map[string]any{
						"analyzer": map[string]any{
							"analyzer_42baef3b": map[string]any{
								"filter":    []any{},
								"type":      "custom",
								"tokenizer": "tokenizer_log_data",
							},
						},
						"tokenizer": map[string]any{
							"tokenizer_log_data": map[string]any{
								"type": "char_group",
								"tokenize_on_chars": []string{
									"@", "&", "(", ")", "=", "'", "\"", ",", ";", ":",
									"<", ">", "[", "]", "{", "}", "/", " ", "\n", "\t", "\r", "\\",
								},
							},
						},
					},
				},
			},
			mappings: map[string]any{
				"properties": map[string]any{
					"log": map[string]any{
						"type":     "text",
						"norms":    false,
						"analyzer": "analyzer_42baef3b",
					},
					"path": map[string]any{
						"type": "keyword",
					},
				},
			},
			fieldMap: `{"log":{"alias_name":"","field_name":"log","field_type":"text","origin_field":"log","is_agg":false,"is_analyzed":true,"is_case_sensitive":true,"tokenize_on_chars":["@","&","(",")","=","'","\"",",",";",":","<",">","[","]","{","}","/"," ","\n","\t","\r","\\"]},"path":{"alias_name":"","field_name":"path","field_type":"keyword","origin_field":"path","is_agg":true,"is_analyzed":false,"is_case_sensitive":true,"tokenize_on_chars":[]}}`,
		},
		{
			name: "根据 normalizer 和 analyzer 判断字段大小写语义",
			settings: map[string]any{
				"analysis": map[string]any{
					"filter": map[string]any{
						"my_lowercase": map[string]any{
							"type": "lowercase",
						},
					},
					"normalizer": map[string]any{
						"keyword_lowercase": map[string]any{
							"type":   "custom",
							"filter": []string{"my_lowercase"},
						},
					},
					"analyzer": map[string]any{
						"my_standard": map[string]any{
							"type": "standard",
						},
						"case_sensitive_pattern": map[string]any{
							"type":      "pattern",
							"lowercase": false,
						},
						"index_lowercase": map[string]any{
							"type":      "custom",
							"tokenizer": "standard",
							"filter":    []string{"my_lowercase"},
						},
						"search_raw": map[string]any{
							"type":      "custom",
							"tokenizer": "standard",
							"filter":    []string{},
						},
						"custom_pattern_tokenizer": map[string]any{
							"type":      "custom",
							"tokenizer": "pattern_tokenizer",
							"filter":    []string{},
						},
					},
					"tokenizer": map[string]any{
						"pattern_tokenizer": map[string]any{
							"type":    "pattern",
							"pattern": "\\W+",
						},
					},
				},
			},
			mappings: map[string]any{
				"properties": map[string]any{
					"raw_keyword": map[string]any{
						"type": "keyword",
					},
					"normalized_keyword": map[string]any{
						"type":       "keyword",
						"normalizer": "keyword_lowercase",
					},
					"builtin_normalized_keyword": map[string]any{
						"type":       "keyword",
						"normalizer": "lowercase",
					},
					"lowercase_text": map[string]any{
						"type":            "text",
						"analyzer":        "index_lowercase",
						"search_analyzer": "index_lowercase",
					},
					"english_text": map[string]any{
						"type":     "text",
						"analyzer": "english",
					},
					"serbian_text": map[string]any{
						"type":     "text",
						"analyzer": "serbian",
					},
					"standard_type_text": map[string]any{
						"type":     "text",
						"analyzer": "my_standard",
					},
					"case_sensitive_pattern_text": map[string]any{
						"type":     "text",
						"analyzer": "case_sensitive_pattern",
					},
					"custom_pattern_tokenizer_text": map[string]any{
						"type":     "text",
						"analyzer": "custom_pattern_tokenizer",
					},
					"wildcard_field": map[string]any{
						"type": "wildcard",
					},
					"mixed_text": map[string]any{
						"type":            "text",
						"analyzer":        "index_lowercase",
						"search_analyzer": "search_raw",
					},
					"quote_sensitive_text": map[string]any{
						"type":                  "text",
						"analyzer":              "index_lowercase",
						"search_analyzer":       "index_lowercase",
						"search_quote_analyzer": "search_raw",
					},
					"unknown_analyzer_text": map[string]any{
						"type":     "text",
						"analyzer": "plugin_analyzer",
					},
				},
			},
			fieldMap: `{"builtin_normalized_keyword":{"alias_name":"","field_name":"builtin_normalized_keyword","field_type":"keyword","origin_field":"builtin_normalized_keyword","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"is_case_insensitive":true,"tokenize_on_chars":[]},"case_sensitive_pattern_text":{"alias_name":"","field_name":"case_sensitive_pattern_text","field_type":"text","origin_field":"case_sensitive_pattern_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":true,"tokenize_on_chars":[]},"custom_pattern_tokenizer_text":{"alias_name":"","field_name":"custom_pattern_tokenizer_text","field_type":"text","origin_field":"custom_pattern_tokenizer_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":true,"tokenize_on_chars":[]},"english_text":{"alias_name":"","field_name":"english_text","field_type":"text","origin_field":"english_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},"lowercase_text":{"alias_name":"","field_name":"lowercase_text","field_type":"text","origin_field":"lowercase_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},"mixed_text":{"alias_name":"","field_name":"mixed_text","field_type":"text","origin_field":"mixed_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},"normalized_keyword":{"alias_name":"","field_name":"normalized_keyword","field_type":"keyword","origin_field":"normalized_keyword","is_agg":true,"is_analyzed":false,"is_case_sensitive":false,"is_case_insensitive":true,"tokenize_on_chars":[]},"quote_sensitive_text":{"alias_name":"","field_name":"quote_sensitive_text","field_type":"text","origin_field":"quote_sensitive_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},"raw_keyword":{"alias_name":"","field_name":"raw_keyword","field_type":"keyword","origin_field":"raw_keyword","is_agg":true,"is_analyzed":false,"is_case_sensitive":true,"tokenize_on_chars":[]},"serbian_text":{"alias_name":"","field_name":"serbian_text","field_type":"text","origin_field":"serbian_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},"standard_type_text":{"alias_name":"","field_name":"standard_type_text","field_type":"text","origin_field":"standard_type_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]},"unknown_analyzer_text":{"alias_name":"","field_name":"unknown_analyzer_text","field_type":"text","origin_field":"unknown_analyzer_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":true,"tokenize_on_chars":[]},"wildcard_field":{"alias_name":"","field_name":"wildcard_field","field_type":"wildcard","origin_field":"wildcard_field","is_agg":true,"is_analyzed":false,"is_case_sensitive":true,"tokenize_on_chars":[]}}`,
		},
		{
			name: "字段未配置 analyzer 时使用当前索引的 default analyzer",
			settings: map[string]any{
				"analysis": map[string]any{
					"analyzer": map[string]any{
						"default": map[string]any{
							"type":      "custom",
							"tokenizer": "whitespace",
							"filter":    []string{},
						},
					},
				},
			},
			mappings: map[string]any{
				"properties": map[string]any{
					"default_text": map[string]any{
						"type": "text",
					},
				},
			},
			fieldMap: `{"default_text":{"alias_name":"","field_name":"default_text","field_type":"text","origin_field":"default_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":true,"tokenize_on_chars":[]}}`,
		},
		{
			name:     "缺少 analyzer settings 时保留旧版 lower fallback",
			settings: map[string]any{},
			mappings: map[string]any{
				"properties": map[string]any{
					"custom_text": map[string]any{
						"type":     "text",
						"analyzer": "custom_lowercase_analyzer",
					},
				},
			},
			fieldMap: `{"custom_text":{"alias_name":"","field_name":"custom_text","field_type":"text","origin_field":"custom_text","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"tokenize_on_chars":[]}}`,
		},
		{
			name: "根据索引版本判断是否支持 wildcard case_insensitive",
			settings: map[string]any{
				"index": map[string]any{
					"version": map[string]any{
						"created": "7140299",
					},
				},
			},
			mappings: map[string]any{
				"properties": map[string]any{
					"log": map[string]any{
						"type": "text",
					},
				},
			},
			fieldMap: `{"log":{"alias_name":"","field_name":"log","field_type":"text","origin_field":"log","is_agg":false,"is_analyzed":true,"is_case_sensitive":false,"wildcard_case_insensitive":true,"tokenize_on_chars":[]}}`,
		},
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			iof := NewIndexOptionFormat(nil)
			iof.Parse(c.settings, c.mappings)

			fieldMap := iof.FieldsMap()

			actual, _ := json.Marshal(fieldMap)

			assert.JSONEq(t, c.fieldMap, string(actual))
		})
	}
}

func TestIndexFormatDefaultAnalyzerScopedPerParse(t *testing.T) {
	iof := NewIndexOptionFormat(nil)
	iof.Parse(map[string]any{
		"analysis": map[string]any{
			"analyzer": map[string]any{
				"default": map[string]any{
					"type":      "custom",
					"tokenizer": "whitespace",
					"filter":    []string{},
				},
			},
		},
	}, map[string]any{
		"properties": map[string]any{
			"case_sensitive_log": map[string]any{"type": "text"},
		},
	})
	iof.Parse(map[string]any{}, map[string]any{
		"properties": map[string]any{
			"standard_log": map[string]any{"type": "text"},
		},
	})

	fieldsMap := iof.FieldsMap()
	assert.True(t, fieldsMap["case_sensitive_log"].IsCaseSensitive)
	assert.False(t, fieldsMap["standard_log"].IsCaseSensitive)
}

func TestIndexFormatMixedCaseSensitivityAcrossIndices(t *testing.T) {
	iof := NewIndexOptionFormat(nil)
	mappings := map[string]any{
		"properties": map[string]any{
			"log": map[string]any{"type": "text"},
		},
	}
	iof.Parse(map[string]any{
		"index": map[string]any{
			"version": map[string]any{"created": "7090099"},
			"analysis": map[string]any{
				"analyzer": map[string]any{
					"default": map[string]any{
						"type":      "custom",
						"tokenizer": "whitespace",
						"filter":    []string{},
					},
				},
			},
		},
	}, mappings)
	iof.Parse(map[string]any{
		"index": map[string]any{
			"version": map[string]any{"created": "7090099"},
		},
	}, mappings)

	field := iof.FieldsMap()["log"]
	assert.False(t, field.IsCaseSensitive)
	assert.True(t, field.IsMixedCaseSensitivity)
	assert.False(t, field.WildcardCaseInsensitive)
}

func TestIndexFormatMergeIgnoresNonStringCaseSensitivity(t *testing.T) {
	t.Run("text 与 long 同名时 long 不参与 wildcard 大小写合并", func(t *testing.T) {
		iof := NewIndexOptionFormat(nil)
		iof.Parse(map[string]any{
			"analysis": map[string]any{
				"analyzer": map[string]any{
					"case_sensitive": map[string]any{
						"type":      "custom",
						"tokenizer": "whitespace",
						"filter":    []string{},
					},
				},
			},
		}, map[string]any{
			"properties": map[string]any{
				"log": map[string]any{"type": "text", "analyzer": "case_sensitive"},
			},
		})
		iof.Parse(map[string]any{}, map[string]any{
			"properties": map[string]any{
				"log": map[string]any{"type": "long"},
			},
		})

		field := iof.FieldsMap()["log"]
		assert.Equal(t, Text, field.FieldType)
		assert.True(t, field.IsCaseSensitive)
		assert.False(t, field.IsMixedCaseSensitivity)
	})
}

func TestIndexFormatMergeCarriesLowercaseWildcardCoverage(t *testing.T) {
	iof := NewIndexOptionFormat(nil)
	iof.Parse(map[string]any{}, map[string]any{
		"properties": map[string]any{
			"message": map[string]any{"type": "keyword"},
		},
	})
	iof.Parse(map[string]any{}, map[string]any{
		"properties": map[string]any{
			"message": map[string]any{"type": "text"},
		},
	})

	field := iof.FieldsMap()["message"]
	assert.Equal(t, KeyWord, field.FieldType)
	assert.True(t, field.IsAnalyzed)
	assert.False(t, field.IsCaseSensitive)
	assert.True(t, field.IsMixedCaseSensitivity)
}
