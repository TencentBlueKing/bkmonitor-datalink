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
	"fmt"
	"testing"
)

// roundsHorizon is three periods of the absence fixtures' grid. The cases below
// walk a group one round at a time, so a horizon of a few rounds lets a single
// sequence cross it and keep going.
const roundsHorizon = 3 * absencePeriod

func roundsInput(at, horizon int64, roster Roster, present map[string]Group, memory map[string]GroupMemory) AbsenceInput {
	input := fullRound(at, roster, present, memory)
	input.TrackingHorizonSeconds = horizon
	return input
}

// wantNoVerdictFor fails when the round said anything at all about the group.
func wantNoVerdictFor(t *testing.T, result AbsenceResult, key, when string) {
	t.Helper()
	if verdict, judged := result.Verdicts[key]; judged {
		t.Fatalf("%s: the group got %q, want no verdict", when, verdict)
	}
}

// Data that arrives on the very round an absence reaches the horizon is data,
// and the round reports the group present (no-data tracking retention
// proposal, section 1: "when expiry and real data arrive in the same round,
// the real data wins"; section 4 item 2: presence is handled first, and it
// clears what the absence had built up).
//
// The case the stop mark cannot help with is the one where it has not been
// written yet: the absence is still being tracked, its age is exactly the
// horizon, and on this round it is both expiring and over. Taking the expiry
// first leaves the group silent on the one round it reported, so an alert open
// on it would not be offered its recovery. Taking the data first is a NORMAL,
// a memory that starts again from nothing - LastSeen at this round, no
// absence, no stop - and an absence the round after that is counted from that
// round, not from the old one.
//
// Both sides of the bound are run beside the exact one, and each round is run
// once more without the data, so the case shows that the round it chose really
// is the one where the absence would otherwise have stopped.
func TestDataArrivingOnTheRoundTheHorizonIsReachedIsPresentNotExpired(t *testing.T) {
	const firstAbsent = absenceRound1
	group := hostGroup(t, "192.0.2.10")
	whole := WholeItemGroup()
	for _, offset := range []struct {
		name    string
		age     int64
		expires bool
	}{
		{"one second short of the horizon", roundsHorizon - 1, false},
		{"exactly at the horizon", roundsHorizon, true},
		{"one second past the horizon", roundsHorizon + 1, true},
	} {
		at := firstAbsent + offset.age
		for _, shape := range []struct {
			name   string
			roster Roster
			key    string
			// present is what reports when the data comes back: the group
			// itself, or for an item without dimensions every series under
			// the whole-item group.
			present map[string]Group
			memory  GroupMemory
			// openAfter is the memory the round after holds for the group
			// when nothing arrives: a fresh absence from that round, with
			// the sighting just recorded kept as its LastSeen. The
			// whole-item group never records a sighting.
			openAfter GroupMemory
			// stoppedEntry is what the round leaves when the data does not
			// come: a target's group is marked and kept, a history group is
			// forgotten, and the whole-item group is marked.
			stoppedEntry *GroupMemory
		}{
			{
				name: "a static target's group", roster: staticRoster("v1", group), key: group.Key(),
				present: groupSet(group), memory: GroupMemory{LastSeen: firstAbsent - absencePeriod, FirstAbsent: firstAbsent},
				openAfter:    GroupMemory{LastSeen: at, FirstAbsent: at + absencePeriod},
				stoppedEntry: &GroupMemory{LastSeen: firstAbsent - absencePeriod, FirstAbsent: firstAbsent, SuppressedAt: at},
			},
			{
				name: "a history group", roster: historyRoster("v1", group), key: group.Key(),
				present: groupSet(group), memory: GroupMemory{LastSeen: firstAbsent - absencePeriod, FirstAbsent: firstAbsent},
				openAfter:    GroupMemory{LastSeen: at, FirstAbsent: at + absencePeriod},
				stoppedEntry: nil,
			},
			{
				name: "the whole item", roster: Roster{Version: "v1", Source: RosterWhole}, key: whole.Key(),
				present: groupSet(whole), memory: GroupMemory{FirstAbsent: firstAbsent},
				openAfter:    GroupMemory{FirstAbsent: at + absencePeriod},
				stoppedEntry: &GroupMemory{FirstAbsent: firstAbsent, SuppressedAt: at},
			},
		} {
			t.Run(fmt.Sprintf("%s, %s", shape.name, offset.name), func(t *testing.T) {
				memory := map[string]GroupMemory{shape.key: shape.memory}

				// The data arrives.
				back := evaluate(t, roundsInput(at, roundsHorizon, shape.roster, shape.present, memory))
				if got := back.Verdicts[shape.key]; got != VerdictNormal {
					t.Fatalf("the group reported on the round its absence was %ds old and got %q, want NORMAL: "+
						"real data wins over the horizon", offset.age, got)
				}
				wantTrackingFacts(t, back, 0, 0)
				if back.Facts.Absent != 0 {
					t.Fatalf("absent = %d on a round where the only expected group reported", back.Facts.Absent)
				}
				if shape.key == whole.Key() {
					// The whole-item group is not a series and is never
					// written down; its data leaves no entry at all.
					if entry, kept := back.Memory[shape.key]; kept {
						t.Fatalf("the whole item's entry survived its own data as %+v", entry)
					}
				} else {
					wantMemory(t, back, shape.key, GroupMemory{LastSeen: at})
				}

				// The round after, with nothing arriving, starts a new absence
				// from that round: the old start is gone with the data.
				after := evaluate(t, roundsInput(at+absencePeriod, roundsHorizon,
					rosterAfter(shape.roster, back.Memory), nil, back.Memory))
				if got := after.Verdicts[shape.key]; got != VerdictAnomaly {
					t.Fatalf("the round after the data the group got %q, want a fresh ANOMALY", got)
				}
				wantMemory(t, after, shape.key, shape.openAfter)
				if after.Facts.AbsentAges.ThisRound != 1 {
					t.Fatalf("absent ages = %+v, want the new absence filed as this round's", after.Facts.AbsentAges)
				}

				// The same round without the data: what the data overrode.
				silent := evaluate(t, roundsInput(at, roundsHorizon, shape.roster, nil, memory))
				if !offset.expires {
					if got := silent.Verdicts[shape.key]; got != VerdictAnomaly {
						t.Fatalf("without the data the round gave %q, want the absence still reported", got)
					}
					wantTrackingFacts(t, silent, 0, 0)
					return
				}
				wantNoVerdictFor(t, silent, shape.key, "without the data")
				wantTrackingFacts(t, silent, 1, 0)
				entry, kept := silent.Memory[shape.key]
				if shape.stoppedEntry == nil {
					if kept {
						t.Fatalf("without the data the history group stayed remembered as %+v", entry)
					}
					return
				}
				if !kept || entry != *shape.stoppedEntry {
					t.Fatalf("without the data the entry is %+v (kept %t), want %+v", entry, kept, *shape.stoppedEntry)
				}
			})
		}
	}
}

