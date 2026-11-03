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
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// An object whose window reaches back a hundred rounds keeps a hundred
// rounds, where sixteen used to leave the older holes NOT_IN_MEMORY and the
// row this side's for good. The rounds kept are every one from where the
// worker says the windows start, and none older.
func TestAnObjectKeepsTheRoundsItsWindowsReachBackTo(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-long"})
	const period, first = int64(60), int64(6000)
	// A hundred and twenty rounds, the windows reaching back a hundred.
	for i := int64(0); i < 120; i++ {
		end := first + i*period
		round(ctx, tracker, end+period, "FULL_COMPLETED", "", "", primary("FULL", "DATA"),
			&observability.HistoryCoverageFacts{Levels: 1, End: end, WindowStart: end - 99*period})
	}
	last := first + 119*period
	start := last - 99*period
	short := &observability.HistoryCoverageFacts{Levels: 1, Short: 1, WorstValid: 97, WorstRequired: 100, End: last, WindowStart: start,
		Windows: []observability.HistoryWindowFact{{Series: "c", Level: 1, Valid: 97, Required: 100, End: last,
			Missing: []int64{start, start + period, last - period}, MissingTotal: 3}}}
	for i := 0; i < 10; i++ {
		round(ctx, tracker, last+period, "COMPLETED_WITH_UNAVAILABLE", "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED", primary("FULL", "DATA"), short)
	}
	rows := anyColumn(tracker)
	if len(rows) != 1 || rows[0].Coverage == nil || len(rows[0].Coverage.Windows) != 1 {
		t.Fatalf("rows = %+v, want the one object with its one named window", rows)
	}
	coverage := rows[0].Coverage
	if got := coverage.Windows[0].HolesBy; got != (WindowHoleCounts{AnsweredWithoutSeries: 3}) {
		t.Fatalf("holes by cause = %+v, want every minute of the hundred-round window read against its round", got)
	}
	// The hundred rounds from the window start to the last minute, and the
	// ten short rounds of that same minute after them.
	if coverage.RoundsRemembered != 110 || coverage.RoundsKept != coverage.RoundsRemembered {
		t.Fatalf("rounds remembered/kept = %d/%d, want 110 kept by the window", coverage.RoundsRemembered, coverage.RoundsKept)
	}
	state := tracker.groups["qg-long"]
	if state == nil || state.rounds[0].end != start {
		t.Fatalf("the oldest round kept is at %d, want the window start %d", state.rounds[0].end, start)
	}
}

// Rounds are kept in minute order, whatever order they arrive in: a Slot
// replayed after later ones lands at its minute and is found there.
func TestAReplayedRoundLandsAtItsMinute(t *testing.T) {
	state := &queryGroupState{}
	for _, end := range []int64{600, 660, 720} {
		rememberRound(state, end+60, "FULL_COMPLETED", "", &observability.HistoryCoverageFacts{Levels: 1, End: end, WindowStart: 300},
			primary("FULL", "DATA"))
	}
	rememberRound(state, 540, "COMPLETED_WITH_UNAVAILABLE", "QUERY_TIMEOUT", &observability.HistoryCoverageFacts{Levels: 1, End: 480},
		primary("PARTIAL", "DATA"))
	for i := 1; i < len(state.rounds); i++ {
		if state.rounds[i-1].end > state.rounds[i].end {
			t.Fatalf("rounds out of minute order: %+v", state.rounds)
		}
	}
	got, found := roundAt(state.rounds, 480)
	if !found || got.answer != primaryNotWhole || got.reasonWord() != "QUERY_TIMEOUT" {
		t.Fatalf("round at 480 = %+v (found %v), want the replayed incomplete round", got, found)
	}
	// Past the window start nothing older is kept; the replayed round is
	// dropped once the windows start after it.
	rememberRound(state, 840, "FULL_COMPLETED", "", &observability.HistoryCoverageFacts{Levels: 1, End: 780, WindowStart: 600},
		primary("FULL", "DATA"))
	if _, found := roundAt(state.rounds, 480); found || state.rounds[0].end != 600 {
		t.Fatalf("rounds = %+v, want none before the window start 600", state.rounds)
	}
}

