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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
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
	expectedTasks map[string]struct{}
	tasks         map[string]int32
}

func isTenantDataIDTask(task string) bool {
	switch task {
	case define.ModuleBasereport,
		define.ModuleExceptionbeat,
		define.ModuleProcessbeat + "_perf",
		define.ModuleProcessbeat + "_port",
		define.ModuleGatherUpBeat:
		return true
	default:
		return false
	}
}

func newExpectedTaskSet(tasks []string) map[string]struct{} {
	expectedTasks := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		if isTenantDataIDTask(task) {
			expectedTasks[task] = struct{}{}
		}
	}
	return expectedTasks
}

func NewStorage() *Storage {
	return &Storage{
		tasks:         make(map[string]int32),
		expectedTasks: make(map[string]struct{}),
	}
}

// NewResolver returns an immutable view over expected tasks and fetched DataIDs.
func (s *Storage) NewResolver(tasks []string) DataIDResolver {
	resolver, _ := s.NewResolverSnapshot(tasks)
	return resolver
}

// NewResolverSnapshot returns an immutable resolver and its mapping revision.
func (s *Storage) NewResolverSnapshot(tasks []string) (DataIDResolver, uint64) {
	expectedTasks := newExpectedTaskSet(tasks)

	s.mut.RLock()
	defer s.mut.RUnlock()
	fetchedTasks := make(map[string]int32, len(s.tasks))
	for task, dataID := range s.tasks {
		fetchedTasks[task] = dataID
	}

	return &resolver{
		expectedTasks: expectedTasks,
		tasks:         fetchedTasks,
	}, s.revision
}

func (r *resolver) ResolveTaskDataID(task string, fallback int32) int32 {
	if _, ok := r.expectedTasks[task]; !ok {
		return fallback
	}

	if dataID, ok := r.tasks[task]; ok {
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

// NeedsRefresh reports whether expected DataIDs are missing or not yet applied.
func (s *Storage) NeedsRefresh() bool {
	s.mut.RLock()
	defer s.mut.RUnlock()

	if !s.expectedConfigured || len(s.expectedTasks) == 0 {
		return false
	}
	if s.revision != s.appliedRevision {
		return true
	}
	for task := range s.expectedTasks {
		if _, ok := s.tasks[task]; !ok {
			return true
		}
	}
	return false
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

	s.expectedTasks = newExpectedTaskSet(tasks)
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
