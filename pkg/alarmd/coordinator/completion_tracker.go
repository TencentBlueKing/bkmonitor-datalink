// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package coordinator

import (
	"errors"
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

type completionEntry struct {
	offset   int64
	receipt  *contract.MessageReceiptV1
	complete bool
}

// PartitionCompletionTracker advances only over the completed prefix of
// messages registered in broker delivery order. Kafka numeric offset gaps are
// deliberately irrelevant.
type PartitionCompletionTracker struct {
	mu sync.Mutex

	entries      []*completionEntry
	byOffset     map[int64]*completionEntry
	lastOffset   int64
	hasOffset    bool
	pendingCount int
	pendingNext  int64
}

func NewPartitionCompletionTracker() *PartitionCompletionTracker {
	return &PartitionCompletionTracker{byOffset: make(map[int64]*completionEntry)}
}

func (tracker *PartitionCompletionTracker) Register(offset int64) error {
	if tracker == nil || offset < 0 {
		return errors.New("alarmd coordinator: completion tracker requires a non-negative offset")
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.hasOffset && offset <= tracker.lastOffset {
		return errors.New("alarmd coordinator: offsets must be registered once in delivery order")
	}
	entry := &completionEntry{offset: offset}
	tracker.entries = append(tracker.entries, entry)
	tracker.byOffset[offset] = entry
	tracker.lastOffset = offset
	tracker.hasOffset = true
	return nil
}

func (tracker *PartitionCompletionTracker) Complete(offset int64, receipt *contract.MessageReceiptV1) error {
	if tracker == nil {
		return errors.New("alarmd coordinator: completion tracker is required")
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	entry, ok := tracker.byOffset[offset]
	if !ok {
		return errors.New("alarmd coordinator: cannot complete an unregistered offset")
	}
	if entry.complete {
		return errors.New("alarmd coordinator: offset is already complete")
	}
	entry.complete = true
	entry.receipt = receipt
	return nil
}

func (tracker *PartitionCompletionTracker) requireRetryable(offset int64) error {
	if tracker == nil {
		return errors.New("alarmd coordinator: completion tracker is required")
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	entry, ok := tracker.byOffset[offset]
	if !ok {
		return errors.New("alarmd coordinator: cannot retry an unregistered offset")
	}
	if entry.complete {
		return errors.New("alarmd coordinator: completed offset only permits commit retry")
	}
	return nil
}

func (tracker *PartitionCompletionTracker) NextCommit() (int64, []*contract.MessageReceiptV1, bool) {
	if tracker == nil {
		return 0, nil, false
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.pendingCount == 0 {
		for tracker.pendingCount < len(tracker.entries) && tracker.entries[tracker.pendingCount].complete {
			tracker.pendingCount++
		}
		if tracker.pendingCount == 0 {
			return 0, nil, false
		}
		tracker.pendingNext = tracker.entries[tracker.pendingCount-1].offset + 1
	}
	receipts := make([]*contract.MessageReceiptV1, tracker.pendingCount)
	for index := 0; index < tracker.pendingCount; index++ {
		receipts[index] = tracker.entries[index].receipt
	}
	return tracker.pendingNext, receipts, true
}

// CommitACK removes exactly the prefix frozen by NextCommit. Callers must use
// it only after the broker acknowledges the corresponding next offset.
func (tracker *PartitionCompletionTracker) CommitACK(nextOffset int64) error {
	if tracker == nil {
		return errors.New("alarmd coordinator: completion tracker is required")
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.pendingCount == 0 || nextOffset != tracker.pendingNext {
		return errors.New("alarmd coordinator: offset ACK does not match the pending completed prefix")
	}
	for index := 0; index < tracker.pendingCount; index++ {
		delete(tracker.byOffset, tracker.entries[index].offset)
	}
	copy(tracker.entries, tracker.entries[tracker.pendingCount:])
	tracker.entries = tracker.entries[:len(tracker.entries)-tracker.pendingCount]
	tracker.pendingCount = 0
	tracker.pendingNext = 0
	return nil
}

func (tracker *PartitionCompletionTracker) Len() int {
	if tracker == nil {
		return 0
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	return len(tracker.entries)
}