// A remembered round is sixteen bytes and holds no pointer: a day-long
// window keeps a day of them per object.
func TestARememberedRoundIsSixteenBytes(t *testing.T) {
	if size := unsafe.Sizeof(roundMark{}); size != 16 {
		t.Fatalf("roundMark is %d bytes, want 16", size)
	}
}

// The words a round is filed under are held once for the process: the same
// word is the same index, and a table that has run out of indexes reads a
// new word as none rather than overwriting one.
func TestTheRoundWordsAreHeldOnce(t *testing.T) {
	table := &roundWords{index: map[string]uint16{"": 0}, words: []string{""}}
	first, again := table.of("QUERY_TIMEOUT"), table.of("QUERY_TIMEOUT")
	if first == 0 || first != again || table.word(first) != "QUERY_TIMEOUT" {
		t.Fatalf("indexes %d and %d for one word, reading back %q", first, again, table.word(first))
	}
	for len(table.words) <= math.MaxUint16 {
		table.words = append(table.words, "")
	}
	if index := table.of("A_WORD_TOO_MANY"); index != 0 || table.word(first) != "QUERY_TIMEOUT" {
		t.Fatalf("a full table gave index %d and read the first word back as %q", index, table.word(first))
	}
}

// The memory reading counts each object under the least bucket its kept
// rounds fit, the edges inclusive; the bytes are the slices' capacity, what
// the heap holds, not their length; and only the objects whose worker named
// a window start count as window-sized.
func TestTheRoundMemoryReadingCountsEachObjectOnce(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	kept := map[string][2]int{
		"a": {16, 16}, "b": {17, 32}, "c": {64, 64}, "d": {65, 128},
		"e": {256, 256}, "f": {257, 512}, "g": {1440, 1440}, "h": {1441, 2048},
	}
	for key, lengths := range kept {
		tracker.groups[key] = &queryGroupState{rounds: make([]roundMark, lengths[0], lengths[1])}
	}
	tracker.groups["a"].windowStart = 0
	tracker.groups["b"].windowStart = 600
	tracker.groups["h"].windowStart = 60
	got := tracker.RoundMemory()
	want := map[string]int{"le_16": 1, "le_64": 2, "le_256": 2, "le_1440": 2, "gt_1440": 1}
	for _, bucket := range RoundMemoryBuckets {
		if got.Objects[bucket] != want[bucket] {
			t.Fatalf("objects = %v, want %v", got.Objects, want)
		}
	}
	rounds, capacity := 0, 0
	for _, lengths := range kept {
		rounds += lengths[0]
		capacity += lengths[1]
	}
	if got.Rounds != rounds || got.Bytes != uint64(capacity)*16 || got.MaxRounds != 1441 || got.WindowSized != 2 {
		t.Fatalf("reading = %+v, want %d rounds, %d bytes, most 1441, two window-sized", got, rounds, capacity*16)
	}
	if empty := (*Tracker)(nil).RoundMemory(); len(empty.Objects) != len(RoundMemoryBuckets) || empty.Rounds != 0 {
		t.Fatalf("nil tracker reading = %+v, want every bucket at zero", empty)
	}
}

