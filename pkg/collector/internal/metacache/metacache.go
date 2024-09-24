// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metacache

import (
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

type Cacher interface {
	Set(k string, v define.Token)
	Get(k string) (define.Token, bool)
}

var _ Cacher = (*Cache)(nil)

type Cache struct {
	mut   sync.RWMutex
	cache map[string]define.Token
}

func New() *Cache {
	return &Cache{
		cache: make(map[string]define.Token),
	}
}

func (c *Cache) Set(k string, v define.Token) {
	c.mut.Lock()
	defer c.mut.Unlock()

	c.cache[k] = v
}

func (c *Cache) Get(k string) (define.Token, bool) {
	c.mut.RLock()
	defer c.mut.RUnlock()

	v, ok := c.cache[k]
	return v, ok
}

var defaultCache = New()

// Set 调用全局 cache 实例 Set 方法
func Set(k string, v define.Token) {
	defaultCache.Set(k, v)
}

// Get 调用全局 cache 实例 Get 方法
func Get(k string) (define.Token, bool) {
	return defaultCache.Get(k)
}
