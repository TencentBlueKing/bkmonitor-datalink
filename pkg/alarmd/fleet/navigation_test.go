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
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"
)

// Every check row must still be readable without a diagnostic window. These
// synthetic objects also exercise old links that carry only a QG identity.
func TestNavigationEveryFactCanBeReadBack(t *testing.T) {
	snapshots := healthySnapshots()
	s := &snapshots[0]
	s.Anomalies = []Anomaly{{QueryGroup: "qg-error", Kind: KindBlockedRun, ReasonCode: "source_error"}}
	s.Demoted = []Anomaly{{QueryGroup: "qg-pool", Kind: KindQueryCooldown, Failure: &FailureRef{Code: "QUERY_UNAVAILABLE", Detail: "http_status=400"}}}
	s.Undecidable = []Anomaly{{QueryGroup: "qg-window", Kind: KindDegradedRun, CauseReason: "HISTORY_WARMING", Coverage: &HistoryCoverage{Levels: 3, Empty: 2, Short: 2, EmptyRounds: 40, ShortRounds: 40, WorstRequired: 14}}}
	s.ByDesign = []Anomaly{{QueryGroup: "qg-design", Kind: KindDegradedRun, CauseReason: "EFFECTIVE_TIME_INACTIVE"}}
	s.NoData = []Anomaly{{QueryGroup: "qg-empty", Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED", Strategies: []StrategyRef{{StrategyID: "strategy-example", BusinessID: "business-example"}}}}
	s.NoDataMemory = []Anomaly{{QueryGroup: "qg-memory", Kind: KindNoDataMemoryRefused, ReasonCode: "STATE_BUDGET_EXCEEDED"}}
	s.TotalAnomalies, s.TotalDemoted, s.TotalUndecidable, s.TotalByDesign = 1, 1, 1, 1
	s.GapSkips = map[string]SkippedSpan{"qg-retained": {FirstSlot: 1, LastSlot: 2, Slots: 2, At: now.Add(-time.Hour), Replica: "pod-a"}}
	s.PrunedSkips = map[string]PrunedSkip{"qg-pruned": {From: 1, To: 2, At: now.Add(-time.Hour), Replica: "pod-a"}}
	h := handlerWith(t, snapshots, Expectation{Known: true, QueryGroups: 949}, replicas())
	for _, qg := range []string{"qg-error", "qg-pool", "qg-window", "qg-design", "qg-empty", "qg-memory", "qg-retained", "qg-pruned"} {
		t.Run(qg, func(t *testing.T) {
			status, detail := get(t, h, "/api/objects/"+qg+"?records=200")
			if status != 200 || detail["found"] != true {
				t.Fatalf("legacy URL cannot resolve a fact with no records: status=%d body=%v", status, detail)
			}
			fact := detail["anomaly"].(map[string]any)
			if fact["query_group"] != qg {
				t.Fatalf("wrong object: %v", fact)
			}
			finding := fact["finding"].(map[string]any)
			check, _ := finding["check"].(string)
			group, _ := finding["group"].(string)
			if check == "" {
				return
			}
			context := "?check=" + url.QueryEscape(check) + "&group=" + url.QueryEscape(group)
			_, list := get(t, h, "/api/objects"+context)
			var listed map[string]any
			for _, raw := range list["anomalies"].([]any) {
				row := raw.(map[string]any)
				if row["query_group"] == qg {
					listed = row
					break
				}
			}
			status, detail = get(t, h, "/api/objects/"+qg+context)
			if listed == nil || status != 200 || detail["found"] != true || !reflect.DeepEqual(listed, detail["anomaly"]) {
				t.Fatalf("navigation changed the clicked fact: listed=%v status=%d detail=%v", listed, status, detail)
			}
		})
	}
}

func TestNavigationSharedFactsKeepTheSelectedContext(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].NoData = []Anomaly{{QueryGroup: "qg-shared", Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED"}}
	snapshots[0].NoDataMemory = []Anomaly{{QueryGroup: "qg-shared", Kind: KindNoDataMemoryRefused, ReasonCode: "STATE_BUDGET_EXCEEDED"}}
	snapshots[0].GapSkips = map[string]SkippedSpan{"qg-shared": {At: now.Add(-time.Hour), FirstSlot: 1, LastSlot: 2, Replica: "pod-a"}}
	h := handlerWith(t, snapshots, Expectation{Known: true, QueryGroups: 1, IDs: []string{"qg-shared"}}, replicas())
	_, body := get(t, h, "/api/objects/qg-shared")
	if body["facts_total"] != float64(3) {
		t.Fatalf("coexisting facts lost: %v", body)
	}
	for _, check := range []Check{CheckNoDataPersistent, CheckNoDataMemoryRefused, CheckDetectionAbandoned} {
		_, body = get(t, h, "/api/objects/qg-shared?check="+string(check))
		facts := body["facts"].([]any)
		if len(facts) != 1 || facts[0].(map[string]any)["finding"].(map[string]any)["check"] != string(check) {
			t.Fatalf("selected context replaced: %s %v", check, body)
		}
	}
	// A context the object has left: no fact is substituted for it, and the
	// response does not call an object with three facts on file "not
	// observed" -- it says the object is observed and the context did not
	// match, which is a stale click, not a quiet object.
	_, body = get(t, h, "/api/objects/qg-shared?check=NO_DATA_PERSISTENT&group=old-group")
	if body["facts_total"] != float64(0) || body["existence"] != "active" {
		t.Fatalf("stale context fell back to another fact: %v", body)
	}
	if body["runtime"] != "observed" || body["context_matched"] != false {
		t.Fatalf("stale context read as an unobserved object: runtime=%v context_matched=%v", body["runtime"], body["context_matched"])
	}
	// A context that holds the object says so; a request without one says
	// nothing about a context.
	_, body = get(t, h, "/api/objects/qg-shared?check=NO_DATA_PERSISTENT")
	if body["context_matched"] != true || body["runtime"] != "observed" {
		t.Fatalf("matching context = %v / %v, want matched and observed", body["context_matched"], body["runtime"])
	}
	_, body = get(t, h, "/api/objects/qg-shared")
	if _, present := body["context_matched"]; present {
		t.Fatalf("a request naming no check carries context_matched: %v", body["context_matched"])
	}
	// And a quiet object under a named check is still not observed: the
	// object has no fact anywhere, so the context's miss is not a stale click.
	_, body = get(t, h, "/api/objects/qg-quiet?check=NO_DATA_PERSISTENT")
	if body["runtime"] != "not_observed" || body["context_matched"] != false {
		t.Fatalf("quiet object under a named check = runtime %v, context_matched %v; want not_observed, false", body["runtime"], body["context_matched"])
	}
}

func TestNavigationExistenceNeedsTheAuthoritativeSet(t *testing.T) {
	for _, tc := range []struct {
		name        string
		expectation Expectation
		err         error
		status      int
		existence   string
	}{
		{"active", Expectation{Known: true, QueryGroups: 1, IDs: []string{"qg-quiet"}}, nil, 200, "active"},
		{"absent", Expectation{Known: true, QueryGroups: 1, IDs: []string{"qg-other"}}, nil, 404, "absent"},
		{"empty", Expectation{Known: true}, nil, 404, "absent"},
		{"count-only", Expectation{Known: true, QueryGroups: 1}, nil, 503, "unknown"},
		{"partial", Expectation{Known: true, QueryGroups: 2, IDs: []string{"qg-other"}}, nil, 503, "unknown"},
		{"unavailable", Expectation{}, errors.New("unavailable"), 503, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service := mustService(t, stubExpectations{expectation: tc.expectation, err: tc.err}, stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: healthySnapshots()})
			h, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
			if err != nil {
				t.Fatal(err)
			}
			status, body := get(t, h, "/api/objects/qg-quiet")
			if status != tc.status || body["existence"] != tc.existence || body["runtime"] != "not_observed" || body["found"] != (tc.existence == "active") {
				t.Fatalf("status=%d body=%v", status, body)
			}
		})
	}
}

func TestNavigationRecoveryIsOnlyGroupContext(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].Recovered = []RecoveredProblem{{Check: CheckDependencyDown, Key: "COMMIT/REDIS/UNAVAILABLE", Objects: 2, LastRecovery: now}}
	h := handlerWith(t, snapshots, Expectation{Known: true}, replicas())
	status, body := get(t, h, "/api/objects/qg-no-proof?check=DEPENDENCY_DOWN&group=COMMIT%2FREDIS%2FUNAVAILABLE")
	if status != http.StatusNotFound || body["found"] != false {
		t.Fatalf("group recovery claimed object existence: %d %v", status, body)
	}
	ctx, ok := body["recovery_context"].(map[string]any)
	if !ok || ctx["scope"] != "check_group" || ctx["object_recovery_known"] != false {
		t.Fatalf("missing recovery boundary: %v", body)
	}
}

