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

func readEarlyFacts(since time.Time) ReadEarlyFacts {
	return ReadEarlyFacts{StepSeconds: 60, CurrentDelaySeconds: 60, SuggestedDelaySeconds: 180, Since: since}
}

// An object the lookback reports as read early is a row under the
// strategies this process has seen evaluate on it, with the time_delay it
// runs under and the one that would have read it complete; one this process
// never saw evaluate has no strategy to be named under and is left out.
func TestAnObjectReadEarlyIsARowUnderItsStrategies(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	tracker := newTracker(t, &clock{at: now})
	tracker.Observe(context.Background(), slotCompleted("qg-late", 0, 0, now))
	rows := rowsOfKind(tracker.ReadEarly(map[string]ReadEarlyFacts{"qg-late": readEarlyFacts(now), "qg-unseen": readEarlyFacts(now)}),
		KindReadBeforeComplete)
	if len(rows) != 1 {
		t.Fatalf("rows %+v, want the object seen evaluating only", rows)
	}
	row := rows["qg-late"]
	if row.ReadEarly == nil || row.ReadEarly.SuggestedDelaySeconds != 180 || row.ReadEarly.CurrentDelaySeconds != 60 ||
		len(row.Strategies) != 1 || row.Strategies[0].StrategyID != "4101" || !row.Since.Equal(now) {
		t.Fatalf("row %+v facts %+v", row, row.ReadEarly)
	}
}

// End to end from the replica's snapshot: the object lands on
// READ_BEFORE_COMPLETE as the strategy's to act on while its rounds
// complete, the words are "the results cannot be taken as they stand" and
// "the strategy's owner changes it", and a diagnosis of the strategy reads
// it there; the health response names the object with the suggested
// time_delay.
func TestAReadEarlyObjectReachesItsLineTheDiagnosisAndTheHealthResponse(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	tracker := newTracker(t, &clock{at: now})
	tracker.Observe(context.Background(), slotCompleted("qg-late", 0, 0, now))
	snapshot := Snapshot{Replica: "pod-a", TakenAt: now, Owned: 1, Determined: 1, OwnedObjects: []string{"qg-late"},
		ReadEarly: tracker.ReadEarly(map[string]ReadEarlyFacts{"qg-late": readEarlyFacts(now)})}
	view := Aggregate(Expectation{Known: true, QueryGroups: 1, IDs: []string{"qg-late"}}, []Snapshot{snapshot}, []string{"pod-a"}, now, time.Minute)
	Decide(&view, now, 0)
	if len(view.ReadEarly) != 1 {
		t.Fatalf("view carries %d rows, want the snapshot's one", len(view.ReadEarly))
	}
	finding := view.ReadEarly[0].Finding
	if finding.Check != CheckReadBeforeComplete || finding.Owner != OwnerStrategy || finding.Result != ResultCompleted {
		t.Fatalf("finding %+v, want the strategy's line with its rounds completed", finding)
	}
	// Nothing is stuck anywhere: the rounds complete, so the row names no
	// stage, dependency or class a failure would.
	if blocked := view.ReadEarly[0].Blocked; blocked != nil {
		t.Fatalf("blocked %+v, want none on a row whose rounds complete", blocked)
	}
	var line *CheckReport
	for _, report := range ReportChecks(nil, nil, &view, now) {
		if report.Code == CheckReadBeforeComplete {
			line = &report
			break
		}
	}
	if line == nil || line.Current != 1 {
		t.Fatalf("line %+v, want READ_BEFORE_COMPLETE with the one object", line)
	}
	if pair := checkWords[CheckReadBeforeComplete]; pair.State != StateResultUntrusted || pair.Action != ActionStrategyEdit {
		t.Fatalf("words %+v, want the results not taken as they stand, for the strategy's owner", pair)
	}
	facts := StrategyLookupFacts{Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
		Plans:        []StrategyPlanRef{{Tenant: "default", Business: "2", QueryGroup: "qg-late", SnapshotRevision: "s1", QueryRevision: "q", ScheduleRevision: "r"}},
		Dispositions: []StrategyDisposition{{Scope: "PLAN", Disposition: "ACCEPTED"}}}
	row := diagnoseStrategy("4101", facts, newDiagnosisContext(&view, "pod-a", now))
	if row.Verdict != StateResultUntrusted || row.Action != ActionStrategyEdit || row.Check != CheckReadBeforeComplete {
		t.Fatalf("diagnosis %s/%s/%s, want RESULT_UNTRUSTED/STRATEGY_EDIT under READ_BEFORE_COMPLETE", row.Verdict, row.Action, row.Check)
	}

	snapshots := healthySnapshots()
	snapshots[0].ReadEarly = snapshot.ReadEarly
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, []string{"pod-a", "pod-b"})
	body := requestJSON(t, handler, "/api/health")
	list, ok := body["read_early"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("health read_early = %v, want the one object", body["read_early"])
	}
	entry, _ := list[0].(map[string]any)
	if entry["query_group"] != "qg-late" || entry["suggested_time_delay_seconds"] != float64(180) || entry["current_time_delay_seconds"] != float64(60) {
		t.Fatalf("health entry %v", entry)
	}
}

