// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package licensestore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestController(t *testing.T) {
	t.Run("base", func(t *testing.T) {
		ctr := newController()
		defer ctr.Clean()
		assert.Nil(t, ctr.Get("key1"))

		cacher := ctr.GetOrCreate("key1")
		assert.NotNil(t, cacher)

		cacher.Set("1")
		assert.True(t, cacher.Exist("1"))
	})

	t.Run("Gc", func(t *testing.T) {
		ctr := &controller{
			cached:     map[string]Cacher{},
			stop:       make(chan struct{}),
			gcInterval: time.Second,
		}
		go ctr.gc()
		defer ctr.Clean()

		cacher := ctr.GetOrCreate("key1")
		cacher.Set("1")
		ctr.GetOrCreate("key2")

		time.Sleep(time.Millisecond * 1200)
		assert.NotNil(t, ctr.Get("key1"))
		assert.NotNil(t, ctr.GetOrCreate("key1"))
		assert.Nil(t, ctr.Get("key2")) // gc
	})
}
