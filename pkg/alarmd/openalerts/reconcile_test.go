// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func testBinding() TargetBinding {
	return TargetBinding{EventSourceID: "source", HookName: "active", KeyPrefix: "test:active", Address: "redis:6379", Database: 3, Sources: []string{"source"}}
}

// testIndex is where the tests' process reads the sets: the same place
// testBinding says the link writes them.
func testIndex() IndexLocation {
	b := testBinding()
	return IndexLocation{KeyPrefix: b.KeyPrefix, Address: b.Address, Database: b.Database}
}

func reconciliationJSON() map[string]any {
	return map[string]any{
		"target": testBinding(), "tenantId": keyA.TenantID, "strategyId": keyA.StrategyID, "key": "test:active:" + keyA.TenantID + ":" + keyA.StrategyID,
		"complete": true, "redis": map[string]any{"complete": true}, "alerts": map[string]any{"complete": true},
		"rows": []any{
			map[string]any{"fingerprint": "matched", "status": "matched", "alerts": []Alert{{AlertID: "a", EventSourceID: "source", Fingerprint: "matched", Severity: "critical"}}},
			map[string]any{"fingerprint": "missing", "status": "missing_redis", "alerts": []Alert{{AlertID: "b", EventSourceID: "source", Fingerprint: "missing"}}},
			map[string]any{"fingerprint": "stale", "status": "redis_only", "alerts": []Alert{}},
		},
	}
}

func TestHTTPReconcilerChecksBindingQueryAuthAndRetainsMetadata(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "user" || password != "secret" {
			t.Error("BasicAuth missing")
		}
		requests = append(requests, r.URL.Path)
		if r.URL.Path == "/api/modules/linkd/strategy-index/targets" {
			_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
			return
		}
		if r.URL.Path == "/api/modules/linkd/event-sources/source" {
			_ = json.NewEncoder(w).Encode(map[string]any{"id": "source", "revision": 3, "published": 3,
				"spec": map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id"}})
			return
		}
		q := r.URL.Query()
		if q.Get("event_source_id") != "source" || q.Get("hook_name") != "active" || q.Get("bk_tenant_id") != keyA.TenantID || q.Get("strategy_id") != keyA.StrategyID {
			t.Errorf("query %v", q)
		}
		_ = json.NewEncoder(w).Encode(reconciliationJSON())
	}))
	defer server.Close()
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(), Username: "user", Password: "secret", Index: testIndex(), MaxResponseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Reconcile(consoleCtx(), keyA)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Members, []string{"matched", "missing"}) || !reflect.DeepEqual(result.Missing, []string{"missing"}) || !reflect.DeepEqual(result.Suppressed, []string{"stale"}) {
		t.Fatalf("result %+v", result)
	}
	if len(result.Alerts) != 2 || result.Alerts[0].Severity != "critical" || result.Alerts[1].Severity != "" {
		t.Fatal("optional severity lost or fabricated")
	}
	// A reconciliation also reads the event source's keying, once until it
	// is due again: the Console's word on how the link keys our alerts.
	if !reflect.DeepEqual(requests, []string{"/api/modules/linkd/strategy-index/targets", "/api/modules/linkd/event-sources/source", "/api/modules/linkd/strategy-index/reconcile"}) {
		t.Fatalf("requests %v", requests)
	}
	if keyed, _, known := reader.KeyedByAlertID(); !keyed || !known || !reader.LocationConfirmed() {
		t.Fatalf("keyed %v known %v location %v, want both facts confirmed by the reconciliation", keyed, known, reader.LocationConfirmed())
	}
	if _, err := reader.Reconcile(consoleCtx(), keyA); err != nil {
		t.Fatal(err)
	}
	// The second reconciliation reuses the target and reads neither the
	// target list nor the event source again: one request.
	if len(requests) != 4 || requests[3] != "/api/modules/linkd/strategy-index/reconcile" {
		t.Fatalf("requests %v, want the second reconciliation as one reconcile request", requests)
	}
}

