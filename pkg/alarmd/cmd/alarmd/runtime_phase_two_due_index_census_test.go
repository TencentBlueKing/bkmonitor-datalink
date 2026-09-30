// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package main

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The census counts every entry by where it is in its cycle, with the grace
// of one period: past due within a period is late, past it is overdue. Objects
// with no entry are the ones nothing has been evaluated for since takeover.
func TestDueIndexCensusCountsEveryEntryByItsCyclePosition(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(10_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{
			"waiting": {}, "cooling": {}, "late": {}, "late-edge": {}, "overdue": {}, "no-period": {},
		})
	index := dispatcher.dueIndex
	record := func(queryGroup execution.QueryGroupIdentity, dueAt, interval int64, cooling bool) {
		index.Record(queryGroup, dispatcher.bundle.runners[queryGroup], index.versionEpoch,
			scheduler.RunnerDueBound{NotDueUntilUnix: dueAt, IntervalSeconds: interval, QueryCooldown: cooling},
			time.Unix(dueAt-1, 0))
	}
	record("waiting", 10_040, 60, false)
	record("cooling", 10_300, 60, true)
	record("late", 9_990, 60, false)      // 10 s past due, period 60: late
	record("late-edge", 9_940, 60, false) // exactly one period past due: still late, not overdue
	record("overdue", 9_800, 60, false)   // 200 s past due: a turn missed
	record("no-period", 9_000, 0, false)

	// Seven objects owned, six with entries: one never evaluated.
	census := index.Census(clock.at, 7)
	// The same entries by period: five on the sixty-second period, one of them
	// cooling and one overdue; the entry with no period under 0. Ordered by
	// period so two censuses of one deployment list the same way.
	want := fleet.ScheduleCensus{Waiting: 2, Cooling: 1, Late: 3, Overdue: 1, Never: 1,
		OldestLateSeconds: 1000, MissingPeriod: 1,
		Cohorts: []fleet.ScheduleCohort{{IntervalSeconds: 0, Objects: 1}, {IntervalSeconds: 60, Objects: 5, Cooling: 1, Overdue: 1}}}
	if !reflect.DeepEqual(census, want) {
		t.Errorf("census = %+v, want %+v", census, want)
	}
	// The wake facts for one object are the entry as it stands; an object with
	// no entry is Known false, which is a different answer from a zero bound.
	if wake := index.WakeOf("overdue"); !wake.Known || wake.DueAt.Unix() != 9_800 || wake.IntervalSeconds != 60 {
		t.Errorf("WakeOf(overdue) = %+v", wake)
	}
	if wake := index.WakeOf("cooling"); !wake.Cooling {
		t.Errorf("WakeOf(cooling) = %+v, want Cooling", wake)
	}
	if wake := index.WakeOf("never"); wake.Known {
		t.Errorf("WakeOf(never) = %+v, want Known false", wake)
	}
}

