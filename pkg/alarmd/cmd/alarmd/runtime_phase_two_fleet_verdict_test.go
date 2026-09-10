// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

func countOf(counts []metric.FleetCount, value string) metric.FleetCount {
	for _, count := range counts {
		if count.Value == value {
			return count
		}
	}
	return metric.FleetCount{}
}

// Many objects collapse into a few bounded kinds. Exporting them per object
// would put the Query Group identity into a label, which is exactly the
// cardinality boundary every other family here respects.
func TestFleetVerdictCountsByKindAndKeepsTheOldest(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	expected := 949
	view := fleet.View{
		Health: fleet.HealthDegraded, Expected: &expected,
		Covered: 949, Determined: 941, Unknown: 8,
		Anomalies: []fleet.Anomaly{
			{QueryGroup: "a", Kind: fleet.KindDegradedRun, Since: at.Add(-2 * time.Hour), Stalled: true},
			{QueryGroup: "b", Kind: fleet.KindDegradedRun, Since: at.Add(-10 * time.Minute)},
			{QueryGroup: "c", Kind: fleet.KindBlockedRun, Since: at.Add(-time.Minute)},
		},
		Gaps: []fleet.Gap{
			{Kind: fleet.GapUndetermined},
			{Kind: fleet.GapReplicaMissing, Replica: "pod-a"},
			{Kind: fleet.GapReplicaMissing, Replica: "pod-b"},
		},
	}

	verdict := fleetVerdictOf(view, at)

	if verdict.Health != string(fleet.HealthDegraded) || verdict.Covered != 949 ||
		verdict.Determined != 941 || verdict.Unknown != 8 || verdict.Expected == nil {
		t.Fatalf("coverage not carried through: %+v", verdict)
	}
	if len(verdict.Anomalies) != 2 {
		t.Fatalf("kinds = %d, want 2 bounded kinds rather than one series per object", len(verdict.Anomalies))
	}
	degraded := countOf(verdict.Anomalies, fleet.KindDegradedRun)
	if degraded.Count != 2 {
		t.Fatalf("degraded count = %d, want 2", degraded.Count)
	}
	// The oldest member is what says how bad it is; averaging or taking the
	// newest would hide the object that has been broken for two hours.
	if degraded.OldestAgeSeconds != (2 * time.Hour).Seconds() {
		t.Fatalf("oldest degraded age = %v, want the two hour member", degraded.OldestAgeSeconds)
	}
	if countOf(verdict.Anomalies, fleet.KindBlockedRun).Count != 1 {
		t.Fatalf("blocked count = %+v", verdict.Anomalies)
	}
	// Two replicas missing is worse than one, and the export has to say so.
	if countOf(verdict.Gaps, string(fleet.GapReplicaMissing)).Count != 2 {
		t.Fatalf("replica gaps = %+v", verdict.Gaps)
	}
	if countOf(verdict.Gaps, string(fleet.GapUndetermined)).Count != 1 {
		t.Fatalf("undetermined gap = %+v", verdict.Gaps)
	}
	if verdict.Stalled != 1 {
		t.Fatalf("stalled = %d, want 1", verdict.Stalled)
	}
}

// An unreadable denominator travels as absent, not as zero.
func TestFleetVerdictKeepsAnUnreadableDenominatorAbsent(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	verdict := fleetVerdictOf(fleet.View{
		Health: fleet.HealthUnknown, Covered: 12, Determined: 12,
		Gaps: []fleet.Gap{{Kind: fleet.GapDenominatorUnavailable}},
	}, at)
	if verdict.Expected != nil {
		t.Fatalf("expected = %v, want absent", *verdict.Expected)
	}
	if countOf(verdict.Gaps, string(fleet.GapDenominatorUnavailable)).Count != 1 {
		t.Fatalf("the reason the denominator is missing was not exported: %+v", verdict.Gaps)
	}
}

// A label set that grows whenever someone upstream adds a classification is a
// cardinality budget nobody owns. Unknown values land in a visible bucket, so a
// new classification shows up as a growing OTHER instead of a silently widening
// metric -- while the JSON API keeps reporting the real value, because a
// response has no budget.
func TestFleetVerdictClosesTheLabelSet(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	verdict := fleetVerdictOf(fleet.View{
		Health: fleet.HealthDegraded,
		Anomalies: []fleet.Anomaly{
			{QueryGroup: "a", Kind: "A_KIND_FROM_THE_FUTURE", Since: at.Add(-time.Minute)},
			{QueryGroup: "b", Kind: fleet.KindDegradedRun, Since: at.Add(-time.Minute),
				Failure: &fleet.FailureRef{Category: "a_category_from_the_future"}},
			{QueryGroup: "c", Kind: fleet.KindDegradedRun, Since: at.Add(-time.Minute),
				Failure: &fleet.FailureRef{Category: "source_backend"}},
		},
		Gaps: []fleet.Gap{{Kind: fleet.GapKind("A_GAP_FROM_THE_FUTURE")}, {Kind: fleet.GapUndetermined}},
	}, at)

	if countOf(verdict.Anomalies, fleet.LabelOther).Count != 1 {
		t.Fatalf("an unknown kind escaped the closed label set: %+v", verdict.Anomalies)
	}
	if countOf(verdict.Anomalies, fleet.KindDegradedRun).Count != 2 {
		t.Fatalf("known kinds = %+v", verdict.Anomalies)
	}
	if countOf(verdict.Gaps, fleet.LabelOther).Count != 1 ||
		countOf(verdict.Gaps, string(fleet.GapUndetermined)).Count != 1 {
		t.Fatalf("gaps = %+v", verdict.Gaps)
	}
	if countOf(verdict.Failures, "other").Count != 1 ||
		countOf(verdict.Failures, "source_backend").Count != 1 {
		t.Fatalf("failures = %+v", verdict.Failures)
	}
}

// The anomaly count says how many objects are unwell; it cannot say what broke.
// Objects that failed before reaching a classification stay out of the failure
// counts rather than being invented into a bucket, so the two totals differ by
// exactly the objects nobody can speak for yet.
func TestFleetVerdictLeavesUnclassifiedFailuresOut(t *testing.T) {
	at := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	verdict := fleetVerdictOf(fleet.View{
		Health: fleet.HealthDegraded,
		Anomalies: []fleet.Anomaly{
			{QueryGroup: "a", Kind: fleet.KindDegradedRun, Since: at.Add(-time.Minute)},
			{QueryGroup: "b", Kind: fleet.KindDegradedRun, Since: at.Add(-time.Minute),
				Failure: &fleet.FailureRef{Category: "budget"}},
		},
	}, at)

	if countOf(verdict.Anomalies, fleet.KindDegradedRun).Count != 2 {
		t.Fatalf("anomalies = %+v", verdict.Anomalies)
	}
	total := 0
	for _, count := range verdict.Failures {
		total += count.Count
	}
	if total != 1 {
		t.Fatalf("failure total = %d, want only the classified one", total)
	}
}
