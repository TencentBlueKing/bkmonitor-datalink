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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func handlerWith(t *testing.T, snapshots []Snapshot, expectation Expectation, replicaList []string) http.Handler {
	t.Helper()
	service := mustService(t, stubExpectations{expectation: expectation}, stubRegistry{replicas: replicaList}, stubSnapshots{snapshots: snapshots})
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func snapshotsWithAnomalies(count int) []Snapshot {
	snapshots := healthySnapshots()
	for index := 0; index < count; index++ {
		item := anomaly(fmt.Sprintf("qg-%03d", index))
		item.Since = now.Add(-time.Duration(index) * time.Minute)
		snapshots[1].Anomalies = append(snapshots[1].Anomalies, item)
	}
	snapshots[1].TotalAnomalies = count
	return snapshots
}

func get(t *testing.T, handler http.Handler, target string) (int, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v (%s)", target, err, response.Body.String())
	}
	return response.Code, body
}

// An endpoint that returns everything by default is fine until the day it is
// needed most, when the list is longest.
func TestListPagesByDefaultAndCapsTheRequestedSize(t *testing.T) {
	handler := handlerWith(t, snapshotsWithAnomalies(120), Expectation{QueryGroups: 949, Known: true}, replicas())

	status, body := get(t, handler, "/api/objects")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	anomalies, _ := body["anomalies"].([]any)
	if len(anomalies) != DefaultPageSize {
		t.Fatalf("default page = %d items, want %d", len(anomalies), DefaultPageSize)
	}
	page, _ := body["page"].(map[string]any)
	if page["total"].(float64) != 120 {
		t.Fatalf("total = %v, want 120", page["total"])
	}

	_, body = get(t, handler, "/api/objects?limit=100000")
	page, _ = body["page"].(map[string]any)
	if page["limit"].(float64) != MaxPageSize {
		t.Fatalf("limit = %v, want the cap %d", page["limit"], MaxPageSize)
	}
}

func TestListRejectsUnusablePaging(t *testing.T) {
	handler := handlerWith(t, healthySnapshots(), Expectation{QueryGroups: 949, Known: true}, replicas())
	for _, target := range []string{"/api/objects?offset=-1", "/api/objects?limit=0", "/api/objects?limit=abc"} {
		status, _ := get(t, handler, target)
		if status != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", target, status)
		}
	}
}

func TestListFiltersByReplicaWithoutChangingTheCoverageArithmetic(t *testing.T) {
	handler := handlerWith(t, snapshotsWithAnomalies(4), Expectation{QueryGroups: 949, Known: true}, replicas())
	_, body := get(t, handler, "/api/objects?replica=pod-a")
	anomalies, _ := body["anomalies"].([]any)
	if len(anomalies) != 0 {
		t.Fatalf("pod-a anomalies = %d, want none", len(anomalies))
	}
	if body["covered"].(float64) != 949 {
		t.Fatalf("covered = %v, want the whole deployment even when filtered", body["covered"])
	}
}

func TestDetailReturnsTheObjectWhenTheViewIsComplete(t *testing.T) {
	handler := handlerWith(t, snapshotsWithAnomalies(3), Expectation{QueryGroups: 949, Known: true}, replicas())
	status, body := get(t, handler, "/api/objects/qg-001")
	if status != http.StatusOK || body["found"] != true {
		t.Fatalf("status = %d body = %+v, want the object", status, body)
	}
	if body["view_complete"] != true {
		t.Fatalf("view_complete = %v, want true", body["view_complete"])
	}
}

// Absent from an incomplete view is not the same as healthy, and the response
// has to let a caller tell those apart.
func TestDetailSaysWhetherNotFoundCanBeTrusted(t *testing.T) {
	ids := make([]string, 949)
	for index := range ids {
		ids[index] = fmt.Sprintf("qg-%03d", index)
	}
	complete := handlerWith(t, snapshotsWithAnomalies(3), Expectation{QueryGroups: 949, Known: true, IDs: ids}, replicas())
	status, body := get(t, complete, "/api/objects/qg-absent")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if body["view_complete"] != true || body["health"] != string(HealthDegraded) {
		t.Fatalf("body = %+v, want a trustworthy not-found", body)
	}

	service := mustService(t,
		stubExpectations{err: errors.New("no active set")},
		stubRegistry{replicas: replicas()},
		stubSnapshots{snapshots: snapshotsWithAnomalies(3)},
	)
	incompleteHandler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	status, body = get(t, incompleteHandler, "/api/objects/qg-absent")
	if status != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", status)
	}
	if body["view_complete"] != false || body["health"] != string(HealthUnknown) || body["existence"] != "unknown" {
		t.Fatalf("body = %+v, want unknown membership", body)
	}
}

func TestHealthEndpointCarriesTheCoverageArithmetic(t *testing.T) {
	service := mustService(t,
		stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()},
		stubSnapshots{snapshots: healthySnapshots()[:1]},
	)
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	status, body := get(t, handler, "/api/health")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if body["health"] != string(HealthUnknown) {
		t.Fatalf("health = %v, want unknown when a replica is missing", body["health"])
	}
	if body["unknown"].(float64) != 449 {
		t.Fatalf("unknown = %v, want the missing replica's objects", body["unknown"])
	}
	if len(body["gaps"].([]any)) == 0 {
		t.Fatal("health response carries no gaps to explain the unknown")
	}
}

func TestNewHandlerRequiresAService(t *testing.T) {
	if _, err := NewHandler(nil, nil, nil, 0, nil, nil, ""); err == nil {
		t.Fatal("handler was built without a service")
	}
}

// The coverage numbers stay deployment-wide even when the anomaly list is
// filtered, so a name belonging to no replica would otherwise answer with an
// empty list beside a full-coverage HEALTHY verdict: a green tile for something
// that does not exist. A typo has to be refused, not answered.
func TestFilteringByAReplicaThatDoesNotExistIsRefused(t *testing.T) {
	handler := handlerWith(t, healthySnapshots(), Expectation{QueryGroups: 949, Known: true}, replicas())
	status, body := get(t, handler, "/api/objects?replica=pod-typo")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d body = %+v, want the unknown replica refused", status, body)
	}
}