// Every executed round is judged against the bound it fell due by, and the
// two windows add up separately. Only executed rounds count, only rounds with
// a period can be judged, and a round that returns before its bound (a
// publication pulled the bound forward) is on time and not late.
func TestDueIndexCountsCompletionsAgainstTheBoundTheyFellDueBy(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(100_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{"a": {}, "b": {}, "c": {}, "d": {}})
	index := dispatcher.dueIndex
	epoch := index.versionEpoch
	lifecycle := func(name execution.QueryGroupIdentity) *phaseTwoQueryGroupLifecycle {
		return dispatcher.bundle.runners[name]
	}
	// First rounds establish bounds; they are not judged (nothing to judge
	// against).
	for _, name := range []execution.QueryGroupIdentity{"a", "b", "c", "d"} {
		index.Record(name, lifecycle(name), epoch,
			scheduler.RunnerDueBound{NotDueUntilUnix: 100_060, IntervalSeconds: 60, Executed: true},
			time.Unix(100_000, 0))
	}
	if census := index.Census(time.Unix(100_000, 0), 4); census.Completed1h != 0 {
		t.Fatalf("first rounds were judged: %+v", census)
	}
	// a: on time (returned 10 s after falling due, period 60).
	index.Record("a", lifecycle("a"), epoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 100_130, IntervalSeconds: 60, Executed: true}, time.Unix(100_070, 0))
	// b: pushed back by the dispatcher, and still on time (returned 50 s after
	// falling due).
	index.MarkHeldBack("b")
	index.Record("b", lifecycle("b"), epoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 100_170, IntervalSeconds: 60, Executed: true}, time.Unix(100_110, 0))
	// c: pushed back and missed its turn (returned 130 s after falling due).
	index.MarkHeldBack("c")
	index.Record("c", lifecycle("c"), epoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 100_250, IntervalSeconds: 60, Executed: true}, time.Unix(100_190, 0))
	// d: returned without executing. Not a completion.
	index.Record("d", lifecycle("d"), epoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 100_120, IntervalSeconds: 60, Executed: false}, time.Unix(100_100, 0))
	// d again, executed exactly one period after falling due: on time. The
	// deadline is the next turn, and returning as it falls due is meeting it.
	index.Record("d", lifecycle("d"), epoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 100_240, IntervalSeconds: 60, Executed: true}, time.Unix(100_180, 0))
	// Marking an object nothing has been evaluated for marks nothing.
	index.MarkHeldBack("never-seen")

	census := index.Census(time.Unix(100_200, 0), 4)
	if census.Completed1h != 4 || census.OnTime1h != 3 {
		t.Errorf("1h window = %d completed / %d on time, want 4 / 3", census.Completed1h, census.OnTime1h)
	}
	if census.HeldBack1h != 2 || census.HeldBackOnTime1h != 1 {
		t.Errorf("held back = %d, of which on time %d, want 2 and 1", census.HeldBack1h, census.HeldBackOnTime1h)
	}
	// The mark is consumed by the return it was made before: b's next round
	// is not held back unless the dispatcher says so again.
	index.Record("b", lifecycle("b"), epoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 100_260, IntervalSeconds: 60, Executed: true}, time.Unix(100_200, 0))
	if census := index.Census(time.Unix(100_200, 0), 4); census.HeldBack1h != 2 {
		t.Errorf("a mark survived the return it was made before: held back = %d, want 2", census.HeldBack1h)
	}
	if census.Completed6h != 4 || census.OnTime6h != 3 {
		t.Errorf("6h window = %d / %d, want 4 / 3", census.Completed6h, census.OnTime6h)
	}
	// Two hours on, the hour window has forgotten all five returns and the
	// six-hour one has not; seven hours on, both have.
	later := index.Census(time.Unix(100_200+2*3600, 0), 4)
	if later.Completed1h != 0 || later.Completed6h != 5 {
		t.Errorf("after two hours: 1h = %d, 6h = %d, want 0 and 5", later.Completed1h, later.Completed6h)
	}
	if gone := index.Census(time.Unix(100_200+7*3600, 0), 4); gone.Completed6h != 0 {
		t.Errorf("after seven hours the six-hour window still holds %d", gone.Completed6h)
	}
	// A bucket six hours old is recognised by its minute, not its slot: a
	// completion now lands in a fresh bucket, not on top of the stale one.
	index.Record("a", lifecycle("a"), epoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 100_200 + 7*3600 + 60, IntervalSeconds: 60, Executed: true},
		time.Unix(100_200+7*3600, 0))
	if fresh := index.Census(time.Unix(100_200+7*3600, 0), 4); fresh.Completed1h != 1 || fresh.Completed6h != 1 {
		t.Errorf("after the ring wrapped: 1h = %d, 6h = %d, want 1 and 1", fresh.Completed1h, fresh.Completed6h)
	}
}

