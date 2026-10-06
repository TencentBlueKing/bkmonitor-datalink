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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// redisCommitFailure is a round that failed committing to Redis, in the two
// observations the pipeline reports it with: the classified failure on the
// way, then the terminal with the error's words. The shape a Redis restart
// leaves on every object for a minute or two.
func redisCommitFailure(tracker *Tracker, queryGroup string, slot int64) {
	tracker.Observe(context.Background(), observability.Observation{
		QueryFailure: &observability.QueryFailureFacts{Stage: "execute", Category: "other", Code: "REDIS_UNAVAILABLE", Detail: "dial tcp 10.0.0.1:6379: i/o timeout"},
		Trace:        observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: slot},
	})
	tracker.Observe(context.Background(), observability.Observation{
		ExecuteOutcome: "error", Operation: "commit",
		Err:   errors.New("alarmd worker: commit: redis: connection pool timeout"),
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: slot},
	})
}

func healthyRound(queryGroup string, slot int64) observability.Observation {
	return observability.Observation{ProgressCompletionKind: "FULL_COMPLETED",
		Trace: observability.TraceFields{QueryGroupKey: queryGroup, EvaluationTime: slot}}
}

// A listed object that completes healthily is a recovery, recorded under
// the line and fold it was on at that moment -- the positive evidence the
// RECOVERED reading is made of. Two objects recovering from the same fold
// are one problem with two objects; the same object recovering twice is
// still one.
func TestAHealthyCompletionOfAListedObjectIsRecordedAsItsFoldsRecovery(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		for _, queryGroup := range []string{"qg-a", "qg-b"} {
			redisCommitFailure(tracker, queryGroup, int64(100+60*round))
		}
		at.at = at.at.Add(time.Minute)
	}
	if rows := tracker.Anomalies(); len(rows) != 2 {
		t.Fatalf("rows = %+v, want both objects listed before they recover", rows)
	}
	if recovered := tracker.Recovered(); len(recovered) != 0 {
		t.Fatalf("recovered = %+v, want nothing recorded while the objects still fail", recovered)
	}
	firstRecovery := at.at
	tracker.Observe(context.Background(), healthyRound("qg-a", 400))
	at.at = at.at.Add(time.Minute)
	tracker.Observe(context.Background(), healthyRound("qg-b", 460))
	if rows := tracker.Anomalies(); len(rows) != 0 {
		t.Fatalf("rows = %+v, want the recovered objects off the list", rows)
	}
	// qg-a fails its way back onto the list and recovers again: the same
	// object twice within the hour is one object.
	for round := 0; round < DefaultDegradedRounds; round++ {
		at.at = at.at.Add(time.Minute)
		redisCommitFailure(tracker, "qg-a", int64(500+60*round))
	}
	at.at = at.at.Add(time.Minute)
	lastRecovery := at.at
	tracker.Observe(context.Background(), healthyRound("qg-a", 700))
	recovered := tracker.Recovered()
	if len(recovered) != 1 {
		t.Fatalf("recovered = %+v, want the one fold both objects were under", recovered)
	}
	problem := recovered[0]
	if problem.Check != CheckDependencyDown || problem.Key != "COMMIT/REDIS/UNAVAILABLE" {
		t.Fatalf("problem = %+v, want the Redis commit fold under DEPENDENCY_DOWN", problem)
	}
	if problem.Objects != 2 {
		t.Fatalf("objects = %d, want two distinct objects, the second recovery of qg-a not counted again", problem.Objects)
	}
	if !problem.FirstFailure.Equal(now) || !problem.FirstRecovery.Equal(firstRecovery) || !problem.LastRecovery.Equal(lastRecovery) {
		t.Fatalf("clocks = %+v, want onset %v, first recovery %v, last recovery %v", problem, now, firstRecovery, lastRecovery)
	}
	if !problem.LastFailure.Before(lastRecovery) || problem.LastFailure.Before(firstRecovery) {
		t.Fatalf("last failure = %v, want the latest failing round before the last recovery", problem.LastFailure)
	}
}

