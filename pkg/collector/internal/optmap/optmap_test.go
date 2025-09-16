// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package optmap

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOptMap(t *testing.T) {
	tests := []struct {
		input    string
		expected map[string]int
	}{
		{
			input: `foo=1,bar=2`,
			expected: map[string]int{
				"foo": 1,
				"bar": 2,
			},
		},
		{
			input: `foo=1, bar=2`,
			expected: map[string]int{
				"foo": 1,
				"bar": 2,
			},
		},
		{
			input: `foo = 1, bar = 2`,
			expected: map[string]int{
				"foo": 1,
				"bar": 2,
			},
		},
		{
			input: `foo=1`,
			expected: map[string]int{
				"foo": 1,
			},
		},
		{
			input: `foo=1,`,
			expected: map[string]int{
				"foo": 1,
			},
		},
	}

	for _, tt := range tests {
		om := New(tt.input)
		for k, v := range tt.expected {
			i, ok := om.GetInt(k)
			assert.True(t, ok)
			assert.Equal(t, v, i)
		}
	}
}

func TestNameOpts(t *testing.T) {
	tests := []struct {
		nameOpts string
		name     string
		opts     string
	}{
		{
			nameOpts: "foo1",
			name:     "foo1",
		},
		{
			nameOpts: "foo1;",
			name:     "foo1",
		},
		{
			nameOpts: "foo1;k1=v1",
			name:     "foo1",
			opts:     "k1=v1",
		},
		{
			nameOpts: "foo1;k1=v1,k2=v2",
			name:     "foo1",
			opts:     "k1=v1,k2=v2",
		},
	}

	for _, tt := range tests {
		name, opts := NameOpts(tt.nameOpts)
		assert.Equal(t, tt.name, name)
		assert.Equal(t, tt.opts, opts)
	}
}
