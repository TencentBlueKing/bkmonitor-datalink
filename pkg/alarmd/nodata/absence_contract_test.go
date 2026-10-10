// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package nodata

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// These are the contract cases for the absence evaluation, one group of tests
// per rule of the decomposition's layer-3 contract (rules A1 to A9, A8 as
// ruled on 2026-09-16), plus the
// two multi-round cases the rules only imply. They pin what Evaluate must say,
// not how it says it: a test here names an input and the verdicts, memory and
// facts that must come out, and nothing about the order the rules are applied
// in or the shape of the loop that applies them.
//
// Every case is also run against the zero implementation (return
// AbsenceResult{}) before it is trusted, and must fail there: a contract case
// that a do-nothing Evaluate satisfies is not pinning anything.

const (
	absencePeriod = int64(60)
	// Round timestamps sit on a period boundary, as a Slot's EvaluationTime
	// does; nothing here depends on it, but a fixture that could not occur
	// should not be the one a rule is read from.
	absenceRound1 = int64(1_700_000_100)
	absenceRound2 = absenceRound1 + absencePeriod
	absenceRound3 = absenceRound2 + absencePeriod
)

var hostNoDataDimensions = []string{"bk_target_ip", "bk_target_cloud_id"}

// hostGroup is one no-data group of a host-dimension item, the shape the
// first cut judges: a static IP target reduced to bk_target_ip and
// bk_target_cloud_id.
func hostGroup(t *testing.T, ip string) Group {
	t.Helper()
	group, ok := Project(map[string]string{"bk_target_ip": ip, "bk_target_cloud_id": "0"}, hostNoDataDimensions)
	if !ok {
		t.Fatalf("fixture: Project() rejected a complete host series for %s", ip)
	}
	return group
}

func groupSet(groups ...Group) map[string]Group {
	set := make(map[string]Group, len(groups))
	for _, group := range groups {
		set[group.Key()] = group
	}
	return set
}

func staticRoster(version string, groups ...Group) Roster {
	return Roster{Version: version, Source: RosterTargetStatic, Groups: groupSet(groups...)}
}

func historyRoster(version string, groups ...Group) Roster {
	return Roster{Version: version, Source: RosterHistory, Groups: groupSet(groups...)}
}

func fullRound(at int64, roster Roster, present map[string]Group, memory map[string]GroupMemory) AbsenceInput {
	return AbsenceInput{
		EvaluationTime: at, PeriodSeconds: absencePeriod, Completeness: execution.CompletenessFull,
		Present: present, Roster: roster, Memory: memory,
	}
}

func copyMemory(memory map[string]GroupMemory) map[string]GroupMemory {
	if memory == nil {
		return nil
	}
	copied := make(map[string]GroupMemory, len(memory))
	for key, entry := range memory {
		copied[key] = entry
	}
	return copied
}

func copyGroups(groups map[string]Group) map[string]Group {
	if groups == nil {
		return nil
	}
	copied := make(map[string]Group, len(groups))
	for key, group := range groups {
		copied[key] = group
	}
	return copied
}

