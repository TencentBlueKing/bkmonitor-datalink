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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type clock struct{ at time.Time }

func (c *clock) Now() time.Time { return c.at }

func completion(queryGroup, kind, strategy string) observability.Observation {
	return observability.Observation{
		ProgressCompletionKind: kind,
		Trace:                  observability.TraceFields{QueryGroupKey: queryGroup, StrategyID: strategy, BusinessID: "2"},
	}
}

func runOutcome(queryGroup, outcome string) observability.Observation {
	return observability.Observation{
		RunOutcome: outcome,
		Trace:      observability.TraceFields{QueryGroupKey: queryGroup},
	}
}

func newTracker(t *testing.T, at *clock) *Tracker {
	t.Helper()
	return NewTracker(nil, "pod-a", at.Now)
}

// The emitters on both hot paths build their observation without a trace and
// rely on observers merging what the context carries. A tracker that reads only
// the observation sees an anonymous round every time, and the published anomaly
// list is then permanently empty while every unit test still passes.
func TestQueryGroupIsTakenFromTheContextWhenTheObservationOmitsIt(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{
		QueryGroupKey: "qg-from-context", StrategyID: "8930", BusinessID: "2",
	})
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(ctx, observability.Observation{ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE"})
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].QueryGroup != "qg-from-context" {
		t.Fatalf("anomalies = %+v, want the object named only by the context", anomalies)
	}
	if len(anomalies[0].Strategies) != 1 || anomalies[0].Strategies[0].BusinessID != "2" {
		t.Fatalf("strategies = %+v, want the context's strategy and business", anomalies[0].Strategies)
	}
}

// A query group whose every execution fails commits no progress and is never
// blocked, so without this it stays invisible while failing continuously.
func TestExecutionsThatNeverFinishAreReported(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), observability.Observation{
			ExecuteOutcome: "error",
			Trace:          observability.TraceFields{QueryGroupKey: "qg-failing"},
		})
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].Kind != KindDegradedRun || anomalies[0].ReasonCode != "error" {
		t.Fatalf("anomalies = %+v, want the continuously failing execution reported", anomalies)
	}
}

// The observations that carry an outcome never carry a strategy, and the one
// that carries a strategy carries no outcome. Requiring both on the same
// observation leaves every anomaly without the field an operator can act on.
func TestStrategyAssociationComesFromObservationsWithoutAnOutcome(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-1"})
	// Evaluation names the strategy but reports no outcome.
	tracker.Observe(ctx, observability.Observation{
		Trace: observability.TraceFields{StrategyID: "8930", BusinessID: "2"},
	})
	// The rounds that follow report an outcome and name no strategy.
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(ctx, observability.Observation{ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE"})
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 {
		t.Fatalf("anomalies = %+v, want one", anomalies)
	}
	if len(anomalies[0].Strategies) != 1 || anomalies[0].Strategies[0].StrategyID != "8930" ||
		anomalies[0].Strategies[0].BusinessID != "2" {
		t.Fatalf("strategies = %+v, want the strategy learned from the evaluation observation", anomalies[0].Strategies)
	}
}

// Merging one field at a time matters: the observation names the strategy and
// the context names the object, so swapping one for the other loses a half.
func TestTraceFieldsAreMergedNotReplaced(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-1"})
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(ctx, observability.Observation{
			ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE",
			Trace:                  observability.TraceFields{StrategyID: "8930", BusinessID: "2"},
		})
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].QueryGroup != "qg-1" {
		t.Fatalf("anomalies = %+v, want the object named by the context", anomalies)
	}
	if len(anomalies[0].Strategies) != 1 {
		t.Fatalf("strategies = %+v, want the strategy named by the observation kept", anomalies[0].Strategies)
	}
}

func TestHealthyCompletionsKeepAQueryGroupOffTheList(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for _, kind := range []string{"FULL_COMPLETED", "FULL_EMPTY_COMPLETED", "FULL_COMPLETED"} {
		tracker.Observe(context.Background(), completion("qg-1", kind, "8930"))
	}
	if anomalies := tracker.Anomalies(); len(anomalies) != 0 {
		t.Fatalf("anomalies = %+v, want none; empty results are a correct business answer", anomalies)
	}
}

func TestDegradedRunsMustReachTheThresholdBeforeBeingReported(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	// Seen healthy once, before the run: the continuity label means this process
	// watched the object go wrong, which it cannot claim about one whose first
	// conclusive round was already the bad one.
	tracker.Observe(context.Background(), completion("qg-1", "FULL_COMPLETED", "8930"))
	for round := 0; round < DefaultDegradedRounds-1; round++ {
		tracker.Observe(context.Background(), completion("qg-1", "COMPLETED_WITH_UNAVAILABLE", "8930"))
	}
	if anomalies := tracker.Anomalies(); len(anomalies) != 0 {
		t.Fatalf("anomalies = %+v, want none below the threshold", anomalies)
	}
	tracker.Observe(context.Background(), completion("qg-1", "COMPLETED_WITH_UNAVAILABLE", "8930"))
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].Kind != KindDegradedRun {
		t.Fatalf("anomalies = %+v, want one degraded run", anomalies)
	}
	if anomalies[0].ReasonCode != "COMPLETED_WITH_UNAVAILABLE" {
		t.Fatalf("reason = %q, want the completion kind that keeps recurring", anomalies[0].ReasonCode)
	}
	if anomalies[0].SinceFrom != SinceSnapshotContinuity {
		t.Fatalf("since source = %q, want the continuity label", anomalies[0].SinceFrom)
	}
	if len(anomalies[0].Strategies) != 1 || anomalies[0].Strategies[0].StrategyID != "8930" {
		t.Fatalf("strategies = %+v, want the observed strategy", anomalies[0].Strategies)
	}
}

