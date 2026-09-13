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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// Every code in the catalogue has to be on one side or the other, decided here
// rather than by the runtime default.
//
// What this cannot cover, established by probe rather than assumed: it iterates
// one vocabulary, and attributionOf reads four. A failure code is open by
// construction, and a release can introduce a whole vocabulary that reaches
// attribution without touching this catalogue -- 47 rejection codes are due in
// the next one, and this test stays green while every one of them falls
// through. That gap is covered at runtime instead, by counting the objects held
// against the deployment by the default alone.
//
// The default exists so an unclassified code cannot read as healthy, but a
// default that quietly absorbs new codes is a decision nobody made: the code
// would be filed against this deployment forever without anyone having asked
// whether it belongs there. This is what makes adding a reason code a moment
// where somebody answers the question.
func TestEveryReasonCodeIsAttributedToOneSideOrTheOther(t *testing.T) {
	catalogue := contract.ReasonCatalogV2()
	if len(catalogue) == 0 {
		t.Fatal("the reason catalogue is empty; the check would pass vacuously")
	}
	for _, definition := range catalogue {
		_, external := externalReasons[definition.Code]
		_, ours := ourReasons[definition.Code]
		switch {
		case external && ours:
			t.Errorf("%s is listed as both external and ours", definition.Code)
		case !external && !ours:
			t.Errorf("%s is in the reason catalogue and on neither list: it would count "+
				"against this deployment by default, which may be right, but nobody said so",
				definition.Code)
		}
	}
	// And nothing on either list that the catalogue does not have, which would be
	// a rule kept alive for a code that no longer exists.
	known := map[string]bool{}
	for _, definition := range catalogue {
		known[definition.Code] = true
	}
	for code := range externalReasons {
		if !known[code] {
			t.Errorf("externalReasons lists %q, which the reason catalogue does not contain", code)
		}
	}
	for code := range ourReasons {
		if !known[code] {
			t.Errorf("ourReasons lists %q, which the reason catalogue does not contain", code)
		}
	}
}

// The verdict follows only the objects this deployment could have prevented.
//
// A live read had 99 objects in the anomaly column, almost all of them an
// algorithm waiting for history, a strategy edited mid-round, or a backend that
// timed out. Every one of them made the verdict DEGRADED, so the verdict had
// been DEGRADED continuously for things alarmd cannot act on -- and a signal
// that is always on carries nothing.
func TestOnlyWhatThisDeploymentCouldPreventDecidesTheVerdict(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	external := func(id, reason string) Anomaly {
		return Anomaly{QueryGroup: id, Kind: KindDegradedRun, Since: at.Add(-time.Hour),
			Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: reason}
	}
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{
		external("qg-warming", "HISTORY_WARMING"),
		external("qg-gapped", "HISTORY_GAPPED"),
		external("qg-drift", "CONFIG_DRIFT"),
		external("qg-timeout", "QUERY_TIMEOUT"),
	}
	snapshots[1].TotalAnomalies = 4

	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, time.Minute)
	if len(view.Gaps) > 0 {
		t.Fatalf("unexpected gaps make the verdict UNKNOWN regardless: %+v", view.Gaps)
	}
	if len(view.Anomalies) != 4 {
		t.Fatalf("anomalies = %d, want the 4 external ones still listed", len(view.Anomalies))
	}
	if view.Health != HealthHealthy {
		t.Errorf("health = %q with nothing but external anomalies, want %q: the objects are real "+
			"work but not this deployment's, and a verdict that cannot come back while they exist "+
			"says nothing", view.Health, HealthHealthy)
	}

	// One object this deployment could have prevented is enough to turn it.
	snapshots[1].Anomalies = append(snapshots[1].Anomalies, Anomaly{
		QueryGroup: "qg-budget", Kind: KindDegradedRun, Since: at.Add(-time.Hour),
		Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "EXECUTION_BUDGET_EXHAUSTED"})
	snapshots[1].TotalAnomalies = 5
	view = Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, time.Minute)
	if view.Health != HealthDegraded {
		t.Errorf("health = %q with one budget exhaustion among four external anomalies, want %q",
			view.Health, HealthDegraded)
	}
	if got := OursCount(view.Anomalies); got != 1 {
		t.Errorf("ours = %d, want exactly the one budget exhaustion", got)
	}
}

