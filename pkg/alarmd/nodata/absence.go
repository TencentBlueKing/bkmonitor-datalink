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
	RosterTargetStatic RosterSource = "TARGET_STATIC"
	RosterHistory      RosterSource = "HISTORY"
	// RosterTargetPlan is declared for an item whose target is a target_plan
	// and whose no-data dimensions are exactly the dimensions that target's
	// record key is read from: the expected set is the members the worker
	// resolved the plan to in this same Slot, static and dynamic alike.
	RosterTargetPlan RosterSource = "TARGET_PLAN"
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
	// SuppressedAt is the round this group's absence stopped being tracked,
	// zero while it is still tracked. It is read, never recomputed: the
	// horizon is a setting, and deciding suppression from FirstAbsent against
	// the horizon in force now would reopen every stopped absence the moment
	// somebody raised it.
	SuppressedAt int64
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
	// TrackingHorizonSeconds is how long one absence is tracked before this
	// item stops producing it. Zero is the deployment that has not configured
	// one, and it means unlimited tracking - the behaviour before the horizon
	// existed - rather than "stop immediately".
	TrackingHorizonSeconds int64
	// TrackingExhaustedAt is the round a history roster was emptied by the
	// horizon, as the record holds it; zero when it was not. Without it an
	// empty history roster reads as "this item has no groups", and the next
	// round rebuilds the whole-item absence the horizon just stopped.
	TrackingExhaustedAt int64
	// RecoveryDisabled is the item's owner turning automatic recovery off
	// (contract.NoDataConfigV1.RecoveryDisabled): a group whose data comes
	// back is remembered as present and gets no verdict, so the NORMAL that
	// would end its no-data alert is never produced. The closes that are no
	// recovery keep their NORMAL - a group the roster stopped expecting
	// (closeDropped), one in another business (A6) - as the control
	// plane's closes do elsewhere.
	RecoveryDisabled bool
	// AnswerTruncated is SlotInput.AnswerTruncated: the answer says it is
	// full and may have been cut at the query service's group cap. Judged as
	// a round that is not FULL (A1).
	AnswerTruncated bool
	// RosterUnresolved says the expected set could not be built in full this
	// round: a target plan with a selector that did not answer, or members
	// dropped in validation. Roster then holds the members that did resolve,
	// a lower bound. Judged as a round that is not FULL (A1).
	RosterUnresolved bool
}

// trackingExpired says whether an absence that began at firstAbsent has run
// past the horizon by this round.
//
// Both bounds are round times, never a wall clock: a Slot replayed hours late
// must reach the same answer the Slot would have reached when it was due, and
// a clock read here would make the answer depend on when the machine got to
// it. A horizon of zero is the deployment that configured none, and it tracks
// without limit - the behaviour before this existed - rather than stopping
// everything at once.
func trackingExpired(firstAbsent, evaluationTime, horizonSeconds int64) bool {
	if horizonSeconds <= 0 || firstAbsent <= 0 {
		return false
	}
	return evaluationTime-firstAbsent >= horizonSeconds
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
	// Expired counts the absences this round stopped tracking, both the ones
	// forgotten outright and the ones marked. Counted where each is decided
	// rather than by differencing this memory against the last: a difference
	// reads zero in the round that failed to load the memory, which is the
	// round most worth reporting.
	Expired uint64
	// Suppressed counts the groups this round met already stopped, which is
	// the standing size of what the horizon is holding down.
	Suppressed uint64
	// ManualClosed counts the absences this round stopped because a user
	// closed their alert by hand (decision-024), deleted or marked as the
	// horizon would. A marked one is in Suppressed from the next round on.
	ManualClosed uint64
	// AbsentAges is the absences still tracked and reported this round, by
	// how long each has been open: since this round only, under an hour,
	// under a day, a day or more. Counted where Absent is, from the same
	// FirstAbsent the horizon reads, so the two agree about every group.
	//
	// It exists because the horizon's effect cannot be read from Expired
	// alone: a deployment that switched a one-day horizon on and saw thirty
	// absences expire out of four thousand could not tell "the backlog was
	// thirty" from "the horizon is not reaching them". The ages say which --
	// four thousand absences all under an hour old are groups that report
	// every few rounds and reset their clock each time, which no horizon
	// ever reaches -- and they are the number a deployment chooses its
	// horizon by: what a day-long horizon would stop is what sits in the
	// last bucket.
	AbsentAges AbsentAgeBuckets
}

// AbsentAgeBuckets is how long the absences reported this round have been
// open, four buckets by the age of each absence at this evaluation time.
type AbsentAgeBuckets struct {
	// ThisRound is an absence that began at this evaluation time: its first
	// absent round. UnderHour is older than that and under an hour, UnderDay
	// an hour or more and under a day, DayOrMore a day or more.
	ThisRound uint64
	UnderHour uint64
	UnderDay  uint64
	DayOrMore uint64
}