// Query group periods in the deployed population run from ten seconds to ten
// minutes. Counting rounds rather than elapsed time is what keeps a slow group
// from being called broken for being slow.
func TestThresholdCountsRoundsNotElapsedTime(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), completion("slow", "COMPLETED_WITH_UNAVAILABLE", "8930"))
		at.at = at.at.Add(10 * time.Minute)
	}
	slow := tracker.Anomalies()

	at = &clock{at: now}
	fast := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		fast.Observe(context.Background(), completion("fast", "COMPLETED_WITH_UNAVAILABLE", "8930"))
		at.at = at.at.Add(10 * time.Second)
	}
	if len(slow) != 1 || len(fast.Anomalies()) != 1 {
		t.Fatalf("slow = %d fast = %d, want both reported after the same number of rounds", len(slow), len(fast.Anomalies()))
	}
}

func TestOneHealthyRoundClearsTheRun(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), completion("qg-1", "COMPLETED_WITH_UNAVAILABLE", "8930"))
	}
	if len(tracker.Anomalies()) != 1 {
		t.Fatal("query group did not reach the threshold")
	}
	tracker.Observe(context.Background(), completion("qg-1", "FULL_COMPLETED", "8930"))
	if anomalies := tracker.Anomalies(); len(anomalies) != 0 {
		t.Fatalf("anomalies = %+v, want the run cleared by a healthy round", anomalies)
	}
}

// Blocked rounds produce nothing at all, so they are reported sooner than
// degraded ones, which at least produce a result.
func TestBlockedRoundsUseTheLowerThreshold(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultBlockedRounds; round++ {
		tracker.Observe(context.Background(), runOutcome("qg-1", "source_blocked"))
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].Kind != KindBlockedRun {
		t.Fatalf("anomalies = %+v, want one blocked run", anomalies)
	}
}

// Rounds that were simply not due say nothing about health and must neither
// start nor clear a run, or a quiet query group would look broken.
func TestRoundsThatSayNothingDoNotAffectTheRun(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < 20; round++ {
		tracker.Observe(context.Background(), runOutcome("qg-1", "source_not_due"))
	}
	if anomalies := tracker.Anomalies(); len(anomalies) != 0 {
		t.Fatalf("anomalies = %+v, want none from rounds that were not due", anomalies)
	}
}

func TestForgetDropsQueryGroupsThisReplicaNoLongerOwns(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), completion("qg-1", "COMPLETED_WITH_UNAVAILABLE", "8930"))
		tracker.Observe(context.Background(), completion("qg-2", "COMPLETED_WITH_UNAVAILABLE", "8931"))
	}
	tracker.Forget(map[string]struct{}{"qg-1": {}})
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].QueryGroup != "qg-1" {
		t.Fatalf("anomalies = %+v, want only the still-owned query group", anomalies)
	}
	if tracker.Tracked() != 1 {
		t.Fatalf("tracked = %d, want the handed-over group dropped", tracker.Tracked())
	}
}

// Diagnostics must not change what the pipeline reports about itself.
func TestObservationsAreForwardedUnchanged(t *testing.T) {
	at := &clock{at: now}
	var seen []observability.Observation
	tracker := NewTracker(observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		seen = append(seen, observation)
	}), "pod-a", at.Now)
	sent := completion("qg-1", "FULL_COMPLETED", "8930")
	tracker.Observe(context.Background(), sent)
	if len(seen) != 1 || seen[0].ProgressCompletionKind != sent.ProgressCompletionKind {
		t.Fatalf("forwarded = %+v, want the observation unchanged", seen)
	}
}