// A code nobody has classified counts against the deployment. The reverse
// default would let a failure mode this build has only just started producing
// arrive as a HEALTHY verdict, which is the one direction that loses the
// signal rather than costing a look.
func TestAnUnclassifiedReasonCountsAgainstTheDeployment(t *testing.T) {
	got := attributionOf(Anomaly{Kind: KindDegradedRun, CauseReason: "SOMETHING_NOBODY_HAS_MAPPED"})
	if got != AttributionOurs {
		t.Errorf("attribution = %q for an unmapped reason, want %q", got, AttributionOurs)
	}
}

// A stalled object is ours whatever its last reason code was: its last code is
// usually the external thing that happened just before it got stuck, and
// stalling is the one condition here that never clears on its own.
func TestAStalledObjectIsOursEvenWhenItsLastReasonWasExternal(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	anomalies := []Anomaly{{
		QueryGroup: "qg-stuck", Kind: KindDegradedRun,
		Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "QUERY_TIMEOUT",
		FailingSince: at.Add(-2 * time.Hour),
	}}
	Attribute(anomalies)
	if anomalies[0].Attribution != AttributionExternal {
		t.Fatalf("before the stall is marked this object reads as %q; the test no longer "+
			"exercises the upgrade it exists for", anomalies[0].Attribution)
	}
	MarkStalled(anomalies, at, time.Hour)
	if !anomalies[0].Stalled {
		t.Fatal("the object was not marked stalled, so the upgrade is untested")
	}
	if anomalies[0].Attribution != AttributionOurs {
		t.Errorf("a stalled object reports %q, want %q: nothing outside this deployment stops "+
			"rounds from ending, and nothing outside it will start them again",
			anomalies[0].Attribution, AttributionOurs)
	}
}

// The case the first live read of this split produced, four minutes after a
// rollout: seven objects restored from persisted state, which keeps the
// completion kind and not the cause. They were reported against the deployment,
// and would have been after every deploy until each finished one more round --
// the failure this split exists to remove, arriving by another route.
//
// Missing evidence about a real anomaly is neither side. This package already
// refuses to call a view with missing evidence healthy or degraded, and that
// rule does not stop applying because the evidence is missing per object.
func TestARestoredObjectWithNoRecordedCauseIsNotHeldAgainstTheDeployment(t *testing.T) {
	restored := Anomaly{
		QueryGroup: "qg-restored", Kind: KindDegradedRun,
		ReasonCode: "COMPLETED_WITH_UNAVAILABLE", SinceFrom: SinceRestoredLastFull,
	}
	Attribute([]Anomaly{restored})
	if got := attributionOf(restored); got != AttributionUnknown {
		t.Errorf("a restored object with no recorded cause reports %q, want %q: it would make "+
			"every rollout read as a regression", got, AttributionUnknown)
	}

	// The distinction that must not collapse: an object this process watched
	// fail and recorded nothing about is the deployment's own observability
	// failing, not a known persistence gap.
	watched := Anomaly{
		QueryGroup: "qg-watched", Kind: KindDegradedRun,
		ReasonCode: "COMPLETED_WITH_UNAVAILABLE", SinceFrom: SinceSnapshotContinuity,
	}
	if got := attributionOf(watched); got != AttributionOurs {
		t.Errorf("an object we watched fail with nothing recorded reports %q, want %q",
			got, AttributionOurs)
	}

	// And a restored object that does carry evidence is classified on it --
	// either level of evidence, because both survive different things.
	withCause := restored
	withCause.CauseReason = "HISTORY_WARMING"
	if got := attributionOf(withCause); got != AttributionExternal {
		t.Errorf("a restored object carrying a cause reports %q, want it classified on the "+
			"cause (%q)", got, AttributionExternal)
	}
	// A restored object whose failure record survived is not unknowable: we know
	// exactly what happened to it. Calling it unknown would drop a classifiable
	// object out of the verdict, which is the same mistake as the one above with
	// the sign flipped.
	withFailure := restored
	withFailure.Failure = &FailureRef{Category: "source_backend", Code: "QUERY_UNAVAILABLE"}
	if got := attributionOf(withFailure); got != AttributionExternal {
		t.Errorf("a restored object carrying a failure code reports %q, want it classified on "+
			"that code (%q): the evidence is there", got, AttributionExternal)
	}
	// And the case that separates "no evidence" from "evidence nobody has
	// classified", which is the whole reason the two are different words. A
	// recognised code returns above this point, so only an unrecognised one
	// reaches the restored check -- and it must not be treated as absent.
	withUnknownCode := restored
	withUnknownCode.Failure = &FailureRef{Category: "evaluation", Code: "SOMETHING_NEW_WE_EMIT"}
	if got := attributionOf(withUnknownCode); got != AttributionOurs {
		t.Errorf("a restored object carrying an unrecognised failure code reports %q, want %q: "+
			"a code nobody has classified is evidence, and this build produced it", got, AttributionOurs)
	}
}

