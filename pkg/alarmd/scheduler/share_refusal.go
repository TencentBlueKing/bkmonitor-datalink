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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// ShareRefusalsToFinalize is how many identical deterministic share refusals
// of one Slot the Runner takes before it finalizes the Slot without a query,
// the first time its Query Group meets the share.
//
// Two, not one. The refusal is deterministic by construction - the share is
// half the replica's retained pool, fixed by configuration, and is compared
// with the Slot's own total, never with what other Query Groups hold - so the
// first refusal would already be safe to act on. The second costs one more
// read of the window and buys two things: an answer that was oversized once
// for a reason of its own (a provider that answered a batch twice, a share
// changed by a resize between the two attempts, which also changes the limit
// and so does not count as identical) does not lose its Slot, and the Slot is
// still given up within one retry backoff instead of after the replay age -
// ten minutes and a dozen whole-window reads, which is what it cost before.
const ShareRefusalsToFinalize = 2

// ShareRefusalsToFinalizeAgain is the same count for every later Slot of a
// Query Group that already had a Slot finalized for the same refusal (same
// phase, same share): one. The group has shown it does not fit; a second
// read per Slot would double the steady cost of an oversized strategy - at 60
// s Slots and a one-second first retry, two whole-window reads a minute -
// for nothing. One read per Slot still notices the strategy shrinking: the
// first Slot that fits completes from that read and clears the key.
const ShareRefusalsToFinalizeAgain = 1

// shareRefusalState is what the Runner holds of its Query Group's share
// refusals: in memory beside the Slot's recovery attempt, never persisted.
// The key is the group (the Runner is bound to one), the refusal's phase and
// the share it was measured against (execution.ShareRefusal.Same); a refusal
// under another phase or share starts a new key. count is the refusals of the
// Slot named by contract; finalized says a Slot of the group was already
// finalized under this key. A Runner that loses it - the Query Group moves,
// the process restarts - starts again from the first-time count, which costs
// at most one more read before the same decision.
type shareRefusalState struct {
	contract  execution.FrozenExecutionContractRef
	last      execution.ShareRefusal
	count     uint32
	finalized bool
}

func (state *shareRefusalState) threshold() uint32 {
	if state.finalized {
		return ShareRefusalsToFinalizeAgain
	}
	return ShareRefusalsToFinalize
}

// noteShareRefusal records one Execute error of slot. It reports whether the
// Slot is now to be finalized as RESOURCE_SHARE_EXCEEDED: the error is a
// deterministic share refusal and the same refusal (same phase, same share)
// has now been seen as many times in a row for this Slot as its key asks
// (threshold).
//
// A refusal on the pool, or any other error, is not counted and leaves the
// count as it stands: those are retried as before, and a timeout between two
// share refusals says nothing about whether the Slot fits.
func (runner *Runner) noteShareRefusal(slot FrozenSlot, err error) bool {
	refusal, deterministic := execution.ShareRefusalOf(err)
	if !deterministic {
		return false
	}
	state := runner.shareRefusal
	if state == nil || !state.last.Same(refusal) {
		state = &shareRefusalState{contract: slot.Contract}
		runner.shareRefusal = state
	}
	if state.contract != slot.Contract {
		state.contract, state.count = slot.Contract, 0
	}
	state.last = refusal
	state.count++
	return state.count >= state.threshold()
}

// shareExceeded reports whether slot is the Slot this Runner decided to
// finalize for its share.
func (runner *Runner) shareExceeded(slot FrozenSlot) bool {
	state := runner.shareRefusal
	return state != nil && state.contract == slot.Contract && state.count >= state.threshold()
}

// shareSlotCompleted settles the key once a Slot of the group completed. A
// Slot finalized for the share marks the key, so the group's next Slot is
// finalized on its first identical refusal. Any other completion - the Slot
// fit, or it ended some other way - clears it, the same rule the fleet row
// follows: the next refusal is a first one again.
func (runner *Runner) shareSlotCompleted(slot FrozenSlot, result execution.SlotExecutionResult) {
	state := runner.shareRefusal
	if state == nil {
		return
	}
	if result.QueryFreeCause == execution.CauseResourceShareExceeded && state.contract == slot.Contract {
		state.finalized, state.count = true, 0
		return
	}
	runner.shareRefusal = nil
}

// shareSkipFacts is the refusal a share-exceeded skip was decided on, for the
// Slot's slot_skipped line.
func (runner *Runner) shareSkipFacts(slot FrozenSlot) *observability.SlotSkipShareFacts {
	if !runner.shareExceeded(slot) {
		return nil
	}
	last := runner.shareRefusal.last
	return &observability.SlotSkipShareFacts{Phase: last.Phase, OwnUsedBytes: last.OwnUsedBytes,
		RequestedBytes: last.RequestedBytes, LimitBytes: last.LimitBytes, Refusals: runner.shareRefusal.count}
}