func TestTableIsBoundedAndTheBoundIsObservable(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	tracker.maxTracked = 2
	for _, queryGroup := range []string{"qg-1", "qg-2", "qg-3"} {
		tracker.Observe(context.Background(), completion(queryGroup, "COMPLETED_WITH_UNAVAILABLE", "8930"))
	}
	if tracker.Tracked() != 2 {
		t.Fatalf("tracked = %d, want the table bounded at 2", tracker.Tracked())
	}
}

// "cancelled" is what a Slot reports both on an ordinary shutdown and when a
// short-period object keeps blowing its completion deadline. The classifier
// cannot tell those apart, so it must not treat either as evidence of health:
// an object whose every round is cancelled has said nothing about itself.
func TestObjectsWhoseRoundsAreAllInconclusiveStayUndetermined(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < 10; round++ {
		tracker.Observe(context.Background(), observability.Observation{
			ExecuteOutcome: "cancelled",
			Trace:          observability.TraceFields{QueryGroupKey: "qg-deadline"},
		})
	}
	if got := tracker.Determined(); got != 0 {
		t.Fatalf("determined = %d, want the object to stay unaccounted for", got)
	}
	if anomalies := tracker.Anomalies(); len(anomalies) != 0 {
		t.Fatalf("anomalies = %+v, want none: nothing conclusive was observed", anomalies)
	}
}

func TestAConclusiveRoundDeterminesTheObject(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	tracker.Observe(context.Background(), completion("qg-healthy", "FULL_COMPLETED", "8930"))
	tracker.Observe(context.Background(), runOutcome("qg-blocked", "source_blocked"))
	if got := tracker.Determined(); got != 2 {
		t.Fatalf("determined = %d, want both objects accounted for", got)
	}
	// Forget is what keeps the count comparable with what the replica owns.
	tracker.Forget(map[string]struct{}{"qg-healthy": {}})
	if got := tracker.Determined(); got != 1 {
		t.Fatalf("determined after handover = %d, want only the retained object", got)
	}
}

// The publisher cuts this list at the cap, so the order it is built in decides
// which objects survive. Ranging over a Go map is randomised: without sorting
// here, every tick would keep a different arbitrary subset and the object that
// has been broken longest would flicker in and out of the page.
func TestAnomaliesAreSortedSoTruncationKeepsTheOldest(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	// Named so that alphabetical order is the reverse of age: a sort by key, or
	// no sort at all, cannot pass by accident.
	started := []string{"qg-c", "qg-b", "qg-a"}
	for index, queryGroup := range started {
		at.at = now.Add(time.Duration(index) * time.Hour)
		tracker.Observe(context.Background(), runOutcome(queryGroup, "source_blocked"))
	}
	at.at = now.Add(10 * time.Hour)
	for round := 0; round < DefaultBlockedRounds; round++ {
		for _, queryGroup := range started {
			tracker.Observe(context.Background(), runOutcome(queryGroup, "source_blocked"))
		}
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != len(started) {
		t.Fatalf("anomalies = %+v, want one per object", anomalies)
	}
	for index, queryGroup := range started {
		if anomalies[index].QueryGroup != queryGroup {
			t.Fatalf("anomaly order = %+v, want longest-running first: %v", anomalies, started)
		}
	}
}

// The completion kind says a round ended with something unavailable; it never
// says what. A hundred objects sharing one reason code leave the reader with
// nothing to look at unless the classification the pipeline already emits is
// carried along with them.
func TestAnomaliesCarryWhyTheRoundFailed(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(),
		observability.TraceFields{QueryGroupKey: "qg-why"})
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(ctx, observability.Observation{
			Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
			Result: observability.ResultFailed,
			QueryFailure: &observability.QueryFailureFacts{
				Stage: "provider", Category: "provider_transport", Code: "upstream_timeout",
			},
		})
		tracker.Observe(ctx, observability.Observation{ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE"})
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].Failure == nil {
		t.Fatalf("anomalies = %+v, want one carrying why it failed", anomalies)
	}
	if got := anomalies[0].Failure.Category; got != "provider_transport" {
		t.Fatalf("failure category = %q, want the classification the pipeline emitted", got)
	}
}

// A failure is context, not a verdict. Counting it as conclusive would let a
// transient that the pipeline retried and recovered from mark the object as
// determined, and a retried round is exactly what "not yet known" means.
func TestAQueryFailureAloneDoesNotDetermineOrReportTheObject(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(),
		observability.TraceFields{QueryGroupKey: "qg-transient"})
	for round := 0; round < 10; round++ {
		tracker.Observe(ctx, observability.Observation{
			Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
			Result:       observability.ResultFailed,
			QueryFailure: &observability.QueryFailureFacts{Stage: "execute", Category: "source_backend"},
		})
	}
	if got := tracker.Determined(); got != 0 {
		t.Fatalf("determined = %d, want the object to stay unaccounted for", got)
	}
	if anomalies := tracker.Anomalies(); len(anomalies) != 0 {
		t.Fatalf("anomalies = %+v, want none from failures alone", anomalies)
	}
}

