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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// standingHandler serves the standing route over two replicas whose owned
// sets are complete, one anomaly on the object the strategy runs in.
func standingHandler(t *testing.T, lookup StrategyLookupFunc, forward LeaderForward) http.Handler {
	t.Helper()
	snapshots := healthySnapshots()
	snapshots[0].Owned, snapshots[0].Determined = 2, 2
	snapshots[0].OwnedObjects = []string{"qg-4101-a", "qg-other"}
	snapshots[1].Owned, snapshots[1].Determined = 1, 1
	snapshots[1].OwnedObjects = []string{"qg-4101-b"}
	row := anomaly("qg-4101-b")
	row.Replica = "pod-b"
	snapshots[1].Anomalies = []Anomaly{row}
	snapshots[1].TotalAnomalies = 1
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 3, Known: true, IDs: []string{"qg-4101-a", "qg-4101-b", "qg-other"}}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return WithStrategyStanding(handler, service, lookup, forward, nil, nil, "pod-a", func() time.Time { return now }, 0)
}

func plans(refs ...StrategyPlanRef) []StrategyPlanRef { return refs }

var (
	planA = StrategyPlanRef{Tenant: "default", Business: "2", QueryGroup: "qg-4101-a", ObjectDigest: "d-a", SnapshotRevision: "s1", QueryRevision: "q1", ScheduleRevision: "r1"}
	planB = StrategyPlanRef{Tenant: "default", Business: "2", QueryGroup: "qg-4101-b", ObjectDigest: "d-b", SnapshotRevision: "s1", QueryRevision: "q2", ScheduleRevision: "r1"}
	planT = StrategyPlanRef{Tenant: "other", Business: "9", QueryGroup: "qg-4101-t", SnapshotRevision: "s1", QueryRevision: "q3", ScheduleRevision: "r1"}
)

