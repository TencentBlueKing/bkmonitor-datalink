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
	resultcontract "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
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
	// Every word the tracker can write into reason_code has to be decided too:
	// classified, or explicitly carrying no attribution information. Falling
	// through is for codes nobody has looked at yet, not for the vocabulary
	// this package defines itself.
	for _, vocabulary := range [][]string{HealthyCompletions, BlockedOutcomes, FailedExecutions, ResultContractRefusals} {
		for _, word := range vocabulary {
			if !externalReasons[word] && !ourReasons[word] && !uninformativeReasons[word] {
				t.Errorf("the tracker writes %q into reason_code and nothing decides it: "+
					"it would reach the page as an object held against the deployment "+
					"by the fall-through", word)
			}
		}
	}
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
	// And nothing on either list that no vocabulary declares, which would be a
	// rule kept alive for a code nothing emits.
	//
	// There are two vocabularies, not one. The contract catalogue is what the
	// cause and the reason beneath it are drawn from; the tracker has its own
	// words -- what a round's outcome was -- and those are what reach the page
	// in reason_code. Checking only the first is how a rule written for the
	// twelve retired-strategy objects sat there naming a code that field never
	// carries, while the objects it was for went through the fall-through.
	known := map[string]bool{}
	for _, definition := range catalogue {
		known[definition.Code] = true
	}
	for _, vocabulary := range [][]string{HealthyCompletions, BlockedOutcomes, FailedExecutions, ResultContractRefusals} {
		for _, word := range vocabulary {
			known[word] = true
		}
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
	// A word no vocabulary declares, rather than a real code that happens to be
	// unclassified today. A real one is a moving target: this fixture used
	// STATE_FACT_CONTRADICTS_OUTCOME until the result contract refusals were
	// classified, and then the test that guards the fall-through started
	// failing because the fall-through had correctly stopped happening. What is
	// under test is the path, not any particular code that takes it.
	fellThrough := Anomaly{QueryGroup: "qg-new", Kind: KindDegradedRun,
		CauseReason: "A_FAILURE_MODE_NOBODY_HAS_CLASSIFIED_YET"}
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

// The per-replica split has to agree with the deployment total, and it has to
// be the split the verdict is decided on rather than the anomaly count beside
// it. A live read showed 33 anomalies against 53, which reads as one replica
// being much worse; the pair that decides the verdict was 5 against 8, and most
// of the difference was work that is not either replica's doing.
//
// Measured on another line the same day: two pods 66% apart in cost per Slot
// with near-identical throughput. A deployment-level number cannot show that,
// and if the busy one goes quiet the total improves while nothing happened.
func TestThePerReplicaSplitIsTheOneTheVerdictUses(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	on := func(replica, id, reason string) Anomaly {
		return Anomaly{QueryGroup: id, Replica: replica, Kind: KindDegradedRun,
			Since: at.Add(-time.Hour), Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: reason}
	}
	names := replicas()
	snapshots := healthySnapshots()
	snapshots[0].Anomalies = []Anomaly{
		on(names[0], "qg-a1", "HISTORY_WARMING"),
		on(names[0], "qg-a2", "HISTORY_GAPPED"),
	}
	snapshots[0].TotalAnomalies = 2
	snapshots[1].Anomalies = []Anomaly{
		on(names[1], "qg-b1", "HISTORY_WARMING"),
		on(names[1], "qg-b2", "EXECUTION_BUDGET_EXHAUSTED"),
		{QueryGroup: "qg-b3", Replica: names[1], Kind: KindDegradedRun, Since: at.Add(-time.Hour),
			ReasonCode: "COMPLETED_WITH_UNAVAILABLE", SinceFrom: SinceRestoredLastFull},
	}
	snapshots[1].TotalAnomalies = 3

	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, names, now, time.Minute)
	got := map[string]ReplicaView{}
	for _, replica := range view.PerReplica {
		got[replica.Replica] = replica
	}
	if first := got[names[0]]; first.Ours != 0 || first.External != 2 || first.Unattributed != 0 {
		t.Errorf("%s = ours %d / external %d / unattributed %d, want 0/2/0",
			names[0], first.Ours, first.External, first.Unattributed)
	}
	if second := got[names[1]]; second.Ours != 1 || second.External != 1 || second.Unattributed != 1 {
		t.Errorf("%s = ours %d / external %d / unattributed %d, want 1/1/1: the replica carrying "+
			"the budget exhaustion is the one the verdict is about",
			names[1], second.Ours, second.External, second.Unattributed)
	}
	// And the parts add up to the whole, in every column. A per-replica split
	// that does not sum to the deployment total is two answers to one question.
	summary := summarize(view.Anomalies, now)
	var ours, external, unattributed int
	for _, replica := range view.PerReplica {
		ours += replica.Ours
		external += replica.External
		unattributed += replica.Unattributed
	}
	if ours != summary.Ours || external != summary.External || unattributed != summary.Unattributed {
		t.Errorf("per-replica sums to %d/%d/%d, deployment reports %d/%d/%d",
			ours, external, unattributed, summary.Ours, summary.External, summary.Unattributed)
	}
}

