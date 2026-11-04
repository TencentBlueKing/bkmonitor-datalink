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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// filteredRound is one empty completion of a primary that filters its
// values, with what its unfiltered witness answered ("" when it has none).
func filteredRound(queryGroup string, slot time.Time, filtered bool, witness string) observability.Observation {
	observed := emptyAt(queryGroup, "128", slot)
	observed.PrimaryInput = &observability.PrimaryInputFacts{Completeness: "FULL", DataState: "EMPTY",
		FilteredWhenEmpty: filtered, ValueFiltered: true, WitnessData: witness}
	return observed
}

// FILTERED_EMPTY reads detecting with nothing to do, and its sentence's facts
// are the witness rounds. A filtered primary with no witness stays on its
// data line, which says the filter is there. A round with records ends the
// run and its counts.
func TestAFilteredEmptyRowIsDetectingAndSaysItsWitnessRounds(t *testing.T) {
	if words := checkWords[CheckFilteredEmpty]; words.State != StateDetecting || words.Action != ActionNone {
		t.Fatalf("FILTERED_EMPTY reads %+v, want detecting with nothing to do", words)
	}
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for elapsed := 0; elapsed <= 120; elapsed++ {
		tracker.Observe(context.Background(), filteredRound("qg-restart", at.at, true, "DATA"))
		tracker.Observe(context.Background(), filteredRound("qg-unwitnessed", at.at, false, ""))
		at.at = at.at.Add(time.Minute)
	}
	rows := tracker.NoData()
	filtered, listed := rowsOfKind(rows, KindFilteredEmpty)["qg-restart"]
	if !listed || filtered.Witness == nil || filtered.Witness.DataRounds != 121 || filtered.ValueFiltered {
		t.Fatalf("rows %+v, want qg-restart FILTERED_EMPTY with 121 witness rounds", rows)
	}
	unwitnessed, listed := rowsOfKind(rows, KindEmptyEveryRound)["qg-unwitnessed"]
	if !listed || !unwitnessed.ValueFiltered || unwitnessed.Witness != nil {
		t.Fatalf("rows %+v, want qg-unwitnessed on the empty line saying its filter", rows)
	}
	list := []Anomaly{filtered, unwitnessed}
	Attribute(list, at.at)
	sentence, facts, ok := factSentence(CheckFilteredEmpty, list[0], at.at)
	if !ok || facts == nil || facts.FilteredEmpty == nil || facts.FilteredEmpty.DataRounds != 121 || sentence == "" {
		t.Fatalf("FILTERED_EMPTY sentence %q facts %+v", sentence, facts)
	}
	sentence, facts, ok = factSentence(CheckEmptyEveryRound, list[1], at.at)
	if !ok || facts == nil || facts.DataAbsent == nil || !facts.DataAbsent.ValueFiltered || !strings.Contains(sentence, "表达式带值过滤") {
		t.Fatalf("an unwitnessed filter's sentence %q facts %+v, want value_filtered said", sentence, facts)
	}
	encoded, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"witness":{"data_rounds":121,"missing_rounds":0}`) || !strings.Contains(string(encoded), `"value_filtered":true`) {
		t.Fatalf("the rows as the board and object.get read them: %s", encoded)
	}
	tracker.Observe(context.Background(), dataAt("qg-restart", "128", at.at))
	at.at = at.at.Add(time.Minute)
	tracker.Observe(context.Background(), filteredRound("qg-restart", at.at, true, "DATA"))
	tracker.mu.Lock()
	state := tracker.groups["qg-restart"]
	data, missing := state.witnessData, state.witnessMissing
	tracker.mu.Unlock()
	if data != 1 || missing != 0 {
		t.Fatalf("after a round with records the counts are %d and %d, want the run begun again at 1 and 0", data, missing)
	}
}

// A restart restores the filtered word and the run's witness counts from the
// record, under the content the owner runs now.
func TestARestoredFilteredRunKeepsItsWordAndCounts(t *testing.T) {
	tracker := newTracker(t, &clock{at: now})
	lastSlot := now.Add(-24 * time.Hour)
	if !tracker.Restore("qg-restart", RestoredState{
		LastCompletion: "FULL_EMPTY_COMPLETED", NextSlot: now.Add(time.Hour),
		LastRound: &RestoredRound{Slot: lastSlot, CompletedAt: lastSlot.Add(2 * time.Second), Kind: "FULL_EMPTY_COMPLETED",
			Empty: &RestoredEmptyRound{Filtered: true, WitnessDataRounds: 40, WitnessMissingRounds: 3, ContentScope: "content-a"}},
		EmptyRunSince: now.Add(-20 * 24 * time.Hour), ContentScopeNow: "content-a",
	}, now, 0) {
		t.Fatal("not restored")
	}
	row, listed := rowsOfKind(tracker.NoData(), KindFilteredEmpty)["qg-restart"]
	if !listed || row.Witness == nil || row.Witness.DataRounds != 40 || row.Witness.MissingRounds != 3 {
		t.Fatalf("restored row %+v, want FILTERED_EMPTY with 40 and 3 witness rounds", row)
	}
}

// The run's witness counts begin again when the object runs under other
// revisions, as the progress record's do under other content.
func TestTheWitnessCountsBeginAgainUnderOtherRevisions(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	round := func(query string) {
		observed := filteredRound("qg-restart", at.at, true, "DATA")
		observed.Trace.SnapshotRevision, observed.Trace.QueryRevision, observed.Trace.ScheduleRevision = "s", query, "r"
		tracker.Observe(context.Background(), observed)
		at.at = at.at.Add(time.Minute)
	}
	for i := 0; i < 3; i++ {
		round("q1")
	}
	round("q2")
	round("q2")
	tracker.mu.Lock()
	data, missing := tracker.groups["qg-restart"].witnessData, tracker.groups["qg-restart"].witnessMissing
	tracker.mu.Unlock()
	if data != 2 || missing != 0 {
		t.Fatalf("counts %d and %d after three rounds under q1 and two under q2, want 2 and 0", data, missing)
	}
}
