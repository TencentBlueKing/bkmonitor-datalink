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
	"strings"
	"testing"
	"time"
)

// measuredHold is a hold of 99 s on an arrival age measured at 189 s past the
// window's end, frozen since a Slot an hour back, under a time_delay of 60 s
// that would need 180 s to read it whole without the hold.
func measuredHold(now time.Time) ReadHoldFacts {
	return ReadHoldFacts{Millis: 99_000, ArrivalAgeMillis: 189_000, HeldSince: now.Add(-time.Hour).Unix(),
		DelaySeconds: 60, SuggestedDelaySeconds: 180, SettlingWaitSeconds: 30, Buckets: []int64{1, 2}}
}

// heldView is one replica's view of qg-held under strategy 4101, its rounds
// complete, with the given hold published beside them and the rows the
// tracker makes of it.
func heldView(t *testing.T, now time.Time, hold ReadHoldFacts) (View, StrategyLookupFacts) {
	t.Helper()
	tracker := newTracker(t, &clock{at: now})
	tracker.Observe(context.Background(), slotCompleted("qg-held", 0, 0, now))
	holds := map[string]ReadHoldFacts{"qg-held": hold}
	snapshot := Snapshot{Replica: "pod-a", TakenAt: now, Owned: 1, Determined: 1, OwnedObjects: []string{"qg-held"},
		ReadHolds: holds, ReadHeld: tracker.ReadHeld(holds)}
	view := Aggregate(Expectation{Known: true, QueryGroups: 1, IDs: []string{"qg-held"}}, []Snapshot{snapshot}, []string{"pod-a"}, now, time.Minute)
	Decide(&view, now, 0)
	lookup := StrategyLookupFacts{Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
		Plans:        []StrategyPlanRef{{Tenant: "default", Business: "2", QueryGroup: "qg-held", SnapshotRevision: "s1", QueryRevision: "q", ScheduleRevision: "r"}},
		Dispositions: []StrategyDisposition{{Scope: "PLAN", Disposition: "ACCEPTED"}}}
	return view, lookup
}

// A strategy alarmd holds the reads of, on a measured arrival age, and with
// nothing else wrong, is detecting: its line and its diagnosis say so, under
// READ_HELD, with the hold and the time_delay that would need none beside
// them; the row is in no column, so the deployment stays healthy, and the
// strategy counts as a running DETECTING one.
func TestAHeldStrategyIsDetectingWithItsHoldAndTheTimeDelayThatNeedsNone(t *testing.T) {
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	hold := measuredHold(now)
	view, lookup := heldView(t, now, hold)
	if len(view.ReadHeld) != 1 || view.ReadHeld[0].ReadHold == nil || view.ReadHeld[0].ReadHold.Buckets != nil {
		t.Fatalf("held rows %+v, want one for qg-held without the buckets the hold list carries", view.ReadHeld)
	}
	if row := view.ReadHeld[0]; !row.Since.Equal(time.Unix(hold.HeldSince, 0)) || row.SinceFrom != SinceBusinessState || row.Blocked != nil {
		t.Fatalf("held row since %v (%s) blocked %+v, want the first Slot frozen with the hold, from the record, and not stuck",
			row.Since, row.SinceFrom, row.Blocked)
	}
	if todo := ReplicaPartOf(view, now).TodoRows; todo.GovernanceObjects != 1 || todo.Objects != 0 {
		t.Fatalf("to-do %+v, want the held object the strategy's to act on and none of ours", todo)
	}
	counted := false
	for _, report := range ReportChecks(nil, nil, &view, now) {
		counted = counted || (report.Code == CheckReadHeld && report.LineCount() == 1)
	}
	if !counted {
		t.Fatalf("checks %+v, want a READ_HELD line of the one object", ReportChecks(nil, nil, &view, now))
	}
	want := TimeDelayAdvice{CurrentDelaySeconds: 60, SuggestedDelaySeconds: 180, ReadHoldSeconds: 99, Object: "qg-held", Objects: 1,
		Since: time.Unix(hold.HeldSince, 0)}
	row := diagnoseStrategy("4101", lookup, newDiagnosisContext(&view, "pod-a", now))
	if row.Verdict != StateDetecting || row.Action != ActionStrategyEdit || row.Check != CheckReadHeld {
		t.Fatalf("diagnosis %s %s under %s, want detecting, the strategy's to edit, under READ_HELD", row.Verdict, row.Action, row.Check)
	}
	if row.TimeDelayAdvice == nil || !sameAdvice(*row.TimeDelayAdvice, want) {
		t.Fatalf("diagnosis advice %+v, want %+v", row.TimeDelayAdvice, want)
	}
	var line *StrategyLine
	for _, candidate := range StrategyLines(&view, now) {
		if candidate.StrategyID == "4101" {
			line = &candidate
		}
	}
	if line == nil || line.Standing.State != StateDetecting || line.TimeDelayAdvice == nil || !sameAdvice(*line.TimeDelayAdvice, want) {
		t.Fatalf("line %+v, want a detecting line with the advice", line)
	}
	for _, words := range []string{"最晚约在窗口结束后 189 秒到齐", "自动推后 99 秒", "time_delay 改为 180 秒（alarmd 另有约 30 秒就绪等待）"} {
		if !strings.Contains(line.Line, words) {
			t.Fatalf("line %q, want it to say %q", line.Line, words)
		}
	}
	if view.Health != HealthHealthy || view.AnomaliesTotal != 0 {
		t.Fatalf("health %s with %d anomalies, want a held strategy healthy and in no column", view.Health, view.AnomaliesTotal)
	}
	snapshot := Snapshot{Replica: "pod-a", TakenAt: now, Owned: 1, Determined: 1, OwnedObjects: []string{"qg-held"},
		ReadHolds: map[string]ReadHoldFacts{"qg-held": hold}, ReadHeld: view.ReadHeld,
		EvaluatingStrategies: []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}, EvaluatingStrategiesKnown: true}
	running := SummaryOf(snapshot, []string{"qg-held"}, 0).Part.RunningStrategies
	if running[StateDetecting] != 1 || running[StateResultUntrusted] != 0 {
		t.Fatalf("running %v, want the held strategy counted DETECTING", running)
	}
}

