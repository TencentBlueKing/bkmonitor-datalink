// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package forwarder

import (
	"sort"
	"sync/atomic"
	"time"
)

type EndpointNotifier struct {
	sets map[string]struct{}
	ch   chan Event
	stop atomic.Bool
}

func NewEventNotifier() *EndpointNotifier {
	return &EndpointNotifier{
		sets: map[string]struct{}{},
		ch:   make(chan Event, 1024),
	}
}

func (s *EndpointNotifier) Stop() {
	s.stop.Store(true)
	timer := time.NewTimer(time.Second)
	defer timer.Stop()

Outer:
	for {
		select {
		case <-timer.C:
			break Outer
		case <-s.ch: // 排空
		}
	}
	close(s.ch)
}

func (s *EndpointNotifier) Watch() <-chan Event {
	return s.ch
}

func (s *EndpointNotifier) Sync(endpoints []string) {
	if s.stop.Load() {
		return
	}

	eps := make(map[string]struct{})
	for _, ep := range endpoints {
		eps[ep] = struct{}{}
	}

	modify := make(map[string]EventType)
	for ep := range eps {
		if _, ok := s.sets[ep]; !ok {
			modify[ep] = EventTypeAdd
		}
	}

	for ep := range s.sets {
		if _, ok := eps[ep]; !ok {
			modify[ep] = EventTypeDelete
		}
	}
	s.sets = eps

	keys := make([]string, 0, len(modify))
	for ep := range modify {
		keys = append(keys, ep)
	}
	sort.Strings(keys)

	for _, ep := range keys {
		s.ch <- Event{
			Type:     modify[ep],
			Endpoint: ep,
		}
	}
}
