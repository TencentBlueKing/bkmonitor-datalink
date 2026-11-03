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
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

func boardPlan(queryGroup, replica string) DiagnosisPlan {
	return DiagnosisPlan{StrategyPlanRef: StrategyPlanRef{QueryGroup: queryGroup}, Replica: replica, Existence: "active"}
}

// boardFixture is two replicas: r1 published whole lists, r2 a cut late list.
func boardFixture(cut bool) ([]string, map[string]DiagnosisRow, *DueView, *ScheduleCensus) {
	view := &View{}
	aggregateDue(view, []Snapshot{
		{Replica: "r1", Owned: 4, OwnedObjects: []string{"qg-late", "qg-never", "qg-ok", "qg-overdue"}, Schedule: &ScheduleCensus{Late: 1, Overdue: 1, Never: 1},
			Due: &DueClasses{Late: []string{"qg-late"}, Overdue: []string{"qg-overdue"}, Never: []string{"qg-never"}}},
		{Replica: "r2", Owned: 1, OwnedObjects: []string{"qg-r2"}, Schedule: &ScheduleCensus{}, Due: &DueClasses{LateCut: cut}},
	})
	rows := map[string]DiagnosisRow{
		"1": {StrategyID: "1", Verdict: StateDetecting, Action: ActionNone, Plans: []DiagnosisPlan{boardPlan("qg-overdue", "r1"), boardPlan("qg-ok", "r1")}},
		"2": {StrategyID: "2", Verdict: StateDataAbsent, Action: ActionDataCheck, Plans: []DiagnosisPlan{boardPlan("qg-late", "r1"), boardPlan("qg-never", "r1")}},
		"3": {StrategyID: "3", Verdict: StateDetecting, Action: ActionNone, Plans: []DiagnosisPlan{boardPlan("qg-never", "r1")}},
		"4": {StrategyID: "4", Verdict: StateDetecting, Action: ActionNone, Plans: []DiagnosisPlan{boardPlan("qg-ok", "r1")}},
		"5": {StrategyID: "5", Verdict: StateNotDetecting, Action: ActionNone,
			Dispositions: []DiagnosisDisposition{{StrategyDisposition: StrategyDisposition{Disposition: dispositionCapabilityUnsupported, Reason: "QUERY_SOURCE_NOT_MIGRATED"}}}},
		"6": {StrategyID: "6", Verdict: StateRecovered, Action: ActionNone, Plans: []DiagnosisPlan{boardPlan("qg-r2", "r2")}},
		"7": {StrategyID: "7", Verdict: DiagnosisUnknown, Plans: []DiagnosisPlan{boardPlan("qg-overdue", "r1"), boardPlan("qg-r2", "r2")}},
	}
	universe := []string{"1", "2", "3", "4", "5", "6", "7"}
	return universe, rows, view.Due, &ScheduleCensus{Late: 1, Overdue: 1}
}

func boardOf(t *testing.T, cut bool) *boardIndex {
	t.Helper()
	universe, rows, due, census := boardFixture(cut)
	return buildBoard("c1", time.Unix(10_000, 0), "digest-1", universe, func(id string) DiagnosisRow { return rows[id] }, nil, due, census, nil)
}

func boardRows(index *boardIndex) map[string]BoardRow {
	byID := map[string]BoardRow{}
	for _, row := range index.rows {
		byID[row.StrategyID] = row
	}
	return byID
}

// A strategy is as punctual as its worst object: overdue, then late, then an
// object nobody can read, then one not started, then on time. A strategy with
// no object is not detecting.
func TestBoardPunctualityIsTheWorstOfItsObjects(t *testing.T) {
	rows := boardRows(boardOf(t, true))
	want := map[string]Punctuality{
		"1": PunctualityOverdue,      // overdue + on time
		"2": PunctualityLate,         // late + never
		"3": PunctualityNotStarted,   // never only
		"4": PunctualityOnTime,       // on time, its replica whole
		"5": PunctualityNotDetecting, // no object
		"6": PunctualityUnknown,      // its replica's list was cut
		"7": PunctualityOverdue,      // a listed overdue beats an unreadable one
	}
	for id, punctuality := range want {
		if rows[id].Punctuality != punctuality {
			t.Errorf("strategy %s punctuality = %s, want %s", id, rows[id].Punctuality, punctuality)
		}
	}
}