// A minute evaluated twice -- the same Slot run again, or replayed -- has
// two rounds, and its hole is read against the later: that is what the
// minute last came to. In either order, and whether the minute was reported
// or inferred from the Slot.
func TestAMinuteRunTwiceIsReadAgainstItsLaterRound(t *testing.T) {
	type run struct {
		kind, cause, reason string
		primary             *observability.PrimaryInputFacts
	}
	whole := run{"FULL_COMPLETED", "", "", primary("FULL", "DATA")}
	empty := run{"FULL_EMPTY_COMPLETED", "", "", primary("FULL", "EMPTY")}
	incomplete := run{"COMPLETED_WITH_UNAVAILABLE", "PRIMARY_INPUT_UNAVAILABLE", "QUERY_TIMEOUT", primary("PARTIAL", "DATA")}
	for _, tc := range []struct {
		name         string
		first, later run
		reported     bool
		cause        HoleCause
		kind         string
	}{
		{"whole then incomplete", whole, incomplete, true, HoleInputIncomplete, "COMPLETED_WITH_UNAVAILABLE"},
		{"incomplete then whole", incomplete, whole, true, HoleAnsweredWithoutSeries, "FULL_COMPLETED"},
		{"inferred: empty then incomplete", empty, incomplete, false, HoleInputIncomplete, "COMPLETED_WITH_UNAVAILABLE"},
		{"inferred: incomplete then empty", incomplete, empty, false, HoleAnsweredEmpty, "FULL_EMPTY_COMPLETED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := newTracker(t, &clock{at: now})
			ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-twice"})
			// Minute 5880 at Slot 5940 teaches the offset; minute 6000 runs
			// twice at Slot 6060.
			round(ctx, tracker, 5940, "FULL_COMPLETED", "", "", primary("FULL", "DATA"), &observability.HistoryCoverageFacts{Levels: 1, End: 5880})
			for _, r := range []run{tc.first, tc.later} {
				var coverage *observability.HistoryCoverageFacts
				if tc.reported {
					coverage = &observability.HistoryCoverageFacts{Levels: 1, End: 6000}
				}
				round(ctx, tracker, 6060, r.kind, r.cause, r.reason, r.primary, coverage)
			}
			short := &observability.HistoryCoverageFacts{Levels: 1, Short: 1, WorstValid: 2, WorstRequired: 3, End: 6060,
				Windows: []observability.HistoryWindowFact{{Series: "c", Level: 1, Valid: 2, Required: 3, End: 6060,
					Missing: []int64{6000}, MissingTotal: 1}}}
			for i := 0; i < DefaultDegradedRounds; i++ {
				round(ctx, tracker, 6120, "COMPLETED_WITH_UNAVAILABLE", "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED", primary("FULL", "DATA"), short)
			}
			rows := anyColumn(tracker)
			if len(rows) != 1 || rows[0].Coverage == nil || len(rows[0].Coverage.Windows) != 1 || len(rows[0].Coverage.Windows[0].Holes) != 1 {
				t.Fatalf("rows = %+v, want the one object with one hole on its one window", rows)
			}
			hole := rows[0].Coverage.Windows[0].Holes[0]
			if hole.Cause != tc.cause || hole.Round != tc.kind || hole.Inferred == tc.reported {
				t.Fatalf("hole = %+v, want %s from the later %q (inferred %v)", hole, tc.cause, tc.kind, !tc.reported)
			}
		})
	}
}

// The reading names the object keeping the most rounds: its Query Group,
// its strategies sorted and bounded with the total beside them, and where its
// windows start. Of two keeping as many the least key is named, whatever
// order the table is walked in; an object keeping none is never named.
func TestTheRoundMemoryReadingNamesTheLargestObject(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	if got := tracker.RoundMemory(); got.Largest != nil {
		t.Fatalf("empty tracker names %+v, want nothing", got.Largest)
	}
	tracker.groups["qg-none"] = &queryGroupState{}
	if got := tracker.RoundMemory(); got.Largest != nil {
		t.Fatalf("a tracker whose objects keep no rounds names %+v, want nothing", got.Largest)
	}
	strategies := map[StrategyRef]struct{}{}
	for _, id := range []string{"9", "901", "12", "7", "31", "5"} {
		strategies[StrategyRef{StrategyID: id, BusinessID: "2"}] = struct{}{}
	}
	tracker.groups["qg-b-long"] = &queryGroupState{rounds: make([]roundMark, 1469), windowStart: 86_400, strategies: strategies}
	tracker.groups["qg-a-short"] = &queryGroupState{rounds: make([]roundMark, 300), windowStart: 172_000}
	tracker.groups["qg-c-long"] = &queryGroupState{rounds: make([]roundMark, 1469), windowStart: 90_000}
	for i := 0; i < 20; i++ {
		got := tracker.RoundMemory().Largest
		if got == nil || got.QueryGroup != "qg-b-long" || got.Rounds != 1469 || got.StrategiesTotal != 6 ||
			got.WindowStart == nil || !got.WindowStart.Equal(time.Unix(86_400, 0)) {
			t.Fatalf("largest = %+v, want qg-b-long, the least key of the two keeping 1469", got)
		}
		all := make([]StrategyRef, 0, len(strategies))
		for strategy := range strategies {
			all = append(all, strategy)
		}
		sortStrategies(all)
		if len(got.Strategies) != 4 {
			t.Fatalf("strategies = %+v, want the first 4 of the sorted %+v", got.Strategies, all)
		}
		for j := range got.Strategies {
			if got.Strategies[j] != all[j] {
				t.Fatalf("strategies = %+v, want the first 4 of the sorted %+v", got.Strategies, all)
			}
		}
	}
	// Fifty keeping as many: whichever the walk meets first, the least key.
	tied := newTracker(t, &clock{at: now})
	for i := 49; i >= 0; i-- {
		tied.groups[fmt.Sprintf("qg-tied-%02d", i)] = &queryGroupState{rounds: make([]roundMark, 60)}
	}
	for i := 0; i < 20; i++ {
		if got := tied.RoundMemory().Largest; got == nil || got.QueryGroup != "qg-tied-00" {
			t.Fatalf("largest of fifty tied = %+v, want qg-tied-00", got)
		}
	}
	summary := tracker.RoundMemory().Summary()
	if summary.Rounds != 1469*2+300 || summary.Bytes != uint64(1469*2+300)*16 || summary.Largest == nil || summary.Largest.QueryGroup != "qg-b-long" {
		t.Fatalf("summary = %+v, want the counts and the named object", summary)
	}
}