// rosterAfter is the roster the round after reads: a target's roster is the
// target, the whole item's is empty, and a history roster is whatever the
// memory remembers having seen.
func rosterAfter(previous Roster, memory map[string]GroupMemory) Roster {
	if previous.Source != RosterHistory {
		return previous
	}
	groups := make(map[string]Group, len(memory))
	for key, entry := range memory {
		if entry.LastSeen == 0 {
			continue
		}
		group, ok := ParseGroupKey(key)
		if !ok {
			continue
		}
		groups[key] = group
	}
	return Roster{Version: previous.Version, Source: RosterHistory, Groups: groups}
}

// An absence whose tracking stopped is never reported again, so an alert the
// consumer closed in the meantime is not opened a second time (no-data
// tracking retention proposal, section 3: a group still expected by a target,
// and the whole item, keep a stop mark exactly so that the next rounds do not
// start the absence again; section 1 items 5 and 6: a closed alert is not
// reopened, and only data arriving starts a new tracking).
//
// The consumer is modelled the way the contract describes it: every absent
// round reaches it as an anomaly and keeps the alert open, and once tracking
// has stopped it closes the alert on its own. From then on an anomaly for the
// group, before data has come back, would open the alert again - which is the
// one thing the stop exists to prevent. The rounds after the stop are run for
// well over a horizon, so a mark that held for one round and then lapsed fails
// here too. The data then returns and goes away again, and that absence is
// reported as a new one: without it the case would also pass for a fixture
// that never reports anything at all.
func TestAStoppedAbsenceIsNeverReportedAgainSoAClosedAlertStaysClosed(t *testing.T) {
	group := hostGroup(t, "192.0.2.11")
	whole := WholeItemGroup()
	for _, shape := range []struct {
		name    string
		roster  Roster
		key     string
		present map[string]Group
		memory  GroupMemory
	}{
		{name: "a static target's group", roster: staticRoster("v1", group), key: group.Key(),
			present: groupSet(group), memory: GroupMemory{LastSeen: absenceRound1 - absencePeriod}},
		{name: "the whole item", roster: Roster{Version: "v1", Source: RosterWhole}, key: whole.Key(),
			present: groupSet(whole), memory: GroupMemory{}},
	} {
		t.Run(shape.name, func(t *testing.T) {
			consumer := closingConsumer{}
			memory := map[string]GroupMemory{}
			if shape.memory != (GroupMemory{}) {
				memory[shape.key] = shape.memory
			}
			round := func(index int, present map[string]Group) AbsenceResult {
				t.Helper()
				at := absenceRound1 + int64(index)*absencePeriod
				result := evaluate(t, roundsInput(at, roundsHorizon, shape.roster, present, memory))
				consumer.take(result.Verdicts[shape.key])
				memory = result.Memory
				return result
			}

			// Three absent rounds, each reported, then the round the absence
			// reaches the horizon: nothing is said and the stop is recorded.
			for index := 0; index < 3; index++ {
				result := round(index, nil)
				if got := result.Verdicts[shape.key]; got != VerdictAnomaly {
					t.Fatalf("absent round %d gave %q, want ANOMALY", index+1, got)
				}
			}
			stopAt := absenceRound1 + 3*absencePeriod
			stopped := round(3, nil)
			wantNoVerdictFor(t, stopped, shape.key, "the round the horizon was reached")
			wantTrackingFacts(t, stopped, 1, 0)
			mark := stopped.Memory[shape.key]
			if mark.SuppressedAt != stopAt || mark.FirstAbsent != absenceRound1 {
				t.Fatalf("the stop left %+v, want the absence's start kept and the stop at %d", mark, stopAt)
			}
			if consumer.opens != 1 {
				t.Fatalf("the consumer opened %d alerts over three absent rounds, want one", consumer.opens)
			}

			// The consumer closes the alert on its own.
			consumer.close()

			// Six more horizons of absence. Every round is silent, counts the
			// group as held down, and leaves its entry exactly as the stop
			// wrote it.
			for index := 4; index < 4+6*3; index++ {
				result := round(index, nil)
				wantNoVerdictFor(t, result, shape.key, fmt.Sprintf("absent round %d after the stop", index-3))
				wantTrackingFacts(t, result, 0, 1)
				if entry := result.Memory[shape.key]; entry != mark {
					t.Fatalf("round %d after the stop moved the entry to %+v, want it left at %+v", index-3, entry, mark)
				}
			}
			if consumer.reopens != 0 {
				t.Fatalf("the consumer reopened the closed alert %d times while the absence stayed stopped", consumer.reopens)
			}

			// The data comes back, and then goes away again: a new absence,
			// reported from its own first round.
			back := round(4+6*3, shape.present)
			if got := back.Verdicts[shape.key]; got != VerdictNormal {
				t.Fatalf("the round the data came back gave %q, want NORMAL", got)
			}
			again := round(4+6*3+1, nil)
			if got := again.Verdicts[shape.key]; got != VerdictAnomaly {
				t.Fatalf("the absence after the data came back gave %q, want it reported as a new one", got)
			}
			if consumer.reopens != 0 || consumer.opens != 2 {
				t.Fatalf("opens = %d reopens = %d, want the new absence opened as a second alert and nothing reopened",
					consumer.opens, consumer.reopens)
			}
		})
	}
}

