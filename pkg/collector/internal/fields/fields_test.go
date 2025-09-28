// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fields

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDecodeFieldFrom(t *testing.T) {
	tests := []struct {
		input       string
		expectedFf  FieldFrom
		expectedKey string
	}{
		{
			input:       "",
			expectedFf:  FieldFromUnknown,
			expectedKey: "",
		},
		{
			input:       "resource.s",
			expectedFf:  FieldFromResource,
			expectedKey: "s",
		},
		{
			input:       "attributes.a",
			expectedFf:  FieldFromAttributes,
			expectedKey: "a",
		},
		{
			input:       "other",
			expectedFf:  FieldFromMethod,
			expectedKey: "other",
		},
	}

	for _, tt := range tests {
		ff, key := DecodeFieldFrom(tt.input)
		assert.Equal(t, tt.expectedFf, ff)
		assert.Equal(t, tt.expectedKey, key)
	}
}

func TestTrimResourcePrefix(t *testing.T) {
	tests := []struct {
		input    []string
		expected StringOrSlice
	}{
		{
			input:    []string{"resource.s"},
			expected: []string{"s"},
		},
		{
			input:    []string{"resource.s", "resourcex.t"},
			expected: []string{"s", "resourcex.t"},
		},
		{
			input:    []string{"s"},
			expected: []string{"s"},
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, TrimResourcePrefix(tt.input...))
	}
}

func TestTrimAttributesPrefix(t *testing.T) {
	tests := []struct {
		input    []string
		expected StringOrSlice
	}{
		{
			input:    []string{"attributes.s"},
			expected: []string{"s"},
		},
		{
			input:    []string{"attributes.s", "attributesx.t"},
			expected: []string{"s", "attributesx.t"},
		},
		{
			input:    []string{"s"},
			expected: []string{"s"},
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, TrimAttributesPrefix(tt.input...))
	}
}

func TestTrimPrefix(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{
			input:    "resource.s",
			expected: "s",
		},
		{
			input:    "attributes.s",
			expected: "s",
		},
		{
			input:    "s",
			expected: "s",
		},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.expected, TrimPrefix(tt.input))
	}
}
