// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// RetainedShareApproachPercent is the rule's threshold, decided once in
// observability so this line and the metric cannot disagree about it.
const RetainedShareApproachPercent = observability.RetainedShareApproachPercent

// RetainedShareFacts is what a row of KindRetainedShareApproaching says for
// itself: the latest completed Slot's retained bytes, the share it was
// admitted under, and when.
type RetainedShareFacts struct {
	// RetainedBytes is the completed Slot's retained total, the number the
	// share is judged against; ShareBytes is that share as the Slot's own
	// producer computed it. Both from one completion, so the pair describes
	// one moment.
	RetainedBytes uint64 `json:"retained_bytes"`
	ShareBytes    uint64 `json:"share_bytes"`
	// PercentOfShare is RetainedBytes over ShareBytes, in whole percent
	// rounded down, for the page: the rule itself is decided on the bytes.
	PercentOfShare uint64 `json:"percent_of_share"`
	// Slot is the evaluation time of that Slot, and At when this process saw
	// it complete.
	Slot int64     `json:"slot,omitempty"`
	At   time.Time `json:"at"`
	// Since is when this process first saw the object at or past the
	// threshold without a completed Slot below it since: how long it has
	// been near the wall, which is what says whether it is growing into it.
	Since time.Time `json:"since"`
	// The completed Slot's retained bytes by phase, summing to RetainedBytes.
	// They say who acts: a share filled by the state phase is the strategy's
	// retention and series, one filled by the output phase is mostly what
	// this build holds per round, which the strategy's owner cannot change.
	RetainedInputBytes  uint64 `json:"retained_input_bytes"`
	RetainedStateBytes  uint64 `json:"retained_state_bytes"`
	RetainedOutputBytes uint64 `json:"retained_output_bytes"`
	RetainedGapBytes    uint64 `json:"retained_gap_bytes"`
	// ThresholdPercent is the threshold the row was listed at, carried so the
	// page does not state a number of its own.
	ThresholdPercent uint64 `json:"threshold_percent"`
	// Refused marks a reading taken from a Slot given up at the share, not
	// from a completed one: RetainedBytes is then what the Slot held plus
	// what it asked for when the share refused it, ShareBytes that share,
	// and the phase split above is absent (zero) - the refusal reports no
	// split. The object stays on this row: an object refused on every Slot
	// completes none, and this row is where it has to be seen, beside its
	// share_refusal on the round's own row.
	Refused bool `json:"refused,omitempty"`
}

// noteShareUsage keeps the latest completed Slot's reading. Only a Slot that
// completed carries one: a refused or failed Slot reports what it had taken
// when it stopped, and letting that overwrite the reading would clear the
// line on the round the object is refused - the round the line exists for.
func (state *queryGroupState) noteShareUsage(usage *observability.SlotBudgetUsageFacts, slot int64, at time.Time) {
	if usage == nil || usage.RetainedShareBytes == 0 {
		return
	}
	percent := usage.RetainedBytes * 100 / usage.RetainedShareBytes
	next := &RetainedShareFacts{
		RetainedBytes: usage.RetainedBytes, ShareBytes: usage.RetainedShareBytes,
		PercentOfShare: percent, Slot: slot, At: at, Since: at,
		RetainedInputBytes: usage.RetainedInputBytes, RetainedStateBytes: usage.RetainedStateBytes,
		RetainedOutputBytes: usage.RetainedOutputBytes, RetainedGapBytes: usage.RetainedGapBytes,
		ThresholdPercent: RetainedShareApproachPercent,
	}
	if previous := state.shareUsage; previous != nil &&
		observability.RetainedShareApproaching(previous.RetainedBytes, previous.ShareBytes) &&
		observability.RetainedShareApproaching(next.RetainedBytes, next.ShareBytes) {
		next.Since = previous.Since
	}
	state.shareUsage = next
}