// A replica that published nothing is still part of the deployment, and asking
// about it is a legitimate question whose answer is "it reported nothing".
func TestFilteringByAReplicaThatPublishedNothingIsAnswered(t *testing.T) {
	handler := handlerWith(t, healthySnapshots()[:1], Expectation{QueryGroups: 949, Known: true}, replicas())
	status, body := get(t, handler, "/api/objects?replica=pod-b")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want the missing replica to be a valid subject", status)
	}
	if body["replica"] != "pod-b" {
		t.Fatalf("replica = %v, want the filter echoed so the response cannot be read as deployment-wide", body["replica"])
	}
}

// The number that sizes an incident is how many anomalies exist, not how many
// survived truncation. Reporting the retained count as the total understates it
// by exactly the amount that made it worth reporting.
func TestTruncatedListsStillReportTheRealAnomalyCount(t *testing.T) {
	snapshots := snapshotsWithAnomalies(40)
	snapshots[1].TotalAnomalies = 900
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	status, body := get(t, handler, "/api/objects")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if body["anomalies_total"].(float64) != 900 {
		t.Fatalf("anomalies_total = %v, want the count the replicas actually had", body["anomalies_total"])
	}
	page := body["page"].(map[string]any)
	if page["total"].(float64) != 40 {
		t.Fatalf("page total = %v, want what the response can page over", page["total"])
	}
}

// The counts describe the whole list this request is about, not the page that
// happened to be returned. Summarising the page would answer "is this one
// problem or many" with whatever fits on screen, which is the opposite of what
// the question is for.
func TestSummaryCountsTheWholeListRatherThanThePage(t *testing.T) {
	handler := handlerWith(t, snapshotsWithAnomalies(120), Expectation{QueryGroups: 949, Known: true}, replicas())
	status, body := get(t, handler, "/api/objects?limit=5")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	anomalies, _ := body["anomalies"].([]any)
	if len(anomalies) != 5 {
		t.Fatalf("page = %d rows, want the 5 that were asked for", len(anomalies))
	}
	summary, _ := body["summary"].(map[string]any)
	byKind := topOf(summary["by_kind"])
	if len(byKind) != 1 {
		t.Fatalf("by_kind = %+v, want one kind", byKind)
	}
	if got := byKind[0].(map[string]any)["count"].(float64); got != 120 {
		t.Fatalf("kind count = %v, want all 120 rather than the 5 on the page", got)
	}
}

// A filtered request is about the filtered list, so its counts must be too.
func TestSummaryFollowsTheReplicaFilter(t *testing.T) {
	handler := handlerWith(t, snapshotsWithAnomalies(4), Expectation{QueryGroups: 949, Known: true}, replicas())
	_, body := get(t, handler, "/api/objects?replica=pod-a")
	summary, _ := body["summary"].(map[string]any)
	if byReplica := topOf(summary["by_replica"]); len(byReplica) != 0 {
		t.Fatalf("by_replica = %+v, want nothing: pod-a reported no anomalies", byReplica)
	}
}

func strategyAnomaly(queryGroup, strategyID, businessID string) Anomaly {
	item := anomaly(queryGroup)
	item.Strategies = []StrategyRef{{StrategyID: strategyID, BusinessID: businessID}}
	return item
}

func handlerWithStrategies(t *testing.T) http.Handler {
	t.Helper()
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{
		strategyAnomaly("qg-a", "2864", "7"),
		strategyAnomaly("qg-b", "8904", "47"),
		strategyAnomaly("qg-c", "1449", "7"),
	}
	snapshots[1].TotalAnomalies = 3
	return handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
}

func TestObjectsCanBeNarrowedByStrategyAndBusiness(t *testing.T) {
	handler := handlerWithStrategies(t)
	for target, want := range map[string]int{
		"/api/objects?strategy=2864": 1,
		"/api/objects?business=7":    2,
		"/api/objects":               3,
	} {
		_, body := get(t, handler, target)
		anomalies, _ := body["anomalies"].([]any)
		if len(anomalies) != want {
			t.Fatalf("%s returned %d objects, want %d", target, len(anomalies), want)
		}
	}
}

// Filtering happens before the counts are taken, so a narrowed response is
// summarised as the narrowed thing it is.
func TestSummaryFollowsTheStrategyFilter(t *testing.T) {
	handler := handlerWithStrategies(t)
	_, body := get(t, handler, "/api/objects?business=7")
	summary, _ := body["summary"].(map[string]any)
	byKind := topOf(summary["by_kind"])
	if len(byKind) != 1 || byKind[0].(map[string]any)["count"].(float64) != 2 {
		t.Fatalf("by_kind = %+v, want the two objects of business 7", byKind)
	}
}

// A strategy with no anomalies is the ordinary answer to a reasonable question,
// unlike an unknown replica, so it is answered rather than refused. The response
// has to say a filter was applied, or an empty table reads as "nothing is wrong
// anywhere".
func TestAFilterThatMatchesNothingIsAnsweredAndMarked(t *testing.T) {
	handler := handlerWithStrategies(t)
	status, body := get(t, handler, "/api/objects?strategy=999999")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want the empty answer rather than a refusal", status)
	}
	if anomalies, _ := body["anomalies"].([]any); len(anomalies) != 0 {
		t.Fatalf("anomalies = %+v, want none", anomalies)
	}
	if body["filtered"] != true || body["strategy"] != "999999" {
		t.Fatalf("body = %+v, want the applied filter echoed", body)
	}
}