// closingConsumer is the alert consumer as the tracking contract describes
// it: an anomaly opens the alert or keeps it open, a NORMAL ends it, and the
// consumer may close it on its own. An anomaly that arrives for an alert the
// consumer itself closed, before any data has come back in between, reopens
// what the consumer had already settled.
type closingConsumer struct {
	open, closedByConsumer bool
	opens, reopens         int
}

func (consumer *closingConsumer) take(verdict Verdict) {
	switch verdict {
	case VerdictAnomaly:
		if consumer.open {
			return
		}
		if consumer.closedByConsumer {
			consumer.reopens++
		}
		consumer.open = true
		consumer.opens++
	case VerdictNormal:
		consumer.open, consumer.closedByConsumer = false, false
	}
}

func (consumer *closingConsumer) close() {
	consumer.open, consumer.closedByConsumer = false, true
}

// Raising the horizon does not revive an absence a lower one stopped, and it
// does extend the absences that are still being tracked (no-data tracking
// retention proposal, section 1 item 6 and section 6: a longer horizon only
// affects absences that have not ended).
//
// Two groups of one static target, each walked from its first absent round.
// The first is stopped under a 120-second horizon. The horizon is then raised
// to 6000 seconds on the very round the second would have stopped under the
// old one. The second is the control: it is still reported, which shows the
// raised horizon is the one in force and is reaching the evaluation. The first
// says nothing on that round or any after it, is counted as held down, and its
// entry stays exactly as the stop wrote it. Without the control the first half
// passes just as well for an evaluation that never tracked anything.
func TestRaisingTheHorizonLeavesAStoppedAbsenceStoppedAndExtendsAnOpenOne(t *testing.T) {
	const lowered, raised = int64(120), int64(6000)
	stopped, open := hostGroup(t, "192.0.2.12"), hostGroup(t, "192.0.2.13")
	roster := staticRoster("v1", stopped, open)
	at := func(index int) int64 { return absenceRound1 + int64(index)*absencePeriod }
	memory := map[string]GroupMemory{stopped.Key(): {LastSeen: at(-1)}}

	// Round 0: the first group is absent from here, the second still reports.
	first := evaluate(t, roundsInput(at(0), lowered, roster, groupSet(open), memory))
	wantVerdicts(t, first, map[string]Verdict{stopped.Key(): VerdictAnomaly, open.Key(): VerdictNormal,
		WholeItemGroup().Key(): VerdictNormal})
	// Round 1: both absent.
	second := evaluate(t, roundsInput(at(1), lowered, roster, nil, first.Memory))
	wantVerdicts(t, second, map[string]Verdict{stopped.Key(): VerdictAnomaly, open.Key(): VerdictAnomaly})
	// Round 2: the first absence is 120 seconds old and stops; the second is
	// 60 seconds old and is still reported.
	third := evaluate(t, roundsInput(at(2), lowered, roster, nil, second.Memory))
	wantVerdicts(t, third, map[string]Verdict{open.Key(): VerdictAnomaly})
	wantTrackingFacts(t, third, 1, 0)
	mark := GroupMemory{LastSeen: at(-1), FirstAbsent: at(0), SuppressedAt: at(2)}
	wantMemory(t, third, stopped.Key(), mark)

	// Round 3 under the old horizon, for the record: the second absence is
	// now 120 seconds old and would stop here as the first did.
	unraised := evaluate(t, roundsInput(at(3), lowered, roster, nil, third.Memory))
	wantVerdicts(t, unraised, map[string]Verdict{})
	wantTrackingFacts(t, unraised, 1, 1)

	// Round 3 and the rounds after it under the raised horizon.
	memory = third.Memory
	for index := 3; index < 3+8; index++ {
		result := evaluate(t, roundsInput(at(index), raised, roster, nil, memory))
		wantVerdicts(t, result, map[string]Verdict{open.Key(): VerdictAnomaly})
		wantTrackingFacts(t, result, 0, 1)
		wantMemory(t, result, stopped.Key(), mark)
		wantMemory(t, result, open.Key(), GroupMemory{LastSeen: at(0), FirstAbsent: at(1)})
		if result.Facts.Absent != 1 {
			t.Fatalf("round %d: absent = %d, want the open absence alone reported", index, result.Facts.Absent)
		}
		memory = result.Memory
	}
}

