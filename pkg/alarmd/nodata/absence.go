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

// Total is every absence the buckets counted, which equals Absent.
func (buckets AbsentAgeBuckets) Total() uint64 {
	return buckets.ThisRound + buckets.UnderHour + buckets.UnderDay + buckets.DayOrMore
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
	Facts    AbsenceFacts
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
	result := evaluateAbsence(input)

	// The Plan-level fact is decided here and nowhere else, for every path out
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
	// group, which rememberPresent skips because it is not a series - so an
	// exhausted Plan whose data comes back only under that group would other-
	// wise carry the fact forever: the memory it produces is empty, the fact is
	// carried, and the whole-item absence it describes is never reported again.
	// That is reachable rather than theoretical: the no-data agg_dimension is
	// not in deriveStateCompatibilityHash's closure (strategy/compiler.go:315-346
	// closes over the dataset's identity fields, not this), so emptying it
	// leaves StateGeneration - and therefore the memory key - unchanged, and an
	// exhausted history Plan carries its mark straight into whole-item mode.
	//
	// Written per branch instead, this is nine separate returns each having to
	// remember a rule about a field most of them never touch, and the ones that
	// forget do not fail here: they fail in the contract layer, on every write
	// that Plan ever attempts again.
	// "Data arrived" is not the same as "something was present". A group that
	// belongs to another business is present and is deliberately dropped rather
	// than remembered, so counting it here would clear the mark over an empty
	// memory - the other refusal. The whole-item group is the only one that is
	// both this item's own and never written down.
	_, result.WholeItemPresent = input.Present[WholeItemGroup().Key()]
	if len(result.Memory) != 0 || result.WholeItemPresent {
		result.TrackingExhaustedAt = 0
		return result
	}
	if result.TrackingExhaustedAt == 0 {
		result.TrackingExhaustedAt = input.TrackingExhaustedAt
	}
	return result
}