// evaluate runs Evaluate and checks the properties every round must satisfy
// whatever the rules say about the particular input:
//
//   - the input is not modified - the caller keeps the previous Memory to
//     decide what to persist, and a retried Slot must see the same input;
//   - a verdict is only ever given to a roster group, to the whole-item
//     group, or - as the one NORMAL that closes it - to a remembered group
//     with an open absence that the roster no longer expects (A8); never to a
//     group the item did not expect and has nothing open on (A5);
//   - when the roster is not empty, every roster group has a verdict - a
//     group that is expected and gets no answer is the silent gap the
//     completeness gate exists to prevent;
//   - the whole-item group never records a LastSeen: it is not a series, and a
//     LastSeen would carry it into the history roster (A8) as if it were one;
//   - the facts are the counts of what was said and what was expected; the
//     roster source is reported as the caller declared it, never rewritten,
//     and Dropped passes through.
func evaluate(t *testing.T, input AbsenceInput) AbsenceResult {
	t.Helper()
	memoryBefore, presentBefore, rosterBefore := copyMemory(input.Memory), copyGroups(input.Present), copyGroups(input.Roster.Groups)
	result := Evaluate(input)
	if !reflect.DeepEqual(input.Memory, memoryBefore) || !reflect.DeepEqual(input.Present, presentBefore) || !reflect.DeepEqual(input.Roster.Groups, rosterBefore) {
		t.Fatal("Evaluate() modified its input")
	}
	whole := WholeItemGroup().Key()
	for key, verdict := range result.Verdicts {
		if _, expected := input.Roster.Groups[key]; expected || key == whole {
			continue
		}
		if entry, remembered := input.Memory[key]; remembered && entry.FirstAbsent != 0 && verdict == VerdictNormal {
			// The closing NORMAL of an absence the roster stopped expecting (A8).
			continue
		}
		t.Fatalf("Evaluate() gave verdict %q to %q, which is neither a roster group, the whole-item group, nor an open absence being closed", verdict, key)
	}
	if len(input.Roster.Groups) > 0 {
		for key := range input.Roster.Groups {
			if _, judged := result.Verdicts[key]; judged {
				continue
			}
			// The horizon adds a third answer to "present" and "absent":
			// stopped, on purpose, and stated by giving no verdict at all. The
			// property still holds - an expected group is never left without an
			// answer - but the answer is now in the memory rather than in the
			// verdicts, so it is read from there. A history group is forgotten
			// as it expires and an expected one is marked; anything else is the
			// silent gap this check exists to catch.
			entry, remembered := result.Memory[key]
			if remembered && entry.SuppressedAt != 0 {
				continue
			}
			if !remembered && input.Roster.Source == RosterHistory && input.TrackingHorizonSeconds > 0 {
				continue
			}
			t.Fatalf("Evaluate() gave no verdict to roster group %q and did not record it as stopped", key)
		}
	}
	if entry, ok := result.Memory[whole]; ok && entry.LastSeen != 0 {
		t.Fatalf("Evaluate() recorded LastSeen %d for the whole-item group", entry.LastSeen)
	}
	var absent, unavailable uint64
	for _, verdict := range result.Verdicts {
		switch verdict {
		case VerdictAnomaly:
			absent++
		case VerdictUnavailable:
			unavailable++
		case VerdictNormal:
		default:
			t.Fatalf("Evaluate() produced verdict %q, which the contract does not name", verdict)
		}
	}
	// Expired and Suppressed cannot be derived from the verdicts: a group the
	// horizon stopped gets no verdict at all, which is the whole point of it.
	// They are carried through here and asserted by name in the horizon tests.
	// What is checkable from here is the one direction that must hold whatever
	// the rules say: with no horizon configured, nothing can expire this round.
	if input.TrackingHorizonSeconds == 0 && result.Facts.Expired != 0 {
		t.Fatalf("Evaluate() expired %d absences with no horizon configured", result.Facts.Expired)
	}
	// The ages are counted where Absent is, so their sum is Absent on every
	// path; which bucket each absence lands in is asserted by name in the
	// age tests.
	if total := result.Facts.AbsentAges.Total(); total != absent {
		t.Fatalf("Evaluate() filed %d absences by age for %d absent: %+v", total, absent, result.Facts.AbsentAges)
	}
	wantFacts := AbsenceFacts{
		Present: uint64(len(input.Present)), Expected: uint64(len(input.Roster.Groups)), Absent: absent, Unavailable: unavailable,
		Dropped: input.Dropped, RosterSource: input.Roster.Source, RosterVersion: input.Roster.Version,
		Expired: result.Facts.Expired, Suppressed: result.Facts.Suppressed, AbsentAges: result.Facts.AbsentAges,
	}
	if result.Facts != wantFacts {
		t.Fatalf("Facts = %+v, want %+v", result.Facts, wantFacts)
	}
	return result
}

// wantVerdicts compares the verdicts as a set of statements: an
// implementation that has nothing to say may return a nil map or an empty one.
func wantVerdicts(t *testing.T, result AbsenceResult, want map[string]Verdict) {
	t.Helper()
	if len(want) == 0 && len(result.Verdicts) == 0 {
		return
	}
	if !reflect.DeepEqual(result.Verdicts, want) {
		t.Fatalf("Verdicts = %v, want %v", result.Verdicts, want)
	}
}