// A hold that rests on no measured arrival age -- a predecessor's bound at
// the most a Slot may be held -- has no row and suggests nothing: the
// diagnosis says alarmd holds the reads, and no time_delay to move to.
func TestAHoldOnNoMeasurementSaysItHoldsAndSuggestsNothing(t *testing.T) {
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	view, lookup := heldView(t, now, ReadHoldFacts{Millis: 600_000, HeldSince: now.Add(-time.Hour).Unix(), DelaySeconds: 60, AtLimit: true})
	if len(view.ReadHeld) != 0 {
		t.Fatalf("held rows %+v, want none for a hold nothing measured", view.ReadHeld)
	}
	row := diagnoseStrategy("4101", lookup, newDiagnosisContext(&view, "pod-a", now))
	if row.Verdict != StateDetecting {
		t.Fatalf("diagnosis %s, want detecting", row.Verdict)
	}
	advice := row.TimeDelayAdvice
	if advice == nil || advice.ReadHoldSeconds != 600 || advice.SuggestedDelaySeconds != 0 || advice.Object != "qg-held" || advice.Objects != 0 {
		t.Fatalf("advice %+v, want the 600 s hold said and nothing suggested", advice)
	}
	encoded, err := json.Marshal(advice)
	if err != nil || strings.Contains(string(encoded), "suggested_time_delay_seconds") || !strings.Contains(string(encoded), `"read_hold_seconds":600`) {
		t.Fatalf("advice encoded %s %v, want the hold and no suggestion", encoded, err)
	}
}

// A group that holds nothing, or has only chosen a hold no Slot is frozen
// with yet, has no row and gives no advice.
func TestNoHoldInForceGivesNoRowAndNoAdvice(t *testing.T) {
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	chosen := measuredHold(now)
	chosen.HeldSince = 0
	for name, hold := range map[string]ReadHoldFacts{"none": {ArrivalAgeMillis: 30_000, DelaySeconds: 60}, "chosen": chosen} {
		view, lookup := heldView(t, now, hold)
		row := diagnoseStrategy("4101", lookup, newDiagnosisContext(&view, "pod-a", now))
		if len(view.ReadHeld) != 0 || row.TimeDelayAdvice != nil || row.Check != "" {
			t.Fatalf("%s: rows %+v advice %+v check %s, want none", name, view.ReadHeld, row.TimeDelayAdvice, row.Check)
		}
	}
}