func handlerWithStallBudget(t *testing.T, snapshots []Snapshot, budget time.Duration) http.Handler {
	t.Helper()
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	handler, err := NewHandler(service, nil, func() time.Time { return now }, budget, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

// agedAnomaly is an object that has been anomalous for age. When the reason is
// an execution that did not finish, the object has also been failing to finish
// for the whole of that age; use failingFor to give the two clocks different
// values.
func agedAnomaly(queryGroup, reason string, age time.Duration) Anomaly {
	item := anomaly(queryGroup)
	item.Kind = "DEGRADED_RUN"
	item.ReasonCode = reason
	item.Since = now.Add(-age)
	if failedExecution(reason) {
		item.FailingSince = item.Since
	}
	return item
}

func failingFor(item Anomaly, age time.Duration) Anomaly {
	item.FailingSince = now.Add(-age)
	return item
}

// A degraded round still ends and moves the cursor; a round that never ends
// leaves the object replaying the same evaluation forever. Both show the same
// kind and a long duration, so without this flag the list cannot separate the
// object that is recovering slowly from the one that has stopped entirely.
func TestListFlagsObjectsWhoseRoundsStoppedFinishing(t *testing.T) {
	budget := 10 * time.Minute
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{
		agedAnomaly("stuck", "error", 2*time.Hour),
		agedAnomaly("failing-briefly", "error", 2*time.Minute),
		agedAnomaly("degraded-for-hours", "COMPLETED_WITH_UNAVAILABLE", 2*time.Hour),
		failingFor(agedAnomaly("degraded-for-hours-then-retrying", "retrying", 2*time.Hour), 2*time.Minute),
	}
	snapshots[1].TotalAnomalies = len(snapshots[1].Anomalies)

	status, body := get(t, handlerWithStallBudget(t, snapshots, budget), "/api/objects")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	stalled := map[string]bool{}
	for _, raw := range body["anomalies"].([]any) {
		item := raw.(map[string]any)
		flag, _ := item["stalled"].(bool)
		stalled[item["query_group"].(string)] = flag
	}
	if !stalled["stuck"] {
		t.Fatal("an object failing to finish for two hours past a ten minute budget is not flagged")
	}
	if stalled["failing-briefly"] {
		t.Fatal("an object inside the budget is flagged, which makes every transient failure look permanent")
	}
	if stalled["degraded-for-hours"] {
		t.Fatal("a completed degraded round is flagged; that Slot ended and the cursor moved")
	}
	if stalled["degraded-for-hours-then-retrying"] {
		t.Fatal("two minutes of not finishing is flagged because the object was degraded for hours before; the stall clock is not the anomaly clock")
	}
	summary := body["summary"].(map[string]any)
	if summary["stalled"].(float64) != 1 {
		t.Fatalf("summary stalled = %v, want 1", summary["stalled"])
	}
	if body["stall_after_seconds"].(float64) != budget.Seconds() {
		t.Fatalf("stall_after_seconds = %v, want %v", body["stall_after_seconds"], budget.Seconds())
	}
}

// Stalling is marked on every column before the first screen is drawn, so an
// object in the demoted pool that stopped ending rounds is on the
// ROUNDS_STALLED line and counted in the summary whichever column the
// request serves -- and the line is the same one the metric exports.
func TestAStalledObjectInTheDemotedPoolIsOnTheLine(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[1].Demoted = []Anomaly{agedAnomaly("demoted-stuck", "error", 2*time.Hour)}
	snapshots[1].TotalDemoted = 1

	status, body := get(t, handlerWithStallBudget(t, snapshots, 10*time.Minute), "/api/objects")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	var stalledLine map[string]any
	for _, entry := range body["checks"].([]any) {
		report := entry.(map[string]any)
		if report["code"] == string(CheckRoundsStalled) {
			stalledLine = report
		}
	}
	if stalledLine == nil || stalledLine["current"].(float64) != 1 {
		t.Fatalf("ROUNDS_STALLED line = %v, want the demoted object on it", stalledLine)
	}
	// The served column is the anomaly list, which is empty; the deployment
	// count is across every column.
	if body["stalled_total"].(float64) != 1 {
		t.Fatalf("stalled_total = %v, want the demoted object counted", body["stalled_total"])
	}
}

// A deployment that wired no budget has no basis for the claim, and a flag
// asserted without one would label every long-running failure as unrecoverable.
func TestListWithoutAStallBudgetFlagsNothing(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{agedAnomaly("stuck", "error", 30*24*time.Hour)}
	snapshots[1].TotalAnomalies = 1

	_, body := get(t, handlerWithStallBudget(t, snapshots, 0), "/api/objects")
	item := body["anomalies"].([]any)[0].(map[string]any)
	if flag, ok := item["stalled"].(bool); ok && flag {
		t.Fatal("stalled is asserted without a budget to assert it against")
	}
	if _, present := body["stall_after_seconds"]; present {
		t.Fatal("a zero budget is reported as if it were one")
	}
	if body["summary"].(map[string]any)["stalled"].(float64) != 0 {
		t.Fatal("summary counts a stall that was never established")
	}
}

// A filter narrows the table; it must not narrow the one number that says
// somebody has to intervene. Stalled objects do not recover on their own, so a
// filtered view reporting zero of them reads as "nothing to do here" while four
// of them sit outside the filter.
func TestTheStalledTotalSurvivesAFilter(t *testing.T) {
	budget := 10 * time.Minute
	snapshots := healthySnapshots()
	stuck := agedAnomaly("stuck", "error", 2*time.Hour)
	stuck.Strategies = []StrategyRef{{StrategyID: "8568", BusinessID: "7"}}
	other := agedAnomaly("also-stuck", "error", 2*time.Hour)
	other.Strategies = []StrategyRef{{StrategyID: "2849", BusinessID: "7"}}
	snapshots[1].Anomalies = []Anomaly{stuck, other}
	snapshots[1].TotalAnomalies = 2
	handler := handlerWithStallBudget(t, snapshots, budget)

	_, body := get(t, handler, "/api/objects")
	if body["stalled_total"].(float64) != 2 {
		t.Fatalf("unfiltered stalled_total = %v, want 2", body["stalled_total"])
	}

	_, body = get(t, handler, "/api/objects?strategy=8568")
	if body["page"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("the filter did not narrow the table: %v", body["page"])
	}
	// The filtered count belongs in the summary with everything else...
	if body["summary"].(map[string]any)["stalled"].(float64) != 1 {
		t.Fatalf("summary stalled = %v, want the filtered count", body["summary"])
	}
	// ...and the deployment-wide one has to stay reachable beside it.
	if body["stalled_total"].(float64) != 2 {
		t.Fatalf("filtered stalled_total = %v, want the deployment total 2", body["stalled_total"])
	}
}

// The capacity panel shipped blank because the page never called its renderer.
// The static page guard catches that half; this catches the other one -- a
// panel can also render empty because the response it reads never carried the
// data. Both halves have to be closed, or the next blank section is only found
// by looking at production.
func TestHealthResponseCarriesCapacityWhenReplicasReportIt(t *testing.T) {
	snapshots := healthySnapshots()
	for index := range snapshots {
		snapshots[index].Capacity = &Capacity{
			PermitsHeld: 3, PermitBudget: 32, PermitSeconds: 120, Waiting: 0,
			MemoryUsed: 1 << 20, MemoryLimit: 8 << 30, MemorySource: "pod_limit", CPUCores: 8,
			Budgets: map[string]uint64{"state_mutations": 65536},
		}
	}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 2, Known: true}, []string{"pod-a", "pod-b"})

	body := requestJSON(t, handler, "/api/health")
	capacity, ok := body["capacity"].(map[string]any)
	if !ok {
		t.Fatalf("health response carried no capacity, so the panel reading it renders empty: %v", body)
	}
	// The numbers the panel actually shows. A capacity block present but empty
	// would still leave a heading over nothing.
	for _, field := range []string{"replicas", "permits_held", "permit_budget", "memory_limit_bytes"} {
		if capacity[field] == nil {
			t.Fatalf("capacity is missing %q, which the panel renders: %v", field, capacity)
		}
	}
	if capacity["permit_budget"].(float64) != 32 {
		t.Fatalf("a per-replica ceiling was summed: %v", capacity["permit_budget"])
	}
}

