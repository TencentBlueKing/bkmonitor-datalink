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

// An alert on "objects have stopped being evaluated" watches a series that is
// meant to sit at zero, and a series meant to sit at zero has a failure mode of
// its own: it reads the same whether the condition never happened or the label
// can never be produced at all. So the thing to prove is not that the count is
// zero -- it is that this exact label can be made to appear.
//
// Checking the kind against the closed set in isolation does not prove it: that
// says the mapping keeps the name, not that an object carrying it reaches the
// export. This drives the real path, with the age the alert reads, and pins
// that it does not land in OTHER.
func TestAnOverdueObjectReachesTheExportUnderItsOwnKind(t *testing.T) {
	at := time.Date(2026, 9, 11, 12, 0, 0, 0, time.UTC)
	verdict := fleetVerdictOf(fleet.View{
		Health: fleet.HealthDegraded,
		Anomalies: []fleet.Anomaly{
			{QueryGroup: "parked", Kind: fleet.KindOverdueWake, ReasonCode: fleet.ReasonWakeMissed,
				Since: at.Add(-9 * time.Minute)},
		},
	}, at)

	overdue := countOf(verdict.Anomalies, fleet.KindOverdueWake)
	if overdue.Count != 1 {
		t.Fatalf("anomalies = %+v, want the overdue object exported under its own kind", verdict.Anomalies)
	}
	if other := countOf(verdict.Anomalies, fleet.LabelOther); other.Count != 0 {
		t.Fatalf("the overdue kind fell through to OTHER, so no alert can name it: %+v", verdict.Anomalies)
	}
	// The age is what says how long nothing has evaluated it, which is the
	// number that decides whether anyone has to act.
	if overdue.OldestAgeSeconds != 540 {
		t.Fatalf("oldest age = %v seconds, want the 540 it has been overdue", overdue.OldestAgeSeconds)
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

// The columns have to survive the trip from the view to the export, or the
// identity they exist for is computed over zeroes.
//
// The collector test checks that whatever the verdict carries is exported; it
// cannot see a verdict built from the wrong fields. That gap is the same one
// the anomaly count had: the ends were checked and the transit between them
// was not, and a column reported as zero looks exactly like a column that is
// empty.
func TestTheVerdictCarriesEveryColumnOfTheSplit(t *testing.T) {
	at := time.Unix(1_700_000_000, 0)
	verdict := fleetVerdictOf(fleet.View{
		Health: fleet.HealthDegraded, Covered: 979, Determined: 979, Unknown: 0,
		// Adds up to Determined, the way Aggregate computes it by subtraction.
		// A fixture that does not add up cannot check the identity it is here
		// for -- it fails on its own arithmetic instead.
		Healthy: 837, AnomaliesTotal: 90, DemotedTotal: 33,
		UndecidableTotal: 12, ByDesignTotal: 7,
	}, at)

	for name, got := range map[string]int{
		"healthy": verdict.Healthy, "anomalous": verdict.Anomalous, "demoted": verdict.Demoted,
		"undecidable": verdict.Undecidable, "by_design": verdict.ByDesign,
	} {
		if got == 0 {
			t.Errorf("verdict reports 0 for %q, which the view says is not empty: a column lost in "+
				"transit reads exactly like a column with nothing in it", name)
		}
	}
	if verdict.Healthy != 837 || verdict.Anomalous != 90 || verdict.Demoted != 33 ||
		verdict.Undecidable != 12 || verdict.ByDesign != 7 {
		t.Fatalf("verdict columns = %d/%d/%d/%d/%d, want 837/90/33/12/7",
			verdict.Healthy, verdict.Anomalous, verdict.Demoted, verdict.Undecidable, verdict.ByDesign)
	}
	sum := verdict.Healthy + verdict.Anomalous + verdict.Demoted + verdict.Undecidable +
		verdict.ByDesign + verdict.Unknown
	if sum != verdict.Determined {
		t.Fatalf("columns sum to %d, want determined %d", sum, verdict.Determined)
	}
}

// The whole closed table is exported, a line that is down at zero: an alert
// on "this line is up" needs to see it go down, and a series that is absent
// reads the same as a build that never had the family. Order is the table's,
// so two scrapes of an unchanged deployment export the same series in the
// same order.
func TestEveryCheckLineAndDegradationKindIsExportedEvenWhenDown(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	verdict := fleetVerdictOf(fleet.View{Health: fleet.HealthHealthy}, at)

	codes := fleet.Checks()
	if len(verdict.Checks) != len(codes) {
		t.Fatalf("checks exported = %d, want every one of the %d codes", len(verdict.Checks), len(codes))
	}
	for index, code := range codes {
		if verdict.Checks[index].Value != string(code) || verdict.Checks[index].Count != 0 {
			t.Fatalf("checks[%d] = %+v, want %s at zero", index, verdict.Checks[index], code)
		}
	}
	if len(verdict.Degradations) != len(fleet.DegradationKinds) {
		t.Fatalf("degradations exported = %d, want every one of the %d kinds", len(verdict.Degradations), len(fleet.DegradationKinds))
	}
	for index, kind := range fleet.DegradationKinds {
		if verdict.Degradations[index].Value != string(kind) || verdict.Degradations[index].Count != 0 {
			t.Fatalf("degradations[%d] = %+v, want %s at zero", index, verdict.Degradations[index], kind)
		}
	}
}

// A fleet executing a stale publication was a first line on the page and no
// series anywhere; fleet_health could not say what it was degraded on. The
// standing reaches the export under its own code, with the count its line
// prints, and the kind it degrades on is a series of its own -- and the
// numbers are the page's, from the same first screen.
func TestAStandingReachesTheExportUnderItsOwnCode(t *testing.T) {
	at := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	view := fleet.View{
		Health: fleet.HealthDegraded,
		Activation: &fleet.ActivationFacts{Behind: true, BehindBeyondBound: true, ConsecutiveFailures: 120,
			FailureStage: "schedule_cutover", FailureClass: "schedule_conflict"},
		ActivationReplica: "pod-a",
		Degradations: []fleet.Degradation{
			{Kind: fleet.DegradationActivationBehind, Replica: "pod-a"},
			{Kind: fleet.DegradationOpenAlertSetStale, Replica: "pod-a"},
			{Kind: fleet.DegradationOpenAlertSetStale, Replica: "pod-b"},
		},
		// And the third standing: the leader's round would move objects
		// between the two replicas, so the series carries two.
		Rebalance: &fleet.RebalanceFacts{PlannedAt: at, ReadyWorkers: 2, Assigned: 2370, Target: 1185, MostOwned: 2370,
			MostOwnedBy: "pod-a", LeastOwnedBy: "pod-b", Batch: 23, PlannedMoves: 23, StopSpreadPercent: 5, Shadow: true},
		RebalanceReplica: "pod-a",
		Anomalies: []fleet.Anomaly{
			{QueryGroup: "parked", Kind: fleet.KindOverdueWake, ReasonCode: fleet.ReasonWakeMissed, Since: at.Add(-9 * time.Minute)},
		},
		// Records of loss under DETECTION_ABANDONED: one on the page's
		// history fold (not a current line, so not on fleet_checks), one a
		// cooldown's after the object left the pool, one in progress -- the
		// three kinds fleet_checks cannot tell apart and fleet_losses does.
		PerReplica: []fleet.ReplicaView{{Replica: "pod-b", StartedAt: at.Add(-2 * time.Hour)}},
		GapSkips: map[string]fleet.SkippedSpan{
			"lost":       {At: at.Add(-50 * time.Minute), Replica: "pod-b"},
			"after-cool": {At: at.Add(-time.Minute), Replica: "pod-b", HeldBy: "query_cooldown"},
			"losing-now": {At: at.Add(-time.Minute), Replica: "pod-b"},
			"no-anchor":  {At: at.Add(-time.Minute), Replica: "pod-c"},
		},
	}
	// Aggregate attributes every row before the view leaves it; the fixture
	// skips Aggregate, so it does the same.
	fleet.Attribute(view.Anomalies, at)
	fleet.Decide(&view, at, time.Hour)

	verdict := fleetVerdictOf(view, at)

	for code, want := range map[string]int{
		string(fleet.CheckCutoverFailing):     1,
		string(fleet.CheckReplicaDegraded):    2,
		string(fleet.CheckOwnershipSkewed):    2,
		string(fleet.CheckSlotsOverdue):       1,
		string(fleet.CheckDetectionAbandoned): 3,
	} {
		if got := countOf(verdict.Checks, code).Count; got != want {
			t.Errorf("fleet_checks{code=%s} = %d, want %d", code, got, want)
		}
	}
	for loss, want := range map[string]int{
		string(fleet.LossHistorical): 1, string(fleet.LossAfterCooldown): 1, string(fleet.LossOngoing): 2,
		string(fleet.LossAfterRestart): 0, string(fleet.LossWhileDemoted): 0, "GRACE_UNKNOWN": 1,
	} {
		if got := countOf(verdict.Losses, loss).Count; got != want {
			t.Errorf("fleet_losses{loss=%s} = %d, want %d", loss, got, want)
		}
	}
	for kind, want := range map[string]int{
		string(fleet.DegradationActivationBehind):    1,
		string(fleet.DegradationOpenAlertSetStale):   2,
		string(fleet.DegradationControlLeaderAbsent): 0,
	} {
		if got := countOf(verdict.Degradations, kind).Count; got != want {
			t.Errorf("fleet_degradations{kind=%s} = %d, want %d", kind, got, want)
		}
	}
	// The identity the family exists for: every series is the count the
	// page's line prints, from the same screen.
	page := map[string]int{}
	for _, report := range fleet.Report(&view, at).Checks {
		page[string(report.Code)] = report.LineCount()
	}
	for _, count := range verdict.Checks {
		if count.Count != page[count.Value] {
			t.Errorf("fleet_checks{code=%s} = %d, page line prints %d", count.Value, count.Count, page[count.Value])
		}
	}
}