// The late strategies are the strategies with a late or overdue object, the
// facet's two cells added; the late objects are the census's. Both are exact
// while every list was published whole.
func TestBoardLateStrategiesAreTheFacetCellsAndExactWhenWhole(t *testing.T) {
	index := boardOf(t, false)
	facet := index.facets.Punctuality
	if index.lateStrats.Value != facet[PunctualityLate]+facet[PunctualityOverdue] || index.lateStrats.Value != 3 {
		t.Fatalf("late strategies %+v, facet late %d overdue %d; want 3, their sum", index.lateStrats, facet[PunctualityLate], facet[PunctualityOverdue])
	}
	if index.lateObjects.Value != 2 || index.lateObjects.AtLeast || index.lateStrats.AtLeast || index.atLeast != "" {
		t.Fatalf("late objects %+v strategies %+v reason %q; want exact 2 and 3", index.lateObjects, index.lateStrats, index.atLeast)
	}
}

// A cut list makes every count drawn from the lists a lower bound, said so and
// why; the late strategies still equal the facet's cells under the same mark.
func TestBoardCountsAreAtLeastWhenAListWasCut(t *testing.T) {
	index := boardOf(t, true)
	if !index.lateObjects.AtLeast || !index.lateStrats.AtLeast || index.atLeast == "" {
		t.Fatalf("late objects %+v strategies %+v reason %q; want both marked at least, with the reason", index.lateObjects, index.lateStrats, index.atLeast)
	}
	facet := index.facets.Punctuality
	if index.lateStrats.Value != facet[PunctualityLate]+facet[PunctualityOverdue] {
		t.Fatalf("late strategies %d != facet cells %d", index.lateStrats.Value, facet[PunctualityLate]+facet[PunctualityOverdue])
	}
}

// A replica that counts its objects without naming them -- a build before the
// lists -- reads unknown for its objects and marks the counts at least.
func TestBoardReplicaWithoutNamesReadsUnknown(t *testing.T) {
	view := &View{}
	aggregateDue(view, []Snapshot{{Replica: "old", Owned: 1, OwnedObjects: []string{"qg"}, Schedule: &ScheduleCensus{}}})
	index := buildBoard("c1", time.Unix(1, 0), "d", []string{"1"}, func(string) DiagnosisRow {
		return DiagnosisRow{StrategyID: "1", Verdict: StateDetecting, Plans: []DiagnosisPlan{boardPlan("qg", "old")}}
	}, nil, view.Due, &ScheduleCensus{}, nil)
	if index.rows[0].Punctuality != PunctualityUnknown || !index.lateStrats.AtLeast {
		t.Fatalf("row %+v strategies %+v; want unknown and at least", index.rows[0], index.lateStrats)
	}
}

// Every facet counts the whole universe, the states including recovered and
// unknown, so each facet's cells sum to the universe.
func TestBoardFacetsSumToTheUniverse(t *testing.T) {
	index := boardOf(t, false)
	sum := func(counts map[string]int) int {
		total := 0
		for _, count := range counts {
			total += count
		}
		return total
	}
	states, actions, punctualities := map[string]int{}, map[string]int{}, map[string]int{}
	for word, count := range index.facets.State {
		states[string(word)] = count
	}
	for word, count := range index.facets.Punctuality {
		punctualities[string(word)] = count
	}
	for word, count := range index.facets.Action {
		actions[string(word)] = count
	}
	if sum(states) != 7 || sum(punctualities) != 7 || sum(index.facets.Capability) != 7 {
		t.Fatalf("state %d punctuality %d capability %d, want each 7", sum(states), sum(punctualities), sum(index.facets.Capability))
	}
	if index.facets.State[StateRecovered] != 1 || index.facets.State[DiagnosisUnknown] != 1 {
		t.Fatalf("states %+v, want the recovered and the unknown counted", index.facets.State)
	}
	// The unknown row names no action: the action facet counts the six that do.
	if sum(actions) != 6 {
		t.Fatalf("actions sum %d, want 6", sum(actions))
	}
	if index.facets.Capability[CapabilityNotYetSupported] != 1 {
		t.Fatalf("capability %+v, want the withheld strategy not yet supported", index.facets.Capability)
	}
}