// Settle runs twice on the HTTP path -- once when the view is built, and again
// once the caller has marked which objects are stalled, because stalling moves
// an object to ours. So it has to be safe to run twice, and the per-replica
// counts it fills in have to be the counts and not the counts plus the previous
// pass.
//
// Nothing else would catch this. Doubling looks entirely plausible on a page:
// the numbers are still ordered the same way, still sum to each other, and only
// disagree with the deployment total -- which a reader has no reason to add up.
func TestSettleIsSafeToRunTwiceTheWayTheHandlerRunsIt(t *testing.T) {
	at := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	names := replicas()
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{{
		QueryGroup: "qg-budget", Replica: names[1], Kind: KindDegradedRun, Since: at.Add(-time.Hour),
		Cause: "LEVEL_OUTCOME_UNKNOWN", CauseReason: "EXECUTION_BUDGET_EXHAUSTED",
	}}
	snapshots[1].TotalAnomalies = 1

	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, names, now, time.Minute)
	// Exactly what the handler does next.
	MarkStalled(view.Anomalies, now, time.Hour)
	Settle(&view)

	var ours int
	for _, replica := range view.PerReplica {
		ours += replica.Ours
	}
	if ours != 1 {
		t.Errorf("per-replica ours sums to %d after two passes, want 1: the counts are being "+
			"added to rather than replaced", ours)
	}
}

// The twelve retired-strategy objects, as they actually arrive. The rule
// written for them names a contract code; the field carries the tracker's own
// outcome word, so for two releases they were classified by the fall-through
// rather than by the rule meant for them -- same answer, no rule.
//
// Invisible until the fall-through was counted, and the first live read after
// that showed every one of "ours" arriving that way.
func TestABlockedObjectIsClassifiedByTheWordItsFieldActuallyCarries(t *testing.T) {
	blocked := Anomaly{QueryGroup: "qg-retired", Kind: KindBlockedRun,
		ReasonCode: "source_blocked"}
	Attribute([]Anomaly{blocked})
	anomalies := []Anomaly{blocked}
	Attribute(anomalies)
	if anomalies[0].Attribution != AttributionOurs {
		t.Errorf("attribution = %q, want %q", anomalies[0].Attribution, AttributionOurs)
	}
	if anomalies[0].Unclassified {
		t.Error("a blocked object is still reaching the fall-through: the rule for it names a " +
			"code this field does not carry")
	}
}

// A round that failed on a backend timeout reports the outcome word "error" and
// the specific code beside it. The outcome word says a round failed, which is
// true of either side; the code says which. Reading the coarse word first
// decided these against the deployment before the specific one was ever looked
// at.
func TestTheSpecificFailureCodeBeatsTheCoarseOutcomeWord(t *testing.T) {
	timedOut := Anomaly{QueryGroup: "qg-slow", Kind: KindDegradedRun, ReasonCode: "error",
		Failure: &FailureRef{Category: "provider_transport", Code: "QUERY_TIMEOUT"}}
	if got := attributionOf(timedOut); got != AttributionExternal {
		t.Errorf("attribution = %q, want %q: the backend timed out, and \"error\" only says "+
			"the round did not finish", got, AttributionExternal)
	}
	// The same shape with an evaluation fault stays ours, so this is not the
	// failure code simply overriding everything.
	badResult := Anomaly{QueryGroup: "qg-bad", Kind: KindDegradedRun, ReasonCode: "error",
		Failure: &FailureRef{Category: "evaluation", Code: "STATE_CORRUPT"}}
	if got := attributionOf(badResult); got != AttributionOurs {
		t.Errorf("attribution = %q, want %q", got, AttributionOurs)
	}
}

