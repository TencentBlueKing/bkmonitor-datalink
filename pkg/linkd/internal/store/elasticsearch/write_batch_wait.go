// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"sync"
	"time"
)

// batchCallTiming 只保存逻辑调用的收集/执行边界。即使拆成多个物理请求，
// 调用的排队区间也只统计一次；取消时截断到返回时刻，不统计后台善后。
type batchCallTiming struct {
	mu                                               sync.Mutex
	enqueued, collectedFrom, collectedUntil, started time.Time
}

func (t *batchCallTiming) enqueue(at time.Time) { t.mu.Lock(); defer t.mu.Unlock(); t.enqueued = at }

func (t *batchCallTiming) collect(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.collectedFrom = at
}

func (t *batchCallTiming) ready(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.collectedUntil = at
}

func (t *batchCallTiming) execute(at time.Time) { t.mu.Lock(); defer t.mu.Unlock(); t.started = at }

func (t *batchCallTiming) waits(at time.Time) (batch, queue time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.enqueued.IsZero() {
		return 0, 0
	}
	end := func(value time.Time) time.Time {
		if value.IsZero() || value.After(at) {
			return at
		}
		return value
	}
	collectionStart := end(t.collectedFrom)
	collectionEnd := end(t.collectedUntil)
	executionStart := end(t.started)
	batch = max(0, collectionEnd.Sub(collectionStart))
	queue = max(0, collectionStart.Sub(t.enqueued)) + max(0, executionStart.Sub(collectionEnd))
	return batch, queue
}
