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

// absenceDecided is the observation the worker emits for one Plan's no-data
// round that judged, as worker.observeNoDataAbsence builds it: the Plan on the
// trace, the object on the context, the round's own counts on the facts.
func absenceDecided(ctx context.Context, tracker *Tracker, strategy string, slot int64, facts observability.NoDataAbsenceFacts) {
	facts.Outcome = "EVALUATED"
	tracker.Observe(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
		Direction: observability.DirectionInternal, Result: observability.ResultSuccess,
		Trace:         observability.TraceFields{StrategyID: strategy, BusinessID: "2", EvaluationTime: slot},
		NoDataAbsence: &facts,
	})
}

// objectRow is the row the object page would show for one tracked object,
// listed or not.
func objectRow(t *testing.T, tracker *Tracker, queryGroup string) Anomaly {
	t.Helper()
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	state := tracker.groups[queryGroup]
	if state == nil {
		t.Fatalf("object %s is not tracked", queryGroup)
	}
	return tracker.rowOf(queryGroup, state)
}

// The row carries each Plan's last deciding word, whole, with the horizon's
// source read against the platform's; the next deciding round replaces it
// rather than adding to it, and a Plan the object also has is its own entry.
func TestTheRowCarriesEachPlansLastDecidingWordAndWhereItsHorizonCameFrom(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	platform := int64(3600)
	tracker.SetPlatformNoDataHorizon(func() int64 { return platform })
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-track"})

	// Three Plans on one object, one per horizon source; the counts differ
	// per Plan so a row that mixed them up would show.
	absenceDecided(ctx, tracker, "s-platform", 600, observability.NoDataAbsenceFacts{
		HorizonSeconds: 3600, RosterSource: "TARGET_STATIC", Expected: 10, Present: 6, Absent: 2, Expired: 1, Suppressed: 1,
		AbsentAges: observability.NoDataAbsentAges{UnderHour: 1, DayOrMore: 1}})
	absenceDecided(ctx, tracker, "s-strategy", 600, observability.NoDataAbsenceFacts{
		HorizonSeconds: 900, RosterSource: "HISTORY", Expected: 5, Present: 5, Absent: 0, Expired: 0, Suppressed: 0})
	absenceDecided(ctx, tracker, "s-none", 600, observability.NoDataAbsenceFacts{
		HorizonSeconds: 0, RosterSource: "HISTORY", Expected: 4, Present: 1, Absent: 3, Expired: 0, Suppressed: 0,
		AbsentAges: observability.NoDataAbsentAges{ThisRound: 2, UnderDay: 1}})

	row := objectRow(t, tracker, "qg-track")
	if len(row.NoDataTracking) != 3 {
		t.Fatalf("row carries %d tracking entries, want one per Plan: %+v", len(row.NoDataTracking), row.NoDataTracking)
	}
	bySource := map[string]NoDataTracking{}
	for _, tracking := range row.NoDataTracking {
		bySource[tracking.HorizonSource] = tracking
	}
	if got := bySource[NoDataHorizonPlatform]; got.Plan.StrategyID != "s-platform" || got.HorizonSeconds != 3600 ||
		got.Expected != 10 || got.Present != 6 || got.Absent != 2 || got.ExpiredThisRound != 1 || got.Suppressed != 1 ||
		got.RosterSource != "TARGET_STATIC" || got.EvaluationTime != 600 || !got.DecidedAt.Equal(now) ||
		got.AbsentAges != (NoDataAbsentAges{UnderHour: 1, DayOrMore: 1}) {
		t.Fatalf("platform-horizon entry = %+v, want s-platform's counts whole, ages included, decided at slot 600 seen now", got)
	}
	if got := bySource[NoDataHorizonStrategy]; got.Plan.StrategyID != "s-strategy" || got.HorizonSeconds != 900 {
		t.Fatalf("strategy-horizon entry = %+v, want s-strategy with its own 900", got)
	}
	if got := bySource[NoDataHorizonNone]; got.Plan.StrategyID != "s-none" || got.HorizonSeconds != 0 || got.Absent != 3 {
		t.Fatalf("no-horizon entry = %+v, want s-none with 3 tracked", got)
	}
	// Smallest strategy first, so the page reads the same order every time.
	if row.NoDataTracking[0].Plan.StrategyID != "s-none" || row.NoDataTracking[2].Plan.StrategyID != "s-strategy" {
		t.Fatalf("entries are ordered %s, %s, %s; want by strategy id",
			row.NoDataTracking[0].Plan.StrategyID, row.NoDataTracking[1].Plan.StrategyID, row.NoDataTracking[2].Plan.StrategyID)
	}

	// The next deciding round replaces the word: the counts are that round's,
	// not a running total, so 1 expired then 0 expired reads 0, not 1.
	at.at = at.at.Add(time.Minute)
	absenceDecided(ctx, tracker, "s-platform", 660, observability.NoDataAbsenceFacts{
		HorizonSeconds: 3600, RosterSource: "TARGET_STATIC", Expected: 10, Present: 6, Absent: 1, Expired: 0, Suppressed: 2,
		AbsentAges: observability.NoDataAbsentAges{UnderHour: 1}})
	row = objectRow(t, tracker, "qg-track")
	if len(row.NoDataTracking) != 3 {
		t.Fatalf("a second round of one Plan made %d entries, want still 3", len(row.NoDataTracking))
	}
	for _, tracking := range row.NoDataTracking {
		if tracking.Plan.StrategyID != "s-platform" {
			continue
		}
		if tracking.ExpiredThisRound != 0 || tracking.Suppressed != 2 || tracking.Absent != 1 || tracking.EvaluationTime != 660 ||
			!tracking.DecidedAt.Equal(now.Add(time.Minute)) {
			t.Fatalf("after the second round the entry = %+v, want the second round's counts and slot", tracking)
		}
	}

	// The fleet's line sums the last word of every Plan, over every tracked
	// object -- this one is healthy and on no column, and it still counts.
	summary := tracker.NoDataTrackingSummary()
	if summary == nil {
		t.Fatal("summary is nil with three Plans decided")
	}
	want := NoDataTrackingSummary{
		Plans: 3, HorizonNone: 1, HorizonPlatform: 1, HorizonStrategy: 1,
		Expected: 19, Absent: 4, ExpiredThisRound: 0, Suppressed: 2, LastDecidedAt: now.Add(time.Minute),
		// The latest word of each Plan: s-platform's second round replaced
		// its first, so the day-or-more absence it had is gone from the sum.
		AbsentAges: NoDataAbsentAges{ThisRound: 2, UnderHour: 1, UnderDay: 1},
		// Every line here carried no frozen word, so every source was inferred.
		HorizonSourceInferred: ptr(3),
	}
	if summary.HorizonSourceInferred == nil || *summary.HorizonSourceInferred != 3 {
		t.Fatalf("summary counts %v inferred, want 3", summary.HorizonSourceInferred)
	}
	summary.HorizonSourceInferred, want.HorizonSourceInferred = nil, nil
	if *summary != want {
		t.Fatalf("summary = %+v, want %+v", *summary, want)
	}
	if listed := tracker.Anomalies(); len(listed) != 0 {
		t.Fatalf("the object is listed as %+v; the tracking word alone must not list it", listed)
	}
}