func wantMemory(t *testing.T, result AbsenceResult, key string, want GroupMemory) {
	t.Helper()
	if got := result.Memory[key]; got != want {
		t.Fatalf("Memory[%q] = %+v, want %+v", key, got, want)
	}
}

// A1. A Slot that is not FULL cannot tell absence from a query that did not
// return: every expected group is UNAVAILABLE and the memory is left exactly
// as it was - including the LastSeen of a group that did show up, because a
// partial answer is not evidence about what it did not contain either.
func TestAbsence_A1_NonFullSlotIsUnavailableAndLeavesMemoryAlone(t *testing.T) {
	seen, gone := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.2")
	before := map[string]GroupMemory{
		seen.Key(): {LastSeen: absenceRound1 - absencePeriod},
		gone.Key(): {LastSeen: absenceRound1 - 3*absencePeriod, FirstAbsent: absenceRound1 - 2*absencePeriod},
	}
	for name, completeness := range map[string]execution.Completeness{
		"partial":     execution.CompletenessPartial,
		"unavailable": execution.CompletenessUnavailable,
		// A completeness the evaluation does not recognise is not FULL
		// either; the gate fails closed rather than reading an unknown
		// word as a complete answer.
		"unset": "",
	} {
		t.Run(name, func(t *testing.T) {
			input := fullRound(absenceRound1, staticRoster("v1", seen, gone), groupSet(seen), copyMemory(before))
			input.Completeness = completeness
			result := evaluate(t, input)
			wantVerdicts(t, result, map[string]Verdict{seen.Key(): VerdictUnavailable, gone.Key(): VerdictUnavailable})
			if !reflect.DeepEqual(result.Memory, before) {
				t.Fatalf("Memory = %+v, want the input memory %+v untouched", result.Memory, before)
			}
			if result.Facts.RosterSource != RosterTargetStatic {
				t.Fatalf("Facts.RosterSource = %q, want the input's %q", result.Facts.RosterSource, RosterTargetStatic)
			}
		})
	}
}

// A1 with nothing expected: there is nothing to call unavailable, and the
// whole-item rules (A2, A3) do not run on an answer that is not complete.
func TestAbsence_A1_NonFullSlotWithEmptyRosterSaysNothing(t *testing.T) {
	input := fullRound(absenceRound1, staticRoster("v1"), nil, map[string]GroupMemory{})
	input.Completeness = execution.CompletenessPartial
	result := evaluate(t, input)
	wantVerdicts(t, result, map[string]Verdict{})
	if len(result.Memory) != 0 {
		t.Fatalf("Memory = %+v, want empty", result.Memory)
	}
}

// A2. No expected groups and no data at all: the one thing that can be said
// is that the item as a whole has no data, so the whole-item group is the
// anomaly and starts counting its absence from this round. The facts keep the
// roster source as declared: an empty roster declared TARGET_STATIC is a
// target that resolved to no host, and that is the thing to be able to see.
func TestAbsence_A2_NoRosterAndNoDataIsOneWholeItemAnomaly(t *testing.T) {
	whole := WholeItemGroup().Key()
	for name, roster := range map[string]Roster{
		"declared whole":            {Version: "v1", Source: RosterWhole, Groups: map[string]Group{}},
		"history with nothing seen": historyRoster("v1"),
		"target resolved to none":   staticRoster("v1"),
	} {
		t.Run(name, func(t *testing.T) {
			result := evaluate(t, fullRound(absenceRound1, roster, nil, map[string]GroupMemory{}))
			wantVerdicts(t, result, map[string]Verdict{whole: VerdictAnomaly})
			wantMemory(t, result, whole, GroupMemory{FirstAbsent: absenceRound1})
			if result.Facts.RosterSource != roster.Source || result.Facts.Expected != 0 {
				t.Fatalf("Facts = %+v, want the declared source %q and Expected 0", result.Facts, roster.Source)
			}
		})
	}
}

