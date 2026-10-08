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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// healthFromSnapshots is the health route as it answered from every
// replica's snapshot, before it read their summaries: the answer the
// summaries have to give.
func healthFromSnapshots(view View, at time.Time, stallAfter time.Duration) HealthResponse {
	Decide(&view, at, stallAfter)
	columns := viewColumns(&view)
	pruned, retained, readEarly := prunedSkipList(view.PrunedSkips), retainedShareList(view.RetainedShare), readEarlyList(view.ReadEarly)
	return HealthResponse{
		Cohorts: cohortList(Cohorts(&view, columns)), Cooling: Cooling(&view, columns, at),
		Health: view.Health, Expected: view.Expected, Covered: view.Covered,
		Determined: view.Determined, Unknown: view.Unknown, Healthy: view.Healthy,
		AnomaliesTotal: view.AnomaliesTotal, DemotedTotal: view.DemotedTotal,
		UndecidableTotal: view.UndecidableTotal, ByDesignTotal: view.ByDesignTotal,
		EmptyEveryRoundTotal: view.EmptyEveryRoundTotal,
		Ours:                 OursCount(view.Anomalies), Unattributed: UnattributedCount(view.Anomalies),
		Impact:     ImpactOf(view, at),
		DemotedDue: view.DemotedDue, DemotedDueOldestSeconds: view.DemotedDueOldestSeconds,
		DemotionEntries: view.DemotionEntries, DemotionExtensions: view.DemotionExtensions, DemotionExits: view.DemotionExits,
		DemotionRestored: view.DemotionRestored, DemotionHandovers: view.DemotionHandovers,
		DemotionReentries: view.DemotionReentries, LastDemotionExit: momentOrNil(view.LastDemotionExit),
		PrunedSkips: firstScreenList(pruned), PrunedSkipsTotal: len(pruned),
		RetainedShare: firstScreenList(retained), RetainedShareTotal: len(retained),
		ReadEarly: firstScreenList(readEarly), ReadEarlyTotal: len(readEarly),
		Coverage: view.Coverage, Handover: handoverOf(view.Coverage), PerReplica: view.PerReplica,
		PublishedVersion: view.PublishedVersion, Workers: view.Workers, Builds: view.Builds,
		OutputProtocols: outputProtocolList(view.OutputProtocols), Retentions: retentionList(view.Retentions),
		OutputPath: OutputPathOf(&view), Degradations: degradationList(view.Degradations),
		Activation: view.Activation, ActivationReplica: view.ActivationReplica, NoDataHorizon: view.NoDataHorizon,
		Rebalance: view.Rebalance, RebalanceReplica: view.RebalanceReplica,
		AssignmentScope: view.AssignmentScope, AssignmentScopeReplica: view.AssignmentScopeReplica,
		AssignmentSweep: view.AssignmentSweep, AssignmentSweepReplica: view.AssignmentSweepReplica,
		LeaderRound: view.LeaderRound, LeaderRoundReplica: view.LeaderRoundReplica,
		ViewStream: view.ViewStream, ViewStreamReplica: view.ViewStreamReplica,
		Source: view.Source, SourceReplica: view.SourceReplica, SourceStanding: view.SourceStanding,
		NoDataTracking: view.NoDataTracking,
		Dependencies:   dependencyList(view.Dependencies), DependenciesReplica: view.DependenciesReplica,
		DependenciesReplicas: view.DependenciesReplicas, LinkdConsole: view.LinkdConsole, ReplicasNotReady: view.ReplicasNotReady,
		Overdue: view.Overdue, Dispatch: view.Dispatch, Schedule: view.Schedule, Gaps: view.Gaps, Capacity: view.Capacity,
		Load: LoadOf(&view, at),
	}
}

// healthFromSummaries is the route's answer from the replicas' summaries of
// the same snapshots, each summary written out and read back as a replica
// publishes it and the reader reads it.
func healthFromSummaries(t *testing.T, snapshots []Snapshot, expectation Expectation, names []string, at time.Time,
	stallAfter time.Duration) HealthResponse {
	t.Helper()
	view, part := summariesRoundTrip(t, snapshots, expectation, names, at, stallAfter)
	return healthOf(&view, part, at)
}

