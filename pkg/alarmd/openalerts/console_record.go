// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
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

// The classes of a Console failure that are not the transport's: an answer
// other than 200, and a body that could not be read whole or decoded.
const (
	ConsoleFailureStatus     = "status"
	ConsoleFailureIncomplete = "incomplete"
)

// ConsoleFailureClasses is every class a Console failure is counted under:
// the transport's words (execution.TransportFailureWords), then status and
// incomplete. Any other failure of an operation is other.
var ConsoleFailureClasses = append(append([]string(nil), execution.TransportFailureWords...),
	ConsoleFailureStatus, ConsoleFailureIncomplete)

// consoleFailureClass is the class err is counted under.
func consoleFailureClass(err error) string {
	var transport transportError
	var status statusError
	switch {
	case errors.As(err, &transport):
		return string(transport)
	case errors.As(err, &status):
		return ConsoleFailureStatus
	case errors.Is(err, ErrIncomplete):
		return ConsoleFailureIncomplete
	}
	return execution.TransportFailureOther
}

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
	// LastFailureClass is the class of the last failure
	// (ConsoleFailureClasses), and FailuresByClass the failures under each.
	LastFailureClass string
	FailuresByClass  map[string]uint64
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

// record counts one call of op. It answers whether the failure is one to
// tell: the first after a success or none, or one whose class differs from
// the failure before it.
func (calls *consoleCalls) record(op string, at time.Time, err error) (tell bool, class string, failures uint64) {
	calls.mu.Lock()
	defer calls.mu.Unlock()
	if calls.calls == nil {
		calls.calls = make(map[string]ConsoleCall, len(ConsoleOps))
	}
	call := calls.calls[op]
	call.Calls++
	wasFailing, previous := call.LatestFailed, call.LastFailureClass
	call.LatestFailed = err != nil
	if err == nil {
		call.LastSuccessAt = at
	} else {
		class = consoleFailureClass(err)
		call.Failures++
		call.LastFailureAt, call.LastFailure, call.LastFailureClass = at, err.Error(), class
		// A new map each time: Record hands the map out, and one it has
		// handed out is never written again.
		byClass := make(map[string]uint64, len(call.FailuresByClass)+1)
		for name, count := range call.FailuresByClass {
			byClass[name] = count
		}
		byClass[class]++
		call.FailuresByClass = byClass
		tell, failures = !wasFailing || previous != class, call.Failures
	}
	calls.calls[op] = call
	return tell, class, failures
}

// record counts one call of op in the reader's record and tells
// OnFailure, outside the record's lock, of a failure record says to.
func (reader *HTTPReconciler) record(op string, at time.Time, err error) {
	tell, class, failures := reader.calls.record(op, at, err)
	if tell && reader.options.OnFailure != nil {
		reader.options.OnFailure(op, class, failures)
	}
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
	reader.record(ConsoleOpReconcile, reader.now(), err)
	return result, err
}

// Roster reads one page of the link's strategy list. An empty cursor starts
// a walk.
func (reader *HTTPReconciler) Roster(ctx context.Context, cursor string) (RosterPage, error) {
	page, err := reader.roster(ctx, cursor)
	at := reader.now()
	reader.record(ConsoleOpRoster, at, err)
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
	reader.record(ConsoleOpAlertRecord, reader.now(), outcome)
	return record, err
}