// A2 across rounds: the whole-item group's FirstAbsent is the first round it
// was absent, not the latest, because the event text counts periods from it.
func TestAbsence_A2_WholeItemFirstAbsentIsKeptAcrossRounds(t *testing.T) {
	whole := WholeItemGroup().Key()
	first := evaluate(t, fullRound(absenceRound1, historyRoster("v1"), nil, map[string]GroupMemory{}))
	second := evaluate(t, fullRound(absenceRound2, historyRoster("v1"), nil, first.Memory))
	wantVerdicts(t, second, map[string]Verdict{whole: VerdictAnomaly})
	wantMemory(t, second, whole, GroupMemory{FirstAbsent: absenceRound1})
}

// A3. No expected groups but data arrived: the whole-item group is NORMAL
// (so an earlier whole-item alert recovers and its absence clock is cleared),
// the groups that arrived get no verdict of their own, and they enter the
// memory - this is where the history roster grows from.
func TestAbsence_A3_NoRosterWithDataRecoversWholeItemAndRecordsTheGroups(t *testing.T) {
	whole := WholeItemGroup().Key()
	one, two := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.2")
	memory := map[string]GroupMemory{whole: {FirstAbsent: absenceRound1 - absencePeriod}}
	result := evaluate(t, fullRound(absenceRound1, historyRoster("v1"), groupSet(one, two), memory))
	wantVerdicts(t, result, map[string]Verdict{whole: VerdictNormal})
	wantMemory(t, result, whole, GroupMemory{})
	wantMemory(t, result, one.Key(), GroupMemory{LastSeen: absenceRound1})
	wantMemory(t, result, two.Key(), GroupMemory{LastSeen: absenceRound1})
	if result.Facts.RosterSource != RosterHistory || result.Facts.Expected != 0 {
		t.Fatalf("Facts = %+v, want the declared source HISTORY and Expected 0", result.Facts)
	}
}

// A3 with an empty agg_dimension: every series projects onto the whole-item
// group, so Present holds the whole-item group itself. It is NORMAL, and it is
// not remembered as seen - it is not a series, and the history roster must
// not come to expect the item as one of its own groups.
func TestAbsence_A3_WholeItemPresentIsNormalAndNotRemembered(t *testing.T) {
	whole := WholeItemGroup().Key()
	memory := map[string]GroupMemory{whole: {FirstAbsent: absenceRound1 - absencePeriod}}
	roster := Roster{Version: "v1", Source: RosterWhole, Groups: map[string]Group{}}
	result := evaluate(t, fullRound(absenceRound1, roster, groupSet(WholeItemGroup()), memory))
	wantVerdicts(t, result, map[string]Verdict{whole: VerdictNormal})
	if entry, ok := result.Memory[whole]; ok && entry != (GroupMemory{}) {
		t.Fatalf("Memory[whole] = %+v, want cleared", entry)
	}
	if result.Facts.Present != 1 || result.Facts.Expected != 0 || result.Facts.RosterSource != RosterWhole {
		t.Fatalf("Facts = %+v, want Present 1, Expected 0, source WHOLE", result.Facts)
	}
}

// A4. With a roster, each expected group is judged on its own: present is
// NORMAL and stamps LastSeen while clearing FirstAbsent; absent is ANOMALY and
// starts FirstAbsent only if it was not already running, so the period count
// in the event keeps growing instead of resetting every round.
func TestAbsence_A4_EachRosterGroupIsJudgedOnItsOwn(t *testing.T) {
	back, newlyGone, stillGone := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.2"), hostGroup(t, "10.0.0.3")
	earlier := absenceRound1 - 4*absencePeriod
	memory := map[string]GroupMemory{
		back.Key():      {LastSeen: earlier, FirstAbsent: absenceRound1 - absencePeriod},
		newlyGone.Key(): {LastSeen: absenceRound1 - absencePeriod},
		stillGone.Key(): {LastSeen: earlier, FirstAbsent: absenceRound1 - 2*absencePeriod},
	}
	result := evaluate(t, fullRound(absenceRound1, staticRoster("v1", back, newlyGone, stillGone), groupSet(back), memory))
	wantVerdicts(t, result, map[string]Verdict{
		back.Key(): VerdictNormal, newlyGone.Key(): VerdictAnomaly, stillGone.Key(): VerdictAnomaly,
	})
	wantMemory(t, result, back.Key(), GroupMemory{LastSeen: absenceRound1})
	wantMemory(t, result, newlyGone.Key(), GroupMemory{LastSeen: absenceRound1 - absencePeriod, FirstAbsent: absenceRound1})
	wantMemory(t, result, stillGone.Key(), GroupMemory{LastSeen: earlier, FirstAbsent: absenceRound1 - 2*absencePeriod})
	if _, ok := result.Memory[WholeItemGroup().Key()]; ok {
		t.Fatal("Memory carries the whole-item group although the item has a roster")
	}
}