// Recovery has positive evidence or it is not recorded: an object that
// stops being listed for any other reason -- the fence refused it and its
// publisher forgot it, the process restarted -- has not been seen to
// recover. And an object that was never listed did not recover from a
// problem the page ever showed.
func TestOnlyAHealthyCompletionOfAListedObjectCounts(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	// Never listed: two failures, under the threshold, then healthy.
	for round := 0; round < DefaultDegradedRounds-1; round++ {
		redisCommitFailure(tracker, "qg-brief", int64(100+60*round))
		at.at = at.at.Add(time.Minute)
	}
	tracker.Observe(context.Background(), healthyRound("qg-brief", 400))
	// Listed, then moved away: the refusal is not a recovery.
	for round := 0; round < DefaultDegradedRounds; round++ {
		redisCommitFailure(tracker, "qg-moved", int64(100+60*round))
		at.at = at.at.Add(time.Minute)
	}
	tracker.Observe(context.Background(), observability.Observation{RunOutcome: "ownership_rejected",
		Trace: observability.TraceFields{QueryGroupKey: "qg-moved"}})
	tracker.Forget(map[string]struct{}{"qg-brief": {}})
	if recovered := tracker.Recovered(); len(recovered) != 0 {
		t.Fatalf("recovered = %+v, want nothing: neither object was seen to complete healthily after being listed", recovered)
	}
}

// A recovered problem is remembered for the retention and then forgotten:
// the hour is the page's horizon for "recently", and a fold recovered
// yesterday is not a problem anyone is watching.
func TestARecoveredProblemIsForgottenAfterTheRetention(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for round := 0; round < DefaultDegradedRounds; round++ {
		redisCommitFailure(tracker, "qg-a", int64(100+60*round))
		at.at = at.at.Add(time.Minute)
	}
	tracker.Observe(context.Background(), healthyRound("qg-a", 400))
	at.at = at.at.Add(RecoveredRetention)
	if recovered := tracker.Recovered(); len(recovered) != 1 {
		t.Fatalf("recovered at the retention = %+v, want the problem still remembered", recovered)
	}
	at.at = at.at.Add(time.Second)
	if recovered := tracker.Recovered(); len(recovered) != 0 {
		t.Fatalf("recovered past the retention = %+v, want it forgotten", recovered)
	}
}

// Each object counts for the hour after its own recovery, not the fold's
// latest: two objects that recovered seventy and twenty minutes ago are one
// object within the hour, whatever the fold says about its last recovery.
func TestEachObjectCountsForTheHourAfterItsOwnRecovery(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for _, queryGroup := range []string{"qg-early", "qg-late"} {
		for round := 0; round < DefaultDegradedRounds; round++ {
			redisCommitFailure(tracker, queryGroup, int64(100+60*round))
		}
	}
	tracker.Observe(context.Background(), healthyRound("qg-early", 400))
	at.at = at.at.Add(50 * time.Minute)
	tracker.Observe(context.Background(), healthyRound("qg-late", 3400))
	at.at = at.at.Add(20 * time.Minute)
	recovered := tracker.Recovered()
	if len(recovered) != 1 || recovered[0].Objects != 1 {
		t.Fatalf("recovered = %+v, want one object within the hour: the early one recovered seventy minutes ago", recovered)
	}
	if !recovered[0].LastRecovery.Equal(now.Add(50 * time.Minute)) {
		t.Fatalf("last recovery = %v, want the late object's", recovered[0].LastRecovery)
	}
}