// The source is what compilation froze beside the horizon when the line
// carries it; the comparison against the platform's horizon is only the
// fallback for a line that does not (a Plan compiled before the source was
// frozen, or an older Worker). Under the fallback a strategy horizon reads
// as the strategy's own the moment it differs from the platform's, and a
// tracker not told the platform's horizon says unknown rather than
// guessing: the same 3600 is platform under one reading and unknown under
// the other, and none is none whatever the platform says. The frozen word
// wins where the fallback would read otherwise: a strategy that stated
// exactly the platform's value is STRATEGY, and a Plan that inherited the
// platform's is PLATFORM even where the tracker was not told the platform's.
func TestTheHorizonSourceIsReadAgainstThePlatformsAndSaysUnknownWhenNotTold(t *testing.T) {
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-src"})
	for _, testCase := range []struct {
		name     string
		platform *int64
		horizon  int64
		frozen   string
		want     string
	}{
		{name: "equal to the platform's", platform: ptr(int64(3600)), horizon: 3600, want: NoDataHorizonPlatform},
		{name: "different from the platform's", platform: ptr(int64(3600)), horizon: 600, want: NoDataHorizonStrategy},
		{name: "none, platform set", platform: ptr(int64(3600)), horizon: 0, want: NoDataHorizonNone},
		{name: "none, platform none", platform: ptr(int64(0)), horizon: 0, want: NoDataHorizonNone},
		{name: "set while the platform has none", platform: ptr(int64(0)), horizon: 600, want: NoDataHorizonStrategy},
		{name: "not told the platform's", platform: nil, horizon: 3600, want: NoDataHorizonUnknown},
		{name: "not told the platform's, none", platform: nil, horizon: 0, want: NoDataHorizonUnknown},
		{name: "frozen STRATEGY at exactly the platform's value", platform: ptr(int64(3600)), horizon: 3600, frozen: NoDataHorizonStrategy, want: NoDataHorizonStrategy},
		{name: "frozen PLATFORM while not told the platform's", platform: nil, horizon: 3600, frozen: NoDataHorizonPlatform, want: NoDataHorizonPlatform},
		{name: "frozen PLATFORM after the platform's value moved", platform: ptr(int64(7200)), horizon: 3600, frozen: NoDataHorizonPlatform, want: NoDataHorizonPlatform},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			at := &clock{at: now}
			tracker := newTracker(t, at)
			if testCase.platform != nil {
				platform := *testCase.platform
				tracker.SetPlatformNoDataHorizon(func() int64 { return platform })
			}
			absenceDecided(ctx, tracker, "s-1", 600, observability.NoDataAbsenceFacts{HorizonSeconds: testCase.horizon, HorizonSource: testCase.frozen, Expected: 1})
			row := objectRow(t, tracker, "qg-src")
			if len(row.NoDataTracking) != 1 || row.NoDataTracking[0].HorizonSource != testCase.want {
				t.Fatalf("tracking = %+v, want source %s", row.NoDataTracking, testCase.want)
			}
			// The row says which way it was read, and the fleet counts the
			// inferred ones: the frozen word makes both say frozen, anything
			// else is the comparison and is counted as such.
			wantBasis, wantInferred := NoDataHorizonSourceInferred, 1
			if testCase.frozen != "" {
				wantBasis, wantInferred = NoDataHorizonSourceFrozen, 0
			}
			if row.NoDataTracking[0].HorizonSourceBasis != wantBasis {
				t.Fatalf("basis = %s, want %s", row.NoDataTracking[0].HorizonSourceBasis, wantBasis)
			}
			if inferred := tracker.NoDataTrackingSummary().HorizonSourceInferred; inferred == nil || *inferred != wantInferred {
				t.Fatalf("summary counts %v inferred, want %d", inferred, wantInferred)
			}
			summary := tracker.NoDataTrackingSummary()
			counted := map[string]int{
				NoDataHorizonNone: summary.HorizonNone, NoDataHorizonPlatform: summary.HorizonPlatform,
				NoDataHorizonStrategy: summary.HorizonStrategy, NoDataHorizonUnknown: summary.HorizonUnknown,
			}
			for source, count := range counted {
				want := 0
				if source == testCase.want {
					want = 1
				}
				if count != want {
					t.Fatalf("summary counts %d under %s, want %d: %+v", count, source, want, *summary)
				}
			}
		})
	}
}