// count files one open absence by its age at evaluationTime. An absence
// with no start is filed as this round's, which is the only round it can
// have begun in for the memory to lack the start.
func (buckets *AbsentAgeBuckets) count(firstAbsent, evaluationTime int64) {
	age := evaluationTime - firstAbsent
	switch {
	case firstAbsent <= 0 || age <= 0:
		buckets.ThisRound++
	case age < 3600:
		buckets.UnderHour++
	case age < 86400:
		buckets.UnderDay++
	default:
		buckets.DayOrMore++
	}
}

// AbsenceResult is the evaluation. Memory is the whole updated map rather than
// a delta: the caller persists it, and a delta would make "this group was
// removed" and "this group was not mentioned" the same message.
type AbsenceResult struct {
	Verdicts map[string]Verdict
	Memory   map[string]GroupMemory
	// Closing names the groups whose NORMAL is the one close an absence gets
	// when the roster stops expecting its group (A8): the group is forgotten
	// this round, so nothing will speak about it again. Its envelope must not
	// be held by the open-alert gate, or the alert it was for stays open for
	// good; see SyntheticSeries.Closing.
	Closing map[string]bool
	Facts   AbsenceFacts
	// Roster is the expected set the verdicts were made against. The verdicts
	// are keyed by group key, so turning one back into the group it names needs
	// this: a result carrying verdicts without it is incomplete for its own
	// reader, who would have to rebuild the roster and hope it matches.
	Roster Roster
	// TrackingExhaustedAt is the Plan-level fact after this round: the round a
	// history roster was emptied by the horizon, or zero.
	//
	// It is cleared as soon as the memory holds a group again, not only when
	// data arrives: with a non-empty memory the fact has no reader, and leaving
	// it set makes the contract layer refuse every write this Plan attempts.
	// The converse is enforced too - a round that emptied nothing and
	// remembered nothing carries the fact rather than clearing it, because
	// clearing it over an empty memory is the other refusal.
	TrackingExhaustedAt int64
	// WholeItemPresent says the item reported under the whole-item group this
	// round. It is the one arrival the memory cannot record - that group is not
	// a series and is never written - so the caller needs it stated: it is what
	// tells a round where data came back from a round where nothing did, when
	// both produce the same empty memory.
	WholeItemPresent bool
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
	// The rules are applied one group at a time (judge.go), and this is the
	// whole round: every remembered group fed, then the round finished.
	//
	// The Plan-level fact is decided in one place, Finish, for every path out
	// of the evaluation at once.
	//
	// The fact exists only to tell two empty memories apart - one that never
	// had groups from one the horizon emptied - so it is meaningful exactly
	// while the memory is empty, and the contract layer enforces both halves of
	// that by name: a memory stating the fact while holding groups is refused,
	// and so is one that cleared it without data arriving. A round may
	// therefore clear the fact when it produced a memory to clear it with.
	//
	// Data arriving clears it too, even when the memory stays empty. There is
	// exactly one group that is never written to the memory - the whole-item
	// group, which judgePresent skips because it is not a series - so an
	// exhausted Plan whose data comes back only under that group would other-
	// wise carry the fact forever: the memory it produces is empty, the fact is
	// carried, and the whole-item absence it describes is never reported again.
	// That is reachable rather than theoretical: the no-data agg_dimension is
	// not in deriveStateCompatibilityHash's closure (strategy/compiler.go:315-346
	// closes over the dataset's identity fields, not this), so emptying it
	// leaves StateGeneration - and therefore the memory key - unchanged, and an
	// exhausted history Plan carries its mark straight into whole-item mode.
	//
	// Written per path instead, this is a rule each path has to remember about
	// a field most of them never touch, and the ones that forget do not fail
	// here: they fail in the contract layer, on every write that Plan ever
	// attempts again.
	// "Data arrived" is not the same as "something was present". A group that
	// belongs to another business is present and is deliberately dropped rather
	// than remembered, so counting it here would clear the mark over an empty
	// memory - the other refusal. The whole-item group is the only one that is
	// both this item's own and never written down.
	//
	// Both errors the judgement can raise belong to a history roster derived
	// from the memory - a key that does not parse, and the version of what was
	// derived - and a stated roster derives nothing, so neither is reachable
	// here.
	result, _ := judgeAbsenceResult(newAbsenceJudge(input, false, nil), input.Memory, memoryKeys(input.Memory))
	return result
}

func copyGroupMemory(memory map[string]GroupMemory) map[string]GroupMemory {
	copied := make(map[string]GroupMemory, len(memory))
	for key, entry := range memory {
		copied[key] = entry
	}
	return copied
}
