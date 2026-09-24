// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"encoding/json"
	"errors"
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
		if r.URL.Path == "/local-api/strategy-index/targets" {
			_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
			return
		}
		q := r.URL.Query()
		if q.Get("event_source_id") != "source" || q.Get("hook_name") != "active" || q.Get("bk_tenant_id") != keyA.TenantID || q.Get("strategy_id") != keyA.StrategyID {
			t.Errorf("query %v", q)
		}
		_ = json.NewEncoder(w).Encode(reconciliationJSON())
	}))
	defer server.Close()
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "user", Password: "secret", Index: testIndex(), MaxResponseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	result, err := reader.Reconcile(context.Background(), keyA)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.Members, []string{"matched", "missing"}) || !reflect.DeepEqual(result.Missing, []string{"missing"}) || !reflect.DeepEqual(result.Suppressed, []string{"stale"}) {
		t.Fatalf("result %+v", result)
	}
	if len(result.Alerts) != 2 || result.Alerts[0].Severity != "critical" || result.Alerts[1].Severity != "" {
		t.Fatal("optional severity lost or fabricated")
	}
	if len(requests) != 2 {
		t.Fatalf("requests %v", requests)
	}
}

func TestHTTPReconcilerRejectsPartialAndInvalidScope(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"missing rows":   func(m map[string]any) { delete(m, "rows") },
		"partial":        func(m map[string]any) { m["complete"] = false },
		"partial Redis":  func(m map[string]any) { m["redis"] = map[string]any{"complete": false} },
		"partial alerts": func(m map[string]any) { m["alerts"] = map[string]any{"complete": false} },
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
			reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "u", Password: "p", Index: testIndex(), MaxResponseBytes: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Reconcile(context.Background(), keyA); err == nil {
				t.Fatal("invalid response accepted as a baseline")
			}
		})
	}
}

// The target is read from the link, not configured: the one target it lists
// is used; several are refused by name unless the configuration narrows the
// choice; and errors never carry the credentials.
func TestTheTargetIsReadFromTheLinkAndNeverGuessed(t *testing.T) {
	other := testBinding()
	other.EventSourceID, other.HookName, other.Sources = "other", "other-hook", []string{"other"}
	for name, tc := range map[string]struct {
		targets  []TargetBinding
		selector TargetSelector
		ok       bool
	}{
		"the only target":             {[]TargetBinding{testBinding()}, TargetSelector{}, true},
		"several without a selector":  {[]TargetBinding{testBinding(), other}, TargetSelector{}, false},
		"several with a selector":     {[]TargetBinding{other, testBinding()}, TargetSelector{EventSourceID: "source"}, true},
		"a selector matching nothing": {[]TargetBinding{other}, TargetSelector{HookName: "active"}, false},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode(tc.targets)
			}))
			defer server.Close()
			reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "private-user",
				Password: "private-password", Select: tc.selector, Index: testIndex(), MaxResponseBytes: 1 << 20})
			if err != nil {
				t.Fatal(err)
			}
			binding, err := reader.Binding(context.Background())
			if tc.ok != (err == nil) {
				t.Fatalf("binding=%+v err=%v", binding, err)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatalf("an error carried the credentials: %v", err)
			}
			if tc.ok && binding.EventSourceID != "source" {
				t.Fatalf("the wrong target was chosen: %+v", binding)
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
			reader, _ := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "u", Password: "p", Index: testIndex(), MaxResponseBytes: 1 << 20})
			_, err := reader.Binding(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "this process reads redis:6379 db 3 prefix test:active") {
				t.Fatalf("err = %v", err)
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
	}))
	defer server.Close()
	reader, _ := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "u", Password: "p", Index: testIndex(), MaxResponseBytes: 1})
	if _, err := reader.Reconcile(context.Background(), keyA); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("body bound error = %v", err)
	}
}

// Discovery reads the target the link lists whatever this process reads
// today: it is how the process learns where to read.
func TestDiscoveryReadsTheTargetWithoutAskingWhereThisProcessReads(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/local-api/strategy-index/targets" {
			t.Errorf("path %s", r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
	}))
	defer server.Close()
	target, err := DiscoverTarget(context.Background(), HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "user", Password: "secret", MaxResponseBytes: 1 << 20,
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
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "u", Password: "p", Index: fallback, MaxResponseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Binding(context.Background()); err == nil || !strings.Contains(err.Error(), "this process reads runtime:6379 db 8") {
		t.Fatalf("before the move: %v", err)
	}
	if err := reader.SetIndexLocation(IndexLocation{KeyPrefix: "test:active", Address: "", Database: 0}); err == nil {
		t.Fatal("an invalid location was accepted")
	}
	if err := reader.SetIndexLocation(testIndex()); err != nil {
		t.Fatal(err)
	}
	if binding, err := reader.Binding(context.Background()); err != nil || binding.Address != testBinding().Address {
		t.Fatalf("after the move: %+v %v", binding, err)
	}
}
