// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"errors"
	"sync"
	"time"
)

// The Console operations this process calls, closed. Each has its own
// record because they fail independently: a link built before the roster
// existed answers every reconciliation and refuses every browse, and one
// record for both would flip between success and failure every minute.
const (
	// ConsoleOpRoster is the browse of the link's strategy roster, which only
	// the control leader walks.
	ConsoleOpRoster = "roster"
	// ConsoleOpReconcile is one strategy's reconciliation: the calibration
	// every replica runs, and the leader's read of an absent strategy's
	// alerts.
	ConsoleOpReconcile = "reconcile"
	// ConsoleOpAlertRecord is one alert's record. The link answering that it
	// has no such alert is an answer, not a failure.
	ConsoleOpAlertRecord = "alert_record"
	// ConsoleOpEventSource is this deployment's event source definition on
	// the link, read for how the link keys its alerts.
	ConsoleOpEventSource = "event_source"
)

// ConsoleOps is every operation a ConsoleRecord carries, in the order a
// reader shows them.
var ConsoleOps = []string{ConsoleOpRoster, ConsoleOpReconcile, ConsoleOpAlertRecord, ConsoleOpEventSource}

// ConsoleCall is what this process has seen of one operation: how many
// calls and failures, when it last completed, when it last failed and what
// the failure said. Zero times are "never", not "at the epoch". The failure
// text is this package's own sentence, which never carries the address or
// the credentials.
type ConsoleCall struct {
	Calls         uint64
	Failures      uint64
	LastSuccessAt time.Time
	LastFailureAt time.Time
	LastFailure   string
	// LatestFailed is whether the latest call failed. Kept as the outcome
	// itself rather than read off the two times: a success and a failure
	// inside one tick of the clock would otherwise read as not failing.
	LatestFailed bool
}

// ConsoleRecord is what this process has seen of the link's Console: each
// operation's record, and the link's own health as the last roster page
// carried it.
type ConsoleRecord struct {
	Calls map[string]ConsoleCall
	// Link is the link's account of itself from the last roster page read,
	// and LinkReadAt when that was; zero until a roster page has been read,
	// which on a replica that is not the control leader is never.
	Link       LinkHealth
	LinkReadAt time.Time
	// EventSource is this deployment's event source on the link as last
	// read, and EventSourceReadAt when; zero until read.
	EventSource       EventSourceKeying
	EventSourceReadAt time.Time
}

type consoleCalls struct {
	mu                sync.Mutex
	calls             map[string]ConsoleCall
	link              LinkHealth
	linkReadAt        time.Time
	eventSource       EventSourceKeying
	eventSourceReadAt time.Time
}

func (calls *consoleCalls) record(op string, at time.Time, err error) {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	if calls.calls == nil {
		calls.calls = make(map[string]ConsoleCall, len(ConsoleOps))
	}
	call := calls.calls[op]
	call.Calls++
	call.LatestFailed = err != nil
	if err == nil {
		call.LastSuccessAt = at
	} else {
		call.Failures++
		call.LastFailureAt, call.LastFailure = at, err.Error()
	}
	calls.calls[op] = call
}

func (calls *consoleCalls) observeLink(health LinkHealth, at time.Time) {
	calls.mu.Lock()
	calls.link, calls.linkReadAt = health, at
	calls.mu.Unlock()
}

// Record is what this process has seen of the Console so far. Every
// operation has an entry, zero included, so an operation never called reads
// as never called rather than as missing.
func (reader *HTTPReconciler) Record() ConsoleRecord {
	reader.calls.mu.Lock()
	defer reader.calls.mu.Unlock()
	record := ConsoleRecord{Calls: make(map[string]ConsoleCall, len(ConsoleOps)),
		Link: reader.calls.link, LinkReadAt: reader.calls.linkReadAt,
		EventSource: reader.calls.eventSource.clone(), EventSourceReadAt: reader.calls.eventSourceReadAt}
	for _, op := range ConsoleOps {
		record.Calls[op] = reader.calls.calls[op]
	}
	return record
}

// Target is the target the last successful resolution chose and when, zero
// until one has.
func (reader *HTTPReconciler) Target() (TargetBinding, time.Time) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	return reader.binding, reader.resolvedAt
}

func (reader *HTTPReconciler) now() time.Time {
	if reader.options.Now != nil {
		return reader.options.Now()
	}
	return time.Now()
}

// Reconcile reads one strategy's reconciliation from the link's Console.
func (reader *HTTPReconciler) Reconcile(ctx context.Context, key StrategyKey) (Reconciliation, error) {
	result, err := reader.reconcile(ctx, key)
	reader.calls.record(ConsoleOpReconcile, reader.now(), err)
	return result, err
}

// Roster reads one page of the link's strategy list. An empty cursor starts
// a walk.
func (reader *HTTPReconciler) Roster(ctx context.Context, cursor string) (RosterPage, error) {
	page, err := reader.roster(ctx, cursor)
	at := reader.now()
	reader.calls.record(ConsoleOpRoster, at, err)
	if err == nil {
		reader.calls.observeLink(page.Health, at)
	}
	return page, err
}

// AlertRecord reads one alert's record from the link's Console.
func (reader *HTTPReconciler) AlertRecord(ctx context.Context, tenantID, alertID string) (AlertRecord, error) {
	record, err := reader.alertRecord(ctx, tenantID, alertID)
	outcome := err
	if errors.Is(err, ErrAlertNotFound) {
		outcome = nil
	}
	reader.calls.record(ConsoleOpAlertRecord, reader.now(), outcome)
	return record, err
}