// Rows run in the words' action order and, within an action, longest standing
// first.
func TestBoardOrdersByActionThenLongestStanding(t *testing.T) {
	earlier, later := time.Unix(100, 0), time.Unix(200, 0)
	rows := map[string]DiagnosisRow{
		"a": {StrategyID: "a", Verdict: StateDetecting, Action: ActionNone},
		"b": {StrategyID: "b", Verdict: StateDataAbsent, Action: ActionDataCheck},
		"c": {StrategyID: "c", Verdict: StateDataAbsent, Action: ActionDataCheck},
		"d": {StrategyID: "d", Verdict: StateDefect, Action: ActionServiceFix},
	}
	lines := []StrategyLine{{StrategyID: "b", Since: &later}, {StrategyID: "c", Since: &earlier}}
	index := buildBoard("c1", time.Unix(1, 0), "d", []string{"a", "b", "c", "d"}, func(id string) DiagnosisRow { return rows[id] }, lines, nil, nil, nil)
	got := []string{}
	for _, row := range index.rows {
		got = append(got, row.StrategyID)
	}
	if want := []string{"d", "c", "b", "a"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
}

// A page continues its own computation; a cursor from another one is not
// continued but answered with the current computation's first page, marked.
func TestBoardPagesWithinOneComputationAndRereadsOtherwise(t *testing.T) {
	index := boardOf(t, false)
	first := index.page(BoardFilter{}, "", 3, DiagnosisSummaryFreshFor)
	if len(first.Rows) != 3 || first.NextCursor == "" || first.Matched != 7 || first.Reread {
		t.Fatalf("first page %d rows next %q matched %d reread %v", len(first.Rows), first.NextCursor, first.Matched, first.Reread)
	}
	second := index.page(BoardFilter{}, first.NextCursor, 3, DiagnosisSummaryFreshFor)
	if len(second.Rows) != 3 || second.Rows[0].StrategyID == first.Rows[0].StrategyID || second.Reread {
		t.Fatalf("second page %+v", second.Rows)
	}
	seen := map[string]bool{}
	for _, row := range append(append([]BoardRow{}, first.Rows...), second.Rows...) {
		if seen[row.StrategyID] {
			t.Fatalf("row %s on two pages", row.StrategyID)
		}
		seen[row.StrategyID] = true
	}
	other := buildBoard("c2", time.Unix(10_030, 0), "digest-1", []string{"1"}, func(string) DiagnosisRow {
		return DiagnosisRow{StrategyID: "1", Verdict: StateDetecting}
	}, nil, nil, nil, nil)
	reread := other.page(BoardFilter{}, first.NextCursor, 3, DiagnosisSummaryFreshFor)
	if !reread.Reread || len(reread.Rows) != 1 || reread.Rows[0].StrategyID != "1" {
		t.Fatalf("a foreign cursor: %+v", reread)
	}
	filtered := index.page(BoardFilter{Punctuality: map[Punctuality]bool{PunctualityLate: true, PunctualityOverdue: true}}, "", 50, DiagnosisSummaryFreshFor)
	if filtered.Matched != 3 {
		t.Fatalf("late or overdue matched %d, want 3", filtered.Matched)
	}
}

// boardAllowed is the board's page-facing keys at every level, and nothing
// else: a field added here without the page's agreement fails.
var boardAllowed = map[string][]string{
	"":       {"generated_at", "max_age_seconds", "universe_digest", "universe", "facets", "late_objects", "late_strategies", "at_least_reason", "matched", "rows", "next_cursor", "reread"},
	"facets": {"state", "action", "capability", "punctuality", "urgency"},
	"count":  {"value", "at_least"},
	"row": {"strategy_id", "tenant", "business_id", "state", "state_word", "action", "action_word", "watch", "watch_word",
		"capability_class", "line", "punctuality", "late_wait", "since", "since_basis", "since_word", "open_alerts", "kb_anchor",
		"urgency", "urgency_word", "reason"},
	"late_wait": {"stage", "reason", "since", "unknown", "warming"},
	"warming":   {"short", "short_fresh", "empty", "guarded"},
	"reason":    {"code", "sentence", "facts"},
	"facts":     {"read_hold", "read_early", "data_absent", "window", "refusal", "withheld"},
	"read_hold": {"arrival_latest_seconds", "hold_seconds", "hold_limit_seconds", "at_limit", "time_delay_seconds",
		"suggested_time_delay_seconds", "settling_wait_seconds"},
}

func keysOf(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func subset(t *testing.T, level string, keys []string) {
	t.Helper()
	allowed := map[string]bool{}
	for _, key := range boardAllowed[level] {
		allowed[key] = true
	}
	for _, key := range keys {
		if !allowed[key] {
			t.Errorf("level %q carries %q, not a page field", level, key)
		}
	}
}

// The board carries the page's fields and nothing else, at every level; it
// decodes strictly into its own type; and no string value names a developer
// coordinate -- the words of a query group, a Slot, a replica or a revision,
// or a query group's 64-hex identity -- including inside the server-written
// line.
func TestBoardCarriesOnlyPageFields(t *testing.T) {
	index := boardOf(t, true)
	since := time.Unix(50, 0)
	index.rows[0].Since, index.rows[0].SinceBasis, index.rows[0].SinceWord = &since, SinceAtLeast, "只会更久"
	index.rows[0].Watch, index.rows[0].WatchWord = WatchWindowFilling, "窗口在补"
	index.rows[0].Reason, _, _ = readHoldReasonForTest()
	page := index.page(BoardFilter{}, "", 2, DiagnosisSummaryFreshFor)
	encoded, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var strict BoardResponse
	if err := decoder.Decode(&strict); err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &top); err != nil {
		t.Fatal(err)
	}
	subset(t, "", keysOf(t, encoded))
	subset(t, "facets", keysOf(t, top["facets"]))
	subset(t, "count", keysOf(t, top["late_objects"]))
	subset(t, "count", keysOf(t, top["late_strategies"]))
	var rows []json.RawMessage
	if err := json.Unmarshal(top["rows"], &rows); err != nil {
		t.Fatal(err)
	}
	waits, reasons := 0, 0
	for _, row := range rows {
		subset(t, "row", keysOf(t, row))
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(row, &fields)
		if wait, present := fields["late_wait"]; present {
			waits++
			subset(t, "late_wait", keysOf(t, wait))
		}
		if reason, present := fields["reason"]; present {
			reasons++
			subset(t, "reason", keysOf(t, reason))
			var parts map[string]json.RawMessage
			_ = json.Unmarshal(reason, &parts)
			if raw, present := parts["facts"]; present {
				subset(t, "facts", keysOf(t, raw))
				var facts map[string]json.RawMessage
				_ = json.Unmarshal(raw, &facts)
				if hold, present := facts["read_hold"]; present {
					subset(t, "read_hold", keysOf(t, hold))
				}
			}
		}
	}
	if reasons == 0 {
		t.Fatal("no row carried a reason, so its fields went unchecked")
	}
	if waits == 0 {
		t.Fatal("no row carried a late wait, so its fields went unchecked")
	}
	hex := regexp.MustCompile(`[0-9a-f]{64}`)
	for _, forbidden := range []string{"查询组", "Slot", "副本", "修订", "query_group", "replica"} {
		if strings.Contains(string(encoded), forbidden) {
			t.Errorf("the board carries %q", forbidden)
		}
	}
	if hex.Match(encoded) {
		t.Errorf("the board carries a 64-hex identity")
	}
}

// Every state, action, watch and since word a row carries is the word table's.
func TestBoardWordsComeFromTheWordTable(t *testing.T) {
	words := ProductWords()
	index := boardOf(t, false)
	for _, row := range index.rows {
		if row.StateWord != words.State[row.State] || row.StateWord == "" {
			t.Errorf("row %s state word %q, want %q", row.StrategyID, row.StateWord, words.State[row.State])
		}
		if row.Action != "" && row.ActionWord != words.Action[row.Action] {
			t.Errorf("row %s action word %q, want %q", row.StrategyID, row.ActionWord, words.Action[row.Action])
		}
		if row.KBAnchor != "state-"+string(row.State) {
			t.Errorf("row %s anchor %q", row.StrategyID, row.KBAnchor)
		}
	}
}

// A replica whose owned set was cut cannot say which unlisted objects are on
// time: its unlisted objects read unknown and the counts are at least, even
// with every due list whole.
func TestBoardCutOwnedSetReadsUnknown(t *testing.T) {
	view := &View{}
	aggregateDue(view, []Snapshot{{Replica: "r1", Owned: 3, OwnedObjects: []string{"qg-a"}, Schedule: &ScheduleCensus{}, Due: &DueClasses{}}})
	index := buildBoard("c1", time.Unix(1, 0), "d", []string{"1"}, func(string) DiagnosisRow {
		return DiagnosisRow{StrategyID: "1", Verdict: StateDetecting, Plans: []DiagnosisPlan{boardPlan("qg-a", "r1")}}
	}, nil, view.Due, &ScheduleCensus{}, nil)
	if index.rows[0].Punctuality != PunctualityUnknown || !index.lateObjects.AtLeast {
		t.Fatalf("row %+v objects %+v; want unknown and at least when the owned set was cut", index.rows[0], index.lateObjects)
	}
}

// The row's line is the page's sentence in the table's words: state, then who
// acts, then the wait's reason when it is a wait -- never a code.
func TestBoardLineIsWrittenInTheTableWords(t *testing.T) {
	words := ProductWords()
	rows := map[string]DiagnosisRow{
		"1": {StrategyID: "1", Verdict: StateDataAbsent, Action: ActionDataCheck},
		"2": {StrategyID: "2", Verdict: StateResultUntrusted, Action: ActionWatch},
	}
	lines := []StrategyLine{{StrategyID: "2", Standing: Standing{State: StateResultUntrusted, Action: ActionWatch, Watch: WatchWindowFilling}}}
	index := buildBoard("c1", time.Unix(1, 0), "d", []string{"1", "2"}, func(id string) DiagnosisRow { return rows[id] }, lines, nil, nil, nil)
	got := boardRows(index)
	if want := words.State[StateDataAbsent] + " · " + words.Action[ActionDataCheck]; got["1"].Line != want {
		t.Errorf("line = %q, want %q", got["1"].Line, want)
	}
	if want := words.State[StateResultUntrusted] + " · " + words.Action[ActionWatch] + "（" + words.Watch[WatchWindowFilling] + "）"; got["2"].Line != want {
		t.Errorf("line = %q, want %q", got["2"].Line, want)
	}
}

// A cursor is bound to the selection it was taken under: the same cursor
// under another selection is not continued but answered with that
// selection's first page, marked, so a changed or dropped filter never skips
// or repeats rows silently.
func TestABoardCursorIsBoundToItsSelection(t *testing.T) {
	index := boardOf(t, false)
	first := index.page(BoardFilter{}, "", 3, DiagnosisSummaryFreshFor)
	late := BoardFilter{Punctuality: map[Punctuality]bool{PunctualityLate: true, PunctualityOverdue: true}}
	switched := index.page(late, first.NextCursor, 3, DiagnosisSummaryFreshFor)
	want := index.page(late, "", 3, DiagnosisSummaryFreshFor)
	if !switched.Reread || !reflect.DeepEqual(switched.Rows, want.Rows) {
		t.Fatalf("cursor under another selection: reread %v rows %+v, want the selection's first page %+v", switched.Reread, switched.Rows, want.Rows)
	}
	same := index.page(BoardFilter{}, first.NextCursor, 3, DiagnosisSummaryFreshFor)
	if same.Reread {
		t.Fatal("the same selection's cursor was not continued")
	}
	// Urgency is part of the selection like the other facets: a cursor taken
	// under one urgency is not continued under another, nor under none.
	optional := BoardFilter{Urgency: map[Urgency]bool{UrgencyOptional: true}}
	needed := BoardFilter{Urgency: map[Urgency]bool{UrgencyActionNeeded: true}}
	under := index.page(optional, "", 1, DiagnosisSummaryFreshFor)
	for name, filter := range map[string]BoardFilter{"another urgency": needed, "no urgency": {}} {
		if moved := index.page(filter, under.NextCursor, 1, DiagnosisSummaryFreshFor); under.NextCursor != "" && !moved.Reread {
			t.Errorf("a cursor taken under OPTIONAL continued under %s", name)
		}
	}
	if from := index.page(BoardFilter{}, first.NextCursor, 3, DiagnosisSummaryFreshFor); from.Reread {
		t.Fatal("the unfiltered cursor was not continued")
	}
	if switched := index.page(optional, first.NextCursor, 3, DiagnosisSummaryFreshFor); !switched.Reread {
		t.Error("an unfiltered cursor continued under OPTIONAL")
	}
}

// An object's class is the one its own replica -- the replica the plan names
// -- gives: a previous owner's snapshot that still lists it late does not make
// it late on the replica that holds it now and publishes it on time.
func TestBoardReadsAnObjectFromItsOwnReplica(t *testing.T) {
	view := &View{}
	aggregateDue(view, []Snapshot{
		{Replica: "a", Owned: 1, OwnedObjects: []string{"g"}, Schedule: &ScheduleCensus{Late: 1}, Due: &DueClasses{Late: []string{"g"}}},
		{Replica: "b", Owned: 1, OwnedObjects: []string{"g"}, Schedule: &ScheduleCensus{}, Due: &DueClasses{}},
	})
	index := buildBoard("c1", time.Unix(1, 0), "d", []string{"1"}, func(string) DiagnosisRow {
		return DiagnosisRow{StrategyID: "1", Verdict: StateDetecting, Plans: []DiagnosisPlan{boardPlan("g", "b")}}
	}, nil, view.Due, &ScheduleCensus{}, nil)
	if index.rows[0].Punctuality != PunctualityOnTime {
		t.Fatalf("punctuality %s, want on time from the replica that holds g", index.rows[0].Punctuality)
	}
}

// A board page takes as many rows as a diagnosis page: one bound over the same
// universe, never a smaller round number of its own.
func TestABoardPageTakesAsManyRowsAsADiagnosisPage(t *testing.T) {
	if BoardMaxRows != DiagnosisPageRows {
		t.Fatalf("board page bound %d, diagnosis page %d; want one bound", BoardMaxRows, DiagnosisPageRows)
	}
}

// readHoldReasonForTest is a held-at-limit reason as strategyReasonOf writes
// it, for the tests that walk its fields.
func readHoldReasonForTest() (*StrategyReason, ReadHoldFacts, DiagnosisRow) {
	hold := ReadHoldFacts{Millis: 600_000, LimitMillis: 600_000, ArrivalAgeMillis: 2_176_000, AtLimit: true, HeldSince: 1,
		DelaySeconds: 60, SuggestedDelaySeconds: 2160}
	decided := DiagnosisRow{StrategyID: "80", Verdict: StateResultUntrusted, Action: ActionStrategyEdit, Check: CheckReadBeforeComplete,
		deciding: &Anomaly{Kind: KindReadHeld, ReadHold: &hold}}
	return strategyReasonOf(decided, time.Unix(10_000, 0)), hold, decided
}