// The platform's horizon is read through the seam every time, not captured:
// when the deployment's number changes, a Plan still carrying the old one
// reads as the strategy's own from the next deciding round -- which is the
// propagation delay showing, and is the honest reading of it.
func TestThePlatformHorizonIsReadThroughTheSeamNotCaptured(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	platform := int64(3600)
	tracker.SetPlatformNoDataHorizon(func() int64 { return platform })
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-seam"})
	absenceDecided(ctx, tracker, "s-1", 600, observability.NoDataAbsenceFacts{HorizonSeconds: 3600, Expected: 1})
	if got := objectRow(t, tracker, "qg-seam").NoDataTracking[0].HorizonSource; got != NoDataHorizonPlatform {
		t.Fatalf("source = %s before the change, want platform", got)
	}
	platform = 7200
	absenceDecided(ctx, tracker, "s-1", 660, observability.NoDataAbsenceFacts{HorizonSeconds: 3600, Expected: 1})
	if got := objectRow(t, tracker, "qg-seam").NoDataTracking[0].HorizonSource; got != NoDataHorizonStrategy {
		t.Fatalf("source = %s after the platform moved to 7200 and the Plan kept 3600, want strategy", got)
	}
	absenceDecided(ctx, tracker, "s-1", 720, observability.NoDataAbsenceFacts{HorizonSeconds: 7200, Expected: 1})
	if got := objectRow(t, tracker, "qg-seam").NoDataTracking[0].HorizonSource; got != NoDataHorizonPlatform {
		t.Fatalf("source = %s once the Plan was recompiled to 7200, want platform", got)
	}
}