// The five standings a strategy can have, each from the facts that produce
// it, with the objects joined to what the fleet sees: which replica holds
// each and the row it is on. Every answer is a 200 -- "not listed" is an
// answer, told apart from "withheld" -- and the sentence names the withheld
// items with their reason and field.
func TestAStrategysStandingIsAnsweredFromTheLookupAndTheFleetsView(t *testing.T) {
	facts := map[string]StrategyLookupFacts{
		"4101": {Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
			Plans:        plans(planA, planB, planT),
			Dispositions: []StrategyDisposition{{Scope: "PLAN", Disposition: "ACCEPTED"}}},
		"4102": {Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
			Dispositions: []StrategyDisposition{
				{Scope: "PLAN", Disposition: "UNSUPPORTED_PHASE2_CAPABILITY", Reason: "ALGORITHM_NOT_MIGRATED", FieldPath: "items[0].algorithms[0]"}}},
		"4103": {Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
			Plans: plans(planA),
			Dispositions: []StrategyDisposition{
				{Scope: "LEVEL", LevelID: 2, Disposition: "CONFIG_REJECTED", Reason: "LEVEL_INVALID", FieldPath: "items[0].algorithms[1].config"},
				{Scope: "LEVEL", LevelID: 1, Disposition: "ACCEPTED"}}},
		"4104": {Available: true, Found: true, Retained: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
			Plans:        plans(planB),
			Dispositions: []StrategyDisposition{{Scope: "PLAN", Disposition: "STALE_CONFIG", Reason: "QUERY_CONFIG_INVALID", FieldPath: "items[0].query_configs[0]"}}},
		"4105": {Available: true, Found: false, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7}},
		// Taken out by the source: one more round on the last good Plan,
		// then withdrawn. Neither is a refusal, and the sentence says which
		// round it is in.
		"4106": {Available: true, Found: true, Retained: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
			Plans:        plans(planA),
			Dispositions: []StrategyDisposition{{Scope: "STRATEGY", Disposition: "PENDING_REMOVAL", Reason: "REMOVED_FROM_ACTIVE_SET"}}},
		"4107": {Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
			Dispositions: []StrategyDisposition{{Scope: "STRATEGY", Disposition: "REMOVED", Reason: "ABSENT_FROM_ACTIVE_SET"}}},
		// Accepted with one Level's time range read as the whole day: the
		// Plan runs, and the sentence says wider-than-written, not withheld.
		"4108": {Available: true, Found: true, Publication: StrategyPublication{SnapshotRevision: "s1", Epoch: 7},
			Plans: plans(planA),
			Dispositions: []StrategyDisposition{
				{Scope: "LEVEL", LevelID: 1, Disposition: "ACCEPTED"},
				{Scope: "LEVEL", LevelID: 1, Disposition: "CONFIG_NORMALIZED", Reason: "EFFECTIVE_TIME_RANGE_INVALID"}}},
	}
	handler := standingHandler(t, func(id string) StrategyLookupFacts { return facts[id] }, nil)

	status, body := get(t, handler, "/api/strategies/4101")
	if status != http.StatusOK || body["standing"] != string(StandingDetecting) || body["found"] != true || body["answered_by"] != "pod-a" {
		t.Fatalf("4101: status %d body %v, want DETECTING answered by pod-a", status, body)
	}
	plansOut, _ := body["plans"].([]any)
	if len(plansOut) != 3 {
		t.Fatalf("4101 plans = %v, want three", body["plans"])
	}
	first, _ := plansOut[0].(map[string]any)
	second, _ := plansOut[1].(map[string]any)
	third, _ := plansOut[2].(map[string]any)
	if first["query_group"] != "qg-4101-a" || first["replica"] != "pod-a" || first["existence"] != "active" || len(first["rows"].([]any)) != 0 {
		t.Errorf("plan a = %v, want held by pod-a, active, on no row", first)
	}
	if second["query_group"] != "qg-4101-b" || second["replica"] != "pod-b" || len(second["rows"].([]any)) != 1 {
		t.Errorf("plan b = %v, want held by pod-b with its one row", second)
	}
	if third["query_group"] != "qg-4101-t" || third["replica"] != nil || third["existence"] != "absent" {
		t.Errorf("plan t = %v, want no replica and absent from the active set", third)
	}
	if line, _ := body["line"].(string); !strings.Contains(line, "已生效，3 个对象在检测") || !strings.Contains(line, "b 持有，程序缺陷·本服务处理") {
		t.Errorf("4101 line = %q", line)
	}
	if publication, _ := body["publication"].(map[string]any); publication["snapshot_revision"] != "s1" || publication["epoch"] != 7.0 {
		t.Errorf("publication = %v", body["publication"])
	}

	// Narrowed to one tenant: the other tenant's Plan is not the answer.
	_, narrowed := get(t, handler, "/api/strategies/4101?tenant=default&business=2")
	if plansOut, _ := narrowed["plans"].([]any); len(plansOut) != 2 || narrowed["tenant"] != "default" {
		t.Errorf("narrowed plans = %v, want the two of tenant default", narrowed["plans"])
	}

	// The normalized strategy is detecting, and its line says what was read
	// in place of what was written -- never "被扣住".
	status, body = get(t, handler, "/api/strategies/4108")
	if status != http.StatusOK || body["standing"] != string(StandingDetecting) {
		t.Fatalf("4108: status %d standing %v, want DETECTING: a normalized item does not withhold", status, body["standing"])
	}
	if line, _ := body["line"].(string); !strings.Contains(line, "已生效，1 个对象在检测") ||
		!strings.Contains(line, "；1 项的读法和配置写的不同、在检测：LEVEL 级别 1：CONFIG_NORMALIZED/EFFECTIVE_TIME_RANGE_INVALID——生效时间段的开始或结束时间格式不合法") ||
		strings.Contains(line, "项被扣住") || strings.Contains(line, "全部被扣住") || strings.Contains(line, "部分生效") {
		t.Errorf("4108 line = %q", line)
	}

	status, body = get(t, handler, "/api/strategies/4102")
	if status != http.StatusOK || body["standing"] != string(StandingWithheld) {
		t.Fatalf("4102: status %d standing %v, want WITHHELD", status, body["standing"])
	}
	if line, _ := body["line"].(string); !strings.Contains(line, "未生效，1 项全部被扣住：PLAN：UNSUPPORTED_PHASE2_CAPABILITY/ALGORITHM_NOT_MIGRATED（items[0].algorithms[0]）") {
		t.Errorf("4102 line = %q", line)
	}

	_, body = get(t, handler, "/api/strategies/4103")
	if body["standing"] != string(StandingPartlyWithheld) {
		t.Fatalf("4103 standing = %v, want PARTLY_WITHHELD", body["standing"])
	}
	if line, _ := body["line"].(string); !strings.Contains(line, "部分生效——1 个对象在检测") || !strings.Contains(line, "LEVEL 级别 2：CONFIG_REJECTED/LEVEL_INVALID（items[0].algorithms[1].config）") {
		t.Errorf("4103 line = %q", line)
	}
	// Dispositions come out sorted by scope then level, the accepted one too.
	if dispositions, _ := body["dispositions"].([]any); len(dispositions) != 2 || dispositions[0].(map[string]any)["level_id"] != 1.0 {
		t.Errorf("4103 dispositions = %v, want both, level 1 first", body["dispositions"])
	}

	_, body = get(t, handler, "/api/strategies/4104")
	if body["standing"] != string(StandingRetainedLastGood) || body["retained"] != true {
		t.Fatalf("4104 = %v, want RETAINED_LAST_GOOD", body)
	}
	if line, _ := body["line"].(string); !strings.Contains(line, "新配置被扣住，仍按上一次生效的配置检测") || !strings.Contains(line, "STALE_CONFIG/QUERY_CONFIG_INVALID") {
		t.Errorf("4104 line = %q", line)
	}

	status, body = get(t, handler, "/api/strategies/4105")
	if status != http.StatusOK || body["standing"] != string(StandingNotListed) || body["found"] != false {
		t.Fatalf("4105: status %d body %v, want NOT_LISTED as an answer, not an error", status, body)
	}
	if line, _ := body["line"].(string); !strings.Contains(line, "策略源没有列出它——不是被扣") {
		t.Errorf("4105 line = %q", line)
	}
	_, body = get(t, handler, "/api/strategies/4106")
	if body["standing"] != string(StandingRetainedLastGood) {
		t.Fatalf("4106 standing = %v, want RETAINED_LAST_GOOD for the grace round", body["standing"])
	}
	if line, _ := body["line"].(string); !strings.Contains(line, "策略源已把它移出活动集，上一次生效的配置再检测一轮后撤下") || strings.Contains(line, "被扣住") {
		t.Errorf("4106 line = %q, want the grace round said as the source's removal, not as a refusal", line)
	}
	status, body = get(t, handler, "/api/strategies/4107")
	if status != http.StatusOK || body["standing"] != string(StandingNotListed) || body["found"] != true {
		t.Fatalf("4107: status %d body %v, want NOT_LISTED (found, since the round recorded the removal) and not WITHHELD", status, body)
	}
	if line, _ := body["line"].(string); !strings.Contains(line, "上一轮已撤下") || strings.Contains(line, "被扣住") {
		t.Errorf("4107 line = %q, want the withdrawal said, not a refusal", line)
	}
	// Every standing produced is on the closed list.
	for _, id := range []string{"4101", "4102", "4103", "4104", "4105", "4106", "4107"} {
		_, body = get(t, handler, "/api/strategies/"+id)
		listed := false
		for _, kind := range StrategyStandingKinds {
			if string(kind) == body["standing"] {
				listed = true
			}
		}
		if !listed {
			t.Errorf("%s: standing %v is not on StrategyStandingKinds", id, body["standing"])
		}
	}
}