// The three sets have to be disjoint, and this is the only place that says so.
//
// A word cannot both carry no attribution information and be classified; if one
// ever appeared in two sets the reading would depend on which check ran first,
// which is not a thing anyone should have to know. This is also what lets
// attributionOf skip the uninformative check entirely -- a listed word is in
// neither classification map, so the search passes over it anyway.
func TestAWordIsEitherClassifiedOrExplicitlyUninformativeNeverBoth(t *testing.T) {
	if len(uninformativeReasons) == 0 {
		t.Fatal("no uninformative words declared; the check would pass vacuously")
	}
	for word := range uninformativeReasons {
		if externalReasons[word] {
			t.Errorf("%q is declared to carry no attribution information and is also "+
				"classified as external", word)
		}
		if ourReasons[word] {
			t.Errorf("%q is declared to carry no attribution information and is also "+
				"classified as ours: attributionOf skips no words, so this one would be "+
				"read as evidence", word)
		}
	}
	for word := range externalReasons {
		if ourReasons[word] {
			t.Errorf("%q is on both classification lists", word)
		}
	}
}

// TestEveryResultContractCodeIsClassified exhausts the result contract's
// refusal codes rather than waiting for one to reach a live page.
//
// Those codes are a closed subset of an otherwise open input. A guard anchored
// to a closed list cannot cover a failure code in general -- that is why the
// fall-through is counted at runtime -- but these are declared in one place, so
// exhausting them is available, and it is strictly earlier than waiting.
//
// Waiting is not a plan for this family: the codes are produced only when the
// result contract refuses a mutation, and that was just driven to near zero, so
// in normal running they may never appear at all. A plan whose trigger may
// never fire and a plan that was forgotten look the same afterwards.
func TestEveryResultContractCodeIsClassified(t *testing.T) {
	codes := resultcontract.ResultContractCodes()
	if len(codes) == 0 {
		t.Fatal("no result contract codes are declared; this check would pass without testing anything")
	}

	// The copy this package keeps is compared with the original in both
	// directions. One direction alone is the failure this file already learned:
	// a list that only has to be a subset agrees with its source on the day it
	// is written and drifts silently afterwards.
	declared := map[string]bool{}
	for _, code := range ResultContractRefusals {
		declared[code] = true
	}
	for _, code := range codes {
		if !declared[code] {
			t.Errorf("the result contract declares %s and ResultContractRefusals does not list it: "+
				"a refusal this deployment can produce would reach the page unclassified", code)
		}
	}
	original := map[string]bool{}
	for _, code := range codes {
		original[code] = true
	}
	for _, code := range ResultContractRefusals {
		if !original[code] {
			t.Errorf("ResultContractRefusals lists %s, which the result contract no longer declares: "+
				"a rule kept alive for a code nothing emits", code)
		}
	}
	for _, code := range codes {
		graded := []Anomaly{{Kind: KindDegradedRun, CauseReason: code}}
		Attribute(graded)
		if graded[0].Unclassified {
			t.Errorf("%s reaches attribution and no rule matches it: it counts against the deployment "+
				"by the fall-through, which is the safe direction but is nobody's decision", code)
		}
	}
}

