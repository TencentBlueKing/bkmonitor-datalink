// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package mapx

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestIsMapKey
func TestIsMapKey(t *testing.T) {
	assert.True(t, IsMapKey("a", map[string]any{"a": "a", "b": "b"}))
	assert.False(t, IsMapKey("a", map[string]any{"a1": "a"}))
}

func TestSetDefault(t *testing.T) {
	testSuite := []struct {
		name     string
		src      map[string]any
		key      string
		value    any
		expected string
	}{
		{name: "null map", src: map[string]any{}, key: "test1", value: "test"},
		{name: "map is {tInit: 1}", src: map[string]any{}, key: "test1", value: "test"},
		{name: "null map", src: map[string]any{}, key: "test1", value: []string{}},
	}
	for _, tt := range testSuite {
		t.Run(tt.name, func(t *testing.T) {
			SetDefault(&tt.src, tt.key, tt.value)
			assert.Equal(t, tt.src[tt.key], tt.value)
		})
	}
}