// The verdict response names the build each counted replica runs. It is the
// first thing to establish about any number on the page after a release, and
// before this field it took a PromQL query per pod.
func TestHealthResponseCarriesTheBuildsTheReplicasRun(t *testing.T) {
	snapshots := healthySnapshots()
	newer := BuildFacts{Version: "0.2.4506", Commit: "62ee924d", SchemaVersion: "v3"}
	older := BuildFacts{Version: "0.2.4505", Commit: "4f338bf3", SchemaVersion: "v3"}
	snapshots[0].Build = &newer
	snapshots[1].Build = &older
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 2, Known: true}, []string{"pod-a", "pod-b"})

	body := requestJSON(t, handler, "/api/health")
	builds, ok := body["builds"].([]any)
	if !ok || len(builds) != 2 {
		t.Fatalf("health response carried builds %v, want two groups, one per build", body["builds"])
	}
	versions := map[string]int{}
	for _, entry := range builds {
		group := entry.(map[string]any)
		versions[group["build"].(map[string]any)["version"].(string)] = len(group["replicas"].([]any))
	}
	if versions["0.2.4506"] != 1 || versions["0.2.4505"] != 1 {
		t.Fatalf("builds = %v, want one replica under each version", versions)
	}
	// And per replica, where the row that shows it reads it.
	for _, entry := range body["per_replica"].([]any) {
		replica := entry.(map[string]any)
		if replica["build"] == nil {
			t.Fatalf("replica %v carries no build on the verdict response", replica["replica"])
		}
	}
}

// The verdict response names the standings it was decided on and the
// leader's activation. Both decided DEGRADED before they were on this
// response, and the page's one sentence then named the object list.
func TestHealthResponseCarriesTheStandingsTheVerdictIsDecidedOn(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].Activation = &ActivationFacts{Applied: "bdc6ffcb", Published: "e7a1b2c3", Behind: true,
		BehindBeyondBound: true, ConsecutiveFailures: 120, FailureStage: "schedule_cutover", FailureClass: "schedule_conflict"}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, []string{"pod-a", "pod-b"})

	body := requestJSON(t, handler, "/api/health")
	if body["health"] != "DEGRADED" {
		t.Fatalf("health = %v, want DEGRADED from the standing alone", body["health"])
	}
	degradations, ok := body["degradations"].([]any)
	if !ok || len(degradations) != 1 || degradations[0].(map[string]any)["kind"] != "ACTIVATION_BEHIND" {
		t.Fatalf("degradations = %v, want the one ACTIVATION_BEHIND", body["degradations"])
	}
	activation, ok := body["activation"].(map[string]any)
	if !ok || activation["applied"] != "bdc6ffcb" || activation["published"] != "e7a1b2c3" || activation["behind_beyond_bound"] != true {
		t.Fatalf("activation = %v, want the leader's facts whole", body["activation"])
	}
	if body["activation_replica"] != "pod-a" {
		t.Fatalf("activation_replica = %v, want pod-a", body["activation_replica"])
	}

	// Nothing degrading: an empty list and an explicit null, both present, so
	// "no standing" and "this build has no such field" read differently.
	plain := requestJSON(t, handlerWith(t, healthySnapshots(), Expectation{QueryGroups: 949, Known: true}, []string{"pod-a", "pod-b"}), "/api/health")
	if list, present := plain["degradations"].([]any); !present || len(list) != 0 {
		t.Fatalf("degradations with none = %v, want []", plain["degradations"])
	}
	if value, present := plain["activation"]; !present || value != nil {
		t.Fatalf("activation with no attempt = present=%v value=%v, want explicit null", present, value)
	}
}

