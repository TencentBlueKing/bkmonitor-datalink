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
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/cleaner"
)

// Cacher 接口定义
type Cacher interface {
	// Type 返回 Cacher 类型
	Type() string

	// Set 设置 key
	Set(k string)

	// Exist 判断 key 是否存在
	Exist(key string) bool

	// Items 返回所有 keys（不保证顺序）
	Items() []string

	// Count 返回 key 数量
	Count() int
}

type CacherController struct {
	mut        sync.RWMutex
	cached     map[string]Cacher
	stop       chan struct{}
	gcInterval time.Duration
}

func NewCacherController() *CacherController {
	cc := &CacherController{
		cached: map[string]Cacher{},
		stop:   make(chan struct{}),
	}
	go cc.gc()
	return cc
}

func (cc *CacherController) Clean() {
	close(cc.stop)
}

func (cc *CacherController) Get(k string) Cacher {
	cc.mut.RLock()
	defer cc.mut.RUnlock()

	return cc.cached[k]
}

func (cc *CacherController) GetOrCreate(k string) Cacher {
	// 先尝试获取对应的 cacher
	cc.mut.RLock()
	cacher, ok := cc.cached[k]
	cc.mut.RUnlock()
	if ok {
		return cacher
	}

	// 获取不到 写锁保护
	cc.mut.Lock()
	defer cc.mut.Unlock()

	cacher, ok = cc.cached[k]
	if !ok {
		cacher = newLocalCacher()
		cc.cached[k] = cacher
	}
	return cacher
}

func (cc *CacherController) gc() {
	d := cc.gcInterval
	if cc.gcInterval <= 0 {
		d = time.Minute
	}
	ticker := time.NewTicker(d)
	defer ticker.Stop()

	for {
		select {
		case <-cc.stop:
			return

		case <-ticker.C:
			cc.mut.Lock()
			for k, v := range cc.cached {
				if v.Count() == 0 {
					delete(cc.cached, k)
				}
			}
			cc.mut.Unlock()
		}
	}
}

var defaultCacherController = NewCacherController()

func init() {
	cleaner.Register("licensecache", CleanCacher)
}

func GetCacher(k string) Cacher {
	return defaultCacherController.Get(k)
}

func GetOrCreateCacher(k string) Cacher {
	return defaultCacherController.GetOrCreate(k)
}

func CleanCacher() error {
	defaultCacherController.Clean()
	return nil
}