// noteShareRefusedUsage keeps, as the object's reading, the bytes of a Slot
// given up at its share. It keeps the Since an earlier reading at or past
// the threshold had, so an object that grew into its share and is now
// refused reads as near the wall since it first got there.
func (state *queryGroupState) noteShareRefusedUsage(share *observability.SlotSkipShareFacts, slot int64, at time.Time) {
	if share == nil || share.LimitBytes == 0 {
		return
	}
	retained := share.OwnUsedBytes + share.RequestedBytes
	next := &RetainedShareFacts{
		RetainedBytes: retained, ShareBytes: share.LimitBytes, PercentOfShare: retained * 100 / share.LimitBytes,
		Slot: slot, At: at, Since: at, ThresholdPercent: RetainedShareApproachPercent, Refused: true,
	}
	if previous := state.shareUsage; previous != nil &&
		observability.RetainedShareApproaching(previous.RetainedBytes, previous.ShareBytes) {
		next.Since = previous.Since
	}
	state.shareUsage = next
}

// RetainedShare is every object whose latest reading held at least
// RetainedShareApproachPercent of its one-object share of the retained pool,
// fullest first: a completed Slot near the share, whose results stand, or a
// Slot given up at it (Refused), whose strategy is stopped until it is
// sharded. Listed because the refusal stops the strategy whole: this is the
// place it can be seen coming, and where it stays once it has come.
func (tracker *Tracker) RetainedShare() []Anomaly {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	anomalies := make([]Anomaly, 0)
	for queryGroup, state := range tracker.groups {
		usage := state.shareUsage
		if usage == nil || !observability.RetainedShareApproaching(usage.RetainedBytes, usage.ShareBytes) {
			continue
		}
		facts := *usage
		anomaly := Anomaly{
			QueryGroup: queryGroup, Kind: KindRetainedShareApproaching,
			Since: facts.Since, SinceFrom: SinceSnapshotContinuity, Replica: tracker.replica,
			RetainedShare: &facts, LastHealthyAt: state.lastHealthyAt,
		}
		for strategy := range state.strategies {
			anomaly.Strategies = append(anomaly.Strategies, strategy)
		}
		sortStrategies(anomaly.Strategies)
		anomalies = append(anomalies, anomaly)
	}
	sort.Slice(anomalies, func(left, right int) bool {
		l, r := anomalies[left].RetainedShare, anomalies[right].RetainedShare
		if l.PercentOfShare != r.PercentOfShare {
			return l.PercentOfShare > r.PercentOfShare
		}
		return anomalies[left].QueryGroup < anomalies[right].QueryGroup
	})
	return anomalies
}

// ShareRefusalFacts is what a row filed under RESOURCE_SHARE_EXCEEDED says for
// itself: the object's Slots are being finalized without a query because each
// one's own retained bytes are over the share one object may hold, and these
// are the bytes of the latest refusal against that share. One row per object,
// however many of its Slots are given up: until the strategy is sharded every
// Slot of it lands here, and a line per Slot would bury the one fact.
type ShareRefusalFacts struct {
	// Phase is where the reservation was refused (normal_input,
	// normal_output, normal_gap); OwnUsedBytes what the Slot held there,
	// RequestedBytes what it asked for on top, LimitBytes the share.
	Phase          string `json:"phase"`
	OwnUsedBytes   uint64 `json:"own_used_bytes"`
	RequestedBytes uint64 `json:"requested_bytes"`
	LimitBytes     uint64 `json:"limit_bytes"`
	// Refusals is how many identical refusals the latest Slot took before
	// it was given up.
	Refusals uint32 `json:"refusals"`
	// Slots is how many of the object's Slots this process has given up for
	// the share since FirstSlot, the latest being LastSlot.
	Slots     int       `json:"slots"`
	FirstSlot int64     `json:"first_slot"`
	LastSlot  int64     `json:"last_slot"`
	Since     time.Time `json:"since"`
	At        time.Time `json:"at"`
}

// noteShareRefusal folds one RESOURCE_SHARE_EXCEEDED skip into the object's
// share refusal. Called with the lock held.
func (state *queryGroupState) noteShareRefusal(share *observability.SlotSkipShareFacts, first, last int64, slots uint32, at time.Time) {
	facts := state.shareRefusal
	if facts == nil {
		facts = &ShareRefusalFacts{FirstSlot: first, Since: at}
		state.shareRefusal = facts
	}
	facts.Phase, facts.OwnUsedBytes, facts.RequestedBytes, facts.LimitBytes = share.Phase, share.OwnUsedBytes, share.RequestedBytes, share.LimitBytes
	facts.Refusals = share.Refusals
	facts.Slots += int(max(slots, 1))
	facts.LastSlot, facts.At = last, at
}
