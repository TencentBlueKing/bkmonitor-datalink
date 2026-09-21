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
	"net/http"
	"testing"
	"time"
)

// emptyRounds completes the object empty every period for the whole span,
// advancing the tracker's clock, and returns how many rounds it completed.
func emptyRounds(tracker *Tracker, at *clock, queryGroup string, period, span time.Duration) int {
	rounds := 0
	for elapsed := time.Duration(0); elapsed <= span; elapsed += period {
		tracker.Observe(context.Background(), completion(queryGroup, "FULL_EMPTY_COMPLETED", "4101"))
		rounds++
		at.at = at.at.Add(period)
	}
	return rounds
}

func rowsOfKind(rows []Anomaly, kind string) map[string]Anomaly {
	byObject := map[string]Anomaly{}
	for _, row := range rows {
		if row.Kind == kind {
			byObject[row.QueryGroup] = row
		}
	}
	return byObject
}

// Five fifteen-second objects whose every round completes with no records
// and that never returned any are listed after an hour, each with its run of
// rounds, its start and the one cause this build produces -- and not before
// the hour, however many rounds that is. One that returned records first goes
// to the data side's line and never to this one; one that never did never
// goes to the data side's.
func TestObjectsEmptyEveryRoundAreListedAfterAnHourNotAfterARoundCount(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	period := 15 * time.Second
	// Two hundred rounds in fifty minutes is not an hour: the objects the
	// line exists for run every fifteen seconds, and a round count would
	// list them on the first pass of a source that reports each minute.
	started := at.at
	fifty := emptyRounds(tracker, at, "qg-15s-young", period, 50*time.Minute)
	if fifty < 200 {
		t.Fatalf("fifty minutes of fifteen-second rounds = %d, want at least 200 so the gate is not a count", fifty)
	}
	if listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound); len(listed) != 0 {
		t.Fatalf("listed after fifty minutes and %d empty rounds: %+v, want nothing before the hour", fifty, listed)
	}
	// The same object past the hour is listed, with the run whole.
	more := emptyRounds(tracker, at, "qg-15s-young", period, 11*time.Minute)
	listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)
	row, ok := listed["qg-15s-young"]
	if !ok {
		t.Fatalf("not listed after %d empty rounds over an hour: %+v", fifty+more, tracker.NoData())
	}
	if row.EmptyEveryRound == nil || row.EmptyEveryRound.Rounds != fifty+more || !row.EmptyEveryRound.Since.Equal(started) ||
		!row.EmptyEveryRound.NeverSawData || row.EmptyEveryRound.Cause != EmptyEveryRoundCauseUnknown ||
		!row.Since.Equal(started) || row.SinceFrom != SinceSnapshotContinuity || row.ReasonCode != "FULL_EMPTY_COMPLETED" ||
		len(row.Strategies) != 1 || row.Strategies[0].StrategyID != "4101" {
		t.Errorf("row = %+v facts %+v, want %d rounds since %s, never saw data, CAUSE_UNKNOWN, strategy 4101",
			row, row.EmptyEveryRound, fifty+more, started)
	}
	// Five of them: five rows, one each.
	for _, queryGroup := range []string{"qg-4102", "qg-4103", "qg-4104", "qg-4105"} {
		emptyRounds(tracker, at, queryGroup, period, 61*time.Minute)
	}
	if listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound); len(listed) != 5 {
		t.Errorf("listed = %d objects, want the five", len(listed))
	}
	// Under no column: the equation counts them healthy, which they are.
	if len(tracker.Anomalies())+len(tracker.Undecidable())+len(tracker.ByDesign())+len(tracker.Demoted()) != 0 {
		t.Error("an object empty every round is in a column of the health equation")
	}
	// Never returned records: never the data side's line, however long.
	if stopped := rowsOfKind(tracker.NoData(), KindNoData); len(stopped) != 0 {
		t.Errorf("objects that never returned records are on the data side's line: %v", stopped)
	}

	// Returned records once, then empty for two hours: the data side's line
	// only. The two kinds are told apart by whether records were ever seen,
	// and the hour does not move an object from one to the other.
	tracker.Observe(context.Background(), completion("qg-stopped", "FULL_COMPLETED", "8930"))
	emptyRounds(tracker, at, "qg-stopped", period, 2*time.Hour)
	rows := tracker.NoData()
	if _, listed := rowsOfKind(rows, KindEmptyEveryRound)["qg-stopped"]; listed {
		t.Error("an object that returned records once is listed as never having")
	}
	if stopped, listed := rowsOfKind(rows, KindNoData)["qg-stopped"]; !listed || stopped.EmptyEveryRound != nil {
		t.Errorf("an object whose data stopped = %+v, want on the data side's line without the never-seen facts", stopped)
	}

	// The two lines differ only where a round count would not tell them
	// apart: an object that returned records once and then completed empty
	// too few times for the data side's threshold, but over more than an
	// hour (a slow period). It is on neither line -- the hour is this line's
	// gate, "never saw data" is its predicate, and the second is what keeps
	// a slow object that did see data off it.
	slow := 35 * time.Minute // two empty rounds, seventy minutes: past the hour, below the round count
	tracker.Observe(context.Background(), completion("qg-slow-seen", "FULL_COMPLETED", "8931"))
	if few := emptyRounds(tracker, at, "qg-slow-seen", slow, 61*time.Minute); few >= DefaultDegradedRounds {
		t.Fatalf("slow object completed %d empty rounds, want fewer than the data side's %d so only the predicate decides", few, DefaultDegradedRounds)
	}
	rows = tracker.NoData()
	if _, listed := rowsOfKind(rows, KindEmptyEveryRound)["qg-slow-seen"]; listed {
		t.Error("a slow object that returned records once is listed as never having, on the hour alone")
	}
	if _, listed := rowsOfKind(rows, KindNoData)["qg-slow-seen"]; listed {
		t.Error("a slow object below the data side's round threshold is on the data side's line")
	}

	// Records arriving end it, and the object is then the data side's when
	// its rounds go empty again.
	tracker.Observe(context.Background(), completion("qg-15s-young", "FULL_COMPLETED", "4101"))
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-15s-young"]; listed {
		t.Error("still listed after a round with records")
	}
	emptyRounds(tracker, at, "qg-15s-young", period, 61*time.Minute)
	rows = tracker.NoData()
	if _, listed := rowsOfKind(rows, KindEmptyEveryRound)["qg-15s-young"]; listed {
		t.Error("listed as never having returned records after it did")
	}
	if _, listed := rowsOfKind(rows, KindNoData)["qg-15s-young"]; !listed {
		t.Error("not on the data side's line after its data stopped")
	}
}