func TestHTTPReconcilerRejectsPartialAndInvalidScope(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"missing rows":   func(m map[string]any) { delete(m, "rows") },
		"other tenant":   func(m map[string]any) { m["tenantId"] = "other" },
		"other strategy": func(m map[string]any) { m["strategyId"] = "other" },
		"wrong key":      func(m map[string]any) { m["key"] = "other" },
		"binding moved":  func(m map[string]any) { b := testBinding(); b.Database++; m["target"] = b },
		"unknown row":    func(m map[string]any) { m["rows"].([]any)[0].(map[string]any)["status"] = "unknown" },
		"mismatched fingerprint": func(m map[string]any) {
			m["rows"].([]any)[0].(map[string]any)["alerts"] = []Alert{{AlertID: "a", EventSourceID: "source", Fingerprint: "different"}}
		},
		"foreign source": func(m map[string]any) {
			m["rows"].([]any)[0].(map[string]any)["alerts"] = []Alert{{AlertID: "a", EventSourceID: "foreign", Fingerprint: "matched"}}
		},
		"empty matched": func(m map[string]any) { m["rows"].([]any)[0].(map[string]any)["alerts"] = []Alert{} },
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/targets") {
					_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
					return
				}
				m := reconciliationJSON()
				mutate(m)
				_ = json.NewEncoder(w).Encode(m)
			}))
			defer server.Close()
			reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(), Username: "u", Password: "p", Index: testIndex(), MaxResponseBytes: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Reconcile(consoleCtx(), keyA); err == nil {
				t.Fatal("invalid response accepted as a baseline")
			}
		})
	}
}

// reconcileServer answers every reconcile with body, the tests' one target
// otherwise, with room for answers past the reader's row cap.
func reconcileServer(t *testing.T, body func() map[string]any) *HTTPReconciler {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/targets") {
			_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
			return
		}
		_ = json.NewEncoder(w).Encode(body())
	}))
	t.Cleanup(server.Close)
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(), Username: "u", Password: "p", Index: testIndex(), MaxResponseBytes: 16 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

// matchedRows is n matched rows of our source, alerts alerts each, with
// fingerprints f00000, f00001, ...
func matchedRows(n, alerts int) []any {
	rows := make([]any, 0, n)
	for i := 0; i < n; i++ {
		fingerprint := fmt.Sprintf("f%05d", i)
		list := make([]Alert, 0, alerts)
		for j := 0; j < alerts; j++ {
			list = append(list, Alert{AlertID: fmt.Sprintf("%s-%d", fingerprint, j), EventSourceID: "source", Fingerprint: fingerprint})
		}
		rows = append(rows, map[string]any{"fingerprint": fingerprint, "status": "matched", "alerts": list})
	}
	return rows
}

// An answer the link marked incomplete is read as far as it goes and marked
// Truncated: every row it lists is real, and refusing it left a strategy
// whose set the link cannot answer whole with no close at all.
func TestAnIncompleteAnswerIsReadAndMarkedTruncated(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"partial":        func(m map[string]any) { m["complete"] = false },
		"partial Redis":  func(m map[string]any) { m["redis"] = map[string]any{"complete": false} },
		"partial alerts": func(m map[string]any) { m["alerts"] = map[string]any{"complete": false} },
	} {
		t.Run(name, func(t *testing.T) {
			reader := reconcileServer(t, func() map[string]any { m := reconciliationJSON(); mutate(m); return m })
			result, err := reader.Reconcile(consoleCtx(), keyA)
			if err != nil || !result.Truncated {
				t.Fatalf("result %+v err %v, want the rows read and marked truncated", result, err)
			}
			if len(result.Members) != 2 || len(result.Missing) != 1 || len(result.Suppressed) != 1 || len(result.Alerts) != 2 {
				t.Fatalf("the rows received were not all kept: %+v", result)
			}
		})
	}
	reader := reconcileServer(t, reconciliationJSON)
	if result, err := reader.Reconcile(consoleCtx(), keyA); err != nil || result.Truncated {
		t.Fatalf("a complete answer reads as %+v, %v; want it complete", result, err)
	}
}