// On the report, recoveries land on the fold they came from. With objects
// still under it they say how far the problem has come back and do not
// lift the state -- one object still failing keeps the group blocked. With
// none left, the fold is the record of a problem that recovered, and reads
// RECOVERED with the record's clocks; the line carries the count apart
// from its current objects.
func TestRecoveriesLandOnTheirFoldWithoutLiftingABlockedGroup(t *testing.T) {
	fresh := now.Add(-time.Minute)
	rows := []Anomaly{{QueryGroup: "qg-still", Kind: KindDegradedRun, ReasonCode: "error", Since: now.Add(-20 * time.Minute), ReasonLastAt: fresh, RoundSlot: 100,
		Failure:   &FailureRef{Stage: "execute", Category: "other", Code: "REDIS_UNAVAILABLE", At: &fresh, Slot: 100},
		LastError: &LastError{Text: "alarmd worker: commit: redis: connection pool timeout", At: fresh, Operation: "commit", EvaluationTime: 100}}}
	Attribute(rows, now)
	if rows[0].Finding.Check != CheckDependencyDown || rows[0].Finding.Group != "COMMIT/REDIS/UNAVAILABLE" {
		t.Fatalf("fixture row = %+v, want it under the Redis commit fold", rows[0].Finding)
	}
	view := &View{Replicas: []string{"pod-a"}, Recovered: []RecoveredProblem{
		{Check: CheckDependencyDown, Key: "COMMIT/REDIS/UNAVAILABLE", Objects: 85,
			FirstFailure: now.Add(-25 * time.Minute), LastFailure: now.Add(-3 * time.Minute),
			FirstRecovery: now.Add(-4 * time.Minute), LastRecovery: now.Add(-2 * time.Minute)},
		{Check: CheckBackendNotAnswering, Key: "QUERY/UNLOCATED/TIMEOUT", Objects: 12,
			FirstFailure: now.Add(-50 * time.Minute), LastFailure: now.Add(-31 * time.Minute),
			FirstRecovery: now.Add(-30 * time.Minute), LastRecovery: now.Add(-30 * time.Minute)},
	}}
	reports := ReportChecks([][]Anomaly{rows}, nil, view, now)
	byCode := map[Check]CheckReport{}
	for _, report := range reports {
		byCode[report.Code] = report
	}
	down := byCode[CheckDependencyDown]
	if len(down.Groups) != 1 || down.Groups[0].Objects != 1 || down.Groups[0].Recovered != 85 || down.Groups[0].Recovery != RecoveryBlocked {
		t.Fatalf("Redis fold = %+v, want one object still under it, 85 recovered, and still BLOCKED", down.Groups)
	}
	if down.Current != 1 || down.Objects != 1 || down.Recovered != 85 {
		t.Fatalf("Redis line = current %d objects %d recovered %d, want the recovered apart from the current", down.Current, down.Objects, down.Recovered)
	}
	if down.Groups[0].LastSuccess == nil || !down.Groups[0].LastSuccess.Equal(now.Add(-2*time.Minute)) {
		t.Fatalf("Redis fold last success = %v, want the latest recovery", down.Groups[0].LastSuccess)
	}
	backend := byCode[CheckBackendNotAnswering]
	if backend.Current != 0 || backend.Objects != 0 || backend.Recovered != 12 || backend.RecoveredLast == nil || !backend.RecoveredLast.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("timeout line = %+v, want nothing current, 12 recovered, last recovery half an hour ago", backend)
	}
	if len(backend.Groups) != 1 {
		t.Fatalf("timeout groups = %+v, want the one recovered fold", backend.Groups)
	}
	fold := backend.Groups[0]
	if fold.Recovery != RecoveryRecovered || fold.Objects != 0 || fold.Recovered != 12 {
		t.Fatalf("timeout fold = %+v, want RECOVERED with no current objects and 12 recovered", fold)
	}
	if fold.FirstFailure == nil || !fold.FirstFailure.Equal(now.Add(-50*time.Minute)) || fold.LastFailure == nil || !fold.LastFailure.Equal(now.Add(-31*time.Minute)) ||
		fold.LastSuccess == nil || !fold.LastSuccess.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("timeout fold clocks = %+v, want the record's onset, last failure and recovery", fold)
	}
}