// The settled class is the only part of this list with an answer nobody needs
// to work again. Objects whose series do not live long enough to fill the
// detection window are external for the same reason the rest of External is,
// and unlike the rest they will read exactly the same tomorrow: there is
// nothing to investigate and no change to alarmd that affects them.
//
// Counted, so the page can say so once instead of every reader rediscovering
// it. A subset of External and never a fourth column -- the four-way split is
// what the verdict reads, and a fifth number in it would change the verdict.
func TestTheSettledNeverFillingWindowsAreCountedWithoutLeavingExternal(t *testing.T) {
	filling := Anomaly{QueryGroup: "qg-filling", Kind: KindDegradedRun,
		CauseReason: "HISTORY_WARMING",
		Coverage:    &HistoryCoverage{Levels: 2, Short: 1, WorstValid: 8, WorstRequired: 9, ShortRounds: 3}}
	never := Anomaly{QueryGroup: "qg-never", Kind: KindDegradedRun,
		CauseReason: "HISTORY_WARMING",
		Coverage:    &HistoryCoverage{Levels: 2, Short: 2, WorstValid: 2, WorstRequired: 14, ShortRounds: 40}}
	// An external object with no coverage at all must not be swept in. It is
	// external for a different reason and has not been answered.
	timedOut := Anomaly{QueryGroup: "qg-timeout", Kind: KindDegradedRun, CauseReason: "QUERY_TIMEOUT"}
	// The case a live deployment actually showed. Coverage is reported for
	// every object, so a GAPPED object whose windows have been short for a
	// while satisfies Persistent() exactly as a churning one does. Counting it
	// told the reader "this series does not live long enough to fill its
	// window" about data that arrived and then had holes -- a different
	// situation, a different fix, and one that sends them nowhere.
	//
	// Seven were counted on that page while only two carried the reason.
	gapped := Anomaly{QueryGroup: "qg-gapped", Kind: KindDegradedRun,
		CauseReason: "HISTORY_GAPPED",
		Coverage:    &HistoryCoverage{Levels: 2, Short: 1, WorstValid: 3, WorstRequired: 9, ShortRounds: 40}}
	anomalies := []Anomaly{filling, never, timedOut, gapped}
	Attribute(anomalies)
	for _, anomaly := range anomalies {
		if anomaly.Attribution != AttributionExternal {
			t.Fatalf("%s = %q, want all three external for this test to say anything",
				anomaly.QueryGroup, anomaly.Attribution)
		}
	}
	summary := summarize(anomalies, now)
	if summary.External != 4 {
		t.Errorf("external = %d, want all 4: the settled ones are a subset, not a fourth column",
			summary.External)
	}
	if !gapped.Coverage.Persistent() {
		t.Fatal("the GAPPED fixture no longer satisfies Persistent(), so this test no longer " +
			"checks that the reason is what keeps it out of the count")
	}
	if summary.Ours != 0 {
		t.Errorf("ours = %d, want 0: a strategy whose series churn is not a capacity or design fault",
			summary.Ours)
	}
	if summary.WindowNeverFills != 1 {
		t.Errorf("window_never_fills = %d, want exactly the one that both carries the reason and "+
			"has been short for longer than filling it could take -- the GAPPED object satisfies "+
			"the counts and must not be described by this wording", summary.WindowNeverFills)
	}
	// None of these fixtures say anything about series identity, so none of
	// them can be called churn. The page used to name churn as the likely cause
	// of every object in the count above, which is a guess pointed at a
	// population that also contains series whose data is simply missing.
	if summary.WindowSeriesChurn != 0 {
		t.Errorf("window_series_churn = %d, want 0: no fixture here reports a single fresh window, "+
			"and claiming churn without that is the guess this count exists to replace",
			summary.WindowSeriesChurn)
	}
}