// A restart restores only the last committed round. One that completed with
// records restores "seen", and the object is the data side's an hour later,
// not this line's; one that completed empty says nothing about the rounds
// before it, and the hour starts from the first empty round this process
// watches -- not from the restored round's clock.
func TestARestoredRoundWithRecordsCountsAsSeenAndAnEmptyOneDoesNot(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	restored := func(queryGroup, kind string) {
		if !tracker.Restore(queryGroup, RestoredState{LastCompletion: kind, NextSlot: at.at.Add(time.Minute),
			LastRound: &RestoredRound{Slot: at.at.Add(-3 * time.Hour), CompletedAt: at.at.Add(-3 * time.Hour), Kind: kind}}, at.at, 0) {
			t.Fatalf("%s was not restored", queryGroup)
		}
	}
	restored("qg-had-data", "FULL_COMPLETED")
	restored("qg-was-empty", "FULL_EMPTY_COMPLETED")
	firstEmpty := at.at
	for _, queryGroup := range []string{"qg-had-data", "qg-was-empty"} {
		saved := at.at
		emptyRounds(tracker, at, queryGroup, 15*time.Second, 61*time.Minute)
		at.at = saved
	}
	at.at = at.at.Add(61*time.Minute + 15*time.Second)
	rows := tracker.NoData()
	never, stopped := rowsOfKind(rows, KindEmptyEveryRound), rowsOfKind(rows, KindNoData)
	if _, listed := never["qg-had-data"]; listed {
		t.Error("an object restored from a round with records is listed as never having returned any")
	}
	if _, listed := stopped["qg-had-data"]; !listed {
		t.Error("an object restored from a round with records, empty since, is not on the data side's line")
	}
	row, listed := never["qg-was-empty"]
	if !listed {
		t.Fatalf("an object restored from an empty round, empty for an hour since, is not listed: %+v", rows)
	}
	if !row.Since.Equal(firstEmpty) || !row.EmptyEveryRound.Since.Equal(firstEmpty) {
		t.Errorf("since = %s, want the first empty round this process watched (%s), not the restored round's clock", row.Since, firstEmpty)
	}
	if _, listed := stopped["qg-was-empty"]; listed {
		t.Error("an object restored from an empty round is on the data side's line as if records had been seen")
	}
}

// Every round means every round: an object whose empty completions are
// followed by a blocked run is on the blocked line, not this one, and comes
// back to this one when its rounds complete empty again.
func TestABlockedRunAfterTheEmptyRoundsTakesTheObjectOffTheLine(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	emptyRounds(tracker, at, "qg-then-blocked", 15*time.Second, 61*time.Minute)
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-then-blocked"]; !listed {
		t.Fatal("not listed after an hour of empty rounds")
	}
	for round := 0; round < DefaultBlockedRounds; round++ {
		tracker.Observe(context.Background(), runOutcome("qg-then-blocked", "source_error"))
	}
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-then-blocked"]; listed {
		t.Error("listed as empty every round while its rounds are blocked")
	}
	if len(tracker.Anomalies()) != 1 {
		t.Errorf("anomalies = %d, want the blocked run", len(tracker.Anomalies()))
	}
	// The run ends with an empty completion: every round that completed was
	// empty again, and the hour it already has stands.
	tracker.Observe(context.Background(), completion("qg-then-blocked", "FULL_EMPTY_COMPLETED", "4101"))
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-then-blocked"]; !listed {
		t.Error("not listed again once the rounds complete empty")
	}
}