// summariesRoundTrip is the view and part a reader makes of the replicas'
// summaries of snapshots, each written out and read back as published.
func summariesRoundTrip(t *testing.T, snapshots []Snapshot, expectation Expectation, names []string, at time.Time,
	stallAfter time.Duration) (View, ReplicaPart) {
	t.Helper()
	summaries := make([]ReplicaSummary, 0, len(snapshots))
	owned := map[string][]string{}
	for _, snapshot := range snapshots {
		encoded, err := json.Marshal(SummaryOf(snapshot, snapshot.OwnedObjects, stallAfter))
		if err != nil {
			t.Fatal(err)
		}
		var summary ReplicaSummary
		if err := json.Unmarshal(encoded, &summary); err != nil {
			t.Fatal(err)
		}
		summaries = append(summaries, summary)
		owned[snapshot.Replica] = snapshot.OwnedObjects
	}
	return AggregateSummaries(expectation, DigestOf(expectation.IDs), summaries, names, at, freshness,
		func(replicas []string) ([][]string, bool) {
			sets := make([][]string, 0, len(replicas))
			for _, replica := range replicas {
				sets = append(sets, owned[replica])
			}
			return sets, true
		})
}

// externalRows is snapshotsWithAnomalies with rows the backend's -- a
// query that timed out -- which are not this deployment's until they stall.
func externalRows(count int) []Snapshot {
	snapshots := snapshotsWithAnomalies(count)
	for index := range snapshots[1].Anomalies {
		row := &snapshots[1].Anomalies[index]
		row.Kind, row.Cause, row.CauseReason = KindDegradedRun, "LEVEL_OUTCOME_UNKNOWN", "QUERY_TIMEOUT"
	}
	return snapshots
}

// ownedFixture gives each replica the owned list its counts claim, drawn
// from expected, so coverage can be compared.
func ownedFixture(snapshots []Snapshot, prefix string) ([]Snapshot, []string) {
	var expected []string
	for index := range snapshots {
		snapshots[index].OwnedObjects = nil
		for n := 0; n < snapshots[index].Owned; n++ {
			object := fmt.Sprintf("%s-%s-%d", prefix, snapshots[index].Replica, n)
			snapshots[index].OwnedObjects = append(snapshots[index].OwnedObjects, object)
			expected = append(expected, object)
		}
	}
	return snapshots, expected
}

// The health route reads from the replicas' summaries what it read from
// their snapshots, field for field, while no object is held by two replicas
// -- the verdict and each replica's split, the counts rows give, the impact,
// cohorts and cooling, the load, the lists and coverage -- over fixtures
// that reach every number, with a replica missing, one stale, lists cut to a
// budget, and coverage that agrees and that does not.
func TestHealthFromSummariesIsHealthFromSnapshots(t *testing.T) {
	const stallAfter = 10 * time.Minute
	at := now
	fixtures := summaryFixtures(at)
	for name, fixture := range fixtures {
		snapshots, expectation, names := fixture()
		for index := range snapshots {
			if snapshots[index].TakenAt.After(at.Add(-freshness)) {
				// Decided at the moment the replica publishes, which the whole
				// view decides at the moment it is read: the same moment here.
				snapshots[index].TakenAt = at
			}
		}
		want := healthFromSnapshots(Aggregate(expectation, snapshots, names, at, freshness), at, stallAfter)
		got := healthFromSummaries(t, snapshots, expectation, names, at, stallAfter)
		sameJSON(t, name, got, want)
		if reached := fixtureReaches[name]; !reached(want) {
			t.Errorf("%s: the fixture does not reach what it is for: %+v", name, want)
		}
	}
}

