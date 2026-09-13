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
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func objectSet(prefix string, from, to int) []string {
	ids := make([]string, 0, to-from)
	for index := from; index < to; index++ {
		ids = append(ids, prefix+string(rune('a'+index/26))+string(rune('a'+index%26)))
	}
	return ids
}

// The gap said "these two things look the same and I cannot tell which" on a
// running deployment for more than a day. It was honest and useless: a reader
// was left with "do not trust this" and no way to find out, while the two
// possibilities call for opposite responses.
//
// The sets were available the whole time. The catalogue set is loaded to be
// counted, and each replica knows what it holds.
func TestTheCoverageGapNamesWhichKindOfDisagreementItIs(t *testing.T) {
	at := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	catalogue := objectSet("qg-", 0, 20)
	// replica-a holds the first twelve, replica-b the last twelve: four objects
	// are held twice, which inflates Covered by four with nothing left over.
	first := objectSet("qg-", 0, 12)
	second := objectSet("qg-", 8, 20)

	view := Aggregate(
		Expectation{Known: true, QueryGroups: len(catalogue), IDs: catalogue},
		[]Snapshot{
			{Replica: "replica-a", TakenAt: at, Owned: len(first), OwnedObjects: first, Determined: len(first)},
			{Replica: "replica-b", TakenAt: at, Owned: len(second), OwnedObjects: second, Determined: len(second)},
		}, []string{"replica-a", "replica-b"}, at, time.Minute)

	if view.Coverage == nil {
		t.Fatal("no coverage comparison was made even though both sides published their sets")
	}
	if got := len(view.Coverage.HeldBySeveral); got != 4 {
		t.Errorf("held by several = %d, want the 4 objects both replicas claim: %v",
			got, view.Coverage.HeldBySeveral)
	}
	if got := len(view.Coverage.HeldNotExpected); got != 0 {
		t.Errorf("held but not in the catalogue = %d, want 0: %v", got, view.Coverage.HeldNotExpected)
	}
	if got := len(view.Coverage.ExpectedNotHeld); got != 0 {
		t.Errorf("in the catalogue but unheld = %d, want 0: %v", got, view.Coverage.ExpectedNotHeld)
	}

	var detail string
	for _, gap := range view.Gaps {
		if gap.Kind == GapCoverageInconsistent {
			detail = gap.Detail
		}
	}
	if strings.Contains(detail, "look the same as objects left over") {
		t.Errorf("the gap still only names its two hypotheses when it could name the answer: %q", detail)
	}
	if !strings.Contains(detail, "4 held by more than one replica") {
		t.Errorf("the gap does not say how many objects are double counted: %q", detail)
	}
}

// The other kind, which needs the opposite response: ownership that outlived
// the object. Counting alone reports it identically to the case above.
func TestOwnershipThatOutlivedTheObjectIsToldApartFromDoubleCounting(t *testing.T) {
	at := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	catalogue := objectSet("qg-", 0, 16)
	// Distinct shares, no overlap, but four objects the catalogue dropped.
	first := objectSet("qg-", 0, 8)
	second := append(objectSet("qg-", 8, 16), objectSet("gone-", 0, 4)...)

	view := Aggregate(
		Expectation{Known: true, QueryGroups: len(catalogue), IDs: catalogue},
		[]Snapshot{
			{Replica: "replica-a", TakenAt: at, Owned: len(first), OwnedObjects: first, Determined: len(first)},
			{Replica: "replica-b", TakenAt: at, Owned: len(second), OwnedObjects: second, Determined: len(second)},
		}, []string{"replica-a", "replica-b"}, at, time.Minute)

	if view.Coverage == nil {
		t.Fatal("no coverage comparison was made")
	}
	if got := len(view.Coverage.HeldBySeveral); got != 0 {
		t.Errorf("held by several = %d, want 0: %v", got, view.Coverage.HeldBySeveral)
	}
	if got := len(view.Coverage.HeldNotExpected); got != 4 {
		t.Errorf("held but no longer in the catalogue = %d, want 4: %v", got, view.Coverage.HeldNotExpected)
	}
	// Naming them is the point: "twelve more" is not actionable, a list is.
	for _, object := range view.Coverage.HeldNotExpected {
		if !strings.HasPrefix(object, "gone-") {
			t.Errorf("named %q as left over, which the catalogue does list", object)
		}
	}
}