// A process without a publication hands the request to the Leader once.
// A forwarded request that lands on a process without one is refused, not
// handed on; and with no Leader to hand it to, the refusal says which of
// the discovery's misses it was. The route's shape is checked too: no id,
// or a wrong method, is a 400 or 405 before anything is looked up.
func TestAFollowerForwardsTheStandingRequestOnceAndRefusesWithWhyOtherwise(t *testing.T) {
	unavailable := func(string) StrategyLookupFacts { return StrategyLookupFacts{} }
	forwards := 0
	forward := func(response http.ResponseWriter, request *http.Request) (bool, string) {
		forwards++
		response.Header().Set("Content-Type", "application/json")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte(`{"standing":"DETECTING","answered_by":"pod-leader"}`))
		return true, ""
	}
	handler := standingHandler(t, unavailable, forward)
	status, body := get(t, handler, "/api/strategies/4101")
	if status != http.StatusOK || body["answered_by"] != "pod-leader" || forwards != 1 {
		t.Fatalf("follower answer = %d %v after %d forwards, want the Leader's answer once", status, body, forwards)
	}
	// Already forwarded: refused here, never handed on again.
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/strategies/4101", nil)
	request.Header.Set(ForwardedHeader(), "pod-c")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "NOT_PUBLISHED") || forwards != 1 {
		t.Fatalf("a forwarded request on a follower = %d %s after %d forwards, want NOT_PUBLISHED and no second hop", response.Code, response.Body.String(), forwards)
	}
	// No Leader to forward to: the discovery's word.
	noLeader := standingHandler(t, unavailable, func(http.ResponseWriter, *http.Request) (bool, string) { return false, "LEADER_NO_ENDPOINT" })
	status, body = get(t, noLeader, "/api/strategies/4101")
	if status != http.StatusServiceUnavailable || body["error"] != "LEADER_UNAVAILABLE" || body["reason"] != "LEADER_NO_ENDPOINT" {
		t.Fatalf("no Leader = %d %v, want LEADER_UNAVAILABLE with the discovery's reason", status, body)
	}
	// No forwarder at all: the plain refusal.
	plain := standingHandler(t, unavailable, nil)
	if status, body = get(t, plain, "/api/strategies/4101"); status != http.StatusServiceUnavailable || body["error"] != "NOT_PUBLISHED" {
		t.Fatalf("no forwarder = %d %v, want NOT_PUBLISHED", status, body)
	}
	// The route's shape.
	if status, body = get(t, plain, "/api/strategies/"); status != http.StatusBadRequest || body["error"] != "STRATEGY_ID_REQUIRED" {
		t.Fatalf("no id = %d %v", status, body)
	}
	response = httptest.NewRecorder()
	plain.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/strategies/4101", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST = %d, want 405", response.Code)
	}
	// Other routes pass through untouched.
	if status, _ = get(t, plain, "/api/health"); status != http.StatusOK {
		t.Fatalf("/api/health through the wrapper = %d", status)
	}
}