// A summary merged over replicas sums each replica's account and keeps the
// latest deciding round; a replica with no account adds nothing and does not
// make an empty one appear.
func TestTheFleetSumsTheReplicasNoDataTrackingAccounts(t *testing.T) {
	all := []string{"pod-a", "pod-b", "pod-c"}
	snapshots := []Snapshot{
		{Replica: "pod-a", TakenAt: now, NoDataTracking: &NoDataTrackingSummary{Plans: 2, HorizonPlatform: 2,
			Expected: 8, Absent: 3, Suppressed: 1, LastDecidedAt: now, HorizonSourceInferred: ptr(1)}},
		// A replica from before the count: its summary carries none, and every
		// one of its Plans was read by inference, because its build knew no
		// frozen word. Read as zero it would be the Plans missing from exactly
		// the number meant to say how many are left.
		{Replica: "pod-b", TakenAt: now, NoDataTracking: &NoDataTrackingSummary{Plans: 1, HorizonNone: 1,
			Expected: 4, Absent: 4, LastDecidedAt: now.Add(time.Minute)}},
		{Replica: "pod-c", TakenAt: now},
	}
	view := Aggregate(Expectation{Known: true}, snapshots, all, now, freshness)
	want := NoDataTrackingSummary{Plans: 3, HorizonPlatform: 2, HorizonNone: 1, Expected: 12, Absent: 7, Suppressed: 1,
		LastDecidedAt: now.Add(time.Minute)}
	if view.NoDataTracking == nil || view.NoDataTracking.HorizonSourceInferred == nil || *view.NoDataTracking.HorizonSourceInferred != 2 {
		t.Fatalf("merged inferred = %v, want 2: pod-a's one and all of pod-b's", view.NoDataTracking)
	}
	view.NoDataTracking.HorizonSourceInferred = nil
	if *view.NoDataTracking != want {
		t.Fatalf("merged = %+v, want %+v", view.NoDataTracking, want)
	}
	// Replicas with no account: the fleet has none either, rather than an
	// empty one that would render as a horizon reaching nothing.
	silent := Aggregate(Expectation{Known: true}, []Snapshot{{Replica: "pod-c", TakenAt: now}}, []string{"pod-c"}, now, freshness)
	if silent.NoDataTracking != nil {
		t.Fatalf("a fleet whose replicas report no account has %+v, want none", *silent.NoDataTracking)
	}
}

func ptr[T any](value T) *T { return &value }

