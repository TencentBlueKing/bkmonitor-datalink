// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package consume

import (
	"container/heap"
	"time"
)

type retryItem struct {
	entry *trackedDelivery
	next  time.Time
	seq   uint64
}

type retryQueue []*retryItem

func (q retryQueue) Len() int { return len(q) }

func (q retryQueue) Less(i, j int) bool {
	if q[i].next.Equal(q[j].next) {
		return q[i].seq < q[j].seq
	}
	return q[i].next.Before(q[j].next)
}

func (q retryQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }

func (q *retryQueue) Push(value any) {
	*q = append(*q, value.(*retryItem))
}

func (q *retryQueue) Pop() any {
	old := *q
	last := len(old) - 1
	item := old[last]
	old[last] = nil
	*q = old[:last]
	return item
}

func (q *retryQueue) add(item *retryItem) {
	heap.Push(q, item)
}

func (q *retryQueue) takeReady(now time.Time) *retryItem {
	if len(*q) == 0 || (*q)[0].next.After(now) {
		return nil
	}
	return heap.Pop(q).(*retryItem)
}

func (q retryQueue) nextTime() (time.Time, bool) {
	if len(q) == 0 {
		return time.Time{}, false
	}
	return q[0].next, true
}
