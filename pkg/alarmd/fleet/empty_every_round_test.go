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
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// emptyRounds completes the object empty every period for the whole span,
// advancing the tracker's clock, and returns how many rounds it completed.
// Each round is stamped with its Slot, the clock's time when it ran: the hour
// the line waits is measured Slot to Slot, and a round without one is a round
// on no clock.
func emptyRounds(tracker *Tracker, at *clock, queryGroup string, period, span time.Duration) int {
	rounds := 0
	for elapsed := time.Duration(0); elapsed <= span; elapsed += period {
		tracker.Observe(context.Background(), emptyAt(queryGroup, "4101", at.at))
		rounds++
		at.at = at.at.Add(period)
	}
	return rounds
}

// emptyAt is one empty completion of the object at the given Slot, and
// dataAt one that returned records: the two ends of the data side's hour.
func emptyAt(queryGroup, strategy string, slot time.Time) observability.Observation {
	observed := completion(queryGroup, "FULL_EMPTY_COMPLETED", strategy)
	observed.Trace.EvaluationTime = slot.Unix()
	return observed
}

func dataAt(queryGroup, strategy string, slot time.Time) observability.Observation {
	observed := completion(queryGroup, "FULL_COMPLETED", strategy)
	observed.Trace.EvaluationTime = slot.Unix()
	return observed
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
		!row.EmptyEveryRound.NeverSawData || !row.EmptyEveryRound.SinceIsLowerBound || row.EmptyEveryRound.Cause != EmptyEveryRoundCauseUnknown ||
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
	tracker.Observe(context.Background(), dataAt("qg-stopped", "8930", at.at))
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
	tracker.Observe(context.Background(), dataAt("qg-slow-seen", "8931", at.at))
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
	tracker.Observe(context.Background(), dataAt("qg-15s-young", "4101", at.at))
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

// A record from before the run facts were kept restores only its last
// committed round. One that completed with records restores "seen", and the
// object is the data side's an hour later, not this line's; one that completed
// empty says nothing about the rounds before it, and the hour starts from the
// first empty round this process watches -- not from the restored round's
// clock. The record that carries the facts is the next test.
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
	tracker.Observe(context.Background(), emptyAt("qg-then-blocked", "4101", at.at))
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

// The no-data lines fold by strategy, one object to a group, and a
// population of groups of one cannot show that the runs began together. The
// onset fold can: minutes by objects, largest first, the bound's remainder
// summed, and how many distinct minutes there were -- one is one event,
// hundreds are hundreds of quiet sources. A live deployment's 327 empty
// runs in two minutes were the two minutes a release began recording them.
func TestTheNoDataLinesFoldByOnsetMinuteAsWellAsByStrategy(t *testing.T) {
	at := time.Date(2026, 9, 21, 14, 0, 0, 0, time.UTC)
	release := time.Date(2026, 9, 21, 7, 39, 0, 0, time.UTC)
	rows := []Anomaly{}
	add := func(group string, since time.Time, kind string) {
		rows = append(rows, Anomaly{QueryGroup: group, Kind: kind, ReasonCode: "FULL_EMPTY_COMPLETED", Replica: "pod-a",
			Since: since, Strategies: []StrategyRef{{StrategyID: group, BusinessID: "7"}}})
	}
	// Three hundred that began in the release's two minutes, seconds apart.
	for i := 0; i < 300; i++ {
		add(fmt.Sprintf("qg-r%03d", i), release.Add(time.Duration(i%2)*time.Minute).Add(time.Duration(i%50)*time.Second), KindEmptyEveryRound)
	}
	// Twelve quiet sources of their own, one to a minute, and two rows
	// that carry no start at all.
	for i := 0; i < 12; i++ {
		add(fmt.Sprintf("qg-q%02d", i), at.Add(-time.Duration(i+1)*time.Hour), KindEmptyEveryRound)
	}
	add("qg-unstarted-a", time.Time{}, KindEmptyEveryRound)
	add("qg-unstarted-b", time.Time{}, KindEmptyEveryRound)
	view := &View{NoData: rows}
	Attribute(view.NoData, at)
	var report *CheckReport
	for _, candidate := range ReportChecks([][]Anomaly{nil, nil, nil, nil}, nil, view, at) {
		if candidate.Code == CheckEmptyEveryRound {
			report = &candidate
		}
	}
	if report == nil || report.Objects != 314 || len(report.Groups) != 314 {
		t.Fatalf("report = %+v, want the line over 314 objects in 314 strategy folds", report)
	}
	fold := report.Onsets
	if fold == nil || fold.Distinct != 14 || len(fold.Minutes) != MaxOnsetFold || fold.Other != 12-(MaxOnsetFold-2) || fold.WithoutOnset != 2 {
		t.Fatalf("onsets = %+v, want 14 distinct minutes, %d listed, the rest of the quiet ones under other, two without a start", fold, MaxOnsetFold)
	}
	// The fold adds up to the line, so the reader's subtraction leaves nothing.
	listed := fold.Other + fold.WithoutOnset
	for _, minute := range fold.Minutes {
		listed += minute.Objects
	}
	if listed != report.Objects {
		t.Fatalf("fold adds up to %d, the line to %d", listed, report.Objects)
	}
	if fold.Minutes[0].Objects != 150 || !fold.Minutes[0].Minute.Equal(release) || fold.Minutes[1].Objects != 150 ||
		!fold.Minutes[1].Minute.Equal(release.Add(time.Minute)) || fold.Minutes[2].Objects != 1 {
		t.Fatalf("minutes = %+v, want the release's two minutes of 150 first, then the quiet ones at one each", fold.Minutes)
	}
	// A line with no no-data rows carries no fold, and the record lines
	// never do.
	for _, candidate := range ReportChecks([][]Anomaly{nil, nil, nil, nil}, nil, &View{}, at) {
		if candidate.Onsets != nil {
			t.Fatalf("%s carries an onset fold with nothing to fold", candidate.Code)
		}
	}
}

// The hour this line waits for has to be an hour of empty completions, near
// enough. An object that completed empty long ago, then spent a day on
// rounds that produced no completion at all -- a contract failure on every
// one -- and then began completing empty again is at the start of a new run,
// not an hour into an old one.
//
// On the acceptance release this was three hundred objects at once. A build
// that turned those failures into empty completions put every one of them on
// this line in its first minutes, each row carrying fifty-two rounds of this
// process's evidence beside a start a day earlier, and the count went from
// two hundred to five hundred with nothing about the deployment having
// changed for the worse. A blip does not cost the hour -- the object was
// completing empty either side of it -- and the test above holds that.
func TestAnHourOfEmptyRoundsIsNotInheritedAcrossADayOfNoCompletions(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	// An hour of empty rounds: listed.
	emptyRounds(tracker, at, "qg-interrupted", time.Minute, 61*time.Minute)
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-interrupted"]; !listed {
		t.Fatal("not listed after an hour of empty rounds")
	}
	// A day of rounds that produced no completion at all. The object is on
	// the failing line while they last, and this line does not carry it.
	for round := 0; round < DefaultBlockedRounds; round++ {
		tracker.Observe(context.Background(), runOutcome("qg-interrupted", "source_error"))
	}
	at.at = at.at.Add(24 * time.Hour)
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-interrupted"]; listed {
		t.Fatal("listed as empty every round while its rounds produced no completion")
	}
	// The first empty completion after the day: the run continues, and the
	// hour starts again here rather than being inherited from before the
	// interruption.
	tracker.Observe(context.Background(), emptyAt("qg-interrupted", "4101", at.at))
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-interrupted"]; listed {
		t.Fatal("the first empty round after a day of no completions was listed on an inherited hour")
	}
	// An hour of them from here on, and it is listed again -- dated from the
	// round that began this run, not from the one before the interruption.
	firstAfter := at.at
	at.at = at.at.Add(time.Minute)
	emptyRounds(tracker, at, "qg-interrupted", time.Minute, 61*time.Minute)
	row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-interrupted"]
	if !listed {
		t.Fatal("not listed after an hour of empty rounds following the interruption")
	}
	if row.Since.Before(firstAfter) {
		t.Fatalf("since = %s, want no earlier than the round that began this run (%s)", row.Since, firstAfter)
	}
}