// The leader's rebalance round reaches both routes: the verdict route,
// whole and beside the replica table whose counts it judged, and the
// objects route as the third standing with the round on the line. The
// split does not degrade the verdict -- it is a line to act on, not a bound
// passed -- and a follower-only deployment carries none.
func TestBothRoutesCarryTheOwnershipSplit(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].Rebalance = &RebalanceFacts{PlannedAt: now.Add(-10 * time.Second), ReadyWorkers: 2, Assigned: 949, Target: 474,
		MostOwned: 949, LeastOwned: 0, MostOwnedBy: "pod-a", LeastOwnedBy: "pod-b", Batch: 9, PlannedMoves: 9, StopSpreadPercent: 5, Shadow: true}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, []string{"pod-a", "pod-b"})

	body := requestJSON(t, handler, "/api/health")
	if body["health"] != "HEALTHY" {
		t.Fatalf("health = %v, want HEALTHY: the split is a line, not a degradation", body["health"])
	}
	rebalance, ok := body["rebalance"].(map[string]any)
	if !ok || rebalance["planned_moves"] != 9.0 || rebalance["most_owned_by"] != "pod-a" || rebalance["shadow"] != true ||
		rebalance["stop_spread_percent"] != 5.0 || body["rebalance_replica"] != "pod-a" {
		t.Fatalf("rebalance = %v from %v, want the leader's round whole", body["rebalance"], body["rebalance_replica"])
	}
	load := body["load"].(map[string]any)
	bottleneck := load["bottleneck"].(map[string]any)
	if skew, ok := bottleneck["skew"].(map[string]any); !ok || skew["most_owned"] != 949.0 {
		t.Fatalf("load.bottleneck.skew = %v, want the round on the reading", bottleneck["skew"])
	}

	_, objects := get(t, handler, "/api/objects?limit=1")
	var line map[string]any
	for _, check := range objects["checks"].([]any) {
		if check.(map[string]any)["code"] == string(CheckOwnershipSkewed) {
			line = check.(map[string]any)
		}
	}
	if line == nil || line["owner"] != string(OwnerAlarmd) || line["replica"] != "pod-a" {
		t.Fatalf("objects checks carry no OWNERSHIP_SKEWED line of ours from pod-a: %v", objects["checks"])
	}
	if round, ok := line["rebalance"].(map[string]any); !ok || round["planned_moves"] != 9.0 || round["least_owned_by"] != "pod-b" {
		t.Fatalf("OWNERSHIP_SKEWED line = %v, want the round on it", line)
	}
	groups := line["groups"].([]any)
	if len(groups) != 1 || groups[0].(map[string]any)["key"] != "pod-a" {
		t.Fatalf("OWNERSHIP_SKEWED groups = %v, want one on the loaded replica", groups)
	}
	if replicas := groups[0].(map[string]any)["replicas"].([]any); len(replicas) != 2 || replicas[0] != "pod-a" || replicas[1] != "pod-b" {
		t.Fatalf("OWNERSHIP_SKEWED group replicas = %v, want the pair", groups[0])
	}
	todo := objects["todo"].(map[string]any)
	if todo["checks"] != 1.0 || todo["objects"] != 0.0 {
		t.Fatalf("todo = %v, want the split as one line of ours over no objects", todo)
	}

	// No replica planned a round: absent on the verdict route, no line on
	// the objects route.
	plain := handlerWith(t, healthySnapshots(), Expectation{QueryGroups: 949, Known: true}, []string{"pod-a", "pod-b"})
	if value, present := requestJSON(t, plain, "/api/health")["rebalance"]; present && value != nil {
		t.Fatalf("rebalance with no round = %v, want absent", value)
	}
	_, none := get(t, plain, "/api/objects?limit=1")
	for _, check := range none["checks"].([]any) {
		if check.(map[string]any)["code"] == string(CheckOwnershipSkewed) {
			t.Fatalf("a deployment with no round has the split line: %v", check)
		}
	}
}

// The objects response carries the first screen's arithmetic, computed once
// on the server: the page adding lines up counted past records as work and
// an object under two lines twice.
func TestObjectsResponseCarriesTheTodoArithmetic(t *testing.T) {
	handler := handlerWith(t, snapshotsWithAnomalies(3), Expectation{QueryGroups: 949, Known: true}, replicas())
	_, body := get(t, handler, "/api/objects?limit=1")
	todo, ok := body["todo"].(map[string]any)
	if !ok {
		t.Fatalf("objects response carries no todo: %v", body)
	}
	if todo["checks"].(float64) < 1 || todo["objects"].(float64) != 3 {
		t.Fatalf("todo = %v, want at least one line and the 3 distinct anomalous objects", todo)
	}
	for _, field := range []string{"retained", "retained_last_hour", "governance", "governance_objects"} {
		if _, present := todo[field]; !present {
			t.Fatalf("todo is missing %q, which the first screen reads: %v", field, todo)
		}
	}
}

// A deployment whose replicas report no capacity must say so rather than
// omitting the block, because the page tells those apart and an operator
// reading "no replica reported capacity" is being told something true.
func TestHealthResponseOmitsCapacityWhenNobodyReportedAny(t *testing.T) {
	handler := handlerWith(t, healthySnapshots(), Expectation{QueryGroups: 2, Known: true}, []string{"pod-a", "pod-b"})
	body := requestJSON(t, handler, "/api/health")
	// Present and null, not absent: the field always travels so a reader can
	// tell "no replica reported capacity" from "this build has no such field".
	if value, present := body["capacity"]; !present || value != nil {
		t.Fatalf("capacity should be an explicit null when nobody reported any, got present=%v value=%v",
			present, value)
	}
}

// A category groups conditions that call for opposite responses: two objects
// both classified "evaluation" can be a dependency that broke and a result that
// failed validation, and the category alone cannot tell an operator which. The
// code can, and it is already on every anomaly -- it just was not counted
// anywhere, so answering "which of these was it" meant tallying the list by
// hand. That matters most for a fault that heals itself, because the codes
// disappear with the anomalies that carried them.
func TestSummaryCountsFailureCodesAndNotJustCategories(t *testing.T) {
	snapshots := healthySnapshots()
	for _, shape := range []struct{ queryGroup, category, code string }{
		{"qg-a", "evaluation", "EVALUATION_FAILED"},
		{"qg-b", "evaluation", "EVALUATION_FAILED"},
		{"qg-c", "evaluation", "EVALUATION_RESULT_INVALID"},
		{"qg-d", "source_backend", "QUERY_UNAVAILABLE"},
	} {
		item := anomaly(shape.queryGroup)
		item.Failure = &FailureRef{Stage: "stream_complete", Category: shape.category, Code: shape.code}
		snapshots[1].Anomalies = append(snapshots[1].Anomalies, item)
	}
	snapshots[1].TotalAnomalies = 4

	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, body := get(t, handler, "/api/objects")
	summary, _ := body["summary"].(map[string]any)

	counts := map[string]float64{}
	byCode := topOf(summary["by_failure_code"])
	for _, entry := range byCode {
		row, _ := entry.(map[string]any)
		counts[row["value"].(string)] = row["count"].(float64)
	}
	if counts["EVALUATION_FAILED"] != 2 || counts["EVALUATION_RESULT_INVALID"] != 1 {
		t.Fatalf("by_failure_code = %+v, want the two evaluation codes separated 2 to 1", counts)
	}
	if counts["QUERY_UNAVAILABLE"] != 1 {
		t.Fatalf("by_failure_code lost a code outside the largest category: %+v", counts)
	}
	// The category counts must be unchanged: the code is an extra level, not a
	// replacement, and a reader comparing the two needs both to still add up.
	byCategory := topOf(summary["by_failure"])
	total := 0.0
	for _, entry := range byCategory {
		total += entry.(map[string]any)["count"].(float64)
	}
	if total != 4 {
		t.Fatalf("by_failure now totals %v, want the same 4 anomalies it always did", total)
	}
}