// The verdict route carries the fleet's line, so the first screen can show
// it; without a replica reporting one it is absent rather than empty.
func TestHealthResponseCarriesTheNoDataTrackingLine(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].NoDataTracking = &NoDataTrackingSummary{Plans: 2, HorizonPlatform: 2, Expected: 8, Absent: 3, Suppressed: 1,
		ExpiredThisRound: 1, LastDecidedAt: now, HorizonSourceInferred: ptr(1)}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 2, Known: true}, []string{"pod-a", "pod-b"})
	body := requestJSON(t, handler, "/api/health")
	tracking, ok := body["no_data_tracking"].(map[string]any)
	if !ok {
		t.Fatalf("health response carried no no_data_tracking, so the first screen's line renders empty: %v", body)
	}
	for field, want := range map[string]float64{
		"plans": 2, "horizon_platform": 2, "horizon_none": 0, "horizon_strategy": 0,
		"expected": 8, "absent": 3, "expired_this_round": 1, "suppressed": 1,
	} {
		if got, _ := tracking[field].(float64); got != want {
			t.Fatalf("no_data_tracking.%s = %v, want %v: %v", field, tracking[field], want, tracking)
		}
	}
	if tracking["last_decided_at"] == nil {
		t.Fatalf("no_data_tracking carries no last_decided_at: %v", tracking)
	}
	// The wire says how many sources are still inferred, so nobody reads the
	// partition as wholly frozen while old Plans remain.
	if got, _ := tracking["horizon_source_inferred"].(float64); got != 1 {
		t.Fatalf("no_data_tracking.horizon_source_inferred = %v, want 1", tracking["horizon_source_inferred"])
	}
	silent := handlerWith(t, healthySnapshots(), Expectation{QueryGroups: 2, Known: true}, []string{"pod-a", "pod-b"})
	if body := requestJSON(t, silent, "/api/health"); body["no_data_tracking"] != nil {
		t.Fatalf("with no replica reporting one, the response carries %v; want the key absent", body["no_data_tracking"])
	}
}

// The row's entries are in one order every time, smallest strategy first:
// twelve Plans reported largest first come out smallest first.
func TestTheRowsTrackingEntriesAreOrderedByStrategy(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	tracker.SetPlatformNoDataHorizon(func() int64 { return 3600 })
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-order"})
	want := []string{}
	for index := 12; index >= 1; index-- {
		strategy := "s-" + string(rune('a'+index-1))
		want = append([]string{strategy}, want...)
		absenceDecided(ctx, tracker, strategy, 600, observability.NoDataAbsenceFacts{HorizonSeconds: 3600, Expected: 1})
	}
	row := objectRow(t, tracker, "qg-order")
	got := []string{}
	for _, tracking := range row.NoDataTracking {
		got = append(got, tracking.Plan.StrategyID)
	}
	if len(got) != len(want) {
		t.Fatalf("row carries %d entries, want %d", len(got), len(want))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("entries ordered %v, want %v", got, want)
		}
	}
}

// The row carries the wire format each Plan's events go out as, from the
// Plan's own evaluation line, smallest strategy first; a later line for the
// same Plan replaces the word. This is the one place the word is readable
// without the object catalog.
func TestTheRowCarriesEachPlansWireFormatFromItsEvaluationLine(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-wire"})
	evaluated := func(strategy, format string) {
		tracker.Observe(ctx, observability.Observation{
			Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
			Result: observability.ResultSuccess, Direction: observability.DirectionInternal,
			Trace:            observability.TraceFields{StrategyID: strategy, BusinessID: "2", EvaluationTime: 600},
			OutputWireFormat: format,
		})
	}
	evaluated("s-2", "standard_raw_event")
	evaluated("s-1", "python_compatible")
	// A line that names no format -- a failed evaluation -- changes nothing.
	tracker.Observe(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		Trace: observability.TraceFields{StrategyID: "s-1", BusinessID: "2", EvaluationTime: 600}, Err: context.Canceled,
	})
	row := objectRow(t, tracker, "qg-wire")
	if len(row.WireFormats) != 2 || row.WireFormats[0].Plan.StrategyID != "s-1" || row.WireFormats[0].WireFormat != "python_compatible" ||
		row.WireFormats[1].Plan.StrategyID != "s-2" || row.WireFormats[1].WireFormat != "standard_raw_event" ||
		!row.WireFormats[0].LastSeenAt.Equal(now) {
		t.Fatalf("wire formats = %+v, want s-1 python_compatible then s-2 standard_raw_event, seen now", row.WireFormats)
	}
	at.at = at.at.Add(time.Minute)
	evaluated("s-2", "python_compatible")
	row = objectRow(t, tracker, "qg-wire")
	if len(row.WireFormats) != 2 || row.WireFormats[1].WireFormat != "python_compatible" || !row.WireFormats[1].LastSeenAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("after a recompiled Plan's line the entry = %+v, want the new word and time, still one entry", row.WireFormats)
	}
	if listed := tracker.Anomalies(); len(listed) != 0 {
		t.Fatalf("the object is listed as %+v; an evaluation line alone must not list it", listed)
	}
}
