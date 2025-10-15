// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package json_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
)

func TestParseJson(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]any
		wantErr  bool
	}{
		{
			name:  "test1",
			input: `{"a": "b"}`,
			expected: map[string]any{
				"__ext.a": "b",
			},
		},
		{
			name:  "normal nested json",
			input: `{"a": {"b": 1, "c": "test"}, "d": true}`,
			expected: map[string]any{
				"__ext.a.b": float64(1),
				"__ext.a.c": "test",
				"__ext.d":   true,
			},
			wantErr: false,
		},
		{
			name:  "single level json",
			input: `{"key1": "value1", "key2": 123}`,
			expected: map[string]any{
				"__ext.key1": "value1",
				"__ext.key2": float64(123), // JSON numbers are decoded as float64
			},
			wantErr: false,
		},
		{
			name:     "empty json",
			input:    `{}`,
			expected: map[string]any{},
			wantErr:  false,
		},
		{
			name:     "invalid json",
			input:    `{"key": "value"`,
			expected: map[string]any{},
			wantErr:  true,
		},
		{
			name:  "json with special characters in keys",
			input: `{"a.b": {"c-d": "value"}}`,
			expected: map[string]any{
				"__ext.a.b.c-d": "value",
			},
			wantErr: false,
		},
		{
			name:  "deeply nested json",
			input: `{"a": {"b": {"c": {"d": "value"}}}}`,
			expected: map[string]any{
				"__ext.a.b.c.d": "value",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.ParseObject("__ext", tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseObject() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if !tt.wantErr {
				assert.Equal(t, got, tt.expected)
			}
		})
	}
}

func TestParseJson_WithArrays(t *testing.T) {
	// 虽然当前实现不支持数组，但测试一下看看行为
	input := `{"a": [1, 2, 3], "b": {"c": [4, 5]}}`
	expected := map[string]any{
		"__ext.a":   []any{float64(1), float64(2), float64(3)},
		"__ext.b.c": []any{float64(4), float64(5)},
	}
	got, err := json.ParseObject("__ext", input)
	if err != nil {
		t.Errorf("ParseObject() with arrays returned error: %v", err)
		return
	}

	assert.Equal(t, got, expected)
}
