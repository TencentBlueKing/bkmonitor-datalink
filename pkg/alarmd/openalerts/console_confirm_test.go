// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

// consoleStub is a link Console whose event source answer the test sets.
type consoleStub struct {
	mu          sync.Mutex
	eventSource func(http.ResponseWriter)
	reads       int
	targets     int
	target      TargetBinding
}

func (stub *consoleStub) serve(w http.ResponseWriter, r *http.Request) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	switch r.URL.Path {
	case "/local-api/strategy-index/targets":
		stub.targets++
		_ = json.NewEncoder(w).Encode([]TargetBinding{stub.target})
	case "/local-api/event-sources/source":
		stub.reads++
		stub.eventSource(w)
	default:
		_ = json.NewEncoder(w).Encode(reconciliationJSON())
	}
}

func (stub *consoleStub) answer(answer func(http.ResponseWriter)) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.eventSource = answer
}

func eventSourceAnswer(published int64, deleted bool) func(http.ResponseWriter) {
	return func(w http.ResponseWriter) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "source", "revision": 3, "published": published, "deleted": deleted,
			"spec": map[string]any{"fingerprint_mode": "field", "fingerprint_field": "source_alert_id"}})
	}
}

// The keying is the Console's word, read again every KeyingEvery. A read
// that fails keeps the last answer and its time, which then ages; a read
// that finds the source deleted is the answer at once. Each reading reaches
// the copy as the trusted state.
func TestTheKeyingIsKeptWhileReadsFailAndChangesOnTheNextAnswer(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	stub := &consoleStub{target: testBinding(), eventSource: eventSourceAnswer(3, false)}
	server := httptest.NewServer(http.HandlerFunc(stub.serve))
	defer server.Close()
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "user", Password: "secret",
		Index: testIndex(), MaxResponseBytes: 1 << 20, Now: c.now, LocationConfirmed: true, KeyingEvery: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Reconcile(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	keyed, first, known := reader.KeyedByAlertID()
	if !keyed || !known || !first.Equal(c.now()) {
		t.Fatalf("keyed %v known %v at %s, want keyed as of the first read", keyed, known, first)
	}

	// Inside KeyingEvery nothing is read again.
	c.advance(10 * time.Minute)
	if _, err := reader.Reconcile(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	if stub.reads != 1 {
		t.Fatalf("event source read %d times inside the interval, want once", stub.reads)
	}

	// Due, and failing: the last answer stands, with its time.
	stub.answer(func(w http.ResponseWriter) { http.Error(w, "unavailable", http.StatusBadGateway) })
	c.advance(25 * time.Minute)
	if _, err := reader.Reconcile(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	if stub.reads != 2 {
		t.Fatalf("event source read %d times, want the due read made", stub.reads)
	}
	if keyed, at, known := reader.KeyedByAlertID(); !keyed || !known || !at.Equal(first) {
		t.Fatalf("keyed %v known %v at %s, want the last answer kept with its own time %s", keyed, known, at, first)
	}

	// Due again, and the source is deleted: no longer keyed, on that read.
	stub.answer(eventSourceAnswer(3, true))
	c.advance(31 * time.Minute)
	if _, err := reader.Reconcile(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	if keyed, at, known := reader.KeyedByAlertID(); keyed || !known || !at.Equal(c.now()) {
		t.Fatalf("keyed %v known %v at %s, want a deleted source read as not keyed now", keyed, known, at)
	}
	cache, err := NewIndex(IndexOptions{Source: setReaderFunc(func(context.Context, StrategyKey) ([]string, error) { return nil, nil }),
		Subscriber: subscriberFunc(func(ctx context.Context, ready func(bool), _ func(StrategyKey), _ func(NoticeRefusal)) error {
			ready(true)
			<-ctx.Done()
			return ctx.Err()
		}),
		Now: c.now, MaxStrategies: 1, MaxMembers: 1, MaxBytes: 1 << 10, MaxLocalEntries: 1, ReadBatch: 1, ReconcileBatch: 1,
		RefreshInterval: time.Minute, IndexInterval: time.Minute, ReconcileInterval: time.Hour, CalibrationMaxAge: time.Hour,
		LocalRetention: time.Minute, CycleTimeout: time.Second, Facts: reader})
	if err != nil {
		t.Fatal(err)
	}
	if stats := cache.Stats(); stats.UnavailableReason != UnavailableKeyingUnconfirmed || stats.KeyedByAlertID == nil || *stats.KeyedByAlertID {
		t.Fatalf("reason %q keyed %v, want the copy unavailable on the deleted source", stats.UnavailableReason, stats.KeyedByAlertID)
	}
}

// A reconciliation that finds the link writing elsewhere withdraws the
// confirmation and hands its target over; one that agrees confirms again.
func TestAReconciliationConfirmsOrWithdrawsTheLocation(t *testing.T) {
	stub := &consoleStub{target: testBinding(), eventSource: eventSourceAnswer(3, false)}
	server := httptest.NewServer(http.HandlerFunc(stub.serve))
	defer server.Close()
	var handed []TargetBinding
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "user", Password: "secret",
		Index: testIndex(), MaxResponseBytes: 1 << 20, OnLocationMismatch: func(target TargetBinding) { handed = append(handed, target) }})
	if err != nil {
		t.Fatal(err)
	}
	if reader.LocationConfirmed() {
		t.Fatal("a location not taken from the Console started confirmed")
	}
	if _, err := reader.Reconcile(context.Background(), keyA); err != nil || !reader.LocationConfirmed() {
		t.Fatalf("err %v confirmed %v, want an agreeing reconciliation to confirm", err, reader.LocationConfirmed())
	}
	moved := testBinding()
	moved.Database++
	stub.mu.Lock()
	stub.target = moved
	stub.mu.Unlock()
	if _, err := reader.Reconcile(context.Background(), keyA); err == nil || reader.LocationConfirmed() {
		t.Fatalf("err %v confirmed %v, want a mismatch refused and the confirmation withdrawn", err, reader.LocationConfirmed())
	}
	if len(handed) != 1 || !reflect.DeepEqual(handed[0], moved) {
		t.Fatalf("handed %+v, want the target the reconciliation read", handed)
	}
}

// A keying read for one event source says nothing about another: once the
// target names another source, the keying is unknown until a read of that
// source answers.
func TestAKeyingReadForAnotherSourceIsNoAnswer(t *testing.T) {
	stub := &consoleStub{target: testBinding(), eventSource: eventSourceAnswer(3, false)}
	server := httptest.NewServer(http.HandlerFunc(stub.serve))
	defer server.Close()
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "user", Password: "secret",
		Index: testIndex(), MaxResponseBytes: 1 << 20, LocationConfirmed: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Reconcile(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	if _, _, known := reader.KeyedByAlertID(); !known {
		t.Fatal("fixture: the keying was not read")
	}
	other := testBinding()
	other.EventSourceID, other.Sources = "another", []string{"another"}
	stub.mu.Lock()
	stub.target = other
	stub.mu.Unlock()
	_, _ = reader.Reconcile(context.Background(), keyA)
	if keyed, _, known := reader.KeyedByAlertID(); known {
		t.Fatalf("keyed %v known for the new source on the old source's read", keyed)
	}
}

// Moving the reads to where a Console target names leaves the location
// unconfirmed until a resolution agrees with it - nothing read in between
// is the link's word - and the next one, the copy's own refresh with no
// strategy calibrated, confirms it and reads the keying again.
func TestAMoveIsConfirmedByTheNextResolutionAndRereadsTheKeying(t *testing.T) {
	stub := &consoleStub{target: testBinding(), eventSource: eventSourceAnswer(3, false)}
	server := httptest.NewServer(http.HandlerFunc(stub.serve))
	defer server.Close()
	moved := testBinding()
	moved.Database++
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "user", Password: "secret",
		Index: testIndex(), MaxResponseBytes: 1 << 20, KeyingEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Reconcile(context.Background(), keyA); err != nil {
		t.Fatal(err)
	}
	stub.mu.Lock()
	stub.target = moved
	stub.mu.Unlock()
	_, _ = reader.Reconcile(context.Background(), keyA)
	if reader.LocationConfirmed() {
		t.Fatal("fixture: the mismatch did not withdraw the confirmation")
	}
	// A refresh while the link writes elsewhere asks once and then waits its
	// minute; the move starts that wait over.
	reader.Refresh(context.Background())
	if err := reader.SetIndexLocation(IndexLocation{KeyPrefix: moved.KeyPrefix, Address: moved.Address, Database: moved.Database}); err != nil {
		t.Fatal(err)
	}
	if reader.LocationConfirmed() {
		t.Fatal("a move was confirmed before any resolution agreed with it")
	}
	if reader.KeyingAsked() {
		t.Fatal("the keying counts as asked for the new place before it was")
	}
	reads := stub.reads
	reader.Refresh(context.Background())
	if !reader.LocationConfirmed() || !reader.KeyingAsked() {
		t.Fatal("the refresh after the move neither confirmed the place the Console names nor asked the keying")
	}
	if stub.reads != reads+1 {
		t.Fatalf("event source reads %d after the move, want one more than %d: the keying is read again", stub.reads, reads)
	}
}

// The facts are refreshed without a strategy to calibrate: a first Refresh
// resolves the target - confirming the place when it agrees - and reads the
// keying; once confirmed, a Refresh asks the targets nothing more. While the
// link writes elsewhere, a Refresh asks the targets at most once per minute,
// not on every one-second round.
func TestRefreshLearnsBothFactsWithNoStrategy(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	stub := &consoleStub{target: testBinding(), eventSource: eventSourceAnswer(3, false)}
	server := httptest.NewServer(http.HandlerFunc(stub.serve))
	defer server.Close()
	reader, err := NewHTTPReconciler(HTTPReconcilerOptions{BaseURL: server.URL, Client: server.Client(), Username: "user", Password: "secret",
		Index: testIndex(), MaxResponseBytes: 1 << 20, Now: c.now, KeyingEvery: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	reader.Refresh(context.Background())
	if keyed, _, known := reader.KeyedByAlertID(); !reader.LocationConfirmed() || !known || !keyed {
		t.Fatalf("confirmed %v keyed %v known %v after one refresh, want both facts", reader.LocationConfirmed(), keyed, known)
	}
	targets := stub.targets
	for _, after := range []time.Duration{time.Second, 2 * time.Minute} {
		c.advance(after)
		reader.Refresh(context.Background())
	}
	if stub.targets != targets {
		t.Fatalf("targets asked %d times, want %d: a confirmed place is not asked again by the refresh", stub.targets, targets)
	}
	moved := testBinding()
	moved.Database++
	stub.mu.Lock()
	stub.target = moved
	stub.mu.Unlock()
	if _, err := reader.Reconcile(context.Background(), keyA); err == nil || reader.LocationConfirmed() {
		t.Fatal("fixture: the reconciliation did not find the link elsewhere")
	}
	targets = stub.targets
	for round := 0; round < 59; round++ {
		c.advance(time.Second)
		reader.Refresh(context.Background())
	}
	if stub.targets != targets+1 {
		t.Fatalf("targets asked %d times in 59 one-second rounds while the link writes elsewhere, want once", stub.targets-targets)
	}
}