func evaluateAbsence(input AbsenceInput) AbsenceResult {
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
	if len(input.Roster.Groups) == 0 && input.Roster.Source == RosterTargetPlan {
		// A target plan that resolved, completely, to no member expects
		// nothing and says nothing about the item as a whole: the whole-item
		// absence is the old target's reading and is not claimed for the
		// new form. What a confirmed-empty target does decide is that every
		// member whose absence was open has left the target, so those
		// absences close once, here - a whole-item absence an earlier build
		// left open among them - and nothing is opened.
		closeDroppedAbsences(&result, input, false)
		delete(result.Memory, whole)
		return result
	}
	if len(input.Roster.Groups) == 0 {
		if len(input.Present) == 0 {
			// An empty roster has two causes that look identical from here:
			// this item never had groups, or the horizon emptied its history
			// roster. Only the second carries the Plan-level fact, and without
			// consulting it this branch rebuilds as a whole-item absence
			// exactly what the horizon has just stopped, every round.
			if input.TrackingExhaustedAt != 0 {
				closeDroppedAbsences(&result, input, true)
				return result
			}
			entry := result.Memory[whole]
			if entry.SuppressedAt != 0 {
				// The whole-item absence itself was stopped. Same rule as a
				// group's: no verdict, and the entry stays as it is.
				result.Facts.Suppressed++
				closeDroppedAbsences(&result, input, true)
				return result
			}
			if entry.FirstAbsent == 0 {
				entry.FirstAbsent = input.EvaluationTime
			}
			// The whole-item group is not a series and never records a
			// LastSeen: one would carry it into a history roster as if it were.
			entry.LastSeen = 0
			if trackingExpired(entry.FirstAbsent, input.EvaluationTime, input.TrackingHorizonSeconds) {
				// Marked rather than forgotten. This group is not in any
				// roster - it is what an empty roster produces - so deleting it
				// would have the next empty round build it again from nothing.
				result.Facts.Expired++
				entry.SuppressedAt = input.EvaluationTime
				result.Memory[whole] = entry
				closeDroppedAbsences(&result, input, true)
				return result
			}
			result.Verdicts[whole] = VerdictAnomaly
			result.Facts.Absent++
			result.Facts.AbsentAges.count(entry.FirstAbsent, input.EvaluationTime)
			result.Memory[whole] = entry
			closeDroppedAbsences(&result, input, true)
			return result
		}
		result.Verdicts[whole] = VerdictNormal
		delete(result.Memory, whole)
		for key := range input.Present {
			rememberPresent(result.Memory, key, input)
		}
		closeDroppedAbsences(&result, input, true)
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
			// A whole new entry, which is what clears a suppression: data
			// arriving is the one thing that restarts tracking, and it restarts
			// it from nothing rather than resuming the absence it interrupted.
			result.Memory[key] = GroupMemory{LastSeen: input.EvaluationTime}
			continue
		}
		entry := result.Memory[key]
		if entry.SuppressedAt != 0 {
			// Tracking of this absence has already stopped. No verdict, so no
			// synthetic anomaly, and the entry is left exactly as it is: a
			// horizon raised after the fact must not restart what a lower one
			// stopped, and the only thing that restarts it is data arriving.
			result.Facts.Suppressed++
			continue
		}
		// Only the first absent round starts the clock. Restarting it every
		// round would hold the reported period count at one however long the
		// group stayed away.
		if entry.FirstAbsent == 0 {
			entry.FirstAbsent = input.EvaluationTime
		}
		if trackingExpired(entry.FirstAbsent, input.EvaluationTime, input.TrackingHorizonSeconds) {
			// The horizon is reached: stop producing this absence, and do it
			// without a verdict of any kind. A NORMAL here would be a recovery
			// nobody observed, which is the reading this whole mechanism exists
			// to avoid.
			result.Facts.Expired++
			if input.Roster.Source == RosterHistory {
				// A history roster is derived from this memory, so forgetting
				// the group is what stops it being expected. There is nowhere
				// else for it to come back from.
				delete(result.Memory, key)
				continue
			}
			// Something outside this memory expects this group and will go on
			// expecting it, so forgetting it would let the next round meet it
			// as new and start the clock again. The stopping is recorded
			// instead, compactly, beside the timestamps that explain it.
			entry.SuppressedAt = input.EvaluationTime
			result.Memory[key] = entry
			continue
		}
		result.Verdicts[key] = VerdictAnomaly
		result.Facts.Absent++
		result.Facts.AbsentAges.count(entry.FirstAbsent, input.EvaluationTime)
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

	closeDroppedAbsences(&result, input, false)

	// The Plan-level fact, decided last because it is about what the round
	// left behind rather than about any one group.
	//
	// It is set only for a history roster, and only when the horizon has just
	// emptied it: that is the one case where the next round meets an empty
	// roster that is not "this item has no groups". This is the only place that
	// sets it; carrying it forward on the rounds after belongs to Evaluate,
	// which applies that one rule to every path.
	if input.Roster.Source == RosterHistory && result.Facts.Expired > 0 &&
		len(result.Memory) == 0 && len(input.Present) == 0 {
		result.TrackingExhaustedAt = input.EvaluationTime
	}
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
// wholeItemDecided says the whole-item branch has already settled that group
// this round, including by deciding to say nothing about it. Without it the
// silent decisions are not silent: the whole-item group is not in any roster,
// so "the roster stopped expecting it" is true of it on every empty round, and
// the sweep turns a stopped absence into the closing NORMAL the horizon exists
// to avoid - then deletes the entry, so the next empty round opens the absence
// again from nothing. The paths that give the group a verdict do not need it;
// they are skipped as judged. The confirmed-empty target plan passes false on
// purpose: there the whole-item reading really is retired and closing it is the
// point.
func closeDroppedAbsences(result *AbsenceResult, input AbsenceInput, wholeItemDecided bool) {
	whole := WholeItemGroup().Key()
	for key, entry := range input.Memory {
		if entry.FirstAbsent == 0 {
			continue
		}
		if wholeItemDecided && key == whole {
			continue
		}
		if _, expected := input.Roster.Groups[key]; expected {
			continue
		}
		if _, judged := result.Verdicts[key]; judged {
			continue
		}
		if result.Memory[key].SuppressedAt != 0 {
			// Tracking of this absence was stopped before the roster dropped
			// the group. Nothing is standing to close, so the group is
			// forgotten without a verdict rather than recovered with one.
			delete(result.Memory, key)
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
