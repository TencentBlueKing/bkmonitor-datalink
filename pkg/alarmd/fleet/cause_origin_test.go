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

func degradedRowUnder(t *testing.T, carried *observability.CarriedReasonFacts) Anomaly {
	t.Helper()
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
			ProgressCompletionReason: "QUERY_TIMEOUT", CarriedReason: carried,
			Trace: observability.TraceFields{QueryGroupKey: "qg-307", EvaluationTime: int64(1_700_000_100 + 60*round)},
		})
		at.at = at.at.Add(time.Minute)
	}
	rows := tracker.Anomalies()
	if len(rows) != 1 || rows[0].Cause != "LEVEL_OUTCOME_UNKNOWN" || rows[0].CauseReason != "QUERY_TIMEOUT" {
		t.Fatalf("rows = %+v, want one LEVEL_OUTCOME_UNKNOWN/QUERY_TIMEOUT row", rows)
	}
	return rows[0]
}

// A row restored from a summary has the round's reason and never learned
// whose it was: origin unknown, never physical and never absent. A row
// with no reason has no origin.
func TestARestoredRowWithAReasonHasAnUnknownOriginAndOneWithoutHasNone(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	restore := func(queryGroup, reason string) {
		if !tracker.Restore(queryGroup, RestoredState{LastCompletion: "COMPLETED_WITH_UNAVAILABLE", NextSlot: at.at.Add(time.Minute),
			LastRound: &RestoredRound{Slot: at.at.Add(-time.Minute), CompletedAt: at.at.Add(-time.Minute),
				Kind: "COMPLETED_WITH_UNAVAILABLE", ReasonCode: reason}}, at.at, 0) {
			t.Fatalf("%s was not restored", queryGroup)
		}
	}
	restore("qg-308", "QUERY_TIMEOUT")
	restore("qg-309", "")
	rows := map[string]Anomaly{}
	for _, row := range tracker.Anomalies() {
		rows[row.QueryGroup] = row
	}
	with, found := rows["qg-308"]
	if !found || with.CauseReason != "QUERY_TIMEOUT" || with.CauseOrigin == nil || with.CauseOrigin.Origin != CauseOriginUnknown {
		t.Fatalf("restored row with a reason = %+v (origin %+v), want origin unknown", with, with.CauseOrigin)
	}
	if without, found := rows["qg-309"]; found && (without.CauseReason != "" || without.CauseOrigin != nil) {
		t.Fatalf("restored row without a reason = %+v (origin %+v), want no origin", without, without.CauseOrigin)
	}
	// A row from a replica on a build before the field: the same rule.
	older := Anomaly{QueryGroup: "qg-310", CauseReason: "QUERY_TIMEOUT"}
	settleCauseOrigin(&older)
	if older.CauseOrigin == nil || older.CauseOrigin.Origin != CauseOriginUnknown {
		t.Fatalf("older replica's row origin %+v, want unknown", older.CauseOrigin)
	}
	none := Anomaly{QueryGroup: "qg-311"}
	settleCauseOrigin(&none)
	if none.CauseOrigin != nil {
		t.Fatalf("a row with no reason got origin %+v", none.CauseOrigin)
	}
}

// The strategy row says whose its reason is whenever it has one - unknown
// for a reason that is a check word rather than a round's - and says
// nothing when it has none.
func TestAStrategyRowsReasonAlwaysHasAnOrigin(t *testing.T) {
	at := time.Date(2026, 10, 8, 8, 0, 0, 0, time.UTC)
	view, lookup := heldView(t, at, measuredHold(at))
	row := diagnoseStrategy("4101", lookup, newDiagnosisContext(&view, "pod-a", at))
	if row.Reason == "" || row.ReasonOrigin == nil || row.ReasonOrigin.Origin != CauseOriginDerived {
		t.Fatalf("row reason %q origin %+v, want a check-word reason with origin derived", row.Reason, row.ReasonOrigin)
	}
	carried := DiagnosisRow{Reason: "QUERY_TIMEOUT", ReasonOrigin: &CauseOrigin{Origin: "carried", Slot: 1_700_000_040}}
	settleReasonOrigin(&carried)
	if carried.ReasonOrigin.Origin != "carried" || carried.ReasonOrigin.Slot != 1_700_000_040 {
		t.Fatalf("a carried origin was replaced: %+v", carried.ReasonOrigin)
	}
	unreasoned := DiagnosisRow{ReasonOrigin: &CauseOrigin{Origin: "physical"}}
	settleReasonOrigin(&unreasoned)
	if unreasoned.ReasonOrigin != nil {
		t.Fatalf("a row with no reason kept origin %+v", unreasoned.ReasonOrigin)
	}
	unavailable := diagnoseStrategy("4102", StrategyLookupFacts{}, newDiagnosisContext(&view, "pod-a", at))
	if unavailable.Reason == "" || unavailable.ReasonOrigin == nil || unavailable.ReasonOrigin.Origin != CauseOriginDerived {
		t.Fatalf("unavailable lookup row reason %q origin %+v, want an UNKNOWN verdict's reason derived", unavailable.Reason, unavailable.ReasonOrigin)
	}
	// The deciding object's cause reason restored without an origin stays
	// unknown: derived is only for alarmd's own words.
	restored := DiagnosisRow{Reason: "QUERY_TIMEOUT", ReasonOrigin: unknownCauseOrigin()}
	settleReasonOrigin(&restored)
	if restored.ReasonOrigin.Origin != CauseOriginUnknown {
		t.Fatalf("a restored cause reason's origin became %+v", restored.ReasonOrigin)
	}
}