// Two replicas' recoveries of the same fold are one problem: objects add
// up, the onset is the earliest, the rest the latest. Through the aggregate,
// since that is the only way a replica's record reaches the page.
func TestRecoveriesMergeAcrossReplicasByFold(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].Recovered = []RecoveredProblem{{Check: CheckDependencyDown, Key: "COMMIT/REDIS/UNAVAILABLE", Objects: 40,
		FirstFailure: now.Add(-20 * time.Minute), LastFailure: now.Add(-5 * time.Minute), FirstRecovery: now.Add(-4 * time.Minute), LastRecovery: now.Add(-4 * time.Minute)}}
	snapshots[1].Recovered = []RecoveredProblem{
		{Check: CheckDependencyDown, Key: "COMMIT/REDIS/UNAVAILABLE", Objects: 45,
			FirstFailure: now.Add(-22 * time.Minute), LastFailure: now.Add(-6 * time.Minute), FirstRecovery: now.Add(-5 * time.Minute), LastRecovery: now.Add(-3 * time.Minute)},
		{Check: CheckDefect, Key: "EVALUATE/NONE/CONTRACT", Objects: 1, FirstRecovery: now, LastRecovery: now},
	}
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	if len(view.Recovered) != 2 {
		t.Fatalf("recovered = %+v, want the shared fold merged and the other kept", view.Recovered)
	}
	merged := view.Recovered[0]
	if merged.Objects != 85 || !merged.FirstFailure.Equal(now.Add(-22*time.Minute)) || !merged.LastFailure.Equal(now.Add(-5*time.Minute)) ||
		!merged.FirstRecovery.Equal(now.Add(-5*time.Minute)) || !merged.LastRecovery.Equal(now.Add(-3*time.Minute)) {
		t.Fatalf("merged = %+v, want 85 objects, earliest onset and first recovery, latest failure and recovery", merged)
	}
}