// A4. A target the item has never seen is still expected: the first cut's
// roster is the target itself, so a static IP that never reported is an
// anomaly from its first round, with no history required.
func TestAbsence_A4_NeverSeenTargetIsAnomalyWithoutHistory(t *testing.T) {
	never := hostGroup(t, "10.0.0.9")
	result := evaluate(t, fullRound(absenceRound1, staticRoster("v1", never), nil, map[string]GroupMemory{}))
	wantVerdicts(t, result, map[string]Verdict{never.Key(): VerdictAnomaly})
	wantMemory(t, result, never.Key(), GroupMemory{FirstAbsent: absenceRound1})
}

// A5. A group that showed up but is not expected gets no verdict - it is
// neither alerted nor recovered - and only its LastSeen is recorded, which is
// how the history roster learns about it.
func TestAbsence_A5_UnexpectedGroupIsRememberedButNotJudged(t *testing.T) {
	expected, stranger := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.7")
	result := evaluate(t, fullRound(absenceRound1, staticRoster("v1", expected), groupSet(expected, stranger), map[string]GroupMemory{}))
	wantVerdicts(t, result, map[string]Verdict{expected.Key(): VerdictNormal})
	wantMemory(t, result, stranger.Key(), GroupMemory{LastSeen: absenceRound1})
}

// A6. A host the caller resolved to another business is not this item's to
// alert on: it is NORMAL (so any open alert recovers) and dropped from the
// memory, matching Python's recover-and-skip. One that is not expected and
// out of business does not even enter the memory.
func TestAbsence_A6_HostOutsideTheBusinessRecoversAndIsForgotten(t *testing.T) {
	ours, moved, strangerMoved := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.2"), hostGroup(t, "10.0.0.8")
	memory := map[string]GroupMemory{moved.Key(): {LastSeen: absenceRound1 - 3*absencePeriod, FirstAbsent: absenceRound1 - 2*absencePeriod}}
	input := fullRound(absenceRound1, staticRoster("v1", ours, moved), groupSet(strangerMoved), memory)
	input.OutOfBusiness = map[string]struct{}{moved.Key(): {}, strangerMoved.Key(): {}}
	result := evaluate(t, input)
	wantVerdicts(t, result, map[string]Verdict{ours.Key(): VerdictAnomaly, moved.Key(): VerdictNormal})
	for _, key := range []string{moved.Key(), strangerMoved.Key()} {
		if entry, ok := result.Memory[key]; ok {
			t.Fatalf("Memory[%q] = %+v, want the out-of-business group dropped", key, entry)
		}
	}
}

// A7. There is no "data arrived but is older than the last checkpoint"
// branch: a group in Present is present, whatever the memory says about when
// it was last seen - including a LastSeen that is later than this round.
func TestAbsence_A7_PresentIsNormalWhateverTheMemorySays(t *testing.T) {
	group := hostGroup(t, "10.0.0.1")
	for name, memory := range map[string]map[string]GroupMemory{
		"never seen":         {},
		"seen long ago":      {group.Key(): {LastSeen: absenceRound1 - 100*absencePeriod}},
		"seen in the future": {group.Key(): {LastSeen: absenceRound1 + absencePeriod}},
		"absent until now":   {group.Key(): {LastSeen: absenceRound1 - 2*absencePeriod, FirstAbsent: absenceRound1 - absencePeriod}},
	} {
		t.Run(name, func(t *testing.T) {
			result := evaluate(t, fullRound(absenceRound1, staticRoster("v1", group), groupSet(group), memory))
			wantVerdicts(t, result, map[string]Verdict{group.Key(): VerdictNormal})
			wantMemory(t, result, group.Key(), GroupMemory{LastSeen: absenceRound1})
		})
	}
}