// On the verdict route the line is the strategy's, in governance rather than
// on the work list, with its own number on the first screen and its rows
// carrying the facts as published -- and the data side's line beside it is
// untouched: two owners, two lines, one column.
func TestTheVerdictRouteListsEmptyEveryRoundAsTheStrategysLine(t *testing.T) {
	snapshots := healthySnapshots()
	facts := func(rounds int) *EmptyEveryRoundFacts {
		return &EmptyEveryRoundFacts{Rounds: rounds, Since: now.Add(-2 * time.Hour), IntervalSeconds: 15,
			NeverSawData: true, Cause: EmptyEveryRoundCauseUnknown}
	}
	snapshots[0].NoData = []Anomaly{
		{QueryGroup: "qg-4101", Kind: KindEmptyEveryRound, ReasonCode: "FULL_EMPTY_COMPLETED", Replica: "pod-a",
			Since: now.Add(-2 * time.Hour), SinceFrom: SinceSnapshotContinuity, EmptyEveryRound: facts(480),
			Strategies: []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}},
		{QueryGroup: "qg-4102", Kind: KindEmptyEveryRound, ReasonCode: "FULL_EMPTY_COMPLETED", Replica: "pod-a",
			Since: now.Add(-2 * time.Hour), SinceFrom: SinceSnapshotContinuity, EmptyEveryRound: facts(479),
			Strategies: []StrategyRef{{StrategyID: "4102", BusinessID: "2"}}},
		{QueryGroup: "qg-stopped", Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED", Replica: "pod-a",
			Since: now.Add(-time.Hour), SinceFrom: SinceSnapshotContinuity,
			Strategies: []StrategyRef{{StrategyID: "77", BusinessID: "3"}}},
	}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	status, health := get(t, handler, "/api/health")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if health["empty_every_round_total"] != 2.0 {
		t.Errorf("empty_every_round_total = %v, want 2: the two never-seen objects, not the one whose data stopped", health["empty_every_round_total"])
	}
	// The lines and the arithmetic are on the objects response.
	_, objects := get(t, handler, "/api/objects?limit=1")
	checks, _ := objects["checks"].([]any)
	byCode := map[string]map[string]any{}
	for _, entry := range checks {
		report, _ := entry.(map[string]any)
		byCode[report["code"].(string)] = report
	}
	line := byCode["EMPTY_EVERY_ROUND"]
	if line == nil || line["owner"] != "STRATEGY" || line["objects"] != 2.0 || line["strategies"] != 2.0 || line["group_by"] != "strategy" {
		t.Fatalf("EMPTY_EVERY_ROUND line = %v, want the strategy's, 2 objects of 2 strategies folded by strategy", line)
	}
	if data := byCode["NO_DATA_PERSISTENT"]; data == nil || data["owner"] != "DATA" || data["objects"] != 1.0 {
		t.Errorf("NO_DATA_PERSISTENT line = %v, want the data side's over the one object whose data stopped", data)
	}
	// Governance, not the work list: the badge is decided elsewhere and the
	// reader is not asked to act.
	todo, _ := objects["todo"].(map[string]any)
	if todo["checks"] != 0.0 || todo["governance"] != 2.0 || todo["governance_objects"] != 3.0 {
		t.Errorf("todo = %v, want no line of ours, two governance lines over three objects", todo)
	}
	if health["health"] != "HEALTHY" {
		t.Errorf("health = %v, want HEALTHY: the rounds complete and nothing is wrong with this deployment", health["health"])
	}
	// The rows under the line carry the facts as published.
	_, listed := get(t, handler, "/api/objects?check=EMPTY_EVERY_ROUND&group=4101")
	items, _ := listed["anomalies"].([]any)
	if len(items) != 1 {
		t.Fatalf("under EMPTY_EVERY_ROUND/4101 = %v, want the one row", listed["anomalies"])
	}
	row, _ := items[0].(map[string]any)
	published, _ := row["empty_every_round"].(map[string]any)
	if published == nil || published["rounds"] != 480.0 || published["interval_seconds"] != 15.0 ||
		published["never_saw_data"] != true || published["cause"] != EmptyEveryRoundCauseUnknown {
		t.Errorf("row facts = %v, want rounds 480, interval 15, never_saw_data, CAUSE_UNKNOWN", row["empty_every_round"])
	}
	finding, _ := row["finding"].(map[string]any)
	if finding["check"] != "EMPTY_EVERY_ROUND" || finding["owner"] != "STRATEGY" || finding["result"] != "NO_DATA" {
		t.Errorf("finding = %v, want EMPTY_EVERY_ROUND / STRATEGY / NO_DATA", finding)
	}
	if _, blocked := row["blocked"]; blocked {
		t.Errorf("row carries a blocked reading: %v; nothing is stuck anywhere", row["blocked"])
	}
}
