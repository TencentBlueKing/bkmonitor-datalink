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
	"sync"
)

type Storage struct {
	mut                sync.RWMutex
	tasks              map[string]int32 // 与 gse 通信获取
	expectedTasks      map[string]struct{}
	expectedConfigured bool
}

func NewStorage() *Storage {
	return &Storage{
		tasks:         make(map[string]int32),
		expectedTasks: make(map[string]struct{}),
	}
}

func (s *Storage) GetTaskDataID(task string) (int32, bool) {
	s.mut.RLock()
	defer s.mut.RUnlock()

	dst, ok := s.tasks[task]
	return dst, ok
}

// SetExpectedTasks records tasks that must use tenant DataIDs. Existing mappings
// are retained only when the task remains configured.
func (s *Storage) SetExpectedTasks(tasks []string) {
	s.mut.Lock()
	defer s.mut.Unlock()

	expectedTasks := make(map[string]struct{}, len(tasks))
	for _, task := range tasks {
		expectedTasks[task] = struct{}{}
	}
	for task := range s.tasks {
		if _, ok := expectedTasks[task]; !ok {
			delete(s.tasks, task)
		}
	}

	s.expectedTasks = expectedTasks
	s.expectedConfigured = true
}

// ResolveTaskDataID returns a fetched tenant DataID when present. An expected
// task without a mapping is disabled instead of falling back to a static ID.
func (s *Storage) ResolveTaskDataID(task string, fallback int32) int32 {
	s.mut.RLock()
	defer s.mut.RUnlock()

	if dataID, ok := s.tasks[task]; ok {
		return dataID
	}
	if _, ok := s.expectedTasks[task]; ok {
		return 0
	}
	return fallback
}

func (s *Storage) UpdateTaskDataIDs(tasks map[string]int32) bool {
	s.mut.Lock()
	defer s.mut.Unlock()

	updated := false
	for task, dataID := range tasks {
		if s.expectedConfigured {
			if _, ok := s.expectedTasks[task]; !ok {
				continue
			}
		}
		if oldDataID, ok := s.tasks[task]; ok && oldDataID == dataID {
			continue
		}
		s.tasks[task] = dataID
		updated = true
	}
	return updated
}

var defaultStorage = NewStorage()

func DefaultStorage() *Storage {
	return defaultStorage
}