// The row carries the samples its suggestion rests on, the newest
// MaxReadEarlySamples of what it is given with at most MaxReadEarlyBuckets
// buckets each, in a copy of its own: a longer list, or one changed after,
// does not reach the snapshot. At its widest the row is under 1 KB.
func TestAReadEarlyRowCarriesItsEvidenceWithinItsBounds(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	tracker := newTracker(t, &clock{at: now})
	tracker.Observe(context.Background(), slotCompleted("qg-late", 0, 0, now))
	facts := readEarlyFacts(now)
	// Slots and buckets as they are, ten digits each; ages and rungs at
	// their widest.
	const slot = int64(1_790_694_000)
	for sample := int64(0); sample < 5; sample++ {
		buckets := make([]int64, 12)
		for index := range buckets {
			buckets[index] = slot + 1000*sample + int64(index)
		}
		facts.Samples = append(facts.Samples, ReadEarlySample{EvaluationTime: slot + 60*sample, FirstReadAgeSeconds: 3599,
			CompletionAgeSeconds: 99999, Rung: "x63.5", ChangedAgeSeconds: 99999, Buckets: buckets})
	}
	row := rowsOfKind(tracker.ReadEarly(map[string]ReadEarlyFacts{"qg-late": facts}), KindReadBeforeComplete)["qg-late"]
	facts.Samples[4].Buckets[0] = -1
	got := row.ReadEarly.Samples
	if len(got) != MaxReadEarlySamples || got[0].EvaluationTime != slot+120 || got[2].EvaluationTime != slot+240 {
		t.Fatalf("samples %+v, want the newest %d", got, MaxReadEarlySamples)
	}
	for _, sample := range got {
		if len(sample.Buckets) != MaxReadEarlyBuckets || sample.Buckets[0] != slot+1000*((sample.EvaluationTime-slot)/60) {
			t.Fatalf("sample %+v, want its first %d buckets as given", sample, MaxReadEarlyBuckets)
		}
	}
	encoded, err := json.Marshal(row.ReadEarly)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"samples":[{"evaluation_time":1790694120`) || len(encoded) > 1024 {
		t.Fatalf("encoded %s (%d bytes), want the row within 1 KB at its widest", encoded, len(encoded))
	}
}

// The time_delay advice rides on the strategy's diagnosis and its line
// whatever check decides them: here the object is also on a line above
// READ_BEFORE_COMPLETE, which decides the words, and the advice is beside
// them, with the evidence's partial revisions counted.
func TestTheTimeDelayAdviceRidesOnTheStrategyWhateverCheckDecidesIt(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	tracker := newTracker(t, &clock{at: now})
	tracker.Observe(context.Background(), slotCompleted("qg-late", 0, 0, now))
	facts := readEarlyFacts(now)
	facts.Samples = []ReadEarlySample{{EvaluationTime: now.Unix() - 120, PartialRevised: true}, {EvaluationTime: now.Unix() - 60}}
	snapshot := Snapshot{Replica: "pod-a", TakenAt: now, Owned: 1, Determined: 1, OwnedObjects: []string{"qg-late"},
		ReadEarly: tracker.ReadEarly(map[string]ReadEarlyFacts{"qg-late": facts})}
	view := Aggregate(Expectation{Known: true, QueryGroups: 1, IDs: []string{"qg-late"}}, []Snapshot{snapshot}, []string{"pod-a"}, now, time.Minute)
	Decide(&view, now, 0)
	view.NoData = append(view.NoData, Anomaly{QueryGroup: "qg-late", Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED", Replica: "pod-a",
		Since: now.Add(-time.Hour), Strategies: []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}})
	Attribute(view.NoData, now)
	lookup := StrategyLookupFacts{Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
		Plans:        []StrategyPlanRef{{Tenant: "default", Business: "2", QueryGroup: "qg-late", SnapshotRevision: "s1", QueryRevision: "q", ScheduleRevision: "r"}},
		Dispositions: []StrategyDisposition{{Scope: "PLAN", Disposition: "ACCEPTED"}}}
	row := diagnoseStrategy("4101", lookup, newDiagnosisContext(&view, "pod-a", now))
	if row.Check != CheckNoDataPersistent {
		t.Fatalf("diagnosis decided under %s; the fixture needs a check above READ_BEFORE_COMPLETE to test this", row.Check)
	}
	want := TimeDelayAdvice{CurrentDelaySeconds: 60, SuggestedDelaySeconds: 180, Object: "qg-late", Objects: 1, Since: now, Samples: 2, PartialRevised: 1}
	if advice := row.TimeDelayAdvice; advice == nil || !sameAdvice(*advice, want) {
		t.Fatalf("diagnosis advice %+v, want %+v beside the deciding check", advice, want)
	}
	var line *StrategyLine
	for _, candidate := range StrategyLines(&view, now) {
		if candidate.StrategyID == "4101" {
			line = &candidate
		}
	}
	if line == nil || line.Standing.Check != CheckNoDataPersistent || line.TimeDelayAdvice == nil || !sameAdvice(*line.TimeDelayAdvice, want) {
		t.Fatalf("line %+v, want the no-data words with the advice beside them", line)
	}
	if strings.Contains(line.Line, "time_delay") {
		t.Fatalf("the advice went into the sentence %q; it rides beside it", line.Line)
	}
	encoded, err := json.Marshal(row)
	if err != nil || !strings.Contains(string(encoded), `"time_delay_advice":{"current_time_delay_seconds":60,"suggested_time_delay_seconds":180`) {
		t.Fatalf("encoded row %s %v", encoded, err)
	}
}

// Of a strategy's objects read early, the advice is the largest suggestion
// - time_delay is one setting of the strategy - from the object it is from,
// counting every object read early once; a row of another kind adds nothing.
func TestTheTimeDelayAdviceIsTheLargestSuggestionOfTheStrategysObjects(t *testing.T) {
	now := time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC)
	row := func(queryGroup string, suggested int64, since time.Time) Anomaly {
		facts := readEarlyFacts(since)
		facts.SuggestedDelaySeconds = suggested
		return Anomaly{QueryGroup: queryGroup, Kind: KindReadBeforeComplete, ReadEarly: &facts}
	}
	var advice *TimeDelayAdvice
	if advice = advice.with(Anomaly{QueryGroup: "qg-x", Kind: KindNoData}); advice != nil {
		t.Fatalf("a row of another kind gave advice %+v", advice)
	}
	for _, r := range []Anomaly{row("qg-a", 120, now), row("qg-b", 300, now.Add(time.Minute)), row("qg-c", 300, now), row("qg-a", 120, now)} {
		advice = advice.with(r)
	}
	if advice == nil || advice.SuggestedDelaySeconds != 300 || advice.Object != "qg-c" || advice.Objects != 3 {
		t.Fatalf("advice %+v, want 300 s from the earlier of the two equal objects, over 3 objects", advice)
	}
}

func sameAdvice(left, right TimeDelayAdvice) bool {
	left.objects, right.objects = nil, nil
	return left.CurrentDelaySeconds == right.CurrentDelaySeconds && left.SuggestedDelaySeconds == right.SuggestedDelaySeconds &&
		left.Object == right.Object && left.Objects == right.Objects && left.Since.Equal(right.Since) &&
		left.Samples == right.Samples && left.PartialRevised == right.PartialRevised && left.ReadHoldSeconds == right.ReadHoldSeconds
}