// The field always travels, so a reader can tell "no anomaly carried a code"
// from "this build does not report codes". An absent key reads as the second
// and would send someone to check the deployment's version instead of its data.
func TestSummaryReportsAnEmptyFailureCodeListRatherThanOmittingIt(t *testing.T) {
	handler := handlerWith(t, healthySnapshots(), Expectation{QueryGroups: 949, Known: true}, replicas())
	_, body := get(t, handler, "/api/objects")
	summary, _ := body["summary"].(map[string]any)
	if _, present := summary["by_failure_code"]; !present {
		t.Fatal("by_failure_code is absent, which reads as a build that cannot report codes")
	}
}

// A replica publishes at most DefaultMaxAnomalies of its own anomalies, so a
// bad enough deployment summarises a sample. The counts still answer "which of
// these is it" and no longer give a distribution -- and the reader who most
// needs the distribution is looking at exactly the deployment that truncated.
// The evidence was already in the response and required comparing two other
// fields to notice, which is an inference nobody makes.
func TestSummarySaysWhenItCountedATruncatedList(t *testing.T) {
	snapshots := healthySnapshots()
	for index := 0; index < 3; index++ {
		snapshots[1].Anomalies = append(snapshots[1].Anomalies, anomaly(fmt.Sprintf("qg-%d", index)))
	}
	// The replica held back more than it published, which is what truncation is.
	snapshots[1].TotalAnomalies = 250

	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, body := get(t, handler, "/api/objects")
	summary, _ := body["summary"].(map[string]any)
	if partial, _ := summary["partial"].(bool); !partial {
		t.Fatalf("summary counted 3 of 250 anomalies without saying so: %+v", summary)
	}
}

// On a deployment that published everything the flag must stay off, or it
// becomes a warning that is always on and therefore never read.
func TestSummaryIsNotMarkedPartialWhenNothingWasTruncated(t *testing.T) {
	handler := handlerWith(t, snapshotsWithAnomalies(4), Expectation{QueryGroups: 949, Known: true}, replicas())
	_, body := get(t, handler, "/api/objects?limit=2")
	summary, _ := body["summary"].(map[string]any)
	if partial, _ := summary["partial"].(bool); partial {
		t.Fatal("paging a complete list was reported as a truncated sample")
	}
}

// topOf reads a Distribution's ranked head out of a decoded response. The
// grouped counts are objects rather than bare lists because they carry the
// distinct count and the tail beside them -- a top-five over five groups and a
// top-five over five thousand are not the same answer, and the list alone
// cannot say which it is.
func topOf(field any) []any {
	group, _ := field.(map[string]any)
	top, _ := group["top"].([]any)
	return top
}

// The verdict panel has to carry the numbers the verdict is decided on.
//
// A live deployment read UNKNOWN with every number on that panel at zero: no
// coverage gaps, unknown-state zero, and all five columns adding up to the
// expected total. The verdict was UNKNOWN because some anomalies carried no
// cause -- a per-object hole, decided by a count that had no field on this
// response at all. There was nothing on the page a reader could use to reach
// the right conclusion, or to tell the page was not simply wrong.
func TestHealthEndpointCarriesTheNumbersItsVerdictIsDecidedOn(t *testing.T) {
	// Two objects that went wrong, one of which was restored without its
	// cause. That one, and nothing else, makes the verdict UNKNOWN.
	restored := anomaly("qg-restored")
	restored.Kind = KindDegradedRun
	restored.SinceFrom = SinceRestoredLastFull
	external := anomaly("qg-external")
	external.Kind = KindDegradedRun
	external.CauseReason = "QUERY_TIMEOUT"

	snapshots := healthySnapshots()
	snapshots[1].Anomalies = []Anomaly{restored, external}
	snapshots[1].TotalAnomalies = 2

	service := mustService(t,
		stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()},
		stubSnapshots{snapshots: snapshots},
	)
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	status, body := get(t, handler, "/api/health")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	if body["health"] != string(HealthUnknown) {
		t.Fatalf("health = %v, want UNKNOWN: one anomaly carries no cause", body["health"])
	}
	if gaps, _ := body["gaps"].([]any); len(gaps) != 0 {
		t.Fatalf("gaps = %v, want none -- this test is about the other way into UNKNOWN, and with "+
			"a gap present it would pass without checking it", gaps)
	}
	if body["unknown"].(float64) != 0 {
		t.Fatalf("unknown = %v, want 0: the state column is a different question and a reader who "+
			"sees it non-zero has an explanation already", body["unknown"])
	}
	value, sent := body["unattributed"]
	if !sent {
		t.Fatal("the health response does not carry unattributed: the verdict is decided on it, " +
			"and with every other number at zero the page can say nothing about why")
	}
	if value.(float64) != 1 {
		t.Fatalf("unattributed = %v, want the one object whose cause was not recorded", value)
	}
	if body["ours"].(float64) != 0 {
		t.Fatalf("ours = %v, want 0 -- that is what makes the verdict UNKNOWN rather than DEGRADED",
			body["ours"])
	}
}

// The lost spans reach the response, longest first, with a span and no Slot
// count.
//
// Dropped anywhere on the way here they arrive as nothing, and nothing is the
// reading that says no object ever lost a span -- which is what the page said
// for as long as the event could not reach it at all.
func TestHealthResponseCarriesTheSpansNothingEverEvaluated(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].PrunedSkips = map[string]PrunedSkip{
		"qg-short": {From: 120, To: 300, At: now.Add(-time.Hour)},
		"qg-long":  {From: 120, To: 5520, At: now.Add(-time.Minute), DiscardedSlot: 180},
	}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 2, Known: true}, []string{"pod-a", "pod-b"})

	body := requestJSON(t, handler, "/api/health")
	listed, ok := body["pruned_skips"].([]any)
	if !ok || len(listed) != 2 {
		t.Fatalf("health response carried %v for pruned_skips, want both lost spans; absent, the "+
			"page can only say nothing was ever skipped", body["pruned_skips"])
	}
	first, _ := listed[0].(map[string]any)
	if first["query_group"] != "qg-long" {
		t.Fatalf("first entry is %v, want the longest span first: the length is how much detection "+
			"was lost and the only thing here that ranks", first["query_group"])
	}
	if first["span_seconds"].(float64) != 5400 {
		t.Fatalf("span = %v, want 5400 seconds", first["span_seconds"])
	}
	// No Slot count, at any depth. The number is not knowable and an invented
	// one reads exactly like a measured one.
	for _, field := range []string{"count", "slots", "slot_count"} {
		if _, present := first[field]; present {
			t.Fatalf("the response carries %q for a span whose Slots cannot be counted: %v", field, first)
		}
	}
}

