// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package licensecache

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCache(t *testing.T) {
	cache := New()

	const n = 10
	for i := 0; i < n; i++ {
		cache.Set(strconv.Itoa(i))
	}
	for i := 0; i < n; i++ {
		assert.True(t, cache.Exist(strconv.Itoa(i)))
	}
	assert.False(t, cache.Exist("10"))

	expected := make(map[string]struct{})
	for i := 0; i < n; i++ {
		expected[strconv.Itoa(i)] = struct{}{}
	}
	for _, item := range cache.Items() {
		_, ok := expected[item]
		assert.True(t, ok)
	}
	assert.Equal(t, 10, cache.Count())
}
