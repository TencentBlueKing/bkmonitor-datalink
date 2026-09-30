// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// browseJSON is a browse response in the link's own shape
// (strategyBrowseResultSchema): nullable cursor and health fields, a row
// whose set could not be read carrying null members and an error.
func browseJSON() map[string]any {
	return map[string]any{
		"target": testBinding(), "scannedAt": "2026-09-22T12:00:00.000Z", "nextCursor": "eyJ2ZXJzaW9uIjoxfQ", "phase": "sets",
		"warnings": []string{"not a snapshot"},
		"health": map[string]any{"lastSuccess": "2026-09-22T11:59:00.123Z", "lastAttempt": "2026-09-22T11:59:00.123Z",
			"error": nil, "pendingCount": 2, "oldestDueAt": nil},
		"rows": []any{
			map[string]any{"tenantId": "system", "strategyId": "10", "key": "test:active:system:10", "members": 3, "pending": false,
				"lastSuccess": "2026-09-22T11:58:00Z", "lastAttempt": "2026-09-22T11:58:00Z", "error": nil},
			map[string]any{"tenantId": "system", "strategyId": "11", "key": "test:active:system:11", "members": nil, "pending": nil,
				"lastSuccess": nil, "lastAttempt": nil, "error": "metadata_read_failed"},
		},
	}
}

func rosterServer(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) *HTTPReconciler {
	t.Helper()
	// The link lists one target, the one the tests bind to; every other path
	// is the test's own.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/strategy-index/targets") {
			_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
			return
		}
		respond(w, r)
	}))
	t.Cleanup(server.Close)
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "user", Password: "secret", Index: testIndex(), MaxResponseBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestTheRosterIsReadInTheLinksOwnShape(t *testing.T) {
	reader := rosterServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/local-api/strategy-index/browse" || q.Get("event_source_id") != "source" || q.Get("hook_name") != "active" ||
			q.Get("count") != "200" || q.Get("cursor") != "next-page" {
			t.Errorf("request %s %v", r.URL.Path, q)
		}
		if user, password, ok := r.BasicAuth(); !ok || user != "user" || password != "secret" {
			t.Error("BasicAuth missing")
		}
		_ = json.NewEncoder(w).Encode(browseJSON())
	})
	page, err := reader.Roster(context.Background(), "next-page")
	if err != nil {
		t.Fatal(err)
	}
	if page.Next != "eyJ2ZXJzaW9uIjoxfQ" || page.Health.PendingCount != 2 || page.Health.Error != "" ||
		!page.Health.LastSuccess.Equal(time.Date(2026, 9, 22, 11, 59, 0, 123e6, time.UTC)) {
		t.Fatalf("page %+v", page)
	}
	if len(page.Rows) != 2 || page.Rows[0].Members == nil || *page.Rows[0].Members != 3 {
		t.Fatalf("rows %+v", page.Rows)
	}
	if page.Rows[1].Members != nil || page.Rows[1].Error != "metadata_read_failed" {
		t.Fatalf("an unread set became a count: %+v", page.Rows[1])
	}
}

// The last page carries a null cursor, which is the end of the walk.
func TestTheLastRosterPageEndsTheWalk(t *testing.T) {
	reader := rosterServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := browseJSON()
		body["nextCursor"] = nil
		body["health"].(map[string]any)["error"] = "discovery_failed"
		_ = json.NewEncoder(w).Encode(body)
	})
	page, err := reader.Roster(context.Background(), "")
	if err != nil || page.Next != "" || page.Health.Error != "discovery_failed" {
		t.Fatalf("page %+v err %v", page, err)
	}
}

// A roster of another target, or one missing the parts a round decides on,
// is not read.
func TestARosterOfAnotherTargetOrWithoutHealthIsRefused(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"other database": func(m map[string]any) { b := testBinding(); b.Database++; m["target"] = b },
		"no health":      func(m map[string]any) { delete(m, "health") },
		"no rows":        func(m map[string]any) { delete(m, "rows") },
		"unknown phase":  func(m map[string]any) { m["phase"] = "other" },
		"key of another prefix": func(m map[string]any) {
			m["rows"].([]any)[0].(map[string]any)["key"] = "other:system:10"
		},
		"unparseable time": func(m map[string]any) { m["health"].(map[string]any)["lastSuccess"] = "yesterday" },
	} {
		reader := rosterServer(t, func(w http.ResponseWriter, r *http.Request) {
			body := browseJSON()
			mutate(body)
			_ = json.NewEncoder(w).Encode(body)
		})
		if _, err := reader.Roster(context.Background(), ""); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// alertJSON is an alert detail in the link's own shape (entityItemSchema),
// its payload the stored alert with the labels this deployment wrote.
func alertJSON() map[string]any {
	return map[string]any{
		"tenantId": "system", "id": "alert-1", "timestamp": "2026-09-22T11:00:00Z", "summary": map[string]any{},
		"payload": map[string]any{"alert_id": "alert-1", "bk_tenant_id": "system", "event_source_id": "source",
			"fingerprint": "0123456789abcdef0123456789abcdef", "status": "active", "severity": "warning",
			"labels": map[string]any{"strategy_id": 10, "strategy_version": 7, "bk_biz_id": -3}},
	}
}

func TestAnAlertRecordCarriesTheIdentityItWasCreatedWith(t *testing.T) {
	reader := rosterServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/local-api/alerts/alert-1" || r.URL.Query().Get("bk_tenant_id") != "system" {
			t.Errorf("request %s %v", r.URL.Path, r.URL.Query())
		}
		_ = json.NewEncoder(w).Encode(alertJSON())
	})
	record, err := reader.AlertRecord(context.Background(), "system", "alert-1")
	if err != nil {
		t.Fatal(err)
	}
	if record.StrategyID != "10" || record.Revision != 7 || record.BusinessID != -3 || record.EventSourceID != "source" || record.Status != "active" {
		t.Fatalf("record %+v", record)
	}
}

// Labels written as text read the same as numbers; a missing label is zero,
// never a guess.
func TestAnAlertRecordReadsTextLabelsAndLeavesMissingOnesZero(t *testing.T) {
	reader := rosterServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := alertJSON()
		body["payload"].(map[string]any)["labels"] = map[string]any{"strategy_id": "10", "bk_biz_id": "2"}
		_ = json.NewEncoder(w).Encode(body)
	})
	record, err := reader.AlertRecord(context.Background(), "system", "alert-1")
	if err != nil || record.StrategyID != "10" || record.BusinessID != 2 || record.Revision != 0 {
		t.Fatalf("record %+v err %v", record, err)
	}
}

func TestAMissingAlertIsNamedAndAnotherAlertIsRefused(t *testing.T) {
	missing := rosterServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	if _, err := missing.AlertRecord(context.Background(), "system", "alert-1"); !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("a 404 was not read as a missing alert: %v", err)
	}
	other := rosterServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := alertJSON()
		body["payload"].(map[string]any)["alert_id"] = "alert-2"
		_ = json.NewEncoder(w).Encode(body)
	})
	if _, err := other.AlertRecord(context.Background(), "system", "alert-1"); err == nil {
		t.Fatal("the record of another alert was accepted")
	}
	failing := rosterServer(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusInternalServerError) })
	if _, err := failing.AlertRecord(context.Background(), "system", "alert-1"); err == nil || errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("a failing Console was read as a missing alert: %v", err)
	}
}
