// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package segmented

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
)

type Item struct {
	Min int64
	Max int64
}

type Segmented interface {
	Name() string
	Count() int32
	Add(time int64)
	List() []*Item
	String() string
}

type segmented struct {
	ctx   context.Context
	name  string
	count int32
	list  []*Item

	lastT *int64
	lock  sync.RWMutex
}

func (s *segmented) String() string {
	arr := make([]string, 0, atomic.LoadInt32(&s.count))
	for _, l := range s.List() {
		arr = append(arr, fmt.Sprintf("%d-%d", l.Min, l.Max))
	}
	return strings.Join(arr, ", ")
}

func (s *segmented) Name() string {
	return s.name
}

func (s *segmented) List() []*Item {
	s.lock.RLock()
	defer s.lock.RUnlock()
	return s.list
}

func (s *segmented) Count() int32 {
	return atomic.LoadInt32(&s.count)
}

func (s *segmented) Add(t int64) {
	s.lock.Lock()
	if s.lastT != nil {
		s.list = append(s.list, &Item{
			Min: *s.lastT,
			Max: t,
		})
		s.intCount()
	}
	s.lastT = &t
	s.lock.Unlock()
}

func (s *segmented) intCount() {
	atomic.AddInt32(&s.count, 1)
}

func (s *segmented) decCount() {
	atomic.AddInt32(&s.count, -1)
}

func NewSegmented(ctx context.Context, name string) Segmented {
	s := &segmented{
		ctx:  ctx,
		name: name,
		list: make([]*Item, 0),
	}
	return s
}
