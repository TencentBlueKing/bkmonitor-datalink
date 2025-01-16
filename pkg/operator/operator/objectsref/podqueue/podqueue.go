// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podqueue

import (
	"sync"
)

type Action uint8

const (
	ActionAdd Action = iota
	ActionUpdate
	ActionDelete
)

type Queue struct {
	ring *ring
}

func New(size int) *Queue {
	return &Queue{ring: newRing(size)}
}

type Pod struct {
	Action    Action
	IP        string
	Name      string
	Namespace string
}

type event struct {
	id  int
	pod Pod
}

type ring struct {
	mut     sync.RWMutex
	size    int
	counter int
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

	tailIdx := r.counter % r.size
	var headIdx int
	if r.counter >= r.size {
		headIdx = (tailIdx + 1) % r.size
	}

	r.counter++
	r.tailIdx = tailIdx
	r.headIdx = headIdx

	evt.id = r.counter
	r.events[r.tailIdx] = evt
	return r.counter
}

func (r *ring) pop(n int) []event {
	r.mut.RLock()
	defer r.mut.RUnlock()

	var events []event
	for i := r.headIdx; i < r.size; i++ {
		evt := r.events[i]
		if evt.id > n {
			events = append(events, evt)
		}
	}

	if r.headIdx != 0 {
		for i := 0; i <= r.tailIdx; i++ {
			evt := r.events[i]
			if evt.id > n {
				events = append(events, evt)
			}
		}
	}
	return events
}

func (pq *Queue) Put(pod Pod) int {
	return pq.ring.put(event{
		pod: pod,
	})
}

func (pq *Queue) Pop(id int) []Pod {
	events := pq.ring.pop(id)
	pods := make([]Pod, 0, len(events))
	for _, evt := range events {
		pods = append(pods, evt.pod)
	}
	return pods
}
