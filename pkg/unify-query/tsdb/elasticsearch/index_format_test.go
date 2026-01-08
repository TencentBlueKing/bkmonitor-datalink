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
	}

	for _, c := range testCases {
		t.Run(c.name, func(t *testing.T) {
			iof := NewIndexOptionFormat(nil)
			iof.Parse(c.settings, c.mappings)

			fieldMap := iof.FieldsMap()

			actual, _ := json.Marshal(fieldMap)

			assert.Equal(t, c.fieldMap, string(actual))
		})
	}
}