// The publisher puts the census on the snapshot and the wake facts on every
// listed row, from the same index at the same instant.
func TestFleetPublisherCarriesTheCensusAndWakeFacts(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{"query-group-a": {}, "query-group-b": {}})
	index := dispatcher.dueIndex
	index.Record("query-group-a", dispatcher.bundle.runners["query-group-a"], index.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 19_900, IntervalSeconds: 60}, time.Unix(19_850, 0))

	tracker := fleet.NewTracker(nil, "replica-1", clock.now)
	// Two degraded rounds make an anomaly of each object; b has no entry in
	// the index.
	for _, name := range []string{"query-group-a", "query-group-b"} {
		for i := 0; i < fleet.DefaultDegradedRounds; i++ {
			tracker.Observe(context.Background(), observability.Observation{
				ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE",
				Trace:                  observability.TraceFields{QueryGroupKey: name, StrategyID: "1", BusinessID: "2"},
			})
		}
	}
	// And one whose data stopped, so the no-data list is published and its
	// row carries wake facts like every other: records at one Slot, then
	// empty rounds whose Slots span the data side's hour.
	tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: "FULL_COMPLETED",
		Trace: observability.TraceFields{QueryGroupKey: "query-group-c", StrategyID: "1", BusinessID: "2", EvaluationTime: 20_000 - 3660}})
	for i := 0; i < fleet.DefaultDegradedRounds; i++ {
		tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: "FULL_EMPTY_COMPLETED",
			Trace: observability.TraceFields{QueryGroupKey: "query-group-c", StrategyID: "1", BusinessID: "2", EvaluationTime: int64(20_000 - 120 + 60*i)}})
	}
	publisher := fleetPublisher{
		tracker: tracker, replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity {
			return []execution.QueryGroupIdentity{"query-group-a", "query-group-b", "query-group-c"}
		},
		schedule: index,
	}
	snapshot := publisher.snapshot(context.Background())
	if snapshot.Schedule == nil {
		t.Fatal("the snapshot carried no schedule census")
	}
	if len(snapshot.NoData) != 1 || snapshot.NoData[0].QueryGroup != "query-group-c" || snapshot.NoData[0].Wake == nil {
		t.Errorf("no-data = %+v, want query-group-c with wake facts attached", snapshot.NoData)
	}
	if snapshot.Schedule.Overdue != 1 || snapshot.Schedule.Never != 2 {
		t.Errorf("census = %+v, want one overdue (a, 100 s past a 60 s period) and two never (b, c)", *snapshot.Schedule)
	}
	wakes := map[string]*fleet.WakeFacts{}
	for _, anomaly := range snapshot.Anomalies {
		wakes[anomaly.QueryGroup] = anomaly.Wake
	}
	if wake := wakes["query-group-a"]; wake == nil || !wake.Known || wake.DueAt.Unix() != 19_900 {
		t.Errorf("row a carries wake %+v, want the index's bound", wake)
	}
	if wake := wakes["query-group-b"]; wake == nil || wake.Known {
		t.Errorf("row b carries wake %+v, want Known false: nothing evaluated since takeover", wake)
	}
	// A publisher with no schedule source publishes neither, rather than zeroes
	// and Known-false rows that would read as "never evaluated".
	bare := publisher
	bare.schedule = nil
	plain := bare.snapshot(context.Background())
	if plain.Schedule != nil {
		t.Error("a publisher with no schedule source published a census")
	}
	for _, anomaly := range plain.Anomalies {
		if anomaly.Wake != nil {
			t.Errorf("row %s carries wake facts from no source", anomaly.QueryGroup)
		}
	}
}

