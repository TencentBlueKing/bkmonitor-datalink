// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ring

import (
	"sync"
)

// ResourceVersion 事件版次号 ring 实例内自增如果有变更操作
type ResourceVersion int

type Ring struct {
	ring *ring
}

// New 构建 *Ring 实例
// size 为 ring 容量 写操作为环形覆写 同时每次都会记录 head/tail 所在 index
// 操作为线程安全
func New(size int) *Ring {
	return &Ring{ring: newRing(size)}
}

type event struct {
	resourceVersion int
	obj             any
}

type ring struct {
	mut     sync.RWMutex
	size    int
	maxRv   int
	minRv   int
	headIdx int
	tailIdx int
	events  []event
}

func newRing(size int) *ring {
	events := make([]event, size)
	return &ring{
		size:   size,
		events: events,
	}
}

func (r *ring) put(evt event) int {
	r.mut.Lock()
	defer r.mut.Unlock()

	// 双游标方案
	// headIndex 记录 ring 内当前最早的事件 index 位置
	// tailIndex 记录 ring 内当前最新的事件 index 位置
	tailIdx := r.maxRv % r.size
	var headIdx int
	if r.maxRv >= r.size {
		headIdx = (tailIdx + 1) % r.size
	}

	r.maxRv++
	r.tailIdx = tailIdx
	r.headIdx = headIdx

	evt.resourceVersion = r.maxRv
	r.events[r.tailIdx] = evt
	r.minRv = r.events[r.headIdx].resourceVersion
	return r.maxRv
}

func (r *ring) readGt(n int) []event {
	r.mut.RLock()
	defer r.mut.RUnlock()

	var events []event

	// head 遍历为左闭右开区间
	for i := r.headIdx; i < r.size; i++ {
		evt := r.events[i]
		if evt.resourceVersion > n {
			events = append(events, evt)
		}
	}

	// headIndex 非 0 则意味着需要反向遍历一遍
	// tail 遍历为闭区间
	if r.headIdx != 0 {
		for i := 0; i <= r.tailIdx; i++ {
			evt := r.events[i]
			if evt.resourceVersion > n {
				events = append(events, evt)
			}
		}
	}
	return events
}

func (r *ring) minResourceVersion() int {
	r.mut.RLock()
	defer r.mut.RUnlock()

	return r.minRv
}

func (r *ring) maxResourceVersion() int {
	r.mut.RLock()
	defer r.mut.RUnlock()

	return r.maxRv
}

// Put 将 obj 存放至环内 同时返回当前最新版次号
func (q *Ring) Put(obj any) ResourceVersion {
	return ResourceVersion(q.ring.put(event{
		obj: obj,
	}))
}

// ReadGt 读取 > rv 的所有资源对象
func (q *Ring) ReadGt(rv ResourceVersion) []any {
	events := q.ring.readGt(int(rv))
	objs := make([]any, 0, len(events))
	for _, evt := range events {
		objs = append(objs, evt.obj)
	}
	return objs
}

func (q *Ring) MinResourceVersion() ResourceVersion {
	return ResourceVersion(q.ring.minResourceVersion())
}

func (q *Ring) MaxResourceVersion() ResourceVersion {
	return ResourceVersion(q.ring.maxResourceVersion())
}