// Every period in the deployed population, twelve hours of nothing but empty
// rounds, and the object is listed on all of them. The hole the test above
// looks for is a hole by the object's own cadence; an object evaluated less
// often than the hour this line waits for produces one empty round an hour
// and a bit apart, and every one of those is its ordinary pace.
//
// Measured against the hour instead, the periods past it went silent for
// good: the run's start was cleared each round, the Slot-to-Slot distance
// never left zero, and twelve hours in which not one record came back put
// nothing on the page. One minute either side of the hour decided it, which
// is what a predicate measured against its own constant looks like.
func TestAnObjectSlowerThanTheWindowIsStillListed(t *testing.T) {
	for _, period := range []time.Duration{15 * time.Second, 30 * time.Minute, time.Hour, 61 * time.Minute, 2 * time.Hour} {
		at := &clock{at: now}
		tracker := newTracker(t, at)
		rounds := emptyRounds(tracker, at, "qg-slow", period, 12*time.Hour)
		row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-slow"]
		if !listed {
			t.Errorf("period %s: twelve hours of empty rounds (%d of them), not listed", period, rounds)
			continue
		}
		if row.EmptyEveryRound == nil || row.EmptyEveryRound.Rounds != rounds {
			t.Errorf("period %s: row counts %+v, want all %d rounds", period, row.EmptyEveryRound, rounds)
		}
	}
}