// summaryFixtures are the snapshots the summaries are checked against:
// every number rows give, cohorts, coverage that agrees and that does not,
// a row about to stall, and a replica missing, one stale, a list cut.
func summaryFixtures(at time.Time) map[string]func() ([]Snapshot, Expectation, []string) {
	return map[string]func() ([]Snapshot, Expectation, []string){
		"every row number": func() ([]Snapshot, Expectation, []string) {
			snapshots := partReplicas()
			names := []string{}
			for index := range snapshots {
				names = append(names, snapshots[index].Replica)
			}
			return snapshots, Expectation{QueryGroups: 300, Known: true}, names
		},
		"cohorts": func() ([]Snapshot, Expectation, []string) {
			return cohortSnapshots(), Expectation{QueryGroups: 949, Known: true}, replicas()
		},
		"coverage agrees": func() ([]Snapshot, Expectation, []string) {
			snapshots, expected := ownedFixture(snapshotsWithAnomalies(3), "qg")
			return snapshots, Expectation{QueryGroups: len(expected), Known: true, IDs: expected}, replicas()
		},
		"coverage does not agree": func() ([]Snapshot, Expectation, []string) {
			snapshots, expected := ownedFixture(snapshotsWithAnomalies(3), "qg")
			expected = append(expected[:len(expected)-2], "qg-nobody-1", "qg-nobody-2")
			return snapshots, Expectation{QueryGroups: len(expected), Known: true, IDs: expected}, replicas()
		},
		"a row about to stall": func() ([]Snapshot, Expectation, []string) {
			snapshots := externalRows(2)
			snapshots[1].Anomalies[0].FailingSince = at.Add(-9 * time.Minute)
			return snapshots, Expectation{QueryGroups: 949, Known: true}, replicas()
		},
		"a replica missing, one stale, a list cut": func() ([]Snapshot, Expectation, []string) {
			snapshots := snapshotsWithAnomalies(4)
			snapshots[0].TotalAnomalies += 3
			stale := snapshots[1]
			stale.Replica, stale.TakenAt = "pod-c", at.Add(-time.Hour)
			return append(snapshots, stale), Expectation{QueryGroups: 949, Known: true}, []string{"pod-a", "pod-b", "pod-c", "pod-d"}
		},
	}
}

// fixtureReaches is what each fixture's answer has to hold, or the equality
// above is between two empty readings.
var fixtureReaches = map[string]func(HealthResponse) bool{
	"every row number": func(health HealthResponse) bool {
		return health.Ours > 0 && health.Unattributed+health.Ours < health.AnomaliesTotal && health.EmptyEveryRoundTotal > 0 &&
			health.DemotedDue > 0 && health.Impact.Blind.Strategies > 0 && health.Load.Loss.Ongoing+health.Load.Loss.AfterRestart > 0 &&
			health.PrunedSkipsTotal > FirstScreenListBound && len(health.Cohorts) > 1 && health.Cooling.Listed > 0
	},
	"cohorts": func(health HealthResponse) bool { return len(health.Cohorts) > 1 },
	"coverage agrees": func(health HealthResponse) bool {
		return health.Coverage != nil && health.Coverage.Comparable && health.Handover == nil &&
			health.Coverage.ExpectedNotHeldTotal+health.Coverage.HeldNotExpectedTotal+health.Coverage.HeldBySeveralTotal == 0
	},
	// Objects nobody holds and objects held unexpected are no handover.
	"coverage does not agree": func(health HealthResponse) bool {
		return health.Coverage != nil && health.Coverage.ExpectedNotHeldTotal == 2 && health.Coverage.HeldNotExpectedTotal == 2 &&
			health.Handover == nil
	},
	// Not stalled at the moment it is decided, and stalled well within the
	// hour: decided any later than the replica published, it would be ours.
	"a row about to stall": func(health HealthResponse) bool { return health.Ours == 0 && health.AnomaliesTotal == 2 },
	"a replica missing, one stale, a list cut": func(health HealthResponse) bool {
		kinds := map[GapKind]bool{}
		for _, gap := range health.Gaps {
			kinds[gap.Kind] = true
		}
		return kinds[GapReplicaMissing] && kinds[GapSnapshotStale] && kinds[GapListTruncated]
	},
}

