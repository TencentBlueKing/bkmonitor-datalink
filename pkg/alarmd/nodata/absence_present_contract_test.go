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
	"testing"
)

// The cases in this file are read from the finite-tracking contract, not from
// this package. Its rule for present data (section 4, item 2): every valid
// present group, whether or not the roster expects it, produces a normal
// candidate, has its suppression cleared and gets new memory; and every FULL
// round with this item's own data produces one whole-item normal candidate.
// Its rule for the expiry path (section 4, item 4): no NORMAL may come out of
// the dropped-group sweep or the out-of-business rule for a stopped absence,
// and a stopped absence that leaves the roster is cleaned silently.

const absenceRound4 = absenceRound3 + absencePeriod

// assertPresentRecovered checks one round's word on a present group: a NORMAL,
// and a memory entry that is a fresh sighting at that round with nothing open.
func assertPresentRecovered(t *testing.T, round string, result AbsenceResult, key string, at int64) {
	t.Helper()
	if got := result.Verdicts[key]; got != VerdictNormal {
		t.Fatalf("%s: verdict for the present group = %q, want NORMAL", round, got)
	}
	entry, kept := result.Memory[key]
	if !kept {
		t.Fatalf("%s: the present group was forgotten, want it remembered as seen at %d", round, at)
	}
	if entry != (GroupMemory{LastSeen: at}) {
		t.Fatalf("%s: memory for the present group = %+v, want only LastSeen %d", round, entry, at)
	}
}

// A host the target no longer names, with an open absence, reports again. The
// roster dropping it would close the absence with one NORMAL; the data
// arriving says the same, and keeps the host remembered as seen. Every round
// is checked: the retry of the first gives the same answer, and the host keeps
// being recovered and remembered while it keeps reporting.
func TestAPresentGroupTheTargetDroppedRecoversAndStaysRemembered(t *testing.T) {
	gone, stays := hostGroup(t, "192.0.2.1"), hostGroup(t, "192.0.2.2")
	memory := map[string]GroupMemory{
		gone.Key():  {LastSeen: absenceRound1 - 5*absencePeriod, FirstAbsent: absenceRound1 - 3*absencePeriod},
		stays.Key(): {LastSeen: absenceRound1 - absencePeriod},
	}
	roster := staticRoster("v2", stays)
	present := groupSet(gone, stays)

	first := Evaluate(fullRound(absenceRound1, roster, copyGroups(present), copyMemory(memory)))
	assertPresentRecovered(t, "round 1", first, gone.Key(), absenceRound1)

	retried := Evaluate(fullRound(absenceRound1, roster, copyGroups(present), copyMemory(memory)))
	assertPresentRecovered(t, "round 1 retried on the same memory", retried, gone.Key(), absenceRound1)

	second := Evaluate(fullRound(absenceRound2, roster, copyGroups(present), copyMemory(first.Memory)))
	assertPresentRecovered(t, "round 2", second, gone.Key(), absenceRound2)

	third := Evaluate(fullRound(absenceRound3, roster, copyGroups(present), copyMemory(second.Memory)))
	assertPresentRecovered(t, "round 3", third, gone.Key(), absenceRound3)
}

// The same host with its absence already stopped by the horizon. Data arriving
// is what restarts tracking, so the stop is cleared and the host is recovered
// and remembered, not forgotten.
func TestAPresentGroupWhoseStoppedAbsenceTheTargetDroppedRecoversAndIsRemembered(t *testing.T) {
	gone, stays := hostGroup(t, "192.0.2.1"), hostGroup(t, "192.0.2.2")
	memory := map[string]GroupMemory{
		gone.Key(): {LastSeen: absenceRound1 - 30*absencePeriod, FirstAbsent: absenceRound1 - 25*absencePeriod,
			SuppressedAt: absenceRound1 - 5*absencePeriod},
		stays.Key(): {LastSeen: absenceRound1 - absencePeriod},
	}
	result := Evaluate(fullRound(absenceRound1, staticRoster("v2", stays), groupSet(gone, stays), memory))
	assertPresentRecovered(t, "round 1", result, gone.Key(), absenceRound1)
}

// The other side of the same sweep: a stopped absence on a host that left the
// target and does not report is cleaned without a word.
func TestAnAbsentGroupWhoseStoppedAbsenceTheTargetDroppedIsForgottenSilently(t *testing.T) {
	gone, stays := hostGroup(t, "192.0.2.1"), hostGroup(t, "192.0.2.2")
	memory := map[string]GroupMemory{
		gone.Key(): {LastSeen: absenceRound1 - 30*absencePeriod, FirstAbsent: absenceRound1 - 25*absencePeriod,
			SuppressedAt: absenceRound1 - 5*absencePeriod},
		stays.Key(): {LastSeen: absenceRound1 - absencePeriod},
	}
	result := Evaluate(fullRound(absenceRound1, staticRoster("v2", stays), groupSet(stays), memory))
	if got, judged := result.Verdicts[gone.Key()]; judged {
		t.Fatalf("verdict for the stopped absence that left the target = %q, want none", got)
	}
	if _, kept := result.Memory[gone.Key()]; kept {
		t.Fatalf("the stopped absence that left the target is still remembered: %+v", result.Memory[gone.Key()])
	}
}

