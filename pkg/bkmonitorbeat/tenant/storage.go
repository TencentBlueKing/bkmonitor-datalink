// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package tenant

import (
	"reflect"
	"sync"
)

type Storage struct {
	mut                sync.RWMutex
	tasks              map[string]int32 // 与 gse 通信获取
	expectedTasks      map[string]struct{}
	expectedConfigured bool
	revision           uint64
	appliedRevision    uint64
}

// DataIDResolver resolves task DataIDs against a fixed expected-task view.
type DataIDResolver interface {
	ResolveTaskDataID(task string, fallback int32) int32
}

type resolver struct {
	storage       *Storage
	expectedTasks map[string]struct{}
}

func NewStorage() *Storage {
	return &Storage{
		tasks:         make(map[string]int32),
		expectedTasks: make(map[string]struct{}),
	}
}

// NewResolver returns an isolated expected-task view over fetched DataIDs.
func (s *Storage) NewResolver(tasks []string) DataIDResolver {
	expectedTasks := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		expectedTasks[task] = struct{}{}
	}
	return &resolver{
		storage:       s,
		expectedTasks: expectedTasks,
	}
}

func (r *resolver) ResolveTaskDataID(task string, fallback int32) int32 {
	if _, ok := r.expectedTasks[task]; !ok {
		return fallback
	}

	r.storage.mut.RLock()
	defer r.storage.mut.RUnlock()

	if dataID, ok := r.storage.tasks[task]; ok {
		return dataID
	}
	return 0
}

// Revision returns the current mapping revision.
func (s *Storage) Revision() uint64 {
	s.mut.RLock()
	defer s.mut.RUnlock()

	return s.revision
}

// MarkApplied records the newest mapping revision used by a successful reload.
func (s *Storage) MarkApplied(revision uint64) {
	s.mut.Lock()
	defer s.mut.Unlock()

	if s.expectedConfigured {
		for task := range s.tasks {
			if _, ok := s.expectedTasks[task]; !ok {
				delete(s.tasks, task)
			}
		}
	}
	if revision > s.appliedRevision && revision <= s.revision {
		s.appliedRevision = revision
	}
}

func (s *Storage) GetTaskDataID(task string) (int32, bool) {
	s.mut.RLock()
	defer s.mut.RUnlock()

	if s.expectedConfigured {
		if _, ok := s.expectedTasks[task]; !ok {
			return 0, false
		}
	}
	dst, ok := s.tasks[task]
	return dst, ok
}

// SetExpectedTasks records tasks that must use tenant DataIDs.
func (s *Storage) SetExpectedTasks(tasks []string) {
	s.mut.Lock()
	defer s.mut.Unlock()

	expectedTasks := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		expectedTasks[task] = struct{}{}
	}

	s.expectedTasks = expectedTasks
	s.expectedConfigured = true
}

// ResolveTaskDataID returns a fetched tenant DataID when present. An expected
// task without a mapping is disabled instead of falling back to a static ID.
func (s *Storage) ResolveTaskDataID(task string, fallback int32) int32 {
	s.mut.RLock()
	defer s.mut.RUnlock()

	if s.expectedConfigured {
		if _, ok := s.expectedTasks[task]; !ok {
			return fallback
		}
		if dataID, ok := s.tasks[task]; ok {
			return dataID
		}
		return 0
	}
	if dataID, ok := s.tasks[task]; ok {
		return dataID
	}
	return fallback
}

// UpdateTaskDataIDs replaces mappings with the latest Metadata response.
func (s *Storage) UpdateTaskDataIDs(tasks map[string]int32) bool {
	s.mut.Lock()
	defer s.mut.Unlock()

	nextTasks := make(map[string]int32, len(tasks))
	for task, dataID := range tasks {
		if s.expectedConfigured {
			if _, ok := s.expectedTasks[task]; !ok {
				continue
			}
		}
		nextTasks[task] = dataID
	}
	if !reflect.DeepEqual(s.tasks, nextTasks) {
		s.tasks = nextTasks
		s.revision++
		return true
	}
	return s.revision != s.appliedRevision
}

var defaultStorage = NewStorage()

func DefaultStorage() *Storage {
	return defaultStorage
}