// A skip the old holder records while the new holder has pooled the object
// reads, on the old holder's part, as a loss in progress, where the whole
// view read it as the pool's consequence: the one reading the handover
// changes rather than doubles. The route says so beside it.
func TestAHandoversLossInProgressIsMarked(t *testing.T) {
	const stallAfter = 10 * time.Minute
	snapshots := healthySnapshots()
	for index := range snapshots {
		snapshots[index].TakenAt = now
		snapshots[index].OwnedObjects = []string{fmt.Sprintf("qg-own-%d", index), "qg-handed-over"}
		snapshots[index].Owned, snapshots[index].Determined = 2, 2
	}
	snapshots[0].GapSkips = map[string]SkippedSpan{"qg-handed-over": {FirstSlot: 1, LastSlot: 3, Slots: 3,
		At: now.Add(-time.Minute), Replica: snapshots[0].Replica}}
	pooled := anomaly("qg-handed-over")
	pooled.Replica = snapshots[1].Replica
	pooled.QueryCooldown = &observability.QueryCooldownFacts{Until: now.Add(time.Hour)}
	snapshots[1].Demoted, snapshots[1].TotalDemoted = []Anomaly{pooled}, 1
	snapshots[1].Determined = 2
	expectation := Expectation{QueryGroups: 3, Known: true, IDs: []string{"qg-own-0", "qg-own-1", "qg-handed-over"}}
	whole := healthFromSnapshots(Aggregate(expectation, snapshots, replicas(), now, freshness), now, stallAfter)
	parts := healthFromSummaries(t, snapshots, expectation, replicas(), now, stallAfter)
	if whole.Load.Loss.Ongoing != 0 || parts.Load.Loss.Ongoing != 1 {
		t.Fatalf("loss in progress: whole view %d, parts %d; want the whole view to read the pool and the parts the skip",
			whole.Load.Loss.Ongoing, parts.Load.Loss.Ongoing)
	}
	if parts.Handover == nil || parts.Handover.Objects == nil || *parts.Handover.Objects != 1 {
		t.Fatalf("handover %+v, want the one object held by both named beside the loss", parts.Handover)
	}
}

// A replica whose owned list was cut to its budget cannot be compared, from
// its snapshot as from its summary: the route says a handover may be going
// on without saying how many, where a number would claim a comparison it
// could not make.
func TestAnOwnedListCutToItsBudgetMarksAHandoverItCannotCount(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].OwnedObjects, snapshots[0].Owned, snapshots[0].Determined = []string{"qg-a1"}, 3, 3
	snapshots[1].OwnedObjects, snapshots[1].Owned, snapshots[1].Determined = []string{"qg-b1"}, 1, 1
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 4, Known: true, IDs: []string{"qg-a1", "qg-a2", "qg-a3", "qg-b1"}},
		[]string{"pod-a", "pod-b"})
	body := requestJSON(t, handler, "/api/health")
	coverage, _ := body["coverage"].(map[string]any)
	marker, marked := body["handover"].(map[string]any)
	if coverage == nil || coverage["comparable"] != false || !marked || marker["objects"] != nil {
		t.Fatalf("coverage %v handover %v, want an incomparable coverage and a note with no number", body["coverage"], body["handover"])
	}
}

// The verdict record the health route keeps counts the objects the verdict
// was decided on, from the parts: a view of summaries has no rows to count.
func TestTheHealthRoutesVerdictRecordCountsFromTheParts(t *testing.T) {
	snapshots := externalRows(2)
	snapshots[1].Anomalies[0].FailingSince = now.Add(-2 * time.Hour)
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 10*time.Minute, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	body := requestJSON(t, handler, "/api/health")
	changes, _ := body["verdict_history"].([]any)
	if len(changes) != 1 {
		t.Fatalf("verdict history %v, want the one verdict decided", body["verdict_history"])
	}
	if change, _ := changes[0].(map[string]any); change["to"] != string(HealthDegraded) || change["ours"] != float64(1) {
		t.Fatalf("recorded %v, want DEGRADED on the one stalled object", change)
	}
}

// A registry that cannot be read is the health route's UNKNOWN with the
// gap that says so: its part is empty, and an empty part counts nothing.
// It is no verdict of the deployment, and is not recorded as one.
func TestTheHealthRouteAnswersAnUnreadableRegistry(t *testing.T) {
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{err: errors.New("registry unreadable")}, stubSnapshots{})
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 10*time.Minute, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	code, body := get(t, handler, "/api/health")
	gaps, _ := body["gaps"].([]any)
	gap, _ := func() (map[string]any, bool) {
		if len(gaps) == 0 {
			return nil, false
		}
		first, ok := gaps[0].(map[string]any)
		return first, ok
	}()
	if code != http.StatusOK || body["health"] != string(HealthUnknown) || gap["kind"] != string(GapRegistryUnavailable) {
		t.Fatalf("answered %d, health %v, gaps %v; want UNKNOWN with the registry unavailable", code, body["health"], body["gaps"])
	}
	if history, _ := service.VerdictHistory(); len(history) != 0 {
		t.Fatalf("verdict history %+v, want nothing recorded for a registry that could not be read", history)
	}
}