// On the route: a demoted object's record rides on its refusal line as that
// line's consequence, a loss in progress is current on the record line and
// on the to-do arithmetic, a stopped loss is the record, and the window all
// of it was decided on travels with the numbers -- so the page prints what
// the server decided, and neither reads "曾经漏检、不用处理" over a loss
// still happening or "容量限制" over a refusal.
func TestTheRouteTellsTheThreeKindsOfLossApart(t *testing.T) {
	snapshots := healthySnapshots()
	refused := anomaly("qg-refused")
	refused.Kind = KindQueryCooldown
	refused.Failure = &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "response=status_space_table_id_field_is_not_exists"}
	snapshots[1].Demoted = []Anomaly{refused}
	snapshots[1].TotalDemoted = 1
	snapshots[1].GapSkips = map[string]SkippedSpan{
		"qg-refused": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: now.Add(-2 * time.Minute), Replica: "pod-b"},
		"qg-losing":  {FirstSlot: 1, LastSlot: 3, Slots: 3, At: now.Add(-3 * time.Minute), Replica: "pod-b"},
		"qg-stopped": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: now.Add(-time.Hour), Replica: "pod-b"},
	}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())

	status, body := get(t, handler, "/api/objects")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %v", status, body)
	}
	byCode := map[string]map[string]any{}
	for _, entry := range body["checks"].([]any) {
		report := entry.(map[string]any)
		byCode[report["code"].(string)] = report
	}
	target := byCode[string(CheckQueryTargetMissing)]
	consequence, _ := target["consequence"].(map[string]any)
	if consequence == nil || consequence["skipped"].(float64) != 1 || consequence["skipped_recent"].(float64) != 1 {
		t.Fatalf("QUERY_TARGET_MISSING = %v, want its consequence: 1 skipped, 1 within the window", target)
	}
	abandoned := byCode[string(CheckDetectionAbandoned)]
	if abandoned["current"].(float64) != 1 || abandoned["retained"].(float64) != 1 || abandoned["group_by"] != string(GroupByLoss) {
		t.Fatalf("DETECTION_ABANDONED = %v, want 1 current, 1 retained, folded on loss", abandoned)
	}
	todo := body["todo"].(map[string]any)
	for field, want := range map[string]float64{
		"checks": 1, "objects": 1, "undetermined": 0, "undetermined_objects": 0,
		"ongoing": 1, "while_demoted": 1, "while_demoted_recent": 1, "retained": 1,
		"recent_window_seconds": RecentSkipWindow.Seconds(), "governance": 1, "governance_objects": 1,
	} {
		if got, _ := todo[field].(float64); got != want {
			t.Errorf("todo.%s = %v, want %v (todo = %v)", field, todo[field], want, todo)
		}
	}
	// Opening the refusal line: the object's row carries its record and what
	// it is; opening the record line by kind lists the one in progress.
	_, rows := get(t, handler, "/api/objects?check="+string(CheckQueryTargetMissing))
	row := rows["anomalies"].([]any)[0].(map[string]any)
	if row["skip"] == nil || row["loss"] != string(LossWhileDemoted) {
		t.Fatalf("the refused object's row = %v, want its record and WHILE_DEMOTED", row)
	}
	_, ongoing := get(t, handler, "/api/objects?check="+string(CheckDetectionAbandoned)+"&group="+string(LossOngoing))
	if list := ongoing["anomalies"].([]any); len(list) != 1 || list[0].(map[string]any)["query_group"] != "qg-losing" {
		t.Fatalf("under DETECTION_ABANDONED/ONGOING = %v, want the one object losing rounds now", list)
	}
}

// The health route carries the operating judgment, decided from the same
// view as the numbers under it, with the limit that nothing estimates
// headroom always on it.
func TestTheHealthRouteCarriesTheOperatingJudgment(t *testing.T) {
	snapshots := healthySnapshots()
	earlier := 2
	snapshots[0].Schedule = &ScheduleCensus{Overdue: 5, Completed1h: 100, OnTime1h: 80, Completed6h: 600, OnTime6h: 590,
		OverdueAgo: &earlier, OverdueAgoSeconds: 3000}
	snapshots[1].Schedule = &ScheduleCensus{Overdue: 4, Completed1h: 100, OnTime1h: 80, Completed6h: 600, OnTime6h: 590,
		OverdueAgo: &earlier, OverdueAgoSeconds: 3600}
	snapshots[0].Capacity = &Capacity{PermitAcquires: 1000, PermitWaits: 800}
	status, body := get(t, handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas()), "/api/health")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %v", status, body)
	}
	load, _ := body["load"].(map[string]any)
	if load == nil {
		t.Fatalf("health carries no load: %v", body)
	}
	onTime := load["on_time"].(map[string]any)
	backlog := load["backlog"].(map[string]any)
	if onTime["state"] != string(OnTimeFallingBehind) || onTime["overdue"].(float64) != 9 {
		t.Errorf("on_time = %v, want FALLING_BEHIND over 9 overdue (80%% this hour against 98%% over six)", onTime)
	}
	if backlog["state"] != string(BacklogGrowing) || backlog["earlier"].(float64) != 4 || backlog["span_seconds"].(float64) != 3000 {
		t.Errorf("backlog = %v, want GROWING: 9 now against 4 over the shorter span of 3000 s", backlog)
	}
	limits := map[string]bool{}
	for _, limit := range load["limits"].([]any) {
		limits[limit.(string)] = true
	}
	if !limits[string(LimitNoHeadroomEstimate)] || !limits[string(LimitTrendSpan)] {
		t.Errorf("limits = %v, want no headroom estimate and the short trend span", load["limits"])
	}
}