// The completion kind folds four conditions into one word and they call for
// opposite responses. The tracker has to carry which one it was, or the object
// list can only say "degraded" about every one of them.
func TestAnomalyCarriesWhichConditionCausedTheUnavailableCompletion(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		observation := completion("qg-waiting", "COMPLETED_WITH_UNAVAILABLE", "8930")
		observation.ProgressCompletionCause = "DATA_NOT_READY"
		tracker.Observe(context.Background(), observation)
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].Cause != "DATA_NOT_READY" {
		t.Fatalf("anomalies = %+v, want the cause carried through", anomalies)
	}

	// A recovered object must not keep explaining itself with the run that
	// ended: the cause described that run, not this one.
	tracker.Observe(context.Background(), completion("qg-waiting", "FULL_COMPLETED", "8930"))
	if got := tracker.Anomalies(); len(got) != 0 {
		t.Fatalf("recovered object still reported: %+v", got)
	}
	observation := completion("qg-waiting", "COMPLETED_WITH_UNAVAILABLE", "8930")
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), observation)
	}
	if got := tracker.Anomalies(); len(got) != 1 || got[0].Cause != "" {
		t.Fatalf("a new run inherited the previous run's cause: %+v", got)
	}
}

// coverageCompletion is a degraded completion carrying window coverage, which
// is the only combination that reaches the tracker's coverage path: a healthy
// completion resets the run before the counts are read.
func coverageCompletion(queryGroup string, levels, short, valid, required uint32) observability.Observation {
	observation := completion(queryGroup, "COMPLETED_WITH_UNAVAILABLE", "8930")
	observation.ProgressCompletionCause = "LEVEL_OUTCOME_UNKNOWN"
	observation.ProgressCompletionReason = "HISTORY_WARMING"
	empty := uint32(0)
	if valid == 0 && short > 0 {
		empty = short
	}
	observation.HistoryCoverage = &observability.HistoryCoverageFacts{
		Levels: levels, Short: short, Empty: empty, WorstValid: valid, WorstRequired: required,
	}
	return observation
}

// HISTORY_WARMING describes two situations that need opposite responses and
// reads identically in both. The counts that separate them were computed in
// state/window.go and discarded there, so every consumer downstream -- metrics,
// diagnostics, this page -- had one label and no way to act on it.
func TestAnomalyCarriesHowShortTheDetectionWindowWas(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), coverageCompletion("qg-short", 3, 1, 2, 14))
	}
	// Read from the undecidable column, not the anomaly one: a window that
	// cannot decide recovery is not a fault, and it stopped being listed as an
	// anomaly when that became the classification.
	listed := tracker.Undecidable()
	if len(listed) != 1 || listed[0].Coverage == nil {
		t.Fatalf("undecidable = %+v, want the window coverage carried through", listed)
	}
	got := *listed[0].Coverage
	if got.Levels != 3 || got.Short != 1 || got.WorstValid != 2 || got.WorstRequired != 14 {
		t.Fatalf("coverage = %+v, want the counts as observed", got)
	}
}

// The run length is the one part of the coverage a single observation cannot
// supply, and it is the whole basis of the distinction: one round cannot tell a
// window that is filling from one that never will, because both are short.
func TestShortWindowRoundsAccumulateAcrossRoundsAndStopWhenOneFills(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds+4; round++ {
		tracker.Observe(context.Background(), coverageCompletion("qg-short", 3, 1, 2, 14))
	}
	listed := tracker.Undecidable()
	if len(listed) != 1 || listed[0].Coverage == nil {
		t.Fatalf("undecidable = %+v, want one object carrying coverage", listed)
	}
	if got := listed[0].Coverage.ShortRounds; got != uint32(DefaultDegradedRounds+4) {
		t.Fatalf("short rounds = %d, want one per observed round (%d)", got, DefaultDegradedRounds+4)
	}
	// A round whose windows were all complete ends the run. Without this the
	// count only ever rises, and an object that had one bad patch months ago
	// is permanently labelled as one that can never converge.
	tracker.Observe(context.Background(), coverageCompletion("qg-short", 3, 0, 0, 0))
	listed = tracker.Undecidable()
	if len(listed) != 1 || listed[0].Coverage == nil {
		t.Fatalf("undecidable = %+v, want the object still listed", listed)
	}
	if got := listed[0].Coverage.ShortRounds; got != 0 {
		t.Fatalf("short rounds = %d after a complete window, want the run ended", got)
	}
}

