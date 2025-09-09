// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package set

import (
	"fmt"
	"strings"
	"sync"
)

type Set[T comparable] struct {
	m    map[T]struct{}
	lock sync.RWMutex
}

func New[T comparable](items ...T) *Set[T] {
	set := &Set[T]{
		m: make(map[T]struct{}),
	}
	set.Add(items...)
	return set
}

func (s *Set[T]) First() (v T) {
	s.lock.RLock()
	defer s.lock.RUnlock()
	for k := range s.m {
		v = k
		return v
	}
	return v
}

func (s *Set[T]) Size() int {
	s.lock.RLock()
	defer s.lock.RUnlock()
	size := len(s.m)
	return size
}

func (s *Set[T]) Remove(items ...T) {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, item := range items {
		delete(s.m, item)
	}
}

func (s *Set[T]) String() string {
	s.lock.RLock()
	defer s.lock.RUnlock()
	var res strings.Builder
	for k := range s.m {
		res.WriteString(fmt.Sprintf("%v", k))
	}
	return res.String()
}

func (s *Set[T]) Intersection(t *Set[T]) *Set[T] {
	nt := New[T]()
	if t == nil {
		return nt
	}

	a := s.ToArray()
	for _, i := range a {
		if t.Existed(i) {
			nt.Add(i)
		}
	}
	return nt
}

func (s *Set[T]) Add(items ...T) {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, item := range items {
		if _, ok := s.m[item]; !ok {
			s.m[item] = struct{}{}
		}
	}
}

func (s *Set[T]) Existed(item T) bool {
	s.lock.RLock()
	defer s.lock.RUnlock()
	_, ok := s.m[item]
	return ok
}

func (s *Set[T]) ToArray() []T {
	s.lock.RLock()
	defer s.lock.RUnlock()
	array := make([]T, 0, len(s.m))
	for item := range s.m {
		array = append(array, item)
	}
	return array
}

func (s *Set[T]) Clean() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.m = make(map[T]struct{})
}