// The dispatcher marks the objects it pushes back on the due index at the
// line that makes the decision, so their next return can say whether being
// pushed back cost the deadline. Driven through fillQueues with a ready queue
// of one, which is the branch a live deployment reaches under load.
func TestHeldBackObjectsAreMarkedAtTheDispatchersDecision(t *testing.T) {
	dispatcher := walkDispatcher(1, 4, map[execution.QueryGroupIdentity]time.Time{
		"query-group-a": {}, "query-group-b": {}, "query-group-c": {},
	})
	index := dispatcher.dueIndex
	if index == nil {
		t.Fatal("the walk dispatcher has no due index; the mark has nowhere to land")
	}
	at := time.Unix(50_000, 0)
	for name := range dispatcher.bundle.runners {
		index.Record(name, dispatcher.bundle.runners[name], index.versionEpoch,
			scheduler.RunnerDueBound{NotDueUntilUnix: at.Unix() - 1, IntervalSeconds: 60, Executed: true}, at.Add(-time.Minute))
	}
	runners, revision := dispatcher.bundle.snapshotScheduledRunners()
	// One place in the ready queue: the first object is queued and the walk
	// stops at the second, which is pushed back.
	dispatcher.fillQueues(runners, revision)
	if dispatcher.rotation.deferredQueueFull == 0 {
		t.Fatal("the full ready queue turned nothing away; the branch under test did not run")
	}
	held := 0
	for _, entry := range index.entries {
		if entry.heldBack {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("%d entries marked held back, want the one the full queue turned away", held)
	}
	// Its next return counts as held back, on time or not.
	for name, entry := range index.entries {
		if !entry.heldBack {
			continue
		}
		index.Record(name, dispatcher.bundle.runners[name], index.versionEpoch,
			scheduler.RunnerDueBound{NotDueUntilUnix: at.Unix() + 60, IntervalSeconds: 60, Executed: true}, at.Add(10*time.Second))
	}
	census := index.Census(at.Add(10*time.Second), 3)
	if census.HeldBack1h != 1 || census.HeldBackOnTime1h != 1 {
		t.Errorf("census = %+v, want one held-back round that was still on time", census)
	}
}

// A retained record carries the object's period from the same index the
// wake facts come from: a loss in progress on a ten-second object is the
// scheduler's replay bound, a named mechanism, and the row can say so
// without the reader looking the strategy up.
func TestFleetPublisherPutsThePeriodOnRetainedRecords(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{"qg-short": {}})
	index := dispatcher.dueIndex
	index.Record("qg-short", dispatcher.bundle.runners["qg-short"], index.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 20_010, IntervalSeconds: 10}, time.Unix(19_990, 0))
	tracker := fleet.NewTracker(nil, "replica-1", clock.now)
	for _, name := range []string{"qg-short", "qg-unindexed"} {
		ctx := observability.ContextWithTraceFields(context.Background(),
			observability.TraceFields{QueryGroupKey: name, EvaluationTime: 19_980})
		tracker.Observe(ctx, observability.Observation{ProgressCompletionKind: "GAP_SKIPPED",
			Trace: observability.TraceFields{QueryGroupKey: name}})
	}
	publisher := fleetPublisher{
		tracker: tracker, replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity {
			return []execution.QueryGroupIdentity{"qg-short", "qg-unindexed"}
		},
		schedule: index,
	}
	snapshot := publisher.snapshot(context.Background())
	if skip := snapshot.GapSkips["qg-short"]; skip.IntervalSeconds != 10 {
		t.Fatalf("record for the indexed object = %+v, want its ten-second period", skip)
	}
	// An object the index has no entry for: zero, said as unknown, not
	// invented.
	if skip := snapshot.GapSkips["qg-unindexed"]; skip.IntervalSeconds != 0 {
		t.Fatalf("record for the unindexed object = %+v, want no period", skip)
	}
}