// A round filed under its cause word has alarmd's own word for a reason:
// derived, still naming the carried guard behind it.
func TestARowFiledUnderItsCauseIsDerived(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), observability.Observation{
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "GAP_GUARD_WARMING",
			ProgressCompletionReason: "QUERY_TIMEOUT",
			CarriedReason:            &observability.CarriedReasonFacts{OriginSlot: 1_700_000_040, OriginCause: "QUERY_TIMEOUT"},
			Trace:                    observability.TraceFields{QueryGroupKey: "qg-317", EvaluationTime: int64(1_700_000_100 + 60*round)},
		})
		at.at = at.at.Add(time.Minute)
	}
	// A guard warming is no fault, so the row is not on the anomaly list;
	// it is the object's tracked row the views read.
	row, found := tracker.TrackedRow("qg-317")
	want := CauseOrigin{Origin: CauseOriginDerived, Slot: 1_700_000_040, Cause: "QUERY_TIMEOUT"}
	if !found || row.CauseReason != "GAP_GUARD_WARMING" || row.CauseOrigin == nil || *row.CauseOrigin != want {
		t.Fatalf("row %+v reason %q origin %+v, want GAP_GUARD_WARMING %+v", found, row.CauseReason, row.CauseOrigin, want)
	}
}

// The summary splits each cause reason by origin into three buckets that add
// up to the whole; a row that never learned its origin is unknown, never
// physical.
func TestTheSummarySplitsCauseReasonsByOrigin(t *testing.T) {
	rows := []Anomaly{
		{QueryGroup: "qg-312", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT", CauseOrigin: &CauseOrigin{Origin: "physical"}},
		{QueryGroup: "qg-313", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT", CauseOrigin: &CauseOrigin{Origin: "carried", Slot: 1_700_000_040, Cause: "QUERY_TIMEOUT"}},
		{QueryGroup: "qg-314", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT", CauseOrigin: &CauseOrigin{Origin: "carried"}},
		{QueryGroup: "qg-315", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT", CauseOrigin: &CauseOrigin{Origin: CauseOriginUnknown}},
		{QueryGroup: "qg-316", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT"},
		{QueryGroup: "qg-318", Kind: KindDegradedRun, CauseReason: "GAP_GUARD_WARMING", CauseOrigin: &CauseOrigin{Origin: CauseOriginDerived}},
	}
	summary := summarize(rows, now)
	count := func(distribution Distribution, reason string) int {
		for _, entry := range distribution.Top {
			if entry.Value == reason {
				return entry.Count
			}
		}
		return 0
	}
	parts := func(reason string) [5]int {
		return [5]int{count(summary.ByCauseReason, reason), count(summary.ByCauseReasonPhysical, reason),
			count(summary.ByCauseReasonCarried, reason), count(summary.ByCauseReasonDerived, reason),
			count(summary.ByCauseReasonUnknown, reason)}
	}
	if got := parts("QUERY_TIMEOUT"); got != [5]int{5, 1, 2, 0, 2} {
		t.Fatalf("QUERY_TIMEOUT total/physical/carried/derived/unknown = %v, want [5 1 2 0 2]", got)
	}
	if got := parts("GAP_GUARD_WARMING"); got != [5]int{1, 0, 0, 1, 0} {
		t.Fatalf("GAP_GUARD_WARMING total/physical/carried/derived/unknown = %v, want [1 0 0 1 0]", got)
	}
}

// A row whose Level was held by a guard an earlier round's timeout raised is
// still a row - the series was not evaluated - but it says the timeout was
// carried and from where; a guard stamped before origins were recorded says
// carried with no origin, never physical; a timeout this round says
// physical.
func TestARowSaysWhetherItsCauseReasonWasCarried(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		carried *observability.CarriedReasonFacts
		want    CauseOrigin
	}{
		{name: "physical", want: CauseOrigin{Origin: observability.ReasonOriginPhysical}},
		{name: "carried with its origin", carried: &observability.CarriedReasonFacts{OriginSlot: 1_700_000_040, OriginCause: "QUERY_TIMEOUT"},
			want: CauseOrigin{Origin: observability.ReasonOriginCarried, Slot: 1_700_000_040, Cause: "QUERY_TIMEOUT"}},
		{name: "carried from before origins", carried: &observability.CarriedReasonFacts{},
			want: CauseOrigin{Origin: observability.ReasonOriginCarried}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			row := degradedRowUnder(t, testCase.carried)
			if row.CauseOrigin == nil || *row.CauseOrigin != testCase.want {
				t.Fatalf("cause origin %+v, want %+v", row.CauseOrigin, testCase.want)
			}
			encoded, err := json.Marshal(row)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(encoded), `"cause_origin":{"origin":"`+testCase.want.Origin+`"`) {
				t.Fatalf("row JSON %s, want cause_origin.origin=%s", encoded, testCase.want.Origin)
			}
		})
	}
}