// The list response sends the rows it is about and not the others: the
// served column paged, the other columns and the retained records empty,
// with every total, line and count still taken from the whole view. They
// used to ride along whole on every refresh -- 177 KB of demoted rows under
// a 50-row page at the size of the deployment this page is read on -- on a
// request the page makes for the lines alone.
func TestTheListResponseSendsOnlyTheRowsItIsAbout(t *testing.T) {
	snapshots := columnSnapshots()
	snapshots[1].GapSkips = map[string]SkippedSpan{
		"qg-stopped": {FirstSlot: 1, LastSlot: 3, Slots: 3, At: now.Add(-time.Hour), Replica: "pod-b"}}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())

	_, body := get(t, handler, "/api/objects?limit=1")
	for _, column := range []string{"demoted", "undecidable", "by_design", "no_data"} {
		if rows, _ := body[column].([]any); len(rows) != 0 {
			t.Errorf("%s carries %d rows on the anomaly page, want none", column, len(rows))
		}
	}
	for _, records := range []string{"gap_skips", "pruned_skips"} {
		if entries, _ := body[records].(map[string]any); len(entries) != 0 {
			t.Errorf("%s carries %d records on the anomaly page, want none", records, len(entries))
		}
	}
	if body["demoted_total"].(float64) != 1 || body["undecidable_total"].(float64) != 1 || body["by_design_total"].(float64) != 1 {
		t.Errorf("totals = demoted %v / undecidable %v / by_design %v, want 1 each: counted from the whole view, not the rows sent",
			body["demoted_total"], body["undecidable_total"], body["by_design_total"])
	}
	// The pool asked for: its rows in anomalies, and the demoted list still
	// not repeated beside them.
	_, pool := get(t, handler, "/api/objects?column=demoted")
	if rows, _ := pool["anomalies"].([]any); len(rows) != 1 {
		t.Errorf("column=demoted serves %d rows, want the pool's one", len(rows))
	}
	if rows, _ := pool["demoted"].([]any); len(rows) != 0 {
		t.Errorf("column=demoted repeats %d rows under demoted, want none", len(rows))
	}
	// The record line still opens to its record rows: they were drawn from
	// the view before the response was trimmed.
	_, record := get(t, handler, "/api/objects?check="+string(CheckDetectionAbandoned))
	if rows, _ := record["anomalies"].([]any); len(rows) != 1 || rows[0].(map[string]any)["query_group"] != "qg-stopped" {
		t.Errorf("check=DETECTION_ABANDONED serves %v, want the one retained record as a row", rows)
	}
}

// The verdict route carries each replica's own dependency record and says how
// many replicas the one list it shows is one of; the list route, polled every
// thirty seconds for rows, carries neither the per-replica lists nor a count
// that pretends to. A reader asking "is every replica's output open" reads
// the rows on the verdict route and nothing else.
func TestTheVerdictRouteCarriesEachReplicasDependenciesAndTheListRouteDoesNot(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].TakenAt = now.Add(-5 * time.Second)
	snapshots[0].Dependencies = []Endpoint{outputEntry(true, 1, "")}
	snapshots[1].Dependencies = []Endpoint{outputEntry(false, 7, "kafka: dial tcp 10.0.0.1:9092: i/o timeout")}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())

	_, health := get(t, handler, "/api/health")
	if health["dependencies_replica"] != "pod-a" || health["dependencies_replicas"].(float64) != 2 {
		t.Fatalf("shown list = %v of %v replicas, want pod-a's, one of 2", health["dependencies_replica"], health["dependencies_replicas"])
	}
	rows, _ := health["per_replica"].([]any)
	ready := map[string]bool{}
	for _, item := range rows {
		row := item.(map[string]any)
		list, _ := row["dependencies"].([]any)
		if len(list) != 1 {
			t.Fatalf("%v row carries %v, want its one dependency entry", row["replica"], row["dependencies"])
		}
		entry := list[0].(map[string]any)
		ready[row["replica"].(string)] = entry["ready"].(bool)
	}
	if !ready["pod-a"] || ready["pod-b"] {
		t.Fatalf("per-replica readiness = %v, want pod-a open and pod-b not: each row its own record", ready)
	}

	_, list := get(t, handler, "/api/objects")
	rows, _ = list["per_replica"].([]any)
	if len(rows) != 2 {
		t.Fatalf("list route per_replica = %d rows, want both replicas' counts still there", len(rows))
	}
	for _, item := range rows {
		row := item.(map[string]any)
		if _, present := row["dependencies"]; present {
			t.Errorf("the list route ships %v's dependency record on every poll: %v", row["replica"], row["dependencies"])
		}
	}
}

// The verdict route carries each replica's readiness bits and the count of
// replicas that answer no, so the question a rollout asks -- is every
// replica ready, and on which bit is the one that is not -- is one read.
func TestTheVerdictRouteCarriesEachReplicasReadiness(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].Readiness = &ReadinessFacts{State: "ready", Ready: true, ConfigLoaded: true, SchemaReady: true,
		AssignmentReady: true, RuntimeStateReady: true, OutputSinkReady: true, SnapshotReady: true}
	snapshots[1].Readiness = &ReadinessFacts{State: "not_ready", Ready: false, Reasons: []string{"KAFKA_UNAVAILABLE"},
		ConfigLoaded: true, SchemaReady: true, AssignmentReady: true, RuntimeStateReady: true, OutputSinkReady: false, SnapshotReady: true}
	handler := handlerWith(t, snapshots, Expectation{QueryGroups: 949, Known: true}, replicas())
	_, health := get(t, handler, "/api/health")
	if health["replicas_not_ready"].(float64) != 1 {
		t.Fatalf("replicas_not_ready = %v, want 1", health["replicas_not_ready"])
	}
	rows, _ := health["per_replica"].([]any)
	bits := map[string]map[string]any{}
	for _, item := range rows {
		row := item.(map[string]any)
		readiness, _ := row["readiness"].(map[string]any)
		if readiness == nil {
			t.Fatalf("%v row carries no readiness: %v", row["replica"], row)
		}
		bits[row["replica"].(string)] = readiness
	}
	if bits["pod-a"]["ready"] != true || bits["pod-a"]["output_sink_ready"] != true {
		t.Errorf("pod-a readiness = %v, want ready with the output bit true", bits["pod-a"])
	}
	if bits["pod-b"]["ready"] != false || bits["pod-b"]["output_sink_ready"] != false || bits["pod-b"]["runtime_state_ready"] != true {
		t.Errorf("pod-b readiness = %v, want not ready with the output bit the one that is false", bits["pod-b"])
	}
	if reasons, _ := bits["pod-b"]["reasons"].([]any); len(reasons) != 1 || reasons[0] != "KAFKA_UNAVAILABLE" {
		t.Errorf("pod-b reasons = %v, want the process's one reason", bits["pod-b"]["reasons"])
	}
}