// Which half of "this window will never fill" an object is, and who it goes to.
//
// Persistent() is true of two situations with different owners. A strategy whose
// aggregation dimensions churn gets a new set of series every few rounds, none
// of which lives long enough to fill anything -- that one needs the strategy
// edited. Long-lived series whose data is missing produce the identical counts
// and need someone to go find the data. Editing a strategy's dimensions because
// the page suggested it, when the dimensions were never the problem, is the
// specific cost of holding them in one number.
func TestChurningWindowsAreCountedApartFromOnesWhoseDataIsMissing(t *testing.T) {
	// Every short window belongs to a series with no loaded history, and has
	// for longer than filling one takes.
	churning := Anomaly{QueryGroup: "qg-churn", Kind: KindDegradedRun,
		CauseReason: "HISTORY_WARMING",
		Coverage: &HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
			Fresh: 4, ShortFresh: 4, FreshRounds: 40}}
	// Identical shortfall, identical run, and every short window belongs to a
	// series this round loaded history for.
	starving := Anomaly{QueryGroup: "qg-missing", Kind: KindDegradedRun,
		CauseReason: "HISTORY_WARMING",
		Coverage: &HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
			Fresh: 0, ShortFresh: 0, FreshRounds: 0}}
	// All fresh, but not yet for longer than filling a window takes -- which is
	// what the round after a StateGeneration change looks like, because an edit
	// re-keys every series at once and they all load nothing.
	rekeyed := Anomaly{QueryGroup: "qg-rekeyed", Kind: KindDegradedRun,
		CauseReason: "HISTORY_WARMING",
		Coverage: &HistoryCoverage{Levels: 9, Short: 4, WorstValid: 2, WorstRequired: 9, ShortRounds: 40,
			Fresh: 9, ShortFresh: 4, FreshRounds: 1}}
	anomalies := []Anomaly{churning, starving, rekeyed}
	Attribute(anomalies)
	summary := summarize(anomalies, now)

	if summary.WindowNeverFills != 3 {
		t.Fatalf("window_never_fills = %d, want all 3: they have the same shortfall over the same "+
			"run, which is the point -- nothing but the fresh counts separates them",
			summary.WindowNeverFills)
	}
	if summary.WindowSeriesChurn != 1 {
		t.Errorf("window_series_churn = %d, want 1: only the first has every short window on a "+
			"series with no history for longer than a window takes to fill", summary.WindowSeriesChurn)
	}
	if !starving.Coverage.Persistent() || starving.Coverage.Churning() {
		t.Errorf("the object whose series all load history reads as %+v: it is still a window that "+
			"will not fill, and it is not churn", *starving.Coverage)
	}
	if rekeyed.Coverage.Churning() {
		t.Errorf("a single round of every-series-fresh read as churn: that is also what the round " +
			"after a strategy edit looks like, and the run is the only thing that tells them apart")
	}
}

// Abandoning a window of time is this deployment's own decision, whatever
// provoked it.
//
// The Slot fell further behind than the replay limits allow, so the cursor
// jumped forward and those minutes were never detected on. It was filed as
// external, under "the data does not reach the window the algorithm needs,
// nothing about this deployment changes that" -- wrong twice. Capacity is
// exactly what changes it, and the wording sent "we skipped detection" to
// whoever owns the strategy, who can do nothing about it.
//
// The backend-caused case is not this one: an object whose backend keeps
// failing is demoted, and that column is decided before this one.
func TestAbandoningAWindowOfTimeCountsAgainstThisDeployment(t *testing.T) {
	skipped := []Anomaly{{QueryGroup: "qg-skipped", Kind: KindDegradedRun, CauseReason: "GAP_SKIPPED"}}
	Attribute(skipped)
	if skipped[0].Attribution != AttributionOurs {
		t.Fatalf("attribution = %q, want %q: nobody outside alarmd can act on a window alarmd "+
			"decided not to evaluate", skipped[0].Attribution, AttributionOurs)
	}
	// By a rule, not by the fall-through. The fall-through is the safe default
	// for codes nobody has classified, and it is counted separately precisely
	// so it can be read as "the table has fallen behind" -- which this is not.
	if skipped[0].Unclassified {
		t.Error("GAP_SKIPPED reaches ours by the fall-through; it has a rule and should match it, " +
			"or it inflates the number that says the table needs updating")
	}
	summary := summarize(skipped, now)
	if summary.Ours != 1 || summary.External != 0 || summary.OursUnclassified != 0 {
		t.Errorf("summary ours=%d external=%d unclassified=%d, want 1/0/0",
			summary.Ours, summary.External, summary.OursUnclassified)
	}
}

// The columns that are not faults must not claim it. Each of those says
// "nobody acts on this", and a window nobody evaluated is the opposite.
func TestASkippedWindowIsNotFiledAsRequiringNoAction(t *testing.T) {
	for _, reason := range []string{"GAP_SKIPPED"} {
		if byDesignReasons[reason] {
			t.Errorf("%q is filed as a change already being made; minutes nobody detected on are "+
				"not finished business", reason)
		}
		if undecidableReason(reason) {
			t.Errorf("%q is filed as a window with nothing to decide; the window was never "+
				"evaluated at all, which is a different statement", reason)
		}
		if externalReasons[reason] {
			t.Errorf("%q is still external; it would go to whoever owns the strategy, who cannot "+
				"make this deployment keep up", reason)
		}
	}
}
