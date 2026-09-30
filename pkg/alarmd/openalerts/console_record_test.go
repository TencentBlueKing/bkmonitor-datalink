// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// consoleServer answers the targets and reconciliation of a working link,
// and hands browse and alert records to the caller.
func consoleServer(t *testing.T, browse, alert http.HandlerFunc) *HTTPReconciler {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/local-api/strategy-index/targets":
			_ = json.NewEncoder(w).Encode([]TargetBinding{testBinding()})
		case r.URL.Path == "/local-api/strategy-index/reconcile":
			_ = json.NewEncoder(w).Encode(reconciliationJSON())
		case r.URL.Path == "/local-api/strategy-index/browse":
			browse(w, r)
		case strings.HasPrefix(r.URL.Path, "/local-api/alerts/"):
			alert(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "user",
		Password: "secret", Index: testIndex(), MaxResponseBytes: 1 << 20, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

// A link built before the roster existed answers every reconciliation and
// refuses every browse. One record for both would read as whichever ran
// last; each operation keeps its own, so the roster reads as failing while
// the calibration reads as answering, beside each other.
func TestALinkOlderThanTheRosterFailsTheRosterAloneAndSaysSo(t *testing.T) {
	reader := consoleServer(t, http.NotFound, http.NotFound)
	if _, err := reader.Roster(context.Background(), ""); err == nil {
		t.Fatal("a 404 browse was read as a page")
	}
	if _, err := reader.Reconcile(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	record := reader.Record()
	roster, reconcile := record.Calls[ConsoleOpRoster], record.Calls[ConsoleOpReconcile]
	if !roster.LatestFailed || roster.Calls != 1 || roster.Failures != 1 || !strings.Contains(roster.LastFailure, "status 404") {
		t.Fatalf("the roster's own record does not say it was refused: %+v", roster)
	}
	if reconcile.LatestFailed || reconcile.Calls != 1 || reconcile.Failures != 0 || reconcile.LastSuccessAt.IsZero() {
		t.Fatalf("the reconciliation that answered reads as %+v", reconcile)
	}
	if strings.Contains(roster.LastFailure, "secret") || strings.Contains(roster.LastFailure, "127.0.0.1") {
		t.Fatalf("the failure text carries the address or the credentials: %q", roster.LastFailure)
	}
	if !record.LinkReadAt.IsZero() {
		t.Fatal("a refused browse left a reading of the link's health")
	}
}

// Every operation has an entry from the start, so one never called reads as
// never called rather than as missing.
func TestAConsoleNeverCalledListsEveryOperationAtZero(t *testing.T) {
	record := consoleServer(t, http.NotFound, http.NotFound).Record()
	if len(record.Calls) != len(ConsoleOps) {
		t.Fatalf("%d operations recorded for %d declared", len(record.Calls), len(ConsoleOps))
	}
	for _, op := range ConsoleOps {
		if call, present := record.Calls[op]; !present || call.Calls != 0 || call.LatestFailed {
			t.Errorf("operation %s before any call: %+v (present %v)", op, call, present)
		}
	}
}

// The link answering that it has no such alert is an answer. Counted as a
// failure it would make a working Console read as unreadable every time a
// close looked for a record the link had already let go.
func TestAnAlertTheLinkDoesNotHaveIsAnAnswerNotAFailure(t *testing.T) {
	reader := consoleServer(t, http.NotFound, http.NotFound)
	if _, err := reader.AlertRecord(context.Background(), "system", "a-1"); err != ErrAlertNotFound {
		t.Fatalf("err = %v", err)
	}
	if call := reader.Record().Calls[ConsoleOpAlertRecord]; call.LatestFailed || call.Failures != 0 || call.Calls != 1 {
		t.Fatalf("a 404 alert record was recorded as %+v", call)
	}
	failing := consoleServer(t, http.NotFound, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, _ = failing.AlertRecord(context.Background(), "system", "a-1")
	if call := failing.Record().Calls[ConsoleOpAlertRecord]; !call.LatestFailed || call.Failures != 1 {
		t.Fatalf("a 500 alert record was recorded as %+v", call)
	}
}

// The link's own health rides on the roster page, and is kept from it.
func TestTheLinksOwnHealthIsKeptFromTheRosterPage(t *testing.T) {
	reader := consoleServer(t, func(w http.ResponseWriter, r *http.Request) {
		body := browseJSON()
		body["health"].(map[string]any)["error"] = "discovery_failed"
		_ = json.NewEncoder(w).Encode(body)
	}, http.NotFound)
	if _, err := reader.Roster(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	record := reader.Record()
	if record.LinkReadAt.IsZero() || record.Link.Error != "discovery_failed" || record.Link.PendingCount != 2 {
		t.Fatalf("the link's health was not kept: %+v", record)
	}
	if record.Calls[ConsoleOpRoster].LatestFailed {
		t.Fatal("a page that answered, from a link that says it is behind, was recorded as a failed call")
	}
}
