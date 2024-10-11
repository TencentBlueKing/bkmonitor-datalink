// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tracestore

import (
	"sync"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

type TraceKey struct {
	TraceID pcommon.TraceID
	SpanID  pcommon.SpanID
}

func (tk TraceKey) Bytes() []byte {
	b := make([]byte, 0, 24)

	tb := tk.TraceID.Bytes()
	b = append(b, tb[:]...)
	sb := tk.SpanID.Bytes()
	b = append(b, sb[:]...)
	return b
}

// Storage 使用内置 map 作为存储载体存储 traces
type Storage struct {
	mut   sync.RWMutex
	store map[TraceKey]ptrace.Traces
}

func New() *Storage {
	return &Storage{
		store: map[TraceKey]ptrace.Traces{},
	}
}

func (s *Storage) Clean() {
	s.mut.Lock()
	defer s.mut.Unlock()

	s.store = make(map[TraceKey]ptrace.Traces)
}

func (s *Storage) Get(k TraceKey) (ptrace.Traces, bool) {
	s.mut.RLock()
	defer s.mut.RUnlock()

	v, ok := s.store[k]
	return v, ok
}

func (s *Storage) Set(k TraceKey, v ptrace.Traces) {
	s.mut.RLock()
	_, ok := s.store[k]
	s.mut.RUnlock()
	if ok {
		return
	}

	s.mut.Lock()
	s.store[k] = v
	s.mut.Unlock()
}

func (s *Storage) Del(k TraceKey) {
	s.mut.Lock()
	defer s.mut.Unlock()

	delete(s.store, k)
}