// A round that carries no coverage at all is not a round that reported a
// complete window -- it is a round nobody measured. Counting it as complete
// would reset the run every time an object went through a path that does not
// summarise windows, and the count would never reach the threshold.
//
// It resets anyway, deliberately: a run has to be consecutive to mean
// "consecutively short", and a gap in the evidence is not consecutive. The
// test pins that choice so a later reading of it is a decision, not a drift.
func TestARoundWithoutCoverageEndsTheShortRun(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds+4; round++ {
		tracker.Observe(context.Background(), coverageCompletion("qg-short", 3, 1, 2, 14))
	}
	plain := completion("qg-short", "COMPLETED_WITH_UNAVAILABLE", "8930")
	plain.ProgressCompletionCause = "LEVEL_OUTCOME_UNKNOWN"
	tracker.Observe(context.Background(), plain)
	// A degraded round with no reason at all is not an undecidable one, so the
	// object is back in the anomaly column -- which is itself the run-level
	// rule working: one round that was more than undecidable settles the run.
	listed := tracker.Anomalies()
	if len(listed) != 1 {
		t.Fatalf("anomalies = %+v, want the object still listed", listed)
	}
	if listed[0].Coverage != nil {
		t.Fatalf("coverage = %+v, want none carried from an earlier round: a shortfall left over "+
			"from a round that is no longer on display reads as describing the one that is",
			*listed[0].Coverage)
	}
}

// Persistent is the rule the verdict and the page both read. Each branch is
// pinned, including the two that are false for different reasons: a table of
// only true cases passes against a rule that returns true always.
func TestPersistentSeparatesAFillingWindowFromOneThatNeverFills(t *testing.T) {
	cases := []struct {
		name     string
		coverage *HistoryCoverage
		want     bool
	}{
		{"nil", nil, false},
		{"nothing short", &HistoryCoverage{Levels: 3}, false},
		{"short for fewer rounds than it needs points",
			&HistoryCoverage{Levels: 3, Short: 1, WorstValid: 8, WorstRequired: 9, ShortRounds: 2}, false},
		{"short for exactly as many rounds as it needs points",
			&HistoryCoverage{Levels: 3, Short: 1, WorstValid: 8, WorstRequired: 9, ShortRounds: 9}, false},
		{"short for one round longer than it needs points",
			&HistoryCoverage{Levels: 3, Short: 1, WorstValid: 8, WorstRequired: 9, ShortRounds: 10}, true},
		{"short by most of a long window for a long time",
			&HistoryCoverage{Levels: 3, Short: 2, WorstValid: 2, WorstRequired: 14, ShortRounds: 40}, true},
		{"short with no requirement recorded",
			&HistoryCoverage{Levels: 3, Short: 1, ShortRounds: 40}, false},
	}
	for _, subject := range cases {
		if got := subject.coverage.Persistent(); got != subject.want {
			t.Errorf("%s: Persistent() = %v, want %v", subject.name, got, subject.want)
		}
	}
}

// A window that cannot decide recovery is not a fault. The data that is there
// is read correctly, an anomalous result is still settled before the
// completeness gate and still fires; what has no basis is the decision that
// something went back to normal. Counting that as an anomaly described a
// normal condition as a standing defect.
func TestAWindowThatCannotDecideRecoveryIsNotAnAnomaly(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), coverageCompletion("qg-warming", 3, 1, 2, 14))
	}
	if got := tracker.Anomalies(); len(got) != 0 {
		t.Fatalf("anomalies = %+v, want none: a window with nothing to decide on is not a fault", got)
	}
	undecidable := tracker.Undecidable()
	if len(undecidable) != 1 || undecidable[0].QueryGroup != "qg-warming" {
		t.Fatalf("undecidable = %+v, want the one object", undecidable)
	}
	// It has to be in exactly one column, or the healthy count -- which is a
	// subtraction over all of them -- silently loses an object per round.
	if got := tracker.Demoted(); len(got) != 0 {
		t.Fatalf("demoted = %+v, want none", got)
	}
}