// A whole-item absence that a target roster replaced, on an item whose hosts
// report: the item has its own data, so the whole item recovers - in the round
// the target arrives and in every FULL round with data after it, not only once.
func TestTheWholeItemRecoversInEveryRoundWithItsOwnDataAfterATargetArrives(t *testing.T) {
	host := hostGroup(t, "192.0.2.1")
	whole := WholeItemGroup().Key()
	memory := map[string]GroupMemory{
		whole:      {FirstAbsent: absenceRound1 - 3*absencePeriod},
		host.Key(): {LastSeen: absenceRound1 - absencePeriod},
	}
	roster := staticRoster("v1", host)
	present := groupSet(host)

	previous := copyMemory(memory)
	for index, at := range []int64{absenceRound1, absenceRound2, absenceRound3} {
		result := Evaluate(fullRound(at, roster, copyGroups(present), previous))
		if got := result.Verdicts[whole]; got != VerdictNormal {
			t.Fatalf("round %d: whole-item verdict = %q, want NORMAL while the item has data", index+1, got)
		}
		if entry, kept := result.Memory[whole]; kept {
			t.Fatalf("round %d: whole-item memory kept as %+v, want it cleared by the item's own data", index+1, entry)
		}
		previous = result.Memory
	}
}

// The no-fabrication side of the same rule: the whole-item candidate stands on
// data the round admitted. A round where nothing arrived gives none. A round
// where only a host of another business arrived does give one: the backend
// recovers the whole item on any data, without asking whose host it was
// (nodata.py:85, :100), while that host itself is neither judged nor kept.
func TestTheWholeItemCandidateNeedsData(t *testing.T) {
	host, foreign := hostGroup(t, "192.0.2.1"), hostGroup(t, "192.0.2.9")
	whole := WholeItemGroup().Key()
	memory := map[string]GroupMemory{host.Key(): {LastSeen: absenceRound1 - absencePeriod}}

	nothing := Evaluate(fullRound(absenceRound1, staticRoster("v1", host), map[string]Group{}, copyMemory(memory)))
	if got, judged := nothing.Verdicts[whole]; judged {
		t.Fatalf("whole-item verdict with no data = %q, want none", got)
	}

	onlyForeign := fullRound(absenceRound1, staticRoster("v1", host), groupSet(foreign), copyMemory(memory))
	onlyForeign.OutOfBusiness = map[string]struct{}{foreign.Key(): {}}
	result := Evaluate(onlyForeign)
	if got := result.Verdicts[whole]; got != VerdictNormal {
		t.Fatalf("whole-item verdict with only another business's data = %q, want NORMAL", got)
	}
	if got, judged := result.Verdicts[foreign.Key()]; judged {
		t.Fatalf("the other business's host was judged %q, want no verdict", got)
	}
	if _, kept := result.Memory[foreign.Key()]; kept {
		t.Fatal("the other business's host was remembered")
	}
}

// A history group the horizon forgot comes back. It is in no roster - the
// history roster is read from the memory it was deleted from - and data
// arriving still recovers it in the round it arrives.
func TestAHistoryGroupTheHorizonForgotRecoversInTheRoundItReturns(t *testing.T) {
	returning, steady := hostGroup(t, "192.0.2.1"), hostGroup(t, "192.0.2.2")

	t.Run("beside groups the history still expects", func(t *testing.T) {
		memory := map[string]GroupMemory{steady.Key(): {LastSeen: absenceRound1 - absencePeriod}}
		result := Evaluate(fullRound(absenceRound1, historyRoster("v1", steady), groupSet(returning, steady), memory))
		assertPresentRecovered(t, "round 1", result, returning.Key(), absenceRound1)
	})

	t.Run("after the horizon emptied the history", func(t *testing.T) {
		input := fullRound(absenceRound1, historyRoster("v1"), groupSet(returning), map[string]GroupMemory{})
		input.TrackingExhaustedAt = absenceRound1 - 10*absencePeriod
		result := Evaluate(input)
		assertPresentRecovered(t, "round 1", result, returning.Key(), absenceRound1)
		if result.TrackingExhaustedAt != 0 {
			t.Fatalf("exhaustion fact = %d after a group returned, want it cleared", result.TrackingExhaustedAt)
		}
	})
}