// Each replica's row carries the round memory it published, the largest
// object named, on the health route by its JSON names; a copy, so a later
// change to the snapshot does not reach it; and absent for a replica that
// published none, not filled in as zero.
func TestTheReplicaRowCarriesItsRoundMemory(t *testing.T) {
	windowStart := time.Unix(86_400, 0).UTC()
	snapshots := healthySnapshots()
	snapshots[0].RoundMemory = &RoundMemorySummary{Rounds: 1769, Bytes: 28304, Largest: &LargestRoundMemory{
		QueryGroup: "qg-long", Strategies: []StrategyRef{{StrategyID: "901", BusinessID: "2"}}, StrategiesTotal: 1,
		Rounds: 1469, WindowStart: &windowStart}}
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	rows := map[string]ReplicaView{}
	for _, row := range view.PerReplica {
		rows[row.Replica] = row
	}
	got := rows["pod-a"].RoundMemory
	if got == nil || got.Rounds != 1769 || got.Largest == nil || got.Largest.QueryGroup != "qg-long" || got.Largest.Strategies[0].StrategyID != "901" {
		t.Fatalf("pod-a round memory = %+v, want what it published", got)
	}
	if rows["pod-b"].RoundMemory != nil {
		t.Fatalf("pod-b published none and its row says %+v", rows["pod-b"].RoundMemory)
	}
	snapshots[0].RoundMemory.Largest.Strategies[0].StrategyID = "1"
	snapshots[0].RoundMemory.Largest.QueryGroup = "qg-other"
	windowStart = time.Unix(1, 0).UTC()
	if got.Largest.Strategies[0].StrategyID != "901" || got.Largest.QueryGroup != "qg-long" || !got.Largest.WindowStart.Equal(time.Unix(86_400, 0)) {
		t.Fatal("the replica row aliases the snapshot's round memory")
	}
	snapshots[0].RoundMemory.Largest.Strategies[0].StrategyID = "901"
	snapshots[0].RoundMemory.Largest.QueryGroup = "qg-long"
	windowStart = time.Unix(86_400, 0).UTC()

	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	perReplica, _ := health["per_replica"].([]any)
	var memory map[string]any
	for _, row := range perReplica {
		if fields := row.(map[string]any); fields["replica"] == "pod-a" {
			memory, _ = fields["round_memory"].(map[string]any)
		}
	}
	largest, _ := memory["largest"].(map[string]any)
	if memory["rounds"] != float64(1769) || memory["bytes"] != float64(28304) || largest["query_group"] != "qg-long" ||
		largest["rounds"] != float64(1469) || largest["strategies_total"] != float64(1) || largest["window_start"] != "1970-01-02T00:00:00Z" {
		t.Fatalf("health per_replica round_memory = %v, want pod-a's by its JSON names", memory)
	}
}