// The census remembers the overdue count it found, one sample a minute for an
// hour, and reports the oldest one it still holds with its age: the first
// census has nothing earlier; half an hour on, the comparison is half an
// hour old and says so; past an hour, the oldest sample is the one from an
// hour ago, not the first one ever taken.
func TestCensusReportsTheBacklogItFoundEarlier(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(100_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{"a": {}, "b": {}, "c": {}})
	index := dispatcher.dueIndex
	epoch := index.versionEpoch
	lifecycle := func(name execution.QueryGroupIdentity) *phaseTwoQueryGroupLifecycle {
		return dispatcher.bundle.runners[name]
	}
	// Three objects due at 100_060 on a 60 s period: overdue from 100_121.
	for _, name := range []execution.QueryGroupIdentity{"a", "b", "c"} {
		index.Record(name, lifecycle(name), epoch,
			scheduler.RunnerDueBound{NotDueUntilUnix: 100_060, IntervalSeconds: 60}, time.Unix(100_000, 0))
	}
	first := index.Census(time.Unix(100_200, 0), 3)
	if first.Overdue != 3 || first.OverdueAgo != nil {
		t.Fatalf("first census = %+v, want 3 overdue and no earlier sample", first)
	}
	// One object returns; half an hour on, the backlog is 2 against 3.
	index.Record("a", lifecycle("a"), epoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 200_000, IntervalSeconds: 60, Executed: true}, time.Unix(100_210, 0))
	later := index.Census(time.Unix(100_200+1800, 0), 3)
	if later.Overdue != 2 || later.OverdueAgo == nil || *later.OverdueAgo != 3 || later.OverdueAgoSeconds != 1800 {
		t.Fatalf("half an hour on = %+v, want 2 overdue against 3 half an hour ago", later)
	}
	// Two hours on, the sample from the start has aged out of the ring; the
	// oldest one still held is the half-hour census, now ninety minutes
	// old -- past the ring, so it too is gone, and only what was sampled in
	// the last hour remains: nothing, until this census samples.
	twoHours := index.Census(time.Unix(100_200+7200, 0), 3)
	if twoHours.OverdueAgo != nil {
		t.Fatalf("two hours on with no census in between = %+v, want no earlier sample within the hour", twoHours)
	}
	// And a census 59 minutes after that reads the two-hour one.
	end := index.Census(time.Unix(100_200+7200+59*60, 0), 3)
	if end.OverdueAgo == nil || *end.OverdueAgo != 2 || end.OverdueAgoSeconds != 59*60 {
		t.Fatalf("59 minutes on = %+v, want the two-hour census as the earlier sample", end)
	}
}

// A row that says every round was empty carries the object's period from the
// same index, on the row's own facts: the number a reader holds the source's
// reporting period against is beside the run it explains, and an object the
// index has no entry for says zero rather than a period nobody recorded.
func TestFleetPublisherPutsThePeriodOnEmptyEveryRoundRows(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{"qg-15s": {}})
	index := dispatcher.dueIndex
	index.Record("qg-15s", dispatcher.bundle.runners["qg-15s"], index.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 20_015, IntervalSeconds: 15}, time.Unix(19_990, 0))
	tracker := fleet.NewTracker(nil, "replica-1", clock.now)
	// An hour and a quarter of empty rounds on both, from before the clock's
	// present so the hour has passed when the snapshot is taken.
	for _, name := range []string{"qg-15s", "qg-unindexed"} {
		at := time.Unix(20_000-75*60, 0)
		for ; at.Before(time.Unix(20_000, 0)); at = at.Add(15 * time.Second) {
			clock.at = at
			tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: "FULL_EMPTY_COMPLETED",
				Trace: observability.TraceFields{QueryGroupKey: name, StrategyID: "4101", EvaluationTime: at.Unix()}})
		}
	}
	clock.at = time.Unix(20_000, 0)
	publisher := fleetPublisher{
		tracker: tracker, replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity {
			return []execution.QueryGroupIdentity{"qg-15s", "qg-unindexed"}
		},
		schedule: index,
	}
	snapshot := publisher.snapshot(context.Background())
	byObject := map[string]fleet.Anomaly{}
	for _, row := range snapshot.NoData {
		byObject[row.QueryGroup] = row
	}
	row := byObject["qg-15s"]
	if row.Kind != fleet.KindEmptyEveryRound || row.EmptyEveryRound == nil || row.EmptyEveryRound.IntervalSeconds != 15 ||
		row.Wake == nil || row.Wake.IntervalSeconds != 15 {
		t.Fatalf("row for the indexed object = %+v (facts %+v), want EMPTY_EVERY_ROUND with its fifteen-second period on the facts", row, row.EmptyEveryRound)
	}
	if row := byObject["qg-unindexed"]; row.Kind != fleet.KindEmptyEveryRound || row.EmptyEveryRound == nil || row.EmptyEveryRound.IntervalSeconds != 0 {
		t.Fatalf("row for the unindexed object = %+v (facts %+v), want listed with no period", row, row.EmptyEveryRound)
	}
}