func TestNavigationRejectsInvalidContext(t *testing.T) {
	h := handlerWith(t, healthySnapshots(), Expectation{}, replicas())
	for _, query := range []string{"?check=NO_SUCH_CHECK", "?group=orphan"} {
		status, _ := get(t, h, "/api/objects/qg-example"+query)
		if status != http.StatusBadRequest {
			t.Fatalf("%s: %d", query, status)
		}
	}
}

func TestNavigationActiveSetSurvivesMissingSnapshots(t *testing.T) {
	for _, registryErr := range []error{nil, errors.New("registry unavailable")} {
		service := mustService(t, stubExpectations{expectation: Expectation{Known: true, QueryGroups: 1, IDs: []string{"qg-active"}}},
			stubRegistry{replicas: replicas(), err: registryErr}, stubSnapshots{err: errors.New("snapshots unavailable")})
		h, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		status, body := get(t, h, "/api/objects/qg-active")
		if status != 200 || body["found"] != true || body["runtime"] != "not_observed" || body["health"] != string(HealthUnknown) {
			t.Fatalf("read failures lost authoritative membership: %d %v", status, body)
		}
	}
}

func TestNavigationFactSurvivesUnknownCatalogAndEmptyRecords(t *testing.T) {
	snapshots := healthySnapshots()
	snapshots[0].NoData = []Anomaly{{QueryGroup: "qg-empty", Kind: KindNoData, ReasonCode: "FULL_EMPTY_COMPLETED"}}
	service := mustService(t, stubExpectations{err: errors.New("catalog unavailable")}, stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: snapshots})
	store, err := NewDiagnosticStore(&fakeDiagnosticRedis{lists: map[string][]string{}}, "navigation-test")
	if err != nil {
		t.Fatal(err)
	}
	h, err := NewHandler(service, nil, func() time.Time { return now }, 0, nil, store, "")
	if err != nil {
		t.Fatal(err)
	}
	status, body := get(t, h, "/api/objects/qg-empty?records=20&check=NO_DATA_PERSISTENT")
	if status != 200 || body["found"] != true || body["existence"] != "unknown" || body["records_status"] != "empty" {
		t.Fatalf("records or catalog failure hid an observed fact: %d %v", status, body)
	}
	if body["anomaly"].(map[string]any)["reason_code"] != "FULL_EMPTY_COMPLETED" {
		t.Fatalf("completion reason changed: %v", body)
	}
}

func TestNavigationFactsAreBoundedWithoutLosingTheirCount(t *testing.T) {
	snapshots := healthySnapshots()
	for i := 0; i < MaxPageSize+1; i++ {
		snapshots[0].NoData = append(snapshots[0].NoData, Anomaly{QueryGroup: "qg-shared", Kind: KindNoData})
	}
	h := handlerWith(t, snapshots, Expectation{}, replicas())
	_, body := get(t, h, "/api/objects/qg-shared")
	if body["facts_total"] != float64(MaxPageSize+1) || len(body["facts"].([]any)) != MaxPageSize {
		t.Fatalf("facts were unbounded or count concealed: %v", body)
	}
}
