// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"context"
	"sync"

	"github.com/emirpasic/gods/lists/arraylist"
)

// HookFunction :
type HookFunction func(ctx context.Context)

// HookManager :
type HookManager struct {
	lock  sync.RWMutex
	hooks arraylist.List
}

// Add :
func (m *HookManager) Add(hook HookFunction) {
	m.lock.Lock()
	defer m.lock.Unlock()
	m.hooks.Add(hook)
}

// Apply :
func (m *HookManager) Apply(ctx context.Context) {
	m.lock.RLock()
	defer m.lock.RUnlock()
	iter := m.hooks.Iterator()

	for iter.Next() {
		hook := iter.Value().(HookFunction)
		hook(ctx)
	}
}