// The run decides the column, not the latest round. Judged off the last round,
// an object that failed for an hour and then reported one warming round would
// move out of the anomaly column and be described as normal, taking the hour
// with it -- and it would look entirely reasonable on the page.
func TestOneRoundOfWarmingDoesNotExcuseARunThatWasAlreadyFailing(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	failing := completion("qg-mixed", "COMPLETED_WITH_UNAVAILABLE", "8930")
	failing.ProgressCompletionCause = "LEVEL_OUTCOME_UNKNOWN"
	failing.ProgressCompletionReason = "STATE_FACT_CONTRADICTS_OUTCOME"
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), failing)
	}
	if got := tracker.Anomalies(); len(got) != 1 {
		t.Fatalf("anomalies = %+v, want the failing object listed before the warming round", got)
	}
	tracker.Observe(context.Background(), coverageCompletion("qg-mixed", 3, 1, 2, 14))
	if got := tracker.Undecidable(); len(got) != 0 {
		t.Fatalf("undecidable = %+v, want none: one warming round does not make an hour of "+
			"failures a normal condition", got)
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 {
		t.Fatalf("anomalies = %+v, want the object still counted against the deployment", anomalies)
	}
	// And a healthy round does clear it, so the flag is not a one-way trap that
	// keeps an object out of the undecidable column for the life of the process.
	tracker.Observe(context.Background(), completion("qg-mixed", HealthyCompletions[0], "8930"))
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), coverageCompletion("qg-mixed", 3, 1, 2, 14))
	}
	if got := tracker.Undecidable(); len(got) != 1 {
		t.Fatalf("undecidable = %+v, want the object once a healthy round started a new run", got)
	}
	if got := tracker.Anomalies(); len(got) != 0 {
		t.Fatalf("anomalies = %+v, want none after the new run", got)
	}
}

// Blocked rounds never reached the window at all, so they are not a window
// declining to decide. Without this they would fall into the undecidable
// column by default -- their cause reason is empty, which is not
// HISTORY_WARMING, but the flag that keeps them out has to be set by something
// and the blocked path does not go through the completion branch.
func TestABlockedRunIsNotMistakenForAWindowWithNothingToDecide(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultBlockedRounds; round++ {
		tracker.Observe(context.Background(), runOutcome("qg-blocked", "source_blocked"))
	}
	if got := tracker.Undecidable(); len(got) != 0 {
		t.Fatalf("undecidable = %+v, want none: a round that never ran is not a window with "+
			"nothing to decide", got)
	}
	if got := tracker.Anomalies(); len(got) != 1 {
		t.Fatalf("anomalies = %+v, want the blocked object", got)
	}
	// The sequence that actually needs the blocked branch to mark the run.
	// While the kind is still BLOCKED_RUN the column is decided by the kind
	// alone, so the mark looks redundant -- it stops looking redundant the
	// moment a warming round follows, because from then on the kind says
	// DEGRADED_RUN and the reason says warming, and nothing else remembers
	// that this run began with rounds that never ran at all.
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), coverageCompletion("qg-blocked", 3, 1, 2, 14))
	}
	if got := tracker.Undecidable(); len(got) != 0 {
		t.Fatalf("undecidable = %+v, want none: this run began with rounds that never reached "+
			"the window, and a later warming round does not turn it into a normal condition", got)
	}
	if got := tracker.Anomalies(); len(got) != 1 {
		t.Fatalf("anomalies = %+v, want the object still counted against the deployment", got)
	}
}

// A window holding nothing at all is not the normal condition. It is where a
// series whose data stopped ends up -- FULL, then GAPPED while the last real
// point is in the window, then WARMING for ever once it slides out -- and both
// ends of that report HISTORY_WARMING. Filed with the churning strategies it
// would read as "working as designed", on a metric that had died.
func TestAWindowHoldingNothingIsNotTheNormalCondition(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	// Long enough to pass the threshold: more rounds than the window is wide.
	for round := 0; round < 20; round++ {
		tracker.Observe(context.Background(), coverageCompletion("qg-starved", 3, 1, 0, 14))
	}
	if got := tracker.Undecidable(); len(got) != 0 {
		t.Fatalf("undecidable = %+v, want none: a window with no points at all is not a strategy "+
			"whose series churn, it is one producing nothing usable", got)
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].Coverage == nil {
		t.Fatalf("anomalies = %+v, want the starved object listed with its coverage", anomalies)
	}
	coverage := anomalies[0].Coverage
	if !coverage.Starved() {
		t.Errorf("coverage = %+v, want Starved()", *coverage)
	}
	if coverage.Persistent() {
		t.Errorf("coverage = %+v, want not Persistent(): the two are different situations and an "+
			"object must not be described as both", *coverage)
	}
}

