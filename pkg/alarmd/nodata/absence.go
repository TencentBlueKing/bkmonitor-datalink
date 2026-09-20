// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"

// RosterSource says where the expected set came from. It is carried into the
// facts as the caller declared it - never rewritten by the evaluation - so a
// deployment can tell a roster that was enumerated from one that fell back to
// history, and a target that resolved to no host (declared TARGET_STATIC,
// Expected 0) from an item that expects nothing by design (declared WHOLE): a
// strategy silently detecting only what it has already seen, or nothing at
// all, looks the same from the outside as one detecting everything it should.
type RosterSource string

const (
	RosterTargetStatic  RosterSource = "TARGET_STATIC"
	RosterTargetTopo    RosterSource = "TARGET_TOPO"
	RosterTargetService RosterSource = "TARGET_SERVICE"
	RosterHistory       RosterSource = "HISTORY"
	// RosterWhole is declared by the roster derivation for an item that expects
	// no group by design: the backend's host scenario returns None when the
	// no-data dimensions do not name bk_target_ip while a target is configured,
	// and then expects nothing, so only the item as a whole can be absent.
	RosterWhole RosterSource = "WHOLE"
)

// Roster is the set of groups this item expects to see, keyed by Group.Key().
// Version names the derivation it came from; it is carried, not interpreted.
type Roster struct {
	Version string
	Source  RosterSource
	Groups  map[string]Group
}

// GroupMemory is what one group's history amounts to: when it was last seen,
// and when the absence it is currently in began. Both are unix seconds and
// both are zero for "not that" - never seen, and not currently absent.
//
// FirstAbsent is kept rather than a count of rounds because the event text
// counts periods from it, and a count would have to be right about every round
// that did not happen - a Worker that was down, a Slot that was not FULL.
type GroupMemory struct {
	LastSeen    int64
	FirstAbsent int64
}

// AbsenceInput is one Slot's evidence about one item.
type AbsenceInput struct {
	EvaluationTime int64
	PeriodSeconds  int64
	Completeness   execution.Completeness
	// Present is what this Slot saw, already projected onto groups.
	Present map[string]Group
	// Dropped is how many series the projection refused. It reaches the facts
	// and nothing else: a dropped series says the item's agg_dimension does not
	// match its data, which is a strategy to fix rather than an absence.
	Dropped uint64
	Roster  Roster
	Memory  map[string]GroupMemory
	// OutOfBusiness names the groups whose host the caller resolved to another
	// business. Resolving it needs the CMDB index, which this package does not
	// read; the caller answers that question and this one uses the answer.
	OutOfBusiness map[string]struct{}
}

// Verdict is what this evaluation says about one expected group.
type Verdict string

const (
	VerdictAnomaly     Verdict = "ANOMALY"
	VerdictNormal      Verdict = "NORMAL"
	VerdictUnavailable Verdict = "UNAVAILABLE"
)

// AbsenceFacts are the low-cardinality counts one Slot reports. Expected is
// the roster's size: with RosterSource it is what makes an enumeration that
// came back empty visible, which is the failure that otherwise reads as an item
// with nothing to say.
type AbsenceFacts struct {
	Present       uint64
	Expected      uint64
	Absent        uint64
	Unavailable   uint64
	Dropped       uint64
	RosterSource  RosterSource
	RosterVersion string
}

// AbsenceResult is the evaluation. Memory is the whole updated map rather than
// a delta: the caller persists it, and a delta would make "this group was
// removed" and "this group was not mentioned" the same message.
type AbsenceResult struct {
	Verdicts map[string]Verdict
	Memory   map[string]GroupMemory
	Facts    AbsenceFacts
	// Roster is the expected set the verdicts were made against. The verdicts
	// are keyed by group key, so turning one back into the group it names needs
	// this: a result carrying verdicts without it is incomplete for its own
	// reader, who would have to rebuild the roster and hope it matches.
	Roster Roster
}