// The same for the whole item, whose stop lives in a branch of its own: it is
// what an empty roster produces and never enters the roster loop, so the rule
// has to hold there separately. The whole item is one group per item, so the
// control is a second item walked in lockstep whose absence began a round
// later: under the raised horizon it is still reported on the round it would
// have stopped under the old one.
func TestRaisingTheHorizonLeavesAStoppedWholeItemAbsenceStopped(t *testing.T) {
	const lowered, raised = int64(120), int64(6000)
	whole := WholeItemGroup().Key()
	empty := Roster{Version: "v1", Source: RosterWhole}
	at := func(index int) int64 { return absenceRound1 + int64(index)*absencePeriod }
	whole1 := groupSet(WholeItemGroup())

	// Round 0: the first item is silent from here; the second still reports.
	stopped := evaluate(t, roundsInput(at(0), lowered, empty, nil, map[string]GroupMemory{}))
	open := evaluate(t, roundsInput(at(0), lowered, empty, whole1, map[string]GroupMemory{}))
	wantVerdicts(t, stopped, map[string]Verdict{whole: VerdictAnomaly})
	wantVerdicts(t, open, map[string]Verdict{whole: VerdictNormal})
	// Round 1: both silent.
	stopped = evaluate(t, roundsInput(at(1), lowered, empty, nil, stopped.Memory))
	open = evaluate(t, roundsInput(at(1), lowered, empty, nil, open.Memory))
	wantVerdicts(t, stopped, map[string]Verdict{whole: VerdictAnomaly})
	wantVerdicts(t, open, map[string]Verdict{whole: VerdictAnomaly})
	// Round 2: the first absence is 120 seconds old and stops.
	stopped = evaluate(t, roundsInput(at(2), lowered, empty, nil, stopped.Memory))
	open = evaluate(t, roundsInput(at(2), lowered, empty, nil, open.Memory))
	wantVerdicts(t, stopped, map[string]Verdict{})
	wantTrackingFacts(t, stopped, 1, 0)
	wantVerdicts(t, open, map[string]Verdict{whole: VerdictAnomaly})
	mark := GroupMemory{FirstAbsent: at(0), SuppressedAt: at(2)}
	wantMemory(t, stopped, whole, mark)

	// Round 3 under the old horizon, for the record: the second item's
	// absence would stop here.
	unraised := evaluate(t, roundsInput(at(3), lowered, empty, nil, open.Memory))
	wantVerdicts(t, unraised, map[string]Verdict{})
	wantTrackingFacts(t, unraised, 1, 0)

	for index := 3; index < 3+8; index++ {
		stopped = evaluate(t, roundsInput(at(index), raised, empty, nil, stopped.Memory))
		open = evaluate(t, roundsInput(at(index), raised, empty, nil, open.Memory))
		wantVerdicts(t, stopped, map[string]Verdict{})
		wantTrackingFacts(t, stopped, 0, 1)
		wantMemory(t, stopped, whole, mark)
		wantVerdicts(t, open, map[string]Verdict{whole: VerdictAnomaly})
		wantTrackingFacts(t, open, 0, 0)
		wantMemory(t, open, whole, GroupMemory{FirstAbsent: at(1)})
	}
}
