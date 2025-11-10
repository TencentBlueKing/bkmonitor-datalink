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
	mut   sync.Mutex
	tasks map[string]int32 // 与 gse 通信获取
}

func NewStorage() *Storage {
	return &Storage{
		tasks: make(map[string]int32),
	}
}

func (s *Storage) GetTaskDataID(task string) (int32, bool) {
	s.mut.Lock()
	defer s.mut.Unlock()

	dst, ok := s.tasks[task]
	return dst, ok
}

func (s *Storage) UpdateTaskDataIDs(tasks map[string]int32) bool {
	s.mut.Lock()
	defer s.mut.Unlock()

	if reflect.DeepEqual(tasks, s.tasks) {
		return false
	}
	s.tasks = tasks
	return true
}

var defaultStorage = NewStorage()

func DefaultStorage() *Storage {
	return defaultStorage
}