// A replica that could not publish its whole set makes the arithmetic partial,
// and a zero from a partial read is not "none". This is the same distinction
// the anomaly list already makes between a short list and an empty one.
func TestASetTooLargeToPublishIsNotComparedAsIfItWereWhole(t *testing.T) {
	at := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	catalogue := objectSet("qg-", 0, 20)
	published := objectSet("qg-", 0, 5)

	view := Aggregate(
		Expectation{Known: true, QueryGroups: len(catalogue), IDs: catalogue},
		[]Snapshot{{Replica: "replica-a", TakenAt: at,
			Owned: 20, OwnedObjects: published, Determined: 20}},
		[]string{"replica-a"}, at, time.Minute)

	if view.Coverage == nil {
		t.Fatal("expected a comparison, even a partial one")
	}
	if view.Coverage.Comparable {
		t.Error("a replica published 5 of its 20 objects and the comparison still calls itself complete")
	}
	var detail string
	for _, gap := range view.Gaps {
		if gap.Kind == GapCoverageInconsistent {
			detail = gap.Detail
		}
	}
	if detail != "" && !strings.Contains(detail, "could not compare the sets") {
		t.Errorf("an incomparable read reported set arithmetic as if it were whole: %q", detail)
	}
}

// With no sets published at all -- an older replica, or a catalogue that only
// answered with a count -- the gap has to fall back to naming both
// possibilities rather than inventing an answer from nothing.
func TestWithoutTheSetsTheGapStillSaysItCannotTell(t *testing.T) {
	at := time.Date(2026, 9, 13, 10, 0, 0, 0, time.UTC)
	view := Aggregate(
		Expectation{Known: true, QueryGroups: 8},
		[]Snapshot{{Replica: "replica-a", TakenAt: at, Owned: 12, Determined: 12}},
		[]string{"replica-a"}, at, time.Minute)

	if view.Coverage != nil {
		t.Errorf("a comparison was reported with nothing to compare: %+v", view.Coverage)
	}
	var detail string
	for _, gap := range view.Gaps {
		if gap.Kind == GapCoverageInconsistent {
			detail = gap.Detail
		}
	}
	if !strings.Contains(detail, "could not compare the sets") {
		t.Errorf("the fallback wording is gone, so a reader cannot tell an unanswerable "+
			"gap from an answered one: %q", detail)
	}
}

// The cause stops one level short of the answer, and the page was showing only
// the cause: on a running deployment 61 of 62 objects carried
// LEVEL_OUTCOME_UNKNOWN, which the contract requires to mean either "the data
// does not reach this window" (nobody here's doing) or "this clears on its own".
// Those need opposite responses and the cause cannot tell them apart.
func TestTheReasonBelowTheCauseSurvivesToTheSummary(t *testing.T) {
	at := time.Date(2026, 9, 13, 11, 0, 0, 0, time.UTC)
	tracker := NewTracker(nil, "replica-a", func() time.Time { return at })
	// Driven through the observation stream rather than by building the rows,
	// because the defect was the reason being dropped in transit. A first pass
	// of this test built Anomaly values directly and passed against the broken
	// wiring -- the same way a test earlier today tested a helper instead of the
	// call site that was wrong.
	degrade := func(queryGroup, reason string) {
		for round := 0; round < DefaultDegradedRounds; round++ {
			tracker.Observe(context.Background(), observability.Observation{
				Trace:                    observability.TraceFields{QueryGroupKey: queryGroup},
				ProgressCompletionKind:   "COMPLETED_WITH_UNAVAILABLE",
				ProgressCompletionCause:  "LEVEL_OUTCOME_UNKNOWN",
				ProgressCompletionReason: reason,
			})
		}
	}
	degrade("a", "EFFECTIVE_TIME_UNKNOWN")
	degrade("b", "EFFECTIVE_TIME_UNKNOWN")
	degrade("c", "STATE_RETRYABLE_IO")

	anomalies := tracker.Anomalies()
	if len(anomalies) != 3 {
		t.Fatalf("anomalies = %d, want 3", len(anomalies))
	}
	for _, anomaly := range anomalies {
		if anomaly.Cause != "LEVEL_OUTCOME_UNKNOWN" {
			t.Fatalf("%s carries cause %q, want the one being split", anomaly.QueryGroup, anomaly.Cause)
		}
		if anomaly.CauseReason == "" {
			t.Fatalf("%s lost its reason in transit: %+v", anomaly.QueryGroup, anomaly)
		}
	}

	got := map[string]int{}
	for _, count := range summarize(anomalies).ByCauseReason {
		got[count.Value] = count.Count
	}
	// One cause, two reasons. That split is the whole reason the column exists:
	// without it these three objects are one population that cannot say whose
	// problem it is.
	if len(got) != 2 {
		t.Errorf("one cause split into %d reasons, want 2: %v", len(got), got)
	}
	if got["EFFECTIVE_TIME_UNKNOWN"] != 2 || got["STATE_RETRYABLE_IO"] != 1 {
		t.Errorf("reason counts = %v, want two of one reason and one of the other", got)
	}
}