// A caller that goes away while the health route reads is not answered,
// and what it stopped waiting for is not recorded as a verdict.
func TestAHealthReadItsCallerLeftRecordsNoVerdict(t *testing.T) {
	reader := &blockingSnapshots{release: make(chan struct{}), snapshots: snapshotsWithAnomalies(2)}
	defer close(reader.release)
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, reader)
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 10*time.Minute, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, leave := context.WithCancel(context.Background())
	served := make(chan struct{})
	go func() {
		defer close(served)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/health", nil).WithContext(ctx))
	}()
	for deadline := time.Now().Add(2 * time.Second); reader.count() == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
	}
	leave()
	<-served
	if history, _ := service.VerdictHistory(); len(history) != 0 {
		t.Fatalf("verdict history %+v after the caller left, want nothing recorded", history)
	}
}

// The verdict scrape records no verdict for a view it did not read -- the
// registry or the snapshots unreadable, or the scrape's own deadline reached
// -- while the verdict it exports stays UNKNOWN; a view whose reads found a
// replica missing is the deployment's, and is recorded. The scrape reads the
// summaries as the health route does (Summarized).
func TestTheScrapeRecordsNoVerdictForAViewItDidNotRead(t *testing.T) {
	expectation := stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}}
	expired, cancel := context.WithCancel(context.Background())
	cancel()
	for name, read := range map[string]func() (*Service, View, ReplicaPart){
		"registry unreadable": func() (*Service, View, ReplicaPart) {
			service := mustService(t, expectation, stubRegistry{err: errors.New("registry unreadable")}, stubSnapshots{})
			view, part := service.Summarized(context.Background(), time.Minute)
			return service, view, part
		},
		"snapshots unreadable": func() (*Service, View, ReplicaPart) {
			service := mustService(t, expectation, stubRegistry{replicas: replicas()}, failingSnapshotsWithSummaries{})
			view, part := service.Summarized(context.Background(), time.Minute)
			return service, view, part
		},
		"deadline reached": func() (*Service, View, ReplicaPart) {
			service := mustService(t, expectation, stubRegistry{replicas: replicas()}, &waitingSnapshots{release: make(chan struct{})})
			view, part := service.Summarized(expired, time.Minute)
			return service, view, part
		},
	} {
		service, view, part := read()
		service.RecordSummarizedVerdict(&view, part, now)
		if history, _ := service.VerdictHistory(); view.Health != HealthUnknown || len(history) != 0 {
			t.Errorf("%s: view %s, history %+v; want UNKNOWN exported and nothing recorded", name, view.Health, history)
		}
	}
	missing := mustService(t, expectation, stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: healthySnapshots()[:1]})
	view, part := missing.Summarized(context.Background(), time.Minute)
	missing.RecordSummarizedVerdict(&view, part, now)
	if history, _ := missing.VerdictHistory(); len(history) != 1 || history[0].To != HealthUnknown {
		t.Fatalf("history %+v, want the missing replica's UNKNOWN recorded", history)
	}
}

// blockingSnapshots holds every read until released, counting them.
type blockingSnapshots struct {
	mu        sync.Mutex
	loads     int
	release   chan struct{}
	snapshots []Snapshot
}

func (reader *blockingSnapshots) Load(context.Context, []string) ([]Snapshot, error) {
	reader.mu.Lock()
	reader.loads++
	reader.mu.Unlock()
	<-reader.release
	return reader.snapshots, nil
}

func (reader *blockingSnapshots) count() int {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.loads
}

// A reader that comes while a summarized read is in progress waits for it
// and gets its answer, instead of reading every replica again.
func TestReadersThatComeDuringASummarizedReadShareIt(t *testing.T) {
	reader := &blockingSnapshots{release: make(chan struct{}), snapshots: snapshotsWithAnomalies(2)}
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, reader)
	answers := make(chan View, 2)
	read := func() {
		view, _ := service.Summarized(context.Background(), time.Minute)
		answers <- view
	}
	go read()
	for deadline := time.Now().Add(2 * time.Second); reader.count() == 0 && time.Now().Before(deadline); time.Sleep(time.Millisecond) {
	}
	go read()
	time.Sleep(100 * time.Millisecond)
	close(reader.release)
	first, second := <-answers, <-answers
	if reader.count() != 1 || first.AnomaliesTotal != 2 || second.AnomaliesTotal != 2 {
		t.Fatalf("%d reads, answers %d and %d anomalies; want one read shared by both", reader.count(), first.AnomaliesTotal,
			second.AnomaliesTotal)
	}
}

