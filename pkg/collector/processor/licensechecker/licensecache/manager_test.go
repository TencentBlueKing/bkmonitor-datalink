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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestCacheManager(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		mgr := NewManager()
		defer mgr.Clean()
		assert.Nil(t, mgr.Get("key1"))

		cache := mgr.GetOrCreate("key1")
		assert.NotNil(t, cache)

		cache.Set("1")
		assert.True(t, cache.Exist("1"))
	})

	t.Run("Gc", func(t *testing.T) {
		mgr := &Manager{
			caches:     map[string]*Cache{},
			stop:       make(chan struct{}),
			gcInterval: time.Second,
		}
		go mgr.gc()
		defer mgr.Clean()

		cache := mgr.GetOrCreate("key1")
		cache.Set("1")
		mgr.GetOrCreate("key2")

		time.Sleep(time.Millisecond * 1200)
		assert.NotNil(t, mgr.Get("key1"))
		assert.NotNil(t, mgr.GetOrCreate("key1"))
		assert.Nil(t, mgr.Get("key2")) // gc
	})
}
