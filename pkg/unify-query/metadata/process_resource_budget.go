// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import "sync"

type ProcessResourceBudgetSnapshot struct {
	Capacity int64
	Used     int64
	Peak     int64
}

// ProcessResourceBudget is a process-wide weighted admission guard. It uses
// estimated live bytes rather than request count so concurrent heavy queries
// cannot each consume the full per-request allowance.
type ProcessResourceBudget struct {
	mu       sync.Mutex
	capacity int64
	used     int64
	peak     int64
}

func NewProcessResourceBudget(capacity int64) *ProcessResourceBudget {
	if capacity <= 0 {
		return nil
	}
	return &ProcessResourceBudget{capacity: capacity}
}

func (b *ProcessResourceBudget) Capacity() int64 {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.capacity
}

func (b *ProcessResourceBudget) SetCapacity(capacity int64) {
	if b == nil || capacity <= 0 {
		return
	}
	b.mu.Lock()
	b.capacity = capacity
	b.mu.Unlock()
}

func (b *ProcessResourceBudget) TryReserve(bytes int64) *ResourceBudgetError {
	if b == nil || bytes <= 0 {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	attempted := saturatingAdd(b.used, bytes)
	if attempted > b.capacity {
		return &ResourceBudgetError{
			Resource:  ResourceProcessCapacityBytes,
			Limit:     b.capacity,
			Attempted: attempted,
		}
	}
	b.used = attempted
	if b.used > b.peak {
		b.peak = b.used
	}
	return nil
}

func (b *ProcessResourceBudget) TryReserveUpTo(bytes int64) (int64, *ResourceBudgetError) {
	if b == nil || bytes <= 0 {
		return bytes, nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	available := b.capacity - b.used
	if available <= 0 {
		return 0, &ResourceBudgetError{
			Resource:  ResourceProcessCapacityBytes,
			Limit:     b.capacity,
			Attempted: saturatingAdd(b.used, bytes),
		}
	}
	if bytes > available {
		bytes = available
	}
	b.used += bytes
	if b.used > b.peak {
		b.peak = b.used
	}
	return bytes, nil
}

func (b *ProcessResourceBudget) Release(bytes int64) {
	if b == nil || bytes <= 0 {
		return
	}
	b.mu.Lock()
	if bytes >= b.used {
		b.used = 0
	} else {
		b.used -= bytes
	}
	b.mu.Unlock()
}

func (b *ProcessResourceBudget) Snapshot() ProcessResourceBudgetSnapshot {
	if b == nil {
		return ProcessResourceBudgetSnapshot{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return ProcessResourceBudgetSnapshot{
		Capacity: b.capacity,
		Used:     b.used,
		Peak:     b.peak,
	}
}