// The two run lengths are counted apart. A window can be short for an hour and
// empty only for the last couple of rounds, and those last two are the ones
// that say the data stopped rather than that the series churns.
func TestEmptyRoundsAreCountedApartFromShortRounds(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < 20; round++ {
		tracker.Observe(context.Background(), coverageCompletion("qg-mixed", 3, 1, 2, 14))
	}
	listed := tracker.Undecidable()
	if len(listed) != 1 || listed[0].Coverage == nil {
		t.Fatalf("undecidable = %+v, want the churning object", listed)
	}
	if got := listed[0].Coverage.EmptyRounds; got != 0 {
		t.Fatalf("empty rounds = %d on windows that held points, want 0", got)
	}
	// Now the data stops. Short rounds keep climbing; empty rounds start.
	tracker.Observe(context.Background(), coverageCompletion("qg-mixed", 3, 1, 0, 14))
	tracker.Observe(context.Background(), coverageCompletion("qg-mixed", 3, 1, 0, 14))
	found := append(tracker.Undecidable(), tracker.Anomalies()...)
	if len(found) != 1 || found[0].Coverage == nil {
		t.Fatalf("object listed %d times, want exactly one column", len(found))
	}
	coverage := found[0].Coverage
	if coverage.ShortRounds != 22 {
		t.Errorf("short rounds = %d, want 22: the short run did not break", coverage.ShortRounds)
	}
	if coverage.EmptyRounds != 2 {
		t.Errorf("empty rounds = %d, want 2: only the rounds since the data stopped", coverage.EmptyRounds)
	}
	// Two empty rounds against a fourteen-point window is not yet enough to
	// call it starved -- the threshold is the window's own requirement, the
	// same rule the churning case uses.
	if coverage.Starved() {
		t.Errorf("coverage = %+v, want not yet Starved() after only 2 empty rounds", *coverage)
	}

	// And a round that holds points again ends the empty run. Without this one
	// bad round would condemn an object for the life of the process, and the
	// count would only ever rise -- which is the same defect as a short run
	// that never ends, in the column that says someone has to look.
	tracker.Observe(context.Background(), coverageCompletion("qg-mixed", 3, 1, 2, 14))
	found = append(tracker.Undecidable(), tracker.Anomalies()...)
	if len(found) != 1 || found[0].Coverage == nil {
		t.Fatalf("object listed %d times, want exactly one column", len(found))
	}
	if got := found[0].Coverage.EmptyRounds; got != 0 {
		t.Fatalf("empty rounds = %d after a round that held points, want the run ended", got)
	}
	if got := found[0].Coverage.ShortRounds; got != 23 {
		t.Fatalf("short rounds = %d, want 23: the short run is still unbroken", got)
	}
}

// A round interrupted by a change already being made on purpose is not a
// fault. The strategy was edited, deactivated or reassigned mid-round; results
// computed under the old configuration must not land, so the round is refused
// and the next one runs under the new configuration. Both halves are correct
// and nobody acts on it.
func TestARoundInterruptedByAChangeAlreadyMadeIsNotAnAnomaly(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	drift := completion("qg-drift", "COMPLETED_WITH_UNAVAILABLE", "8930")
	drift.ProgressCompletionCause = "CONFIG_DRIFT"
	drift.ProgressCompletionReason = "CONFIG_DRIFT"
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), drift)
	}
	if got := tracker.Anomalies(); len(got) != 0 {
		t.Fatalf("anomalies = %+v, want none: the change was already made deliberately", got)
	}
	listed := tracker.ByDesign()
	if len(listed) != 1 || listed[0].QueryGroup != "qg-drift" {
		t.Fatalf("by-design = %+v, want the one object", listed)
	}
	// Exactly one column, or the healthy count -- a subtraction over all of
	// them -- loses an object per round.
	if got := tracker.Undecidable(); len(got) != 0 {
		t.Fatalf("undecidable = %+v, want none: a config edit is not a window with nothing to decide", got)
	}
	if got := tracker.Demoted(); len(got) != 0 {
		t.Fatalf("demoted = %+v, want none", got)
	}
}

// The run-level flag has to be written against every no-action reason, not
// against whichever column is tested first. Written against one, a run
// interrupted by a config edit gets marked as having gone wrong and is filed
// as a fault -- and equally a warming run followed by an edit would be.
func TestOneNoActionReasonDoesNotMarkTheRunForTheOtherColumn(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	drift := completion("qg-both", "COMPLETED_WITH_UNAVAILABLE", "8930")
	drift.ProgressCompletionCause = "CONFIG_DRIFT"
	drift.ProgressCompletionReason = "CONFIG_DRIFT"

	// Warming rounds first, then the edit lands.
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), coverageCompletion("qg-both", 3, 1, 2, 14))
	}
	tracker.Observe(context.Background(), drift)
	if got := tracker.Anomalies(); len(got) != 0 {
		t.Fatalf("anomalies = %+v, want none: neither round was a fault", got)
	}
	if got := tracker.ByDesign(); len(got) != 1 {
		t.Fatalf("by-design = %+v, want the object, filed by its latest reason", got)
	}

	// And a genuine failure in the run still wins, in either order.
	failing := completion("qg-mixed", "COMPLETED_WITH_UNAVAILABLE", "8930")
	failing.ProgressCompletionCause = "LEVEL_OUTCOME_UNKNOWN"
	failing.ProgressCompletionReason = "STATE_FACT_CONTRADICTS_OUTCOME"
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), failing)
	}
	driftMixed := drift
	driftMixed.Trace.QueryGroupKey = "qg-mixed"
	tracker.Observe(context.Background(), driftMixed)
	if got := tracker.ByDesign(); len(got) != 1 {
		t.Fatalf("by-design = %+v, want only qg-both: an edit does not excuse a run that was "+
			"already failing", got)
	}
	anomalies := tracker.Anomalies()
	if len(anomalies) != 1 || anomalies[0].QueryGroup != "qg-mixed" {
		t.Fatalf("anomalies = %+v, want the failing object still counted", anomalies)
	}
}