// The fill projection is attached where the period is: at publication, from
// the same index the row's wake comes from. A flat short window on an
// indexed object carries it; the same window on an object the index has no
// entry for has no period to project with and carries none.
func TestFleetPublisherProjectsTheFillOfAFlatShortWindow(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{"qg-60s": {}})
	index := dispatcher.dueIndex
	index.Record("qg-60s", dispatcher.bundle.runners["qg-60s"], index.versionEpoch,
		scheduler.RunnerDueBound{NotDueUntilUnix: 20_060, IntervalSeconds: 60}, time.Unix(19_990, 0))
	tracker := fleet.NewTracker(nil, "replica-1", clock.now)
	// Five rounds short by the same three positions of a 1469-position
	// window, on both objects.
	for _, name := range []string{"qg-60s", "qg-unindexed"} {
		for round := 0; round < 5; round++ {
			clock.at = time.Unix(20_000-int64(5-round)*60, 0)
			tracker.Observe(context.Background(), observability.Observation{
				ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE", ProgressCompletionCause: "LEVEL_OUTCOME_UNKNOWN",
				ProgressCompletionReason: "GAP_SKIPPED",
				HistoryCoverage:          &observability.HistoryCoverageFacts{Levels: 249, Short: 249, WorstValid: 1466, WorstRequired: 1469, Guarded: 249},
				Trace:                    observability.TraceFields{QueryGroupKey: name, StrategyID: "4101", EvaluationTime: clock.at.Unix()},
			})
		}
	}
	clock.at = time.Unix(20_000, 0)
	publisher := fleetPublisher{
		tracker: tracker, replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity {
			return []execution.QueryGroupIdentity{"qg-60s", "qg-unindexed"}
		},
		schedule: index,
	}
	snapshot := publisher.snapshot(context.Background())
	byObject := map[string]fleet.Anomaly{}
	for _, column := range [][]fleet.Anomaly{snapshot.Anomalies, snapshot.Undecidable, snapshot.Demoted, snapshot.ByDesign} {
		for _, row := range column {
			byObject[row.QueryGroup] = row
		}
	}
	row, listed := byObject["qg-60s"]
	if !listed || row.Coverage == nil || row.Coverage.UnchangedRounds != 4 {
		t.Fatalf("row for the indexed object = %+v, want it listed with the flat count at 4", row)
	}
	fill := row.WindowFill
	wantBy := time.Unix(20_000, 0).Add(time.Duration(1469-4) * time.Minute)
	if fill == nil || fill.Holes != 3 || fill.PeriodSeconds != 60 || fill.SpanSeconds != 1469*60 || !fill.Sliding ||
		fill.LatestFillBy == nil || !fill.LatestFillBy.Equal(wantBy) {
		t.Fatalf("fill on the indexed object = %+v, want 3 sliding holes at 60 s, latest by %v", fill, wantBy)
	}
	if row := byObject["qg-unindexed"]; row.WindowFill != nil {
		t.Fatalf("the unindexed object carries %+v, want no projection without a period", row.WindowFill)
	}
}

// scriptedSchedule answers WakeOf from a table and counts nothing.
type scriptedSchedule map[string]fleet.WakeFacts

func (schedule scriptedSchedule) Census(time.Time, int) fleet.ScheduleCensus {
	return fleet.ScheduleCensus{}
}
func (schedule scriptedSchedule) WakeOf(queryGroup string) fleet.WakeFacts {
	return schedule[queryGroup]
}

