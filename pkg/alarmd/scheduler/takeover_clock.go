// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// TakeoverClock is when this process took each Query Group over from
// another owner: the Slots due before that moment were due while nobody
// here could run them, and they are replayed within the replay age instead
// of being given up on for distance (classifyRecovery).
//
// A takeover is an owner epoch that another owner's epoch preceded. Every
// acquisition moves the epoch on, this process's own included: a Runner
// rebuilt here -- a schedule revision, a reactivation, a Query Group leaving
// the degraded pool -- releases and acquires again, one epoch later, with
// nobody else in between. That is not a takeover, and the Query Group keeps
// the moment it was taken over; the Slots it fell behind on since are this
// owner's own, and the distance rule keeps it fresh on them as before. An
// epoch more than one past the last one held here, or the first one held
// here at all -- a restart, a rebalance that moved the Query Group to this
// process -- is a takeover, and the moment is the one it is first seen at.
//
// Entries are kept for every Query Group this process has held, one epoch
// and one instant each; a Query Group that leaves and comes back after
// another owner is told apart by its epoch, so nothing needs to forget them.
type TakeoverClock struct {
	mu    sync.Mutex
	owned map[execution.QueryGroupIdentity]takeover
}

type takeover struct {
	epoch uint64
	at    time.Time
	// classified is the latest Slot reported as due before the takeover, by
	// outcome (FirstClassification).
	classified map[string]execution.EvaluationTime
}

// NewTakeoverClock is an empty clock: every Query Group's first epoch here
// is a takeover.
func NewTakeoverClock() *TakeoverClock {
	return &TakeoverClock{owned: make(map[execution.QueryGroupIdentity]takeover)}
}

// Anchor is when this process took the Query Group over, for the owner
// epoch it holds it under now, noting a new takeover at now when the epoch
// says there was one. Zero on a nil clock or an epoch not yet known: then no
// Slot is due before a takeover, and the distance rule applies to every one.
func (clock *TakeoverClock) Anchor(queryGroup execution.QueryGroupIdentity, fence execution.OwnerFence, now time.Time) time.Time {
	if clock == nil || fence.OwnerEpoch == 0 {
		return time.Time{}
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	last, held := clock.owned[queryGroup]
	switch {
	case held && fence.OwnerEpoch == last.epoch:
		return last.at
	case held && fence.OwnerEpoch == last.epoch+1:
		// Acquired again right after this process's own epoch: rebuilt
		// here, not taken over.
		clock.owned[queryGroup] = takeover{epoch: fence.OwnerEpoch, at: last.at, classified: last.classified}
		return last.at
	default:
		clock.owned[queryGroup] = takeover{epoch: fence.OwnerEpoch, at: now}
		return now
	}
}

// FirstClassification reports whether this is the first time the Slot at
// evaluationTime is reported with this outcome since the Query Group was
// taken over, and remembers that it was. A Slot classified again -- its
// replay failed and is retried, or its Runner woke before it ran -- is one
// Slot, and counted once; one replayed and later given up on for its age is
// counted once under each. A Query Group's Slots are classified in
// evaluation order, so the latest reported is all it keeps. True on a nil
// clock and for a Query Group it holds no takeover of.
func (clock *TakeoverClock) FirstClassification(queryGroup execution.QueryGroupIdentity, evaluationTime execution.EvaluationTime, outcome string) bool {
	if clock == nil {
		return true
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	last, held := clock.owned[queryGroup]
	if !held {
		return true
	}
	if evaluationTime <= last.classified[outcome] {
		return false
	}
	if last.classified == nil {
		last.classified = make(map[string]execution.EvaluationTime, 2)
	}
	last.classified[outcome] = evaluationTime
	clock.owned[queryGroup] = last
	return true
}