// A deployment holding nothing but objects it cannot yet speak for is not well
// and is not broken. Calling it degraded pages someone for a rollout; calling
// it healthy claims something no evidence supports.
func TestObjectsWeCannotYetSpeakForMakeTheVerdictUnknownNotDegraded(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{{
		QueryGroup: "qg-restored", Kind: KindDegradedRun, Since: now.Add(-time.Hour),
		ReasonCode: "COMPLETED_WITH_UNAVAILABLE", SinceFrom: SinceRestoredLastFull,
	}}
	snapshots[1].TotalAnomalies = 1
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, time.Minute)
	if len(view.Gaps) > 0 {
		t.Fatalf("unexpected gaps decide the verdict regardless: %+v", view.Gaps)
	}
	if view.Health != HealthUnknown {
		t.Errorf("health = %q, want %q: the evidence about this object was never recorded, "+
			"which is not evidence of a fault and not evidence of health", view.Health, HealthUnknown)
	}
	if got := UnattributedCount(view.Anomalies); got != 1 {
		t.Errorf("unattributed = %d, want 1", got)
	}
	if got := OursCount(view.Anomalies); got != 0 {
		t.Errorf("ours = %d, want 0: it must not be counted against the deployment", got)
	}
}

// The next release adds 47 rejection codes in a vocabulary this package's
// completeness test does not iterate, so every one of them will fall through to
// the default and that test will stay green while it happens. Verified by
// probe, not assumed: the expectation was that it would go red.
//
// A guard anchored to one closed list cannot cover an open input -- a failure
// code is open by construction. So the fall-through is counted instead, and a
// batch of new codes shows up on the first read after the deploy as objects
// held against the deployment by nothing but the default.
func TestObjectsHeldAgainstUsByTheDefaultAloneAreCountedApart(t *testing.T) {
	byRule := Anomaly{QueryGroup: "qg-rule", Kind: KindDegradedRun,
		CauseReason: "EXECUTION_BUDGET_EXHAUSTED"}
	fellThrough := Anomaly{QueryGroup: "qg-new", Kind: KindDegradedRun,
		CauseReason: "STATE_FACT_CONTRADICTS_OUTCOME"}
	anomalies := []Anomaly{byRule, fellThrough}
	Attribute(anomalies)

	for _, anomaly := range anomalies {
		if anomaly.Attribution != AttributionOurs {
			t.Fatalf("%s = %q, want both against the deployment for this test to say anything",
				anomaly.QueryGroup, anomaly.Attribution)
		}
	}
	if anomalies[0].Unclassified {
		t.Error("an object a rule matched is marked as having fallen through")
	}
	if !anomalies[1].Unclassified {
		t.Error("an object no rule matched is not marked: a whole vocabulary could arrive " +
			"and be absorbed into the deployment's own count with nothing saying so")
	}
	summary := summarize(anomalies, now)
	if summary.Ours != 2 {
		t.Errorf("ours = %d, want 2", summary.Ours)
	}
	if summary.OursUnclassified != 1 {
		t.Errorf("ours_unclassified = %d, want exactly the one no rule matched",
			summary.OursUnclassified)
	}
}

// The two rules that read no code at all still count as rules. A stalled object
// is decided by a rule about stalling, and marking it "nobody classified this"
// would send someone looking for a missing table entry that should not exist.
func TestTheRulesThatReadNoCodeAreStillRules(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	anomalies := []Anomaly{
		{QueryGroup: "qg-stuck", Kind: KindDegradedRun, FailingSince: at.Add(-2 * time.Hour)},
		{QueryGroup: "qg-missed", Kind: KindOverdueWake},
	}
	MarkStalled(anomalies, at, time.Hour)
	Attribute(anomalies)
	MarkStalled(anomalies, at, time.Hour)
	for _, anomaly := range anomalies {
		if anomaly.Attribution != AttributionOurs {
			t.Errorf("%s = %q, want %q", anomaly.QueryGroup, anomaly.Attribution, AttributionOurs)
		}
		if anomaly.Unclassified {
			t.Errorf("%s is marked as unclassified, but a rule decided it", anomaly.QueryGroup)
		}
	}
}