// The list is what the column means, so it is pinned. A column defined as
// "the rest" becomes the next place things go to stop being looked at, which
// is the failure the whole split exists to end.
func TestTheByDesignColumnHoldsOnlyDeclaredReasons(t *testing.T) {
	if len(byDesignReasons) == 0 {
		t.Fatal("no by-design reasons declared; the column would be empty and the check vacuous")
	}
	for reason := range byDesignReasons {
		if !externalReasons[reason] {
			t.Errorf("%q is by-design but not external: an object nobody acts on must not be "+
				"counted against this deployment if it ever falls back to the anomaly column", reason)
		}
		if undecidableReason(reason) {
			t.Errorf("%q is on two column lists; an object would be claimed by whichever is tested first", reason)
		}
	}
	// Reasons that are retryable are emphatically not on it. "Retrying may
	// help" is a different statement from "nothing is wrong", and the
	// contract's RETRYABLE class holds REDIS_UNAVAILABLE and
	// PROVIDER_UNAVAILABLE -- real failures that would vanish from the list
	// someone works through.
	for _, reason := range []string{"REDIS_UNAVAILABLE", "PROVIDER_UNAVAILABLE", "RESOURCE_HARD_STOP",
		"QUERY_NOT_READY", "KAFKA_UNAVAILABLE"} {
		if byDesignReasons[reason] {
			t.Errorf("%q is filed as needing no action; retryable is not the same as harmless", reason)
		}
	}
}

// A strategy outside its own active window is the configuration doing what it
// was written to do, and it is a standing state rather than a passing one: a
// strategy that runs only in business hours is in it sixteen hours a day.
//
// The suppressed round completes as COMPLETED_WITH_UNAVAILABLE, the same kind
// a real failure gets, so nothing but the reason separates them. Left in the
// anomaly column it would put a working strategy on the list somebody works
// through, every night, for ever.
func TestAStrategyOutsideItsActiveWindowIsNotOnTheToDoList(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	inactive := completion("qg-offhours", "COMPLETED_WITH_UNAVAILABLE", "8930")
	inactive.ProgressCompletionCause = "LEVEL_OUTCOME_UNKNOWN"
	inactive.ProgressCompletionReason = "EFFECTIVE_TIME_INACTIVE"
	// Far more rounds than the listing threshold: this is a state an object
	// sits in for hours, not one it passes through.
	for round := 0; round < DefaultDegradedRounds*20; round++ {
		tracker.Observe(context.Background(), inactive)
	}
	if got := tracker.Anomalies(); len(got) != 0 {
		t.Fatalf("anomalies = %+v, want none: the strategy is configured not to run now", got)
	}
	if got := tracker.ByDesign(); len(got) != 1 {
		t.Fatalf("by-design = %+v, want the one object", got)
	}
}

// The schedule failing to resolve is a fault and must not ride along with it.
// Nobody can say whether the strategy should be running, which is the opposite
// of knowing it should not.
func TestAnUnresolvableScheduleIsStillAFault(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	unknown := completion("qg-badtz", "COMPLETED_WITH_UNAVAILABLE", "8930")
	unknown.ProgressCompletionCause = "LEVEL_OUTCOME_UNKNOWN"
	unknown.ProgressCompletionReason = "EFFECTIVE_TIME_UNKNOWN"
	for round := 0; round < DefaultDegradedRounds; round++ {
		tracker.Observe(context.Background(), unknown)
	}
	if got := tracker.ByDesign(); len(got) != 0 {
		t.Fatalf("by-design = %+v, want none: an unresolved schedule is not a schedule saying no", got)
	}
	if got := tracker.Anomalies(); len(got) != 1 {
		t.Fatalf("anomalies = %+v, want the object still on the list", got)
	}
}
