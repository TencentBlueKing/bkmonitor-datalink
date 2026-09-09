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