// Past the reader's caps the answer is cut at a row boundary, and what is
// kept is the first rows: 6000 alerts in rows of 3 keep 1666 rows (4998
// alerts), and 10001 rows of one alert keep 10000. At the caps exactly,
// nothing is cut.
func TestAnAnswerPastTheCapsIsCutAtARowAndMarkedTruncated(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		rows, alerts         int
		keptRows, keptAlerts int
		truncated            bool
	}{
		{"6000 alerts", 2000, 3, 1666, 4998, true},
		{"5000 alerts exactly", 1000, 5, 1000, 5000, false},
		{"10001 rows", 10001, 0, 10000, 0, true},
		{"10000 rows exactly", 10000, 0, 10000, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := reconcileServer(t, func() map[string]any {
				m := reconciliationJSON()
				if tc.alerts == 0 {
					rows := make([]any, 0, tc.rows)
					for i := 0; i < tc.rows; i++ {
						rows = append(rows, map[string]any{"fingerprint": fmt.Sprintf("s%05d", i), "status": "redis_only", "alerts": []Alert{}})
					}
					m["rows"] = rows
				} else {
					m["rows"] = matchedRows(tc.rows, tc.alerts)
				}
				return m
			})
			result, err := reader.Reconcile(consoleCtx(), keyA)
			if err != nil {
				t.Fatal(err)
			}
			kept := len(result.Members) + len(result.Suppressed)
			if result.Truncated != tc.truncated || kept != tc.keptRows || len(result.Alerts) != tc.keptAlerts {
				t.Fatalf("truncated %v, rows %d, alerts %d; want %v, %d, %d", result.Truncated, kept, len(result.Alerts),
					tc.truncated, tc.keptRows, tc.keptAlerts)
			}
			if tc.alerts > 0 && (result.Members[0] != "f00000" || result.Members[kept-1] != fmt.Sprintf("f%05d", kept-1)) {
				t.Fatalf("not the first rows kept: %s .. %s", result.Members[0], result.Members[kept-1])
			}
		})
	}
}

// A cut answer is still checked whole: a contradiction in a row past the cut
// refuses it, as one in a complete answer does.
func TestACutAnswerThatContradictsItselfIsStillRefused(t *testing.T) {
	for name, bad := range map[string]map[string]any{
		"mismatched fingerprint": {"fingerprint": "late", "status": "matched", "alerts": []Alert{{AlertID: "x", EventSourceID: "source", Fingerprint: "other"}}},
		"duplicate fingerprint":  {"fingerprint": "f00000", "status": "matched", "alerts": []Alert{{AlertID: "x", EventSourceID: "source", Fingerprint: "f00000"}}},
		"unknown status":         {"fingerprint": "late", "status": "unknown", "alerts": []Alert{}},
	} {
		t.Run(name, func(t *testing.T) {
			reader := reconcileServer(t, func() map[string]any {
				m := reconciliationJSON()
				m["complete"] = false
				m["rows"] = append(matchedRows(2000, 3), bad)
				return m
			})
			if result, err := reader.Reconcile(consoleCtx(), keyA); err == nil {
				t.Fatalf("a cut answer with %s was accepted: truncated %v", name, result.Truncated)
			}
		})
	}
	reader := reconcileServer(t, func() map[string]any {
		m := reconciliationJSON()
		m["complete"], m["tenantId"] = false, "other"
		return m
	})
	if _, err := reader.Reconcile(consoleCtx(), keyA); err == nil {
		t.Fatal("a cut answer for another strategy was accepted")
	}
}