// A8. When the roster changes, a remembered group the new roster no longer
// expects is handled by what it has open. One with an open absence - a
// FirstAbsent, so an alert may be standing on it - gets exactly one NORMAL so
// the alert can close, and is then forgotten: the roster stopped expecting it,
// so nothing will ever say NORMAL for it again, and without this one verdict
// the alert would stand forever. One with nothing open (a LastSeen only) is
// not judged and keeps its memory, which is where a history roster grows from.
// A group the new roster still expects carries its absence clock across the
// change. This is the user's ruling of 2026-09-16 (decomposition 5.9 item 3):
// a whole-item group turning into target groups, and a host confirmed gone,
// must end the old alert correctly.
func TestAbsence_A8_OpenAbsenceTheRosterDroppedIsClosedOnceAndForgotten(t *testing.T) {
	kept, removed, quiet := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.2"), hostGroup(t, "10.0.0.3")
	first := evaluate(t, fullRound(absenceRound1, staticRoster("v1", kept, removed, quiet), groupSet(quiet), map[string]GroupMemory{}))
	wantVerdicts(t, first, map[string]Verdict{kept.Key(): VerdictAnomaly, removed.Key(): VerdictAnomaly, quiet.Key(): VerdictNormal})

	// v2 expects only kept: removed has an open absence, quiet has only a LastSeen.
	second := evaluate(t, fullRound(absenceRound2, staticRoster("v2", kept), nil, first.Memory))
	wantVerdicts(t, second, map[string]Verdict{kept.Key(): VerdictAnomaly, removed.Key(): VerdictNormal})
	wantMemory(t, second, kept.Key(), GroupMemory{FirstAbsent: absenceRound1})
	if entry, ok := second.Memory[removed.Key()]; ok {
		t.Fatalf("Memory[removed] = %+v, want the closed absence forgotten", entry)
	}
	wantMemory(t, second, quiet.Key(), GroupMemory{LastSeen: absenceRound1})
	if second.Facts.RosterVersion != "v2" || second.Facts.Absent != 1 {
		t.Fatalf("Facts = %+v, want RosterVersion v2 and one absent (the closing NORMAL is not an absence)", second.Facts)
	}

	// The closing verdict is repeatable: a Slot whose events were not
	// acknowledged keeps the old memory and asks again, and must get the same
	// answer rather than a silent nothing.
	again := evaluate(t, fullRound(absenceRound2, staticRoster("v2", kept), nil, first.Memory))
	if !reflect.DeepEqual(again.Verdicts, second.Verdicts) || !reflect.DeepEqual(again.Memory, second.Memory) {
		t.Fatalf("a retried round answered differently: %v / %v vs %v / %v", again.Verdicts, again.Memory, second.Verdicts, second.Memory)
	}
}

// A8, whole item to targets. An item that expected nothing (A2) and is
// absent has an alert standing on the whole-item group. When a target roster
// appears, that alert is closed by one NORMAL on the whole-item group and the
// whole-item memory is dropped; the new target groups start their own clocks.
func TestAbsence_A8_WholeItemAbsenceIsClosedWhenATargetRosterArrives(t *testing.T) {
	whole := WholeItemGroup().Key()
	first := evaluate(t, fullRound(absenceRound1, staticRoster("v0"), nil, map[string]GroupMemory{}))
	wantVerdicts(t, first, map[string]Verdict{whole: VerdictAnomaly})
	wantMemory(t, first, whole, GroupMemory{FirstAbsent: absenceRound1})

	host := hostGroup(t, "10.0.0.1")
	second := evaluate(t, fullRound(absenceRound2, staticRoster("v1", host), nil, first.Memory))
	wantVerdicts(t, second, map[string]Verdict{whole: VerdictNormal, host.Key(): VerdictAnomaly})
	if entry, ok := second.Memory[whole]; ok {
		t.Fatalf("Memory[whole] = %+v, want the whole-item absence forgotten once closed", entry)
	}
	wantMemory(t, second, host.Key(), GroupMemory{FirstAbsent: absenceRound2})

	// And once closed it stays closed: the next round says nothing about the
	// whole-item group.
	third := evaluate(t, fullRound(absenceRound3, staticRoster("v1", host), nil, second.Memory))
	wantVerdicts(t, third, map[string]Verdict{host.Key(): VerdictAnomaly})
}

