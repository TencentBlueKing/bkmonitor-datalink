// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "sync/atomic"

// DispatchSuppression is what the dispatcher held back because the due index
// said an object was not due yet.
//
// Its absence is the load-bearing state, and it is the whole reason this type
// exists. While the index runs in shadow it predicts and records but suppresses
// nothing, so no object is parked, so nothing can be overdue -- and an overdue
// count of zero read off that build reports the good news ("nothing is
// overdue") for a deployment where nothing could have been. That is the same
// zero this page has been bitten by twice: a counter reading zero because the
// thing that produces it was never reached.
//
// So the answer does not come from a value. It comes from whether the field is
// here at all: the code that writes it is the code that suppresses, and a build
// without suppression cannot produce it. Nobody has to remember to set a flag,
// and no query layer gets to turn "present" into "absent" on the way.
//
// The alternative considered and rejected was reading the absence of the
// dispatch_skipped_total series. The page reaches time series only through
// unify-query, which answers an exact metric-name match with zero while the
// same data is there under a regex -- so "series absent" is precisely the
// predicate that cannot be trusted on this path, and its failure direction is
// "suppression is not live", the reassuring one.
type DispatchSuppression struct {
	// Skipped counts dispatches held back since the process started, keyed by
	// the reason the dispatcher recorded (not_due, backoff).
	Skipped map[string]uint64 `json:"skipped"`
	// Parked is how many objects are being held back as of this snapshot. It is
	// an instant, unlike Skipped: "how many are waiting right now" and "how many
	// times something was held back" answer different questions, and a page that
	// showed only the second could not tell a deployment that parks a few
	// objects constantly from one that parked many once.
	Parked int `json:"parked"`
}

// aggregateSuppression folds the replicas' figures into one. Counts add up
// because each replica suppresses its own dispatches; the result stays absent
// unless at least one replica reported it, so a fleet still rolling out reads
// as suppressing rather than as silent.
func aggregateDispatchSuppression(view *View, snapshots []Snapshot) {
	var suppression *DispatchSuppression
	for _, snapshot := range snapshots {
		facts := snapshot.Dispatch
		if facts == nil {
			continue
		}
		if suppression == nil {
			suppression = &DispatchSuppression{Skipped: map[string]uint64{}}
		}
		suppression.Parked += facts.Parked
		for reason, count := range facts.Skipped {
			suppression.Skipped[reason] += count
		}
	}
	view.Dispatch = suppression
}

// DispatchSkipReasons is the closed vocabulary of why a dispatch was not made.
//
// Both are always reported, including at zero. A reason that has not happened
// yet is a real answer - nothing has been held back for it - and leaving it out
// would make "never happened" and "not measured" the same reading, which is the
// confusion this whole field exists to remove.
var DispatchSkipReasons = []string{"not_due", "backoff"}

// DispatchSkipTally counts the dispatches the due index held back.
//
// The same counts exist as a metric, and this is deliberately a second small
// tally rather than a read of it, for the same reason RejectionTally is: the
// page answers from the replica's own snapshot, so it must not need collection
// to have happened first.
//
// Atomics rather than a guarded map because the vocabulary is closed at two and
// the increment happens inside the dispatcher's walk over everything the
// replica owns. A map write there would put a lock on the path whose cost this
// whole change exists to remove.
type DispatchSkipTally struct {
	notDue  atomic.Uint64
	backoff atomic.Uint64
}

func NewDispatchSkipTally() *DispatchSkipTally {
	return &DispatchSkipTally{}
}

// SkippedNotDue counts one object passed over because its next Slot is still
// ahead.
func (tally *DispatchSkipTally) SkippedNotDue() {
	if tally == nil {
		return
	}
	tally.notDue.Add(1)
}

// SkippedOnBackoff counts one object passed over because it is waiting out a
// backoff of its own.
func (tally *DispatchSkipTally) SkippedOnBackoff() {
	if tally == nil {
		return
	}
	tally.backoff.Add(1)
}

// Counts returns both reasons, including the ones still at zero.
func (tally *DispatchSkipTally) Counts() map[string]uint64 {
	counts := make(map[string]uint64, len(DispatchSkipReasons))
	for _, reason := range DispatchSkipReasons {
		counts[reason] = 0
	}
	if tally == nil {
		return counts
	}
	counts["not_due"] = tally.notDue.Load()
	counts["backoff"] = tally.backoff.Load()
	return counts
}