// Evaluate decides, for one Slot, which expected groups are absent.
//
// It reads no clock and no store, and it does not modify its input: the caller
// holds the previous memory until it has decided what to persist, and a Slot
// that is retried has to reach the same answer from the same evidence.
//
// The completeness gate comes first and is the reason this signature carries
// completeness at all. Absence is only evidence when the round actually looked:
// a query that did not return makes every expected group look missing at once,
// which is an alert storm rather than a detection. The backend has no such gate
// - its input arrives by push, so a push that does not happen is indistinguishable
// from data that is not there - and this is the one place where having the Slot's
// own completeness lets alarmd not repeat it.
func Evaluate(input AbsenceInput) AbsenceResult {
	result := AbsenceResult{
		Verdicts: make(map[string]Verdict, len(input.Roster.Groups)+1),
		Memory:   copyGroupMemory(input.Memory),
		Roster:   input.Roster,
		Facts: AbsenceFacts{
			Present: uint64(len(input.Present)), Expected: uint64(len(input.Roster.Groups)), Dropped: input.Dropped,
			RosterSource: input.Roster.Source, RosterVersion: input.Roster.Version,
		},
	}

	// A1. Not FULL: every expected group is unavailable, and nothing is
	// learned - not even about a group that did arrive, because a partial
	// answer is not evidence about what it left out either. An absence the
	// roster has stopped expecting is not closed here either: the roster
	// change is known, but closing is a verdict, and a round that did not look
	// gives no verdicts. The next FULL round closes it.
	if input.Completeness != execution.CompletenessFull {
		for key := range input.Roster.Groups {
			result.Verdicts[key] = VerdictUnavailable
			result.Facts.Unavailable++
		}
		return result
	}

	whole := WholeItemGroup().Key()

	// A2 and A3. With nothing expected, the item speaks about itself: it is
	// absent when nothing arrived, and recovers as soon as anything does. The
	// groups that arrive are remembered without being judged, which is where a
	// history roster grows from. The declared roster source is left as it is:
	// an empty roster that was declared TARGET_STATIC is a target that resolved
	// to no host, and rewriting it to WHOLE would hide exactly that.
	if len(input.Roster.Groups) == 0 {
		if len(input.Present) == 0 {
			result.Verdicts[whole] = VerdictAnomaly
			result.Facts.Absent++
			entry := result.Memory[whole]
			if entry.FirstAbsent == 0 {
				entry.FirstAbsent = input.EvaluationTime
			}
			// The whole-item group is not a series and never records a
			// LastSeen: one would carry it into a history roster as if it were.
			entry.LastSeen = 0
			result.Memory[whole] = entry
			closeDroppedAbsences(&result, input)
			return result
		}
		result.Verdicts[whole] = VerdictNormal
		delete(result.Memory, whole)
		for key := range input.Present {
			rememberPresent(result.Memory, key, input)
		}
		closeDroppedAbsences(&result, input)
		return result
	}

	for key := range input.Roster.Groups {
		// A6. A host that belongs to another business is not this item's to
		// alert on: it recovers and is forgotten, which is the backend's
		// recover-and-skip. Checked before presence, because a group that is
		// out of business is out of business whether or not it reported.
		if _, foreign := input.OutOfBusiness[key]; foreign {
			result.Verdicts[key] = VerdictNormal
			delete(result.Memory, key)
			continue
		}
		// A4 and A7. A group in Present is present. There is no "arrived but
		// older than the last checkpoint" branch: a Slot is evaluated after its
		// readiness, so what it holds is this period's, and the backend's
		// staleness test exists because its input arrives by push.
		if _, seen := input.Present[key]; seen {
			result.Verdicts[key] = VerdictNormal
			result.Memory[key] = GroupMemory{LastSeen: input.EvaluationTime}
			continue
		}
		result.Verdicts[key] = VerdictAnomaly
		result.Facts.Absent++
		entry := result.Memory[key]
		// Only the first absent round starts the clock. Restarting it every
		// round would hold the reported period count at one however long the
		// group stayed away.
		if entry.FirstAbsent == 0 {
			entry.FirstAbsent = input.EvaluationTime
		}
		result.Memory[key] = entry
	}

	// A5. A group that arrived without being expected is remembered and not
	// judged: it is not this roster's to alert on, and it is not this roster's
	// to recover either. Remembering it is what lets a history roster include
	// it next time.
	for key := range input.Present {
		if _, expected := input.Roster.Groups[key]; expected {
			continue
		}
		rememberPresent(result.Memory, key, input)
	}

	closeDroppedAbsences(&result, input)
	return result
}

// closeDroppedAbsences is A8: a remembered group the roster has stopped
// expecting is handled by what it has open.
//
// One with an open absence -- a FirstAbsent, so an alert may be standing on it
// -- gets exactly one NORMAL and is then forgotten. The roster no longer
// expects it, so no later round will ever say NORMAL for it; without this one
// verdict the alert stands until somebody closes it by hand. That is the case
// a host leaving the target, and an item's whole-item absence turning into
// target groups, both land in.
//
// One with nothing open keeps its memory and gets no verdict, which is where a
// history roster grows from.
//
// One sweep rather than a branch in each path, because the reason is one
// reason and it does not care which way the roster changed: a target roster
// becoming empty strands the host groups exactly as a target roster arriving
// strands the whole-item group. The NORMAL is not counted as an absence -- it
// is the end of one.
//
// It reads the input memory, not the result's, so the answer does not depend
// on what the loops above have already written and a retried Slot reaching
// here with the same memory gets the same answer. A group already judged this
// round keeps that verdict: the roster expects it, or it is the whole-item
// group this round has just spoken about.
func closeDroppedAbsences(result *AbsenceResult, input AbsenceInput) {
	for key, entry := range input.Memory {
		if entry.FirstAbsent == 0 {
			continue
		}
		if _, expected := input.Roster.Groups[key]; expected {
			continue
		}
		if _, judged := result.Verdicts[key]; judged {
			continue
		}
		result.Verdicts[key] = VerdictNormal
		delete(result.Memory, key)
	}
}

// rememberPresent records a group that arrived without a verdict. An
// out-of-business one is dropped instead: it does not belong to this item, so
// remembering it would carry it into a later history roster. The whole-item
// group is never remembered as seen: with an empty agg_dimension every series
// projects onto it, so it arrives every round the item has data, but it is not
// a series and a LastSeen would make it one.
func rememberPresent(memory map[string]GroupMemory, key string, input AbsenceInput) {
	if _, foreign := input.OutOfBusiness[key]; foreign {
		delete(memory, key)
		return
	}
	if key == WholeItemGroup().Key() {
		return
	}
	memory[key] = GroupMemory{LastSeen: input.EvaluationTime}
}

func copyGroupMemory(memory map[string]GroupMemory) map[string]GroupMemory {
	copied := make(map[string]GroupMemory, len(memory))
	for key, entry := range memory {
		copied[key] = entry
	}
	return copied
}