// A hole is still a hole for a slow object: three of its own cycles with
// nothing completed is the same evidence gap the fast objects are held to,
// and the run starts again where the object did.
func TestASlowObjectLosesItsHeadStartOverThreeOfItsOwnCycles(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	period := 2 * time.Hour
	emptyRounds(tracker, at, "qg-slow-stalled", period, 12*time.Hour)
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-slow-stalled"]; !listed {
		t.Fatal("not listed after twelve hours of empty rounds")
	}
	// A day with no completion at all: twelve of this object's cycles.
	at.at = at.at.Add(24 * time.Hour)
	resumed := at.at
	tracker.Observe(context.Background(), emptyAt("qg-slow-stalled", "4101", resumed))
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-slow-stalled"]; listed {
		t.Fatal("the first empty round after a day of no completions was listed on an inherited run")
	}
	at.at = resumed.Add(period)
	tracker.Observe(context.Background(), emptyAt("qg-slow-stalled", "4101", at.at))
	row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-slow-stalled"]
	if !listed {
		t.Fatal("not listed a cycle after the run started again")
	}
	if row.Since.Before(resumed) {
		t.Fatalf("since = %s, want no earlier than the round that began this run (%s)", row.Since, resumed)
	}
	// And a second stall is caught like the first. It is here because the
	// obvious thing to do with a hole is to keep it as the object's cadence,
	// and that would leave the bar at a day for the rest of the run: this
	// stall is a day long again, and against a day-long cadence it is not
	// three of anything.
	secondStall := at.at.Add(24 * time.Hour)
	at.at = secondStall
	tracker.Observe(context.Background(), emptyAt("qg-slow-stalled", "4101", secondStall))
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-slow-stalled"]; listed {
		t.Fatal("a second day-long stall was not read as a hole: the first one stayed on as the object's cadence")
	}
}

// A round arriving far sooner than the object's pace -- a catch-up, a retry,
// a burst after a restart -- must not become the pace. If it does, the next
// ordinary round is measured against it, reads as a hole, and the run loses
// its start: the row stays on the page and its Since moves later, so "how
// long has this been empty" comes back smaller than the truth. That is worse
// than the row going missing, because the number that replaces it looks
// exactly as credible as the right one.
//
// Both halves are asserted, and the Since half is the point.
func TestACatchUpRoundDoesNotRewriteHowLongTheObjectHasBeenEmpty(t *testing.T) {
	for name, catchUps := range map[string]int{"one catch-up round": 1, "two in a row": 2} {
		at := &clock{at: now}
		tracker := newTracker(t, at)
		period := 2 * time.Hour
		emptyRounds(tracker, at, "qg-catch-up", period, 12*time.Hour)
		settled, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-catch-up"]
		if !listed {
			t.Fatalf("%s: not listed after twelve hours of empty rounds", name)
		}
		since := settled.Since
		// emptyRounds leaves the clock a period past the round it last ran,
		// so the catch-up rounds are placed from that round's own Slot --
		// measured from the clock they would be a period apart, which is the
		// pace and not a catch-up at all.
		slot := at.at.Add(-period)
		for round := 0; round < catchUps; round++ {
			slot = slot.Add(time.Minute)
			at.at = slot
			tracker.Observe(context.Background(), emptyAt("qg-catch-up", "4101", slot))
		}
		// And one ordinary round after them.
		slot = slot.Add(period)
		at.at = slot
		tracker.Observe(context.Background(), emptyAt("qg-catch-up", "4101", slot))
		row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-catch-up"]
		if !listed {
			t.Errorf("%s: dropped off the line by an ordinary round after it", name)
			continue
		}
		if !row.Since.Equal(since) {
			t.Errorf("%s: since moved %s -> %s, want the run's own start kept; a catch-up round became the pace and the next ordinary round read as a hole",
				name, since, row.Since)
		}
	}
}

// The run a record hands over is trusted while its own evidence is
// continuous, and only then. A record whose latest empty round is a day old
// describes a run this process watched none of, and the day between that
// round and the first one watched here is a hole whatever the record's start
// says -- the production shape being a build that turned a day of contract
// failures into empty completions, after which every such object was listed
// on an hour it had inherited rather than watched.
//
// The object stays in the run; it is the head start it loses.
func TestARestoredRunWhoseLatestRoundIsADayOldStartsAgainHere(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	restoredEmptyRun(t, tracker, "qg-stale-record", now, now.Add(-24*time.Hour), now.Add(-48*time.Hour), time.Time{})
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-stale-record"]; !listed {
		t.Fatal("the record's own two days are not listed on restore")
	}
	// The first round watched here, a day after the record's latest.
	tracker.Observe(context.Background(), emptyAt("qg-stale-record", "4101", now))
	if _, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-stale-record"]; listed {
		t.Fatal("listed on the record's start across a day this process did not watch")
	}
	// And it earns its way back on with an hour of its own.
	at.at = now.Add(time.Minute)
	emptyRounds(tracker, at, "qg-stale-record", time.Minute, 61*time.Minute)
	row, listed := rowsOfKind(tracker.NoData(), KindEmptyEveryRound)["qg-stale-record"]
	if !listed {
		t.Fatal("not listed after an hour of rounds watched here")
	}
	if row.Since.Before(now) {
		t.Errorf("since = %s, want no earlier than the first round watched here (%s)", row.Since, now)
	}
}
