// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package memcache

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCacheGetKey(t *testing.T) {
	cache, err := GetMmeCache()
	assert.NoError(t, err)

	key, val := "key", "val"
	ok := cache.Put(key, val, 1)
	assert.True(t, ok)

	cache.Wait()

	actualVal, ok := cache.Get(key)
	assert.True(t, ok)
	assert.Equal(t, val, actualVal.(string))
}

func TestCacheDeleteKey(t *testing.T) {
	cache, err := GetMmeCache()
	assert.NoError(t, err)

	key, val := "key1", "val1"
	ok := cache.Put(key, val, 1)
	assert.True(t, ok)

	cache.Wait()

	// 查询存在
	actualVal, ok := cache.Get(key)
	assert.True(t, ok)
	assert.Equal(t, val, actualVal.(string))

	// 删除
	cache.Delete(key)

	// 查询key不存在
	actualVal, ok = cache.Get(key)
	assert.False(t, ok)
	assert.Equal(t, nil, actualVal)
}