// A row nobody attributed is unattributed on its replica, beside the rows
// whose attribution says unknown, and it does not decide the verdict either
// way: an unset field is not a verdict.
func TestARowWithNoAttributionIsUnattributedOnItsReplica(t *testing.T) {
	view := View{Anomalies: []Anomaly{{QueryGroup: "qg-unset", Replica: "pod-a"},
		{QueryGroup: "qg-unknown", Replica: "pod-a", Attribution: AttributionUnknown}},
		PerReplica: []ReplicaView{{Replica: "pod-a"}}}
	Settle(&view)
	if replica := view.PerReplica[0]; replica.Unattributed != 2 || replica.Ours != 0 || view.Health != HealthUnknown {
		t.Fatalf("split %+v health %s, want both rows unattributed and the unknown one holding the verdict", replica, view.Health)
	}
}

// held is two replicas' snapshots, holding one object between them or not,
// with the catalogue that lists every object held.
func held(both bool) ([]Snapshot, []string) {
	snapshots := healthySnapshots()
	snapshots[0].OwnedObjects = []string{"qg-a1"}
	snapshots[1].OwnedObjects = []string{"qg-b1", "qg-handed-over"}
	if both {
		snapshots[0].OwnedObjects = append(snapshots[0].OwnedObjects, "qg-handed-over")
	}
	for index := range snapshots {
		snapshots[index].TakenAt = now
		snapshots[index].Owned, snapshots[index].Determined = len(snapshots[index].OwnedObjects), len(snapshots[index].OwnedObjects)
	}
	return snapshots, []string{"qg-a1", "qg-b1", "qg-handed-over"}
}

// When the digests disagree and the owned lists that would say why cannot
// be read, the coverage is not comparable rather than absent, and the
// counts beside it carry the note without a number: they may be counting an
// object twice.
func TestOwnedListsThatCannotBeReadLeaveTheHandoverNoteWithoutANumber(t *testing.T) {
	snapshots, ids := held(true)
	expectation := Expectation{QueryGroups: len(ids), Known: true, IDs: ids}
	summaries := make([]ReplicaSummary, 0, len(snapshots))
	for _, snapshot := range snapshots {
		summaries = append(summaries, SummaryOf(snapshot, snapshot.OwnedObjects, time.Minute))
	}
	view, part := AggregateSummaries(expectation, DigestOf(ids), summaries, replicas(), now, freshness,
		func([]string) ([][]string, bool) { return nil, false })
	health := healthOf(&view, part, now)
	if health.Coverage == nil || health.Coverage.Comparable || health.Handover == nil || health.Handover.Objects != nil {
		t.Fatalf("coverage %+v handover %+v, want an incomparable coverage and a note with no number", health.Coverage, health.Handover)
	}
}

// Whether an object is held twice is the owned sets' to say: with the
// catalogue unreadable and the sets whole, the note gives the number when
// one is held twice and is absent when none is -- the missing denominator is
// its own gap, not a handover.
func TestTheHandoverNoteDoesNotWaitOnTheCatalogue(t *testing.T) {
	for name, both := range map[string]bool{"held by both": true, "held by one": false} {
		snapshots, _ := held(both)
		body := requestJSON(t, handlerWith(t, snapshots, Expectation{}, replicas()), "/api/health")
		marker, marked := body["handover"].(map[string]any)
		if marked != both || (both && marker["objects"] != float64(1)) {
			t.Errorf("%s: handover %v, want the one object named only while two replicas hold it", name, body["handover"])
		}
	}
}

// Rows nobody attributed hold the verdict no more than they decide it: a
// view of them alone is healthy, and they stay unattributed on their
// replica.
func TestRowsNobodyAttributedAloneLeaveTheVerdictHealthy(t *testing.T) {
	view := View{Anomalies: []Anomaly{{QueryGroup: "qg-unset", Replica: "pod-a"}}, PerReplica: []ReplicaView{{Replica: "pod-a"}}}
	Settle(&view)
	if view.Health != HealthHealthy || view.PerReplica[0].Unattributed != 1 {
		t.Fatalf("health %s split %+v, want HEALTHY with the row unattributed", view.Health, view.PerReplica[0])
	}
}

