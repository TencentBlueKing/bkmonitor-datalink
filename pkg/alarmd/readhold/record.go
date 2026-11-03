// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package readhold keeps each Query Group's read hold: how much later than
// its schedule says a Slot of it is read and due, for a Query Group whose
// whole window is read before its data has arrived. A Slot carries the hold
// it was frozen with in its contract (execution.FrozenExecutionContractRef).
package readhold

import (
	"encoding/json"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Namespace is the control namespace of a Query Group's read hold record,
// beside its Progress under the schedule prefix.
const Namespace = "read_hold"

// Record is a Query Group's read hold as its owner keeps it: the hold its
// Slots are frozen with now and the first Slot frozen with it, and the hold
// before that and its first Slot. An absent record can also be an expired
// predecessor; it proves no historical hold without independent facts.
type Record struct {
	HoldMillis int64                    `json:"hold_ms"`
	SinceSlot  execution.EvaluationTime `json:"since_slot"`
	// PreviousHoldMillis and PreviousSinceSlot are the hold before and its
	// first Slot. A record begun from no hold has none before it from Slot
	// 1 on: every Slot before SinceSlot was frozen with none.
	PreviousHoldMillis int64                    `json:"previous_hold_ms,omitempty"`
	PreviousSinceSlot  execution.EvaluationTime `json:"previous_since_slot,omitempty"`
	// A change is chosen before its next Slot is known. Freeze moves it into
	// HoldMillis/SinceSlot; until then the previous frozen interval is intact.
	PendingHoldMillis *int64 `json:"pending_hold_ms,omitempty"`
	ArrivalAgeMillis  int64  `json:"arrival_age_ms,omitempty"`
	// ArrivalAgeSeed is where ArrivalAgeMillis came from while the group has
	// not measured its own (SeedPeer, SeedColdStart); empty once it has. A
	// seeded age is where reading starts, not a measurement, and nothing
	// that advises on a measured one reads it.
	ArrivalAgeSeed  string `json:"arrival_age_seed,omitempty"`
	LastEarlyMillis int64  `json:"last_early_ms,omitempty"`
	// MaxEarlyIntervalMillis is the longest gap between two read-early
	// arrivals: over the record's whole life in a build before EarlyGaps,
	// which reads it alone, and read once into EarlyGaps (earlyGapsOf) by
	// this one. This build writes it as the longest of EarlyGaps
	// (longestGap), so an older reader - a rollback, a mixed rollout - keeps
	// a bar no shorter than this one's rather than falling to
	// SingleEventQuiet.
	MaxEarlyIntervalMillis int64 `json:"max_early_interval_ms,omitempty"`
	// EarlyGaps is the longest gap between two read-early arrivals that
	// ended on each UTC day, for the days within RecordTTL: the quiet a
	// lowering waits for is the longest of them (quietBar). A gap that ended
	// longer ago than the record lives says nothing of how the data arrives
	// now, so it ages out with the days instead of holding the bar forever.
	EarlyGaps        []EarlyGap               `json:"early_gaps,omitempty"`
	QuietSinceMillis int64                    `json:"quiet_since_ms,omitempty"`
	EarlierMatches   int                      `json:"earlier_matches,omitempty"`
	LastEarlierSlot  execution.EvaluationTime `json:"last_earlier_slot,omitempty"`
	// EarlierFloorMillis is the largest candidate shown too early under the
	// current hold (execution.CandidateReadHold), nil while none is, and
	// EarlierFloorAtMillis when it was learned, zero for a floor written
	// before floors were dated; and EarlierDifferents the different trials
	// in a row at the current candidate, which move the floor once there are
	// FloorDifferentsRequired of them. They are cleared when the hold is
	// raised or seeded and at each renewal.
	EarlierFloorMillis   *int64 `json:"earlier_floor_ms,omitempty"`
	EarlierFloorAtMillis int64  `json:"earlier_floor_at_ms,omitempty"`
	EarlierDifferents    int    `json:"earlier_differents,omitempty"`
	// EarlierFloorRetests is how many shadow probes of the floor have failed
	// since it took its value (FloorProbe): each doubles how long the floor
	// stands before the next. Zero whenever the floor changes value or is
	// cleared.
	EarlierFloorRetests int `json:"earlier_floor_retests,omitempty"`
	// EarlierFloorProbed says a shadow probe failed at the floor's value: a
	// midpoint that read differently became the floor (floor_raised), or the
	// floor itself read differently (relearned). A probe of the floor, or of
	// a midpoint within EdgeProbeReach above it, reads near a line the
	// group's data has been seen on both sides of, and needs more clean
	// rounds to pass (FloorProbe). False for a floor the search learned - a
	// raise right after a lowering, different trials in a row - and for a
	// record written before the field: such a floor was not probed.
	EarlierFloorProbed bool `json:"earlier_floor_probed,omitempty"`
	// VerifyCandidateMillis is the hold the trials chose to lower to, which a
	// shadow probe reads round by round before the hold moves (FloorProbe,
	// kind verify): the trials stop while it is set, a probe that passes
	// lowers the hold to it, and one that fails makes it the floor. Cleared
	// when the hold is raised or seeded - the data came later than it -
	// and by the probe's end; a renewal clearing the floor keeps it, a
	// candidate the trials chose being no floor's. Nil while there is none.
	VerifyCandidateMillis *int64 `json:"verify_candidate_ms,omitempty"`
	// Probe is how far the group's shadow probe has read, kept so the probe
	// outlives a rollout or a takeover (ProbeProgress). Nil while none is kept.
	Probe *ProbeProgress `json:"probe,omitempty"`
	// LoweredFromMillis is the hold the last lowering came from, while the
	// group stays Lowered: a raise right after it goes back there at least.
	LoweredFromMillis   int64  `json:"lowered_from_ms,omitempty"`
	RaisedAfterLowering uint64 `json:"raised_after_lowering,omitempty"`
	Lowered             bool   `json:"lowered,omitempty"`
	// LoweredVerified marks a lowering to a hold a shadow probe read clean
	// round by round before it was made (FloorProbeResult). Its probation
	// counts a round read early apart, and the raise that follows makes the
	// lowered hold a floor a probe failed at (EarlierFloorProbed). Cleared by
	// any other lowering and by a raise.
	LoweredVerified bool   `json:"lowered_verified,omitempty"`
	Noise           uint64 `json:"noise,omitempty"`
	// AtLimit says the latest arrival reading (an observed arrival, or the
	// seed) wanted a hold past LimitMillis. It is written only there, so it
	// still says so after trials have lowered the hold below the limit: read
	// HeldAtLimit for whether the group is held at its limit now.
	AtLimit         bool                     `json:"at_limit,omitempty"`
	LimitMillis     int64                    `json:"limit_ms,omitempty"`
	Rung            string                   `json:"rung,omitempty"`
	Buckets         []int64                  `json:"buckets,omitempty"`
	SegmentStart    execution.EvaluationTime `json:"segment_start,omitempty"`
	Closed          bool                     `json:"closed,omitempty"`
	Plans           []PlanRecord             `json:"plans,omitempty"`
	Transitions     []Transition             `json:"transitions,omitempty"`
	RenewedAtMillis int64                    `json:"renewed_at_ms,omitempty"`
	// QueryDurationMillis is how long the group's query took as its owner
	// last measured it, rounded up to the second, and QueryDurationAtMillis
	// when that was written: what a process that takes the group over judges
	// its first rounds by (query_duration.go). A record without them, as one
	// a build before this one wrote, has no reading.
	QueryDurationMillis   int64 `json:"query_duration_ms,omitempty"`
	QueryDurationAtMillis int64 `json:"query_duration_at_ms,omitempty"`
}

// EarlyGap is the longest gap between two read-early arrivals that ended on
// one UTC day (Day, days since the Unix epoch).
type EarlyGap struct {
	Day    int64 `json:"day"`
	Millis int64 `json:"ms"`
}

// ErrRecordInvalid is a record that does not decode or holds out of range.
var ErrRecordInvalid = errors.New("alarmd readhold: invalid read hold record")

// Decode reads a record. Fields it does not know are ignored: a later
// build may keep more beside the holds.
func Decode(raw []byte) (Record, error) {
	var record Record
	if err := json.Unmarshal(raw, &record); err != nil {
		return Record{}, ErrRecordInvalid
	}
	if !inRange(record.HoldMillis) || !inRange(record.PreviousHoldMillis) || record.SinceSlot <= 0 ||
		record.PreviousSinceSlot < 0 || record.PreviousSinceSlot > record.SinceSlot ||
		(record.PendingHoldMillis != nil && !inRange(*record.PendingHoldMillis)) ||
		(record.EarlierFloorMillis != nil && *record.EarlierFloorMillis < 0) || (record.VerifyCandidateMillis != nil && *record.VerifyCandidateMillis < 0) || !record.Probe.valid() || record.EarlierFloorAtMillis < 0 || record.EarlierDifferents < 0 || record.EarlierFloorRetests < 0 || record.LoweredFromMillis < 0 ||
		record.ArrivalAgeMillis < 0 || record.MaxEarlyIntervalMillis < 0 ||
		record.EarlierMatches < 0 || record.EarlierMatches > EarlierMatchesRequired || !inRange(record.LimitMillis) ||
		record.SegmentStart < 0 || record.QueryDurationMillis < 0 || record.QueryDurationAtMillis < 0 {
		return Record{}, ErrRecordInvalid
	}
	if len(record.EarlyGaps) > earlyGapDays {
		return Record{}, ErrRecordInvalid
	}
	for index, gap := range record.EarlyGaps {
		if gap.Day < 0 || gap.Millis <= 0 || (index > 0 && gap.Day <= record.EarlyGaps[index-1].Day) {
			return Record{}, ErrRecordInvalid
		}
	}
	for _, plan := range record.Plans {
		if plan.Key.PlanIdentity.Validate() != nil ||
			!inRange(plan.PreviousHoldMillis) || plan.PreviousSlot < 0 || plan.CompletionOffsetMillis < 0 ||
			plan.ClosedAt < 0 || (plan.ClosedAt > 0 && plan.PreviousSlot >= plan.ClosedAt) {
			return Record{}, ErrRecordInvalid
		}
		if plan.InheritedClosedAt < 0 || (plan.InheritedClosedAt > 0 && plan.InheritedQueryGroup == "") {
			return Record{}, ErrRecordInvalid
		}
	}
	for _, transition := range record.Transitions {
		if transition.Key.PlanIdentity.Validate() != nil || transition.DeadlineMillis <= 0 {
			return Record{}, ErrRecordInvalid
		}
	}
	return record, nil
}

// HoldAt is the hold the Slot at slot was frozen with, and false when the
// record no longer says: a Slot before the hold before this one.
func (record Record) HoldAt(slot execution.EvaluationTime) (int64, bool) {
	switch {
	case slot >= record.SinceSlot:
		return record.HoldMillis, true
	case record.PreviousSinceSlot > 0 && slot >= record.PreviousSinceSlot:
		return record.PreviousHoldMillis, true
	}
	return 0, false
}

func inRange(hold int64) bool {
	return hold >= 0 && hold <= execution.MaxReadHoldMillis
}

// HeldAtLimit says the group is held at its limit now: its latest arrival
// reading wanted more than the limit, and its hold - the pending one when a
// change is pending - has not been lowered below the limit since. Derived on
// read rather than stored, so a lowering that writes nothing about arrivals
// cannot leave it standing.
func (record Record) HeldAtLimit() bool {
	return record.AtLimit && record.LimitMillis > 0 && current(record) >= record.LimitMillis
}

// ProbeProgress is how far a shadow probe has read: what it reads - its kind,
// the hold it reads its rounds at, and the group's hold and floor then - the
// Slot it started at, its clean rounds in a row and the Slot of the latest of
// them. It is the progress of that probe alone: the probe the record has now,
// of the same kind at the same holds over the same floor, whose latest kept
// round is fresh (progressStale), goes on from it (FloorProbeResume); any
// other starts from none. A probe of a long period runs past FloorStanding
// from its start - ProbationRounds rounds at twenty minutes are forty hours -
// so its age is not what makes its rounds stale. The lookback
// writes it every ProbeProgressEvery clean rounds (FloorProbeProgress); a
// probe's end, and every write that moves what the probe reads, clears it.
type ProbeProgress struct {
	Kind            string                   `json:"kind"`
	CandidateMillis int64                    `json:"candidate_ms"`
	HoldMillis      int64                    `json:"hold_ms"`
	FloorMillis     *int64                   `json:"floor_ms,omitempty"`
	Clean           int                      `json:"clean_rounds"`
	StartedSlot     execution.EvaluationTime `json:"started_slot"`
	KeptSlot        execution.EvaluationTime `json:"kept_slot"`
	// PeriodMillis is the distance between the probe's rounds.
	PeriodMillis int64 `json:"period_ms"`
}

func (progress *ProbeProgress) valid() bool {
	return progress == nil || (progress.CandidateMillis >= 0 && progress.HoldMillis > progress.CandidateMillis &&
		(progress.FloorMillis == nil || *progress.FloorMillis >= 0) && progress.Clean >= 0 && progress.StartedSlot > 0 &&
		progress.KeptSlot >= progress.StartedSlot && progress.PeriodMillis > 0)
}