// The out-of-business rule recovers a host whose absence is open, but a
// stopped absence has nothing standing to recover: the host is forgotten
// without a word. Both sides, so neither direction can be lost.
func TestAnOutOfBusinessHostRecoversOnlyAnAbsenceThatIsStillOpen(t *testing.T) {
	host := hostGroup(t, "192.0.2.1")
	foreign := map[string]struct{}{host.Key(): {}}

	open := fullRound(absenceRound1, staticRoster("v1", host), map[string]Group{}, map[string]GroupMemory{
		host.Key(): {LastSeen: absenceRound1 - 5*absencePeriod, FirstAbsent: absenceRound1 - 3*absencePeriod},
	})
	open.OutOfBusiness = foreign
	openResult := Evaluate(open)
	if got := openResult.Verdicts[host.Key()]; got != VerdictNormal {
		t.Fatalf("open absence on an out-of-business host: verdict = %q, want NORMAL", got)
	}
	if _, kept := openResult.Memory[host.Key()]; kept {
		t.Fatal("open absence on an out-of-business host is still remembered")
	}

	stopped := fullRound(absenceRound1, staticRoster("v1", host), map[string]Group{}, map[string]GroupMemory{
		host.Key(): {LastSeen: absenceRound1 - 30*absencePeriod, FirstAbsent: absenceRound1 - 25*absencePeriod,
			SuppressedAt: absenceRound1 - 5*absencePeriod},
	})
	stopped.OutOfBusiness = foreign
	stoppedResult := Evaluate(stopped)
	if got, judged := stoppedResult.Verdicts[host.Key()]; judged {
		t.Fatalf("stopped absence on an out-of-business host: verdict = %q, want none", got)
	}
	if _, kept := stoppedResult.Memory[host.Key()]; kept {
		t.Fatal("stopped absence on an out-of-business host is still remembered")
	}
}

// The dropped-group sweep, for a host that left the target and now reports
// for another business: its entry is removed as foreign data before the sweep
// runs, so the sweep has to read what was open from the memory the round
// started with. A stopped absence is forgotten without a word; an open one
// gets its one closing NORMAL. Both sides.
func TestAHostThatLeftTheTargetAndReportsForAnotherBusinessRecoversOnlyAnOpenAbsence(t *testing.T) {
	moved, stays := hostGroup(t, "192.0.2.1"), hostGroup(t, "192.0.2.2")
	for name, test := range map[string]struct {
		entry   GroupMemory
		verdict Verdict
	}{
		"open": {
			entry:   GroupMemory{LastSeen: absenceRound1 - 5*absencePeriod, FirstAbsent: absenceRound1 - 3*absencePeriod},
			verdict: VerdictNormal,
		},
		"stopped": {
			entry: GroupMemory{LastSeen: absenceRound1 - 30*absencePeriod, FirstAbsent: absenceRound1 - 25*absencePeriod,
				SuppressedAt: absenceRound1 - 5*absencePeriod},
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := fullRound(absenceRound1, staticRoster("v2", stays), groupSet(moved, stays), map[string]GroupMemory{
				moved.Key(): test.entry, stays.Key(): {LastSeen: absenceRound1 - absencePeriod},
			})
			input.OutOfBusiness = map[string]struct{}{moved.Key(): {}}
			result := Evaluate(input)
			if got := result.Verdicts[moved.Key()]; got != test.verdict {
				t.Fatalf("verdict = %q, want %q", got, test.verdict)
			}
			if _, kept := result.Memory[moved.Key()]; kept {
				t.Fatalf("the host that left for another business is still remembered: %+v", result.Memory[moved.Key()])
			}
		})
	}
}

// An item with nothing expected whose only data this round is another
// business's hosts: A3 as the backend has it, which recovers the whole item on
// whatever arrived (nodata.py:85, :100). The open whole-item absence closes,
// and the hosts are neither judged nor remembered. The other side is a round
// with nothing at all, which keeps the absence open.
func TestAnItemWithNothingExpectedRecoversOnAnyData(t *testing.T) {
	foreign := hostGroup(t, "192.0.2.9")
	whole := WholeItemGroup().Key()
	open := GroupMemory{FirstAbsent: absenceRound1 - 3*absencePeriod}

	onlyForeign := fullRound(absenceRound1, historyRoster("v1"), groupSet(foreign), map[string]GroupMemory{whole: open})
	onlyForeign.OutOfBusiness = map[string]struct{}{foreign.Key(): {}}
	result := Evaluate(onlyForeign)
	if got := result.Verdicts[whole]; got != VerdictNormal {
		t.Fatalf("whole-item verdict on another business's data = %q, want NORMAL", got)
	}
	if entry, kept := result.Memory[whole]; kept {
		t.Fatalf("whole-item memory = %+v, want it dropped once data arrived", entry)
	}
	if got, judged := result.Verdicts[foreign.Key()]; judged {
		t.Fatalf("another business's host was judged %q, want no verdict", got)
	}
	if _, kept := result.Memory[foreign.Key()]; kept {
		t.Fatal("another business's host was remembered")
	}

	nothing := Evaluate(fullRound(absenceRound1, historyRoster("v1"), nil, map[string]GroupMemory{whole: open}))
	if got := nothing.Verdicts[whole]; got != VerdictAnomaly {
		t.Fatalf("whole-item verdict with nothing at all = %q, want ANOMALY", got)
	}
	if got := nothing.Memory[whole]; got != open {
		t.Fatalf("whole-item memory = %+v with nothing at all, want the open absence kept %+v", got, open)
	}
}
