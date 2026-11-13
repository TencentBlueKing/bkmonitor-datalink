// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package readhold

import (
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A Query Group a moved Plan went to reads the hold of the group it left,
// so that it reads no earlier than that group's last Slot could still be
// written. A predecessor whose record is not closed yet, or could not be
// read, used to answer the hold bound at once: frozen into the successor's
// first Slot, about ten minutes - for a predecessor that, more often than
// not, closed seconds later. The successor waits instead, without freezing,
// and reads the predecessor again on its next round: closed, it is ready on
// the predecessor's own hold, with the time it waited covered; still open at
// the time its first Slot would have been ready today, it freezes the bound
// as it did. A corrupt record is no better for waiting, and freezes the
// bound at once.

// PredecessorRetry is how soon a successor waiting on its predecessor reads
// it again.
const PredecessorRetry = 5 * time.Second

// How a successor's wait on its predecessors ended, or that one began:
// waiting when it starts, inherited when every predecessor it waited on was
// read closed, bounded when one was still open at the end of the wait.
// TransitionWaitOutcomes is the closed list.
const (
	TransitionWaitWaiting   = "waiting"
	TransitionWaitInherited = "inherited"
	TransitionWaitBounded   = "bounded"
)

var TransitionWaitOutcomes = []string{TransitionWaitWaiting, TransitionWaitInherited, TransitionWaitBounded}

// AwaitingUnread is a predecessor waited on because it has not been read
// since it was forgotten: read again on the next round.
const AwaitingUnread = "unread"

// AwaitingPredecessorError is a Slot not frozen because the group waits on a
// predecessor: retry at RetryAt, and the wait ends, the bound frozen, at Until.
type AwaitingPredecessorError struct {
	QueryGroup  execution.QueryGroupIdentity
	Predecessor execution.QueryGroupIdentity
	// Reason is what this round read of the predecessor:
	// PredecessorRecordOpen, PredecessorUnreadable, or AwaitingUnread.
	Reason         string
	SinceUnixMilli int64
	UntilUnixMilli int64
	RetryAt        time.Time
}

func (err *AwaitingPredecessorError) Error() string {
	return fmt.Sprintf("alarmd readhold: %s waits on predecessor %s (%s) until %d", err.QueryGroup, err.Predecessor, err.Reason, err.UntilUnixMilli)
}

// predecessorWait is one link a group waits on, or waited on in this seed.
type predecessorWait struct {
	predecessor execution.QueryGroupIdentity
	reason      string
	sinceMillis int64
	endMillis   int64
	// closedAtMillis is when the predecessor was first read closed: the
	// deadline the successor's transition keeps is no earlier, so the hold it
	// freezes covers the time it waited. Zero while it is still open.
	closedAtMillis int64
	// bounded is a wait that reached its end still open: the bound froze.
	bounded bool
	// counted is a wait counted as begun: one that read the predecessor open
	// or unreadable, not only unread.
	counted bool
}

// waitKey is a link's wait: the predecessor, the boundary it closed at, and
// the Plan.
func waitKey(predecessor Previous, link PlanLink) string {
	return fmt.Sprint(predecessor.QueryGroup, "|", predecessor.ClosedAt, "|", link.Key)
}

// keepWaits keeps the waits of the links spec still has: a group configured
// again for links it no longer has forgets theirs, which then end neither
// inherited nor bounded.
func keepWaits(state *entry, spec GroupSpec) {
	if len(state.waits) == 0 {
		return
	}
	links := make(map[string]struct{})
	for _, predecessor := range spec.Previous {
		for _, link := range predecessor.Links {
			links[waitKey(predecessor, link)] = struct{}{}
		}
	}
	for key := range state.waits {
		if _, kept := links[key]; !kept {
			delete(state.waits, key)
		}
	}
}

// awaitPredecessor decides one link of a seed that may wait. A link that
// reads open or unreadable starts its wait, or goes on with it, until its end;
// at the end the bound stands, as it always did. A predecessor not read since
// it was forgotten (unread) is read again on the next round: it neither
// starts a counted wait nor ends one, and is no unreadable record. A link
// that waited and now reads closed (or zero) keeps the instant it was read as
// its deadline's floor. A link read closed at its first reading did not wait
// and is taken as it always was: its successor's first Slot, late for a
// reason of its own, must not have that lateness folded into its hold. Nor
// does a first Slot already past the end wait: it is ready on the bound.
func (controller *Controller) awaitPredecessor(state *entry, at execution.EvaluationTime, predecessor Previous, link PlanLink,
	fact *previousFact, unread bool) (awaiting bool, retryAt int64) {
	key := waitKey(predecessor, link)
	wait, waited := state.waits[key]
	now := controller.options.Now().UnixMilli()
	transient := fact.reason == PredecessorRecordOpen || fact.reason == PredecessorUnreadable
	if !transient && !unread {
		if waited {
			if wait.closedAtMillis == 0 {
				wait.closedAtMillis = now
			}
			state.waits[key] = wait
			fact.closedAtMillis = wait.closedAtMillis
		}
		return false, 0
	}
	if !waited {
		limit := min(state.spec.HoldLimit, controller.options.MaxHold).Milliseconds()
		wait = predecessorWait{predecessor: predecessor.QueryGroup, sinceMillis: now,
			endMillis: min(int64(fact.slot)*1000+controller.options.MaxHold.Milliseconds()+fact.offset,
				int64(at)*1000+state.spec.SettlingWait.Milliseconds()+limit)}
		if now >= wait.endMillis {
			return false, 0
		}
		if state.waits == nil {
			state.waits = make(map[string]predecessorWait)
		}
	}
	if !unread {
		wait.reason = fact.reason
		if !wait.counted {
			wait.counted = true
			controller.counted(controller.transitionWaits, TransitionWaitWaiting)
		}
	} else if wait.reason != "" {
		// The bound a wait ends on is for what its predecessor last read.
		fact.reason = wait.reason
	}
	wait.closedAtMillis = 0
	if now < wait.endMillis {
		state.waits[key] = wait
		return true, min(now+PredecessorRetry.Milliseconds(), wait.endMillis)
	}
	wait.bounded = true
	state.waits[key] = wait
	return false, 0
}

// endWaits counts how the group's waits ended, once its seed is frozen, and
// forgets them.
func (controller *Controller) endWaits(state *entry) {
	if len(state.waits) == 0 {
		return
	}
	bounded, counted := false, false
	for _, wait := range state.waits {
		bounded, counted = bounded || wait.bounded, counted || wait.counted
	}
	if !counted {
		// Waited only on a predecessor not read yet: no wait began.
		state.waits = nil
		return
	}
	outcome := TransitionWaitInherited
	if bounded {
		outcome = TransitionWaitBounded
	}
	controller.counted(controller.transitionWaits, outcome)
	state.waits = nil
}

// waitingGroups is the groups waiting on a predecessor now.
func (controller *Controller) waitingGroups() int {
	controller.mu.RLock()
	defer controller.mu.RUnlock()
	waiting := 0
	for _, state := range controller.groups {
		state.mu.Lock()
		for _, wait := range state.waits {
			if !wait.bounded && wait.closedAtMillis == 0 {
				waiting++
				break
			}
		}
		state.mu.Unlock()
	}
	return waiting
}
