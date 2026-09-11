// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"math"
	"testing"
	"time"
)

// A window is opened while the deployment is running, so the first rounds it
// sees are rounds that were queued before anyone was watching. Those carry no
// queued-at stamp, and measuring from the zero time overflows the duration and
// saturates at MaxInt64 -- which the page rendered as "排队等了 9223372036.85 s"
// beside the round's real facts.
//
// Nothing marks that value as wrong. It is a number, in the right units, in the
// right place, and the only way to know it is meaningless is to recognise
// MaxInt64 nanoseconds on sight.
func TestARoundQueuedBeforeTheWindowOpenedReportsNoWaitRatherThanMaxInt64(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 8, 30, 0, time.UTC)

	facts := dispatchQueueFacts(time.Time{}, at)

	if facts.QueueWaitNS != 0 || facts.QueuedAtMS != 0 {
		t.Fatalf("facts = %+v, want no wait reported for a round nobody timed", facts)
	}
	if facts.QueueWaitNS == math.MaxInt64 {
		t.Fatal("the saturated duration reached the record")
	}
	// The decision still travels: the round was dispatched, and that is a fact
	// about it whether or not anyone timed the wait.
	if facts.Decision != "execution_slot_acquired" {
		t.Fatalf("decision = %q, want the dispatch still reported", facts.Decision)
	}
}

// A round that was queued while it was being observed reports the real wait.
// Without this the fix above could be "report nothing, ever" and still pass.
func TestARoundQueuedUnderObservationReportsItsRealWait(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 8, 30, 0, time.UTC)
	queuedAt := at.Add(-2500 * time.Millisecond)

	facts := dispatchQueueFacts(queuedAt, at)

	if facts.QueueWaitNS != (2500 * time.Millisecond).Nanoseconds() {
		t.Fatalf("queue wait = %d ns, want the 2.5s it actually waited", facts.QueueWaitNS)
	}
	if facts.QueuedAtMS != queuedAt.UnixMilli() {
		t.Fatalf("queued at = %d, want %d", facts.QueuedAtMS, queuedAt.UnixMilli())
	}
}

// A clock that moved backwards between the stamp and the dispatch would
// otherwise produce a negative wait, which reads as a round that finished
// before it started.
func TestABackwardsClockReportsNoWaitRatherThanANegativeOne(t *testing.T) {
	at := time.Date(2026, 9, 11, 14, 8, 30, 0, time.UTC)

	facts := dispatchQueueFacts(at.Add(time.Second), at)

	if facts.QueueWaitNS != 0 {
		t.Fatalf("queue wait = %d ns, want none from a backwards clock", facts.QueueWaitNS)
	}
}
