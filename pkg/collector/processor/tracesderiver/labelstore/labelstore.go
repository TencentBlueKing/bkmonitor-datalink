// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package labelstore

import (
	"sync"
)

// Storage 用于存储 traces 转 metrics 指标维度
// 此场景下 labels key 相对固定且可枚举 对于所有 lbs 所有的 keys 均相同
// 因此使用 index 来记录 key 减少内存开销
type Storage struct {
	mut sync.RWMutex

	keys  []string
	store map[uint64][]valueIndex
}

type valueIndex struct {
	Value string
	Index uint8
}

func New() *Storage {
	return &Storage{
		store: make(map[uint64][]valueIndex),
	}
}

func (s *Storage) getKeyIndex(k string) uint8 {
	for i := 0; i < len(s.keys); i++ {
		if s.keys[i] == k {
			return uint8(i)
		}
	}

	s.keys = append(s.keys, k)
	return uint8(len(s.keys) - 1)
}

func (s *Storage) SetIf(h uint64, labels map[string]string) {
	s.mut.RLock()
	_, ok := s.store[h]
	s.mut.RUnlock()
	if ok {
		return
	}

	s.mut.Lock()
	defer s.mut.Unlock()

	_, ok = s.store[h]
	if ok {
		return
	}

	vis := make([]valueIndex, 0, len(labels))
	for k, v := range labels {
		vis = append(vis, valueIndex{
			Value: v,
			Index: s.getKeyIndex(k),
		})
	}

	s.store[h] = vis
}

func (s *Storage) Get(h uint64) (map[string]string, bool) {
	s.mut.RLock()
	defer s.mut.RUnlock()

	vis, ok := s.store[h]
	if !ok {
		return nil, false
	}

	ret := make(map[string]string)
	for _, vi := range vis {
		ret[s.keys[vi.Index]] = vi.Value
	}
	return ret, true
}

func (s *Storage) Del(h uint64) {
	s.mut.Lock()
	defer s.mut.Unlock()

	delete(s.store, h)
}

func (s *Storage) Exist(h uint64) bool {
	s.mut.RLock()
	defer s.mut.RUnlock()

	_, ok := s.store[h]
	return ok
}

func (s *Storage) Clean() {
	s.mut.Lock()
	defer s.mut.Unlock()

	s.keys = nil
	s.store = make(map[uint64][]valueIndex)
}
