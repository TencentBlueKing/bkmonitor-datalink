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
)

type Manager struct {
	mut        sync.RWMutex
	caches     map[string]*Cache
	stop       chan struct{}
	gcInterval time.Duration
}

func NewManager() *Manager {
	cc := &Manager{
		caches: map[string]*Cache{},
		stop:   make(chan struct{}),
	}
	go cc.gc()
	return cc
}

func (mgr *Manager) Clean() {
	close(mgr.stop)
}

func (mgr *Manager) Get(k string) *Cache {
	mgr.mut.RLock()
	defer mgr.mut.RUnlock()

	return mgr.caches[k]
}

func (mgr *Manager) GetOrCreate(k string) *Cache {
	// 先尝试获取
	mgr.mut.RLock()
	cache, ok := mgr.caches[k]
	mgr.mut.RUnlock()
	if ok {
		return cache
	}

	// 获取不到 写锁保护
	mgr.mut.Lock()
	defer mgr.mut.Unlock()

	cache, ok = mgr.caches[k]
	if !ok {
		cache = New()
		mgr.caches[k] = cache
	}
	return cache
}

func (mgr *Manager) gc() {
	d := mgr.gcInterval
	if mgr.gcInterval <= 0 {
		d = time.Minute
	}
	ticker := time.NewTicker(d)
	defer ticker.Stop()

	for {
		select {
		case <-mgr.stop:
			return

		case <-ticker.C:
			mgr.mut.Lock()
			for k, v := range mgr.caches {
				if v.Count() == 0 {
					delete(mgr.caches, k)
				}
			}
			mgr.mut.Unlock()
		}
	}
}