// The summaries read the verdict and the counts a view of rows decided as
// each replica published them reads -- the same rows decided at the same
// moments -- when the replicas published at different moments and a row
// stalled between one publish and the read.
func TestRowsDecidedAsPublishedReadTheSummariesVerdict(t *testing.T) {
	const stallAfter = 10 * time.Minute
	snapshots := externalRows(3)
	snapshots[0].TakenAt, snapshots[1].TakenAt = now.Add(-5*time.Second), now.Add(-40*time.Second)
	// Stalled by the read's moment, not by pod-b's publish.
	snapshots[1].Anomalies[0].FailingSince = now.Add(-10*time.Minute - 20*time.Second)
	// Stalled by pod-b's publish too.
	snapshots[1].Anomalies[1].FailingSince = now.Add(-time.Hour)
	// Late by pod-b's publish, and past its own period by the read's moment.
	snapshots[1].Anomalies[2].Wake = &WakeFacts{Known: true, IntervalSeconds: 10, DueAt: snapshots[1].TakenAt.Add(-5 * time.Second)}
	expectation := Expectation{QueryGroups: 949, Known: true}
	service := mustService(t, stubExpectations{expectation: expectation}, stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	rows := rowsAsPublished(expectation, snapshots, replicas(), now, stallAfter)
	summarized, part := service.Summarized(context.Background(), stallAfter)
	whole := service.View(context.Background())
	Decide(&whole, now, stallAfter)
	if OursCount(whole.Anomalies) != 3 || part.Attribution.Ours != 1 {
		t.Fatalf("whole view ours %d, summaries %d: the fixture has to separate rows stalled and overdue by the read from one "+
			"stalled by the publish", OursCount(whole.Anomalies), part.Attribution.Ours)
	}
	stalled := 0
	for _, row := range rows.Anomalies {
		if row.Stalled {
			stalled++
		}
	}
	if rows.Health != summarized.Health || OursCount(rows.Anomalies) != part.Attribution.Ours || stalled != 1 ||
		part.Metrics == nil || part.Metrics.Stalled != stalled {
		t.Fatalf("rows as published: health %s ours %d stalled %d; summaries: health %s ours %d counts %+v", rows.Health,
			OursCount(rows.Anomalies), stalled, summarized.Health, part.Attribution.Ours, part.Metrics)
	}
	sameJSON(t, "each replica's split", rows.PerReplica, summarized.PerReplica)
}

// ownedUnreadable is a store whose summaries read and whose owned lists do
// not.
type ownedUnreadable struct{ summaries []ReplicaSummary }

func (store ownedUnreadable) Load(context.Context, []string) ([]Snapshot, error) { return nil, nil }
func (store ownedUnreadable) LoadSummaries(context.Context, []string) ([]ReplicaSummary, error) {
	return store.summaries, nil
}
func (store ownedUnreadable) LoadOwned(context.Context, []string) (map[string][]string, error) {
	return nil, errors.New("owned lists unreadable")
}

// The service's own read of the owned lists failing is lists not read
// whole: the coverage is not comparable and the note stands without a
// number, though the digests alone say an object is placed wrong.
func TestTheServiceReadsAFailedOwnedListReadAsListsNotWhole(t *testing.T) {
	snapshots, ids := held(true)
	summaries := make([]ReplicaSummary, 0, len(snapshots))
	for _, snapshot := range snapshots {
		summaries = append(summaries, SummaryOf(snapshot, snapshot.OwnedObjects, time.Minute))
	}
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: len(ids), Known: true, IDs: ids}},
		stubRegistry{replicas: replicas()}, ownedUnreadable{summaries: summaries})
	view, _ := service.Summarized(context.Background(), time.Minute)
	if marker := handoverOf(view.Coverage); view.Coverage == nil || view.Coverage.Comparable || marker == nil || marker.Objects != nil {
		t.Fatalf("coverage %+v handover %+v, want an incomparable coverage and a note with no number", view.Coverage, marker)
	}
}