// Of a strategy's objects read early and held, the advice is the largest
// suggestion, whichever said it, and says the hold either way.
func TestTheLargestSuggestionWinsBetweenAReadEarlyRowAndAHold(t *testing.T) {
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	early := func(suggested int64) Anomaly {
		facts := readEarlyFacts(now)
		facts.SuggestedDelaySeconds, facts.Samples = suggested, []ReadEarlySample{{EvaluationTime: now.Unix() - 60}}
		return Anomaly{QueryGroup: "qg-early", Kind: KindReadBeforeComplete, ReadEarly: &facts}
	}
	held := func(suggested int64) Anomaly {
		facts := measuredHold(now)
		facts.SuggestedDelaySeconds = suggested
		return Anomaly{QueryGroup: "qg-held", Kind: KindReadHeld, ReadHold: &facts}
	}
	for name, rows := range map[string][]Anomaly{"held first": {held(240), early(180)}, "early first": {early(180), held(240)}} {
		var advice *TimeDelayAdvice
		for _, row := range rows {
			advice = advice.with(row)
		}
		if advice == nil || advice.SuggestedDelaySeconds != 240 || advice.Object != "qg-held" || advice.ReadHoldSeconds != 99 || advice.Objects != 2 || advice.Samples != 0 {
			t.Fatalf("%s: advice %+v, want the hold's 240 with its hold and both objects", name, advice)
		}
	}
	advice := (*TimeDelayAdvice)(nil).with(held(240)).with(early(300))
	if advice.SuggestedDelaySeconds != 300 || advice.Object != "qg-early" || advice.ReadHoldSeconds != 99 || advice.Samples != 1 {
		t.Fatalf("advice %+v, want the read-early 300 with the hold still said", advice)
	}
}

// The held row is the last line over objects: any other row of the strategy
// decides its words, and the hold and its suggestion ride beside them.
func TestAnyOtherRowOfAHeldStrategyDecidesIt(t *testing.T) {
	now := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	view, lookup := heldView(t, now, measuredHold(now))
	view.NoData = append(view.NoData, Anomaly{QueryGroup: "qg-held", Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED", Replica: "pod-a",
		Since: now.Add(-2 * time.Hour), Strategies: []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}})
	Attribute(view.NoData, now)
	row := diagnoseStrategy("4101", lookup, newDiagnosisContext(&view, "pod-a", now))
	if row.Check != CheckNoDataPersistent || row.TimeDelayAdvice == nil || row.TimeDelayAdvice.ReadHoldSeconds != 99 ||
		row.TimeDelayAdvice.SuggestedDelaySeconds != 180 {
		t.Fatalf("diagnosis under %s with advice %+v, want the no-data words and the hold beside them", row.Check, row.TimeDelayAdvice)
	}
	for _, line := range StrategyLines(&view, now) {
		if line.StrategyID == "4101" && (line.Standing.Check != CheckNoDataPersistent || line.TimeDelayAdvice == nil || line.TimeDelayAdvice.ReadHoldSeconds != 99) {
			t.Fatalf("line %+v, want the no-data words with the hold beside them", line)
		}
	}
}

// The object list sends the column it is asked for: the held rows, like the
// other lists beside the columns, are not shipped with it.
func TestTheObjectListDoesNotShipTheHeldRows(t *testing.T) {
	snapshots := healthySnapshots()
	hold := measuredHold(now)
	snapshots[0].ReadHolds = map[string]ReadHoldFacts{"qg-held": hold}
	snapshots[0].ReadHeld = []Anomaly{{QueryGroup: "qg-held", Kind: KindReadHeld, Replica: snapshots[0].Replica, ReadHold: &hold,
		Strategies: []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}}}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 2, Known: true}, []string{"pod-a", "pod-b"})
	status, body := get(t, handler, "/api/objects")
	if status != 200 {
		t.Fatalf("status %d", status)
	}
	if rows, _ := body["read_held"].([]any); len(rows) != 0 {
		t.Fatalf("the object list shipped %d held rows it was not asked for", len(rows))
	}
}

// The held line says the settling wait alarmd adds after the time_delay when
// there is one, which is why the time_delay it suggests can read as less
// than when the data arrives; with none it says nothing of it.
func TestTheHeldLineSaysTheSettlingWaitOnlyWhenThereIsOne(t *testing.T) {
	facts := ReadHoldFacts{Millis: 23_000, ArrivalAgeMillis: 33_000, SuggestedDelaySeconds: 30, SettlingWaitSeconds: 10}
	if clause := readHeldClause(&facts); !strings.HasSuffix(clause, "建议把 time_delay 改为 30 秒（alarmd 另有约 10 秒就绪等待）") {
		t.Fatalf("clause %q, want the suggestion with the settling wait beside it", clause)
	}
	facts.SettlingWaitSeconds = 0
	if clause := readHeldClause(&facts); !strings.HasSuffix(clause, "建议把 time_delay 改为 30 秒") || strings.Contains(clause, "就绪等待") {
		t.Fatalf("clause %q, want no settling wait said where there is none", clause)
	}
}