// A9. The first cut has no retirement: a history group that has been absent
// for days is still expected and still an anomaly, with its FirstAbsent
// untouched, until the roster is rebuilt (A8). Python keeps such a group in
// its dimension cache for as long as the item is checked.
func TestAbsence_A9_LongAbsentHistoryGroupIsStillExpected(t *testing.T) {
	old := hostGroup(t, "10.0.0.1")
	tenDays := int64(10 * 24 * 60 * 60)
	memory := map[string]GroupMemory{old.Key(): {LastSeen: absenceRound1 - tenDays, FirstAbsent: absenceRound1 - tenDays + absencePeriod}}
	result := evaluate(t, fullRound(absenceRound1, historyRoster("v1", old), nil, memory))
	wantVerdicts(t, result, map[string]Verdict{old.Key(): VerdictAnomaly})
	wantMemory(t, result, old.Key(), memory[old.Key()])
}

// A1 then FULL. An unavailable round is not an absent round: the group that
// was missing from the partial answer starts its absence clock at the first
// FULL round it is missing from, and the group that was in the partial answer
// has its LastSeen stamped only by the FULL round.
func TestAbsence_UnavailableRoundDoesNotCountAsAbsence(t *testing.T) {
	seen, gone := hostGroup(t, "10.0.0.1"), hostGroup(t, "10.0.0.2")
	partial := fullRound(absenceRound1, staticRoster("v1", seen, gone), groupSet(seen), map[string]GroupMemory{})
	partial.Completeness = execution.CompletenessPartial
	first := evaluate(t, partial)
	if len(first.Memory) != 0 {
		t.Fatalf("Memory after the partial round = %+v, want empty", first.Memory)
	}
	second := evaluate(t, fullRound(absenceRound2, staticRoster("v1", seen, gone), groupSet(seen), first.Memory))
	wantVerdicts(t, second, map[string]Verdict{seen.Key(): VerdictNormal, gone.Key(): VerdictAnomaly})
	wantMemory(t, second, seen.Key(), GroupMemory{LastSeen: absenceRound2})
	wantMemory(t, second, gone.Key(), GroupMemory{FirstAbsent: absenceRound2})
}

// Absence then return then absence again: the clock restarts at the second
// disappearance, not at the first, because the event text counts consecutive
// periods and the return broke the run.
func TestAbsence_ReturnResetsTheAbsenceClock(t *testing.T) {
	group := hostGroup(t, "10.0.0.1")
	roster := staticRoster("v1", group)
	first := evaluate(t, fullRound(absenceRound1, roster, nil, map[string]GroupMemory{}))
	wantMemory(t, first, group.Key(), GroupMemory{FirstAbsent: absenceRound1})
	second := evaluate(t, fullRound(absenceRound2, roster, groupSet(group), first.Memory))
	wantMemory(t, second, group.Key(), GroupMemory{LastSeen: absenceRound2})
	third := evaluate(t, fullRound(absenceRound3, roster, nil, second.Memory))
	wantVerdicts(t, third, map[string]Verdict{group.Key(): VerdictAnomaly})
	wantMemory(t, third, group.Key(), GroupMemory{LastSeen: absenceRound2, FirstAbsent: absenceRound3})
}

// Dropped is the projection's count of series that could not be placed in a
// group; the evaluation reports it beside its own counts without judging it.
func TestAbsence_DroppedSeriesPassThroughToTheFacts(t *testing.T) {
	group := hostGroup(t, "10.0.0.1")
	input := fullRound(absenceRound1, staticRoster("v1", group), groupSet(group), map[string]GroupMemory{})
	input.Dropped = 7
	result := evaluate(t, input)
	if result.Facts.Dropped != 7 {
		t.Fatalf("Facts.Dropped = %d, want 7", result.Facts.Dropped)
	}
}
