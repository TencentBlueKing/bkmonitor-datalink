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
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil)
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
	complete := handlerWith(t, snapshotsWithAnomalies(3), Expectation{QueryGroups: 949, Known: true}, replicas())
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
	incompleteHandler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, body = get(t, incompleteHandler, "/api/objects/qg-absent")
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if body["view_complete"] != false || body["health"] != string(HealthUnknown) {
		t.Fatalf("body = %+v, want not-found marked untrustworthy", body)
	}
}

func TestHealthEndpointCarriesTheCoverageArithmetic(t *testing.T) {
	service := mustService(t,
		stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()},
		stubSnapshots{snapshots: healthySnapshots()[:1]},
	)
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil)
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
	if _, err := NewHandler(nil, nil, nil, 0, nil, nil); err == nil {
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
	byKind, _ := summary["by_kind"].([]any)
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
	if byReplica, _ := summary["by_replica"].([]any); len(byReplica) != 0 {
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
	byKind, _ := summary["by_kind"].([]any)
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
	handler, err := NewHandler(service, nil, func() time.Time { return now }, budget, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func agedAnomaly(queryGroup, reason string, age time.Duration) Anomaly {
	item := anomaly(queryGroup)
	item.Kind = "DEGRADED_RUN"
	item.ReasonCode = reason
	item.Since = now.Add(-age)
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
	summary := body["summary"].(map[string]any)
	if summary["stalled"].(float64) != 1 {
		t.Fatalf("summary stalled = %v, want 1", summary["stalled"])
	}
	if body["stall_after_seconds"].(float64) != budget.Seconds() {
		t.Fatalf("stall_after_seconds = %v, want %v", body["stall_after_seconds"], budget.Seconds())
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