// An object whose worker named no window start publishes none, not the zero
// time read as a real date, and an object running no strategy publishes an
// empty list, not null read as unknown -- on the snapshot as the tracker
// reads it and on the health route after the view copies it.
func TestALargestObjectWithoutAStartOrStrategiesSaysSo(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	tracker.groups["qg-last-sixteen"] = &queryGroupState{rounds: make([]roundMark, 16)}
	summary := tracker.RoundMemory().Summary()
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if text := string(encoded); strings.Contains(text, "window_start") || !strings.Contains(text, `"strategies":[]`) {
		t.Fatalf("encoded = %s, want no window_start and an empty strategies list", text)
	}

	snapshots := healthySnapshots()
	var published RoundMemorySummary
	if err := json.Unmarshal(encoded, &published); err != nil {
		t.Fatal(err)
	}
	snapshots[0].RoundMemory = &published
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	perReplica, _ := health["per_replica"].([]any)
	for _, row := range perReplica {
		fields := row.(map[string]any)
		if fields["replica"] != "pod-a" {
			continue
		}
		memory, _ := fields["round_memory"].(map[string]any)
		largest, _ := memory["largest"].(map[string]any)
		strategies, isList := largest["strategies"].([]any)
		if _, named := largest["window_start"]; named || !isList || len(strategies) != 0 {
			t.Fatalf("health largest = %v, want no window_start and strategies []", largest)
		}
		return
	}
	t.Fatalf("health per_replica = %v, want pod-a's row", perReplica)
}

// The rounds an object keeps past the fixed last sixteen grow under the
// observation memory line: the sixteen are not asked for, a full slice past
// them asks for the half again it grows by, and a refusal keeps it at what it
// holds and lets the oldest round go. The object says which minute it let go
// for the line while its windows still reach it, and stops saying so once
// they do not.
func TestRoundsPastTheFixedFewGrowUnderTheMemoryLine(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	var asked []uint64
	admit := true
	tracker.SetRoundAdmission(func(bytes uint64) bool {
		asked = append(asked, bytes)
		return admit
	})
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-line"})
	const period, first = int64(60), int64(60_000)
	start := first - 200*period
	feed := func(from, count int64, windowStart int64) {
		for i := from; i < from+count; i++ {
			end := first + i*period
			round(ctx, tracker, end+period, "FULL_COMPLETED", "", "", primary("FULL", "DATA"),
				&observability.HistoryCoverageFacts{Levels: 1, End: end, WindowStart: windowStart})
		}
	}
	feed(0, 16, start)
	state := tracker.groups["qg-line"]
	if len(asked) != 0 || len(state.rounds) != 16 {
		t.Fatalf("after sixteen rounds the line was asked %v (kept %d), want nothing asked", asked, len(state.rounds))
	}
	feed(16, 1, start)
	if fmt.Sprint(asked) != "[128]" || cap(state.rounds) != 24 || len(state.rounds) != 17 {
		t.Fatalf("the seventeenth round asked %v and kept %d of %d, want 128 bytes asked and 24 held", asked, len(state.rounds), cap(state.rounds))
	}
	admit = false
	feed(17, 7, start)
	if cap(state.rounds) != 24 || len(state.rounds) != 24 || tracker.RoundMemory().HeldByLine != 0 {
		t.Fatalf("filling the room asked nothing more: kept %d of %d, held %d", len(state.rounds), cap(state.rounds), tracker.RoundMemory().HeldByLine)
	}
	feed(24, 3, start)
	if cap(state.rounds) != 24 || len(state.rounds) != 24 || state.rounds[0].end != first+3*period {
		t.Fatalf("refused, the rounds are %d of %d from %d, want 24 kept from the fourth", len(state.rounds), cap(state.rounds), state.rounds[0].end)
	}
	if got := heldInWindow(state); got != first+2*period {
		t.Fatalf("held through %d, want the third round's minute %d", got, first+2*period)
	}
	if memory := tracker.RoundMemory(); memory.HeldByLine != 1 {
		t.Fatalf("round memory = %+v, want the one object held by the line", memory)
	}
	// The windows move past the minute let go: nothing held is in reach, and
	// the rounds they no longer name make the room, so the line is not asked.
	askedBefore := len(asked)
	feed(27, 1, first+5*period)
	if state.rounds[0].end != first+5*period || len(asked) != askedBefore {
		t.Fatalf("the rounds start at %d and the line was asked %v, want the new window start %d and nothing asked",
			state.rounds[0].end, asked[askedBefore:], first+5*period)
	}
	if got, memory := heldInWindow(state), tracker.RoundMemory(); got != 0 || memory.HeldByLine != 0 {
		t.Fatalf("held through %d, %d objects held, once the windows start past it; want none", got, memory.HeldByLine)
	}
	// Admitted again, the rounds grow again.
	admit = true
	feed(28, 30, start)
	if cap(state.rounds) <= 24 {
		t.Fatalf("admitted again the rounds stayed at %d", cap(state.rounds))
	}
}

