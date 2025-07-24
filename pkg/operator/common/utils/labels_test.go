// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMatchSubLabels(t *testing.T) {
	t.Run("Match/Less", func(t *testing.T) {
		subset := map[string]string{
			"k1": "v1",
			"k2": "v2",
		}
		set := map[string]string{
			"k1": "v1",
			"k2": "v2",
			"k3": "v3",
		}

		assert.True(t, MatchSubLabels(subset, set))
	})

	t.Run("Match/Equal", func(t *testing.T) {
		subset := map[string]string{
			"k1": "v1",
			"k2": "v2",
		}
		set := map[string]string{
			"k1": "v1",
			"k2": "v2",
		}

		assert.True(t, MatchSubLabels(subset, set))
	})

	t.Run("Match/Greater", func(t *testing.T) {
		subset := map[string]string{
			"k1": "v1",
			"k2": "v2",
			"k3": "v3",
		}
		set := map[string]string{
			"k1": "v1",
			"k2": "v2",
		}

		assert.False(t, MatchSubLabels(subset, set))
	})
}

func TestMapToSelector(t *testing.T) {
	tests := []struct {
		input    map[string]string
		expected string
	}{
		{
			input:    map[string]string{},
			expected: "",
		},
		{
			input:    map[string]string{"key1": "value1"},
			expected: "key1=value1",
		},
		{
			input:    map[string]string{"key1": "value1", "key2": "value2"},
			expected: "key1=value1,key2=value2",
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := MapToSelector(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}

func TestSelectorToMap(t *testing.T) {
	tests := []struct {
		input    string
		expected map[string]string
	}{
		{
			input:    "",
			expected: map[string]string{},
		},
		{
			input:    "key1=value1",
			expected: map[string]string{"key1": "value1"},
		},
		{
			input: "__meta_kubernetes_endpoint_address_target_name=^eklet-.*,__meta_kubernetes_endpoint_address_target_kind=Node",
			expected: map[string]string{
				"__meta_kubernetes_endpoint_address_target_name": "^eklet-.*",
				"__meta_kubernetes_endpoint_address_target_kind": "Node",
			},
		},
		{
			input: "__meta_kubernetes_endpoint_address_target_name=^eklet-.*,,",
			expected: map[string]string{
				"__meta_kubernetes_endpoint_address_target_name": "^eklet-.*",
			},
		},
		{
			input:    "key1=value1,key2",
			expected: map[string]string{"key1": "value1"},
		},
		{
			input: "foo=bar, , ,k1=v1 ",
			expected: map[string]string{
				"foo": "bar",
				"k1":  "v1",
			},
		},
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			got := SelectorToMap(tt.input)
			assert.Equal(t, tt.expected, got)
		})
	}
}