// A recovered fold names its objects: the two that recovered are two
// identities with their strategies, most recent first, and across replicas
// the samples join, one entry per object, cut to the bound while the count
// stays the sum. A fold that has ended used to be a count and four clocks,
// and "which two, and what failed" was answered from the logs or not at all.
func TestARecoveredFoldNamesItsObjects(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for _, group := range []string{"qg-first", "qg-second"} {
		for round := 0; round < DefaultDegradedRounds; round++ {
			at.at = at.at.Add(time.Minute)
			tracker.Observe(context.Background(), completion(group, "COMPLETED_WITH_UNAVAILABLE", "4101"))
		}
	}
	at.at = at.at.Add(time.Minute)
	tracker.Observe(context.Background(), completion("qg-first", "FULL_COMPLETED", "4101"))
	firstAt := at.at
	at.at = at.at.Add(time.Minute)
	tracker.Observe(context.Background(), completion("qg-second", "FULL_COMPLETED", "4101"))
	recovered := tracker.Recovered()
	if len(recovered) != 1 || recovered[0].Objects != 2 {
		t.Fatalf("recovered = %+v, want one fold of two objects", recovered)
	}
	samples := recovered[0].Samples
	if len(samples) != 2 || samples[0].QueryGroup != "qg-second" || samples[1].QueryGroup != "qg-first" ||
		!samples[1].RecoveredAt.Equal(firstAt) || len(samples[0].Strategies) != 1 || samples[0].Strategies[0].StrategyID != "4101" {
		t.Fatalf("samples = %+v, want both objects, most recent first, each with its strategy", samples)
	}
	// Across replicas: the same object seen on two replicas is named once,
	// the bound holds, the count is still the sum.
	sample := func(group string, ago time.Duration) RecoveredSample {
		return RecoveredSample{QueryGroup: group, RecoveredAt: now.Add(-ago), Strategies: []StrategyRef{{StrategyID: "4101", BusinessID: "2"}}}
	}
	snapshots := healthySnapshots()
	snapshots[0].Recovered = []RecoveredProblem{{Check: CheckDefect, Key: "COMMIT/NONE/CONTRACT", Objects: 3, FirstRecovery: now, LastRecovery: now,
		Samples: []RecoveredSample{sample("qg-a", time.Minute), sample("qg-b", 2*time.Minute), sample("qg-c", 3*time.Minute)}}}
	snapshots[1].Recovered = []RecoveredProblem{{Check: CheckDefect, Key: "COMMIT/NONE/CONTRACT", Objects: 3, FirstRecovery: now, LastRecovery: now,
		Samples: []RecoveredSample{sample("qg-a", 30*time.Second), sample("qg-d", 4*time.Minute), sample("qg-e", 5*time.Minute)}}}
	view := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	var fold *RecoveredProblem
	for index := range view.Recovered {
		if view.Recovered[index].Check == CheckDefect {
			fold = &view.Recovered[index]
		}
	}
	// Both replicas named every object they counted, so the merge counts
	// distinct identities: five, not the sum of six, with qg-a once.
	if fold == nil || fold.Objects != 5 || len(fold.Samples) != MaxRecoveredSample {
		t.Fatalf("merged fold = %+v, want five distinct objects and %d samples", fold, MaxRecoveredSample)
	}
	order := []string{}
	for _, entry := range fold.Samples {
		order = append(order, entry.QueryGroup)
	}
	if strings.Join(order, ",") != "qg-a,qg-b,qg-c,qg-d" || !fold.Samples[0].RecoveredAt.Equal(now.Add(-30*time.Second)) {
		t.Fatalf("merged samples = %v (qg-a at %v), want qg-a once at its most recent recovery, then b, c, d and the bound", order, fold.Samples[0].RecoveredAt)
	}
	// A fold larger than the bound on either side is not fully named, and
	// its count stays the sum the field says it is.
	snapshots = healthySnapshots()
	snapshots[0].Recovered = []RecoveredProblem{{Check: CheckDefect, Key: "COMMIT/NONE/CONTRACT", Objects: 40, FirstRecovery: now, LastRecovery: now,
		Samples: []RecoveredSample{sample("qg-a", time.Minute), sample("qg-b", 2*time.Minute), sample("qg-c", 3*time.Minute), sample("qg-d", 4*time.Minute)}}}
	snapshots[1].Recovered = []RecoveredProblem{{Check: CheckDefect, Key: "COMMIT/NONE/CONTRACT", Objects: 2, FirstRecovery: now, LastRecovery: now,
		Samples: []RecoveredSample{sample("qg-a", 30*time.Second), sample("qg-z", 5*time.Minute)}}}
	view = Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	for _, candidate := range view.Recovered {
		if candidate.Check == CheckDefect && candidate.Objects != 42 {
			t.Fatalf("large fold merged to %d objects, want the sum 42: forty objects are not named and cannot be made distinct", candidate.Objects)
		}
	}
	// A recovery that names no strategies does not erase the names an
	// earlier recovery of the same object recorded: the fold is asked
	// directly, because a tracker row always carries the names once it has
	// seen them, and the case is a row that never did.
	tracker = newTracker(t, at)
	tracker.recordRecoveryUnder("qg-named", CheckDefect, "COMMIT/NONE/CONTRACT", now, now, now, []StrategyRef{{StrategyID: "4101", BusinessID: "2"}})
	tracker.recordRecoveryUnder("qg-named", CheckDefect, "COMMIT/NONE/CONTRACT", now, now, now.Add(time.Minute), nil)
	for _, problem := range tracker.Recovered() {
		for _, entry := range problem.Samples {
			if entry.QueryGroup == "qg-named" && (len(entry.Strategies) != 1 || !entry.RecoveredAt.Equal(now.Add(time.Minute))) {
				t.Fatalf("a second recovery with no strategy names erased the first's, or the recovery time did not move: %+v", entry)
			}
		}
	}
}