// The snapshot names the owned objects that are only waiting for their first
// round, from the tracker and the schedule together: a long object nothing
// has returned for and whose turn is ahead. A long object that has returned
// a round (and so concluded), a minute object, one on a backoff bound and one
// due further out than its period are not waiting.
func TestFleetPublisherNamesTheObjectsAwaitingTheirFirstRound(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	at := time.Unix(20_000, 0)
	tracker := fleet.NewTracker(nil, "replica-1", clock.now)
	tracker.Observe(context.Background(), observability.Observation{ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE",
		Trace: observability.TraceFields{QueryGroupKey: "long-returned", StrategyID: "1", BusinessID: "2"}})
	if !tracker.HasConclusion("long-returned") {
		t.Fatal("fixture: a returned round must have concluded long-returned")
	}
	schedule := scriptedSchedule{
		"long-new":      {Known: true, IntervalSeconds: 7200, DueAt: at.Add(time.Hour)},
		"long-later":    {Known: true, IntervalSeconds: 216000, DueAt: at.Add(50 * time.Hour)},
		"long-returned": {Known: true, IntervalSeconds: 7200, DueAt: at.Add(time.Hour)},
		"minute-new":    {Known: true, IntervalSeconds: 60, DueAt: at.Add(30 * time.Second)},
		"long-backoff":  {Known: true, IntervalSeconds: 7200, DueAt: at.Add(time.Hour), Deferred: true},
		"long-far":      {Known: true, IntervalSeconds: 7200, DueAt: at.Add(3 * time.Hour)},
	}
	publisher := fleetPublisher{
		tracker: tracker, replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity {
			return []execution.QueryGroupIdentity{"long-later", "long-new", "long-returned", "minute-new", "long-backoff", "long-far"}
		},
		schedule: schedule,
	}
	snapshot := publisher.snapshot(context.Background())
	if snapshot.AwaitingFirstRoundTotal != 2 || len(snapshot.AwaitingFirstRound) != 2 ||
		snapshot.AwaitingFirstRound[0].QueryGroup != "long-new" || snapshot.AwaitingFirstRound[1].QueryGroup != "long-later" ||
		snapshot.AwaitingFirstRound[1].IntervalSeconds != 216000 {
		t.Fatalf("awaiting = %+v total %d, want long-new then long-later, soonest first", snapshot.AwaitingFirstRound, snapshot.AwaitingFirstRoundTotal)
	}
	bare := publisher
	bare.schedule = nil
	if plain := bare.snapshot(context.Background()); plain.AwaitingFirstRoundTotal != 0 {
		t.Errorf("a publisher with no schedule exempted %d objects", plain.AwaitingFirstRoundTotal)
	}
}

// Through the real due index, what each kind of round writes back decides the
// exemption: a round not yet due leaves a bound ahead and is waiting; a
// cancelled round's zero bound is clamped to now, a backoff is Deferred and a
// cooldown is QueryCooldown, and none of those is waiting -- so an object
// whose rounds keep being cancelled is never exempted into a healthy verdict.
func TestTheDueIndexBoundDecidesWhetherAnObjectIsAwaitingItsFirstRound(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(50_000, 0)}
	dispatcher := dueIndexDispatcher(clock, metric.NewRecorder(metric.BuildInfo{}), 8, 8,
		map[execution.QueryGroupIdentity]walkRunner{"not-due": {}, "cancelled": {}, "backoff": {}, "cooling": {}})
	index := dispatcher.dueIndex
	record := func(queryGroup execution.QueryGroupIdentity, bound scheduler.RunnerDueBound) {
		bound.IntervalSeconds = 7200
		index.Record(queryGroup, dispatcher.bundle.runners[queryGroup], index.versionEpoch, bound, clock.at)
	}
	record("not-due", scheduler.RunnerDueBound{NotDueUntilUnix: 50_000 + 3600})
	record("cancelled", scheduler.RunnerDueBound{})
	record("backoff", scheduler.RunnerDueBound{NotDueUntilUnix: 50_000 + 600, Deferred: true})
	record("cooling", scheduler.RunnerDueBound{NotDueUntilUnix: 50_000 + 600, QueryCooldown: true})
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", clock.now), replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity {
			return []execution.QueryGroupIdentity{"not-due", "cancelled", "backoff", "cooling"}
		},
		schedule: index,
	}
	snapshot := publisher.snapshot(context.Background())
	if snapshot.AwaitingFirstRoundTotal != 1 || len(snapshot.AwaitingFirstRound) != 1 || snapshot.AwaitingFirstRound[0].QueryGroup != "not-due" {
		t.Fatalf("awaiting = %+v total %d, want only not-due", snapshot.AwaitingFirstRound, snapshot.AwaitingFirstRoundTotal)
	}
	if wake := index.WakeOf("cancelled"); !wake.Known || wake.DueAt.After(clock.at) {
		t.Fatalf("fixture: a cancelled round's wake %+v should be clamped to now", wake)
	}
}