// The target is read from the link, not configured: the one target it lists
// is used; none, or several, are refused with the count and the names, and
// the refusal no longer points at settings the deployment does not have; and
// errors never carry the credentials.
func TestTheTargetIsReadFromTheLinkAndNeverGuessed(t *testing.T) {
	other := testBinding()
	other.EventSourceID, other.HookName, other.Sources = "other", "other-hook", []string{"other"}
	for name, tc := range map[string]struct {
		targets []TargetBinding
		ok      bool
	}{
		"the only target": {[]TargetBinding{testBinding()}, true},
		"several":         {[]TargetBinding{testBinding(), other}, false},
		"none":            {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(tc.targets)
			}))
			defer server.Close()
			reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(), Username: "private-user",
				Password: "private-password", Index: testIndex(), MaxResponseBytes: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			binding, err := reader.Binding(consoleCtx())
			if tc.ok != (err == nil) {
				t.Fatalf("binding=%+v err=%v", binding, err)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatalf("an error carried the credentials: %v", err)
			}
			if tc.ok && binding.EventSourceID != "source" {
				t.Fatalf("the wrong target was chosen: %+v", binding)
			}
			if !tc.ok {
				var counted *TargetCountError
				if !errors.As(err, &counted) || counted.Count != len(tc.targets) || len(counted.Names) != len(tc.targets) {
					t.Fatalf("refusal %v, want the count %d and every name", err, len(tc.targets))
				}
				if strings.Contains(err.Error(), "event_source_id") || strings.Contains(err.Error(), "hook_name") {
					t.Fatalf("the refusal points at settings that do not exist: %v", err)
				}
			}
		})
	}
}

// A target that writes its sets somewhere this process does not read them is
// refused, and the refusal names both places so the fix is in the message.
func TestATargetWritingElsewhereIsRefusedByName(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*TargetBinding)
		want   string
	}{
		"another database": {func(b *TargetBinding) { b.Database = 9 }, "db 9"},
		"another prefix":   {func(b *TargetBinding) { b.KeyPrefix = "other:active" }, "prefix other:active"},
		"another address":  {func(b *TargetBinding) { b.Address = "other:6379" }, "other:6379"},
	} {
		t.Run(name, func(t *testing.T) {
			elsewhere := testBinding()
			tc.mutate(&elsewhere)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode([]TargetBinding{elsewhere})
			}))
			defer server.Close()
			reader, _ := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(), Username: "u", Password: "p", Index: testIndex(), MaxResponseBytes: 1 << 20})
			_, err := reader.Binding(consoleCtx())
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "this process reads redis:6379 db 3 prefix test:active") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
	}))
	defer server.Close()
	reader, _ := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(), Username: "u", Password: "p", Index: testIndex(), MaxResponseBytes: 1})
	if _, err := reader.Reconcile(consoleCtx(), keyA); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("body bound error = %v", err)
	}
}

// Discovery reads the target the link lists whatever this process reads
// today: it is how the process learns where to read.
func TestDiscoveryReadsTheTargetWithoutAskingWhereThisProcessReads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/modules/linkd/strategy-index/targets" {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
	}))
	defer server.Close()
	target, err := DiscoverTarget(consoleCtx(), HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(), Username: "user", Password: "secret", MaxResponseBytes: 1 << 20,
		Index: IndexLocation{KeyPrefix: "elsewhere", Address: "other:6379", Database: 9}})
	if err != nil || target.Address != "redis:6379" || target.Database != 3 || target.KeyPrefix != "test:active" {
		t.Fatalf("target %+v err %v", target, err)
	}
}

// A reader bound to the fallback location refuses by name until it is moved
// to where the link writes; after the move it resolves, and a location that
// cannot be a location is refused without moving anything.
func TestAReaderMovedToWhereTheLinkWritesResolves(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
	}))
	defer server.Close()
	fallback := IndexLocation{KeyPrefix: "alarmd:open_alerts", Address: "runtime:6379", Database: 8}
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL + testLinkdAPIRoot, Client: server.Client(), Username: "u", Password: "p", Index: fallback, MaxResponseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Binding(consoleCtx()); err == nil || !strings.Contains(err.Error(), "this process reads runtime:6379 db 8") {
		t.Fatalf("before the move: %v", err)
	}
	if err := reader.SetIndexLocation(IndexLocation{KeyPrefix: "test:active", Address: "", Database: 0}); err == nil {
		t.Fatal("an invalid location was accepted")
	}
	if err := reader.SetIndexLocation(testIndex()); err != nil {
		t.Fatal(err)
	}
	if binding, err := reader.Binding(consoleCtx()); err != nil || binding.Address != testBinding().Address {
		t.Fatalf("after the move: %+v %v", binding, err)
	}
}