// A row whose windows are short at a minute let go for the line says so
// beside the NOT_IN_MEMORY hole it reads.
func TestAHoleTheLineLetGoOfSaysSoOnTheRow(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	tracker.SetRoundAdmission(func(uint64) bool { return false })
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-held"})
	const period, first = int64(60), int64(60_000)
	start := first
	for i := int64(0); i < 20; i++ {
		end := first + i*period
		round(ctx, tracker, end+period, "FULL_COMPLETED", "", "", primary("FULL", "DATA"),
			&observability.HistoryCoverageFacts{Levels: 1, End: end, WindowStart: start})
	}
	last := first + 19*period
	short := &observability.HistoryCoverageFacts{Levels: 1, Short: 1, WorstValid: 19, WorstRequired: 20, End: last, WindowStart: start,
		Windows: []observability.HistoryWindowFact{{Series: "c", Level: 1, Valid: 19, Required: 20, End: last,
			Missing: []int64{first}, MissingTotal: 1}}}
	for i := 0; i < DefaultDegradedRounds; i++ {
		round(ctx, tracker, last+period, "COMPLETED_WITH_UNAVAILABLE", "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED", primary("FULL", "DATA"), short)
	}
	rows := anyColumn(tracker)
	if len(rows) != 1 || rows[0].Coverage == nil || len(rows[0].Coverage.Windows) != 1 {
		t.Fatalf("rows = %+v, want the one object with its window", rows)
	}
	coverage := rows[0].Coverage
	// Sixteen rounds fit; each round after them - four healthy, then the
	// short ones - let the oldest go, so the last let go is the one that
	// many minutes in.
	heldThrough := first + (20-16+DefaultDegradedRounds-1)*period
	if coverage.Windows[0].HolesBy != (WindowHoleCounts{HeldByLine: 1}) || coverage.Windows[0].Holes[0].Cause != HoleHeldByLine ||
		coverage.RoundsHeldThrough == nil || coverage.RoundsHeldThrough.Unix() != heldThrough {
		t.Fatalf("coverage = %+v held through %v, want the hole HELD_BY_MEMORY_LINE and held through %d",
			coverage.Windows[0].HolesBy, coverage.RoundsHeldThrough, heldThrough)
	}
	// Once the windows start past the minute let go - and past the next one,
	// so the round they no longer name makes the room and none is let go
	// for the line - the row says nothing of it.
	moved := &observability.HistoryCoverageFacts{Levels: 1, Short: 1, WorstValid: 19, WorstRequired: 20, End: last, WindowStart: heldThrough + 2*period,
		Windows: []observability.HistoryWindowFact{{Series: "c", Level: 1, Valid: 19, Required: 20, End: last,
			Missing: []int64{last}, MissingTotal: 1}}}
	round(ctx, tracker, last+period, "COMPLETED_WITH_UNAVAILABLE", "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED", primary("FULL", "DATA"), moved)
	rows = anyColumn(tracker)
	if len(rows) != 1 || rows[0].Coverage == nil || rows[0].Coverage.RoundsHeldThrough != nil {
		t.Fatalf("rows = %+v, want the one object and no minute held once the windows start past it", rows)
	}
}
