// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

type mapView map[execution.QueryGroupIdentity]viewstream.Entry

func (view mapView) Entry(queryGroup execution.QueryGroupIdentity) (viewstream.Entry, bool) {
	entry, ok := view[queryGroup]
	return entry, ok
}

// Each of the three checks fails on its own and names itself; only all three
// holding yields the timeline revision to hint with. The record's side is
// the lease, never the view: an entry the renewal does not vouch for is not
// executed from the view whatever the entry says.
func TestTheViewGateFailsEachCheckOnItsOwnAndPassesOnlyAllThree(t *testing.T) {
	gate := newViewExecutionGate()
	entry := viewstream.Entry{QueryGroup: "qg-1",
		Content:    &viewstream.Content{ObjectDigest: "obj-a"},
		Assignment: viewstream.Assignment{DesiredWorkerID: "w1", Revision: 3, ContentScope: "obj-a", TimelineRecordRevision: 12}}
	gate.attach(mapView{"qg-1": entry, "qg-draining": {QueryGroup: "qg-draining", Assignment: entry.Assignment}})
	good := ownership.Lease{ContentScope: "obj-a", TimelineRecordRevision: 12}
	cases := []struct {
		name    string
		group   execution.QueryGroupIdentity
		lease   ownership.Lease
		held    bool
		want    viewGateOutcome
		hinting uint64
	}{
		{"all three hold", "qg-1", good, true, viewGateExecutable, 12},
		{"no lease", "qg-1", good, false, viewGateNoLease, 0},
		{"not in the view", "qg-2", good, true, viewGateNotInView, 0},
		{"in the view without content", "qg-draining", good, true, viewGateNoContent, 0},
		{"renewal names another scope", "qg-1", ownership.Lease{ContentScope: "obj-old", TimelineRecordRevision: 12}, true, viewGateScopeMismatch, 0},
		{"renewal names the scope as pending", "qg-1", ownership.Lease{ContentScope: "obj-old", PendingContentScope: "obj-a", TimelineRecordRevision: 12}, true, viewGateExecutable, 12},
		{"record has not said the timeline", "qg-1", ownership.Lease{ContentScope: "obj-a"}, true, viewGateTimelineUnsaid, 0},
		{"record says another timeline", "qg-1", ownership.Lease{ContentScope: "obj-a", TimelineRecordRevision: 11}, true, viewGateTimelineMismatch, 0},
	}
	for _, testCase := range cases {
		hint, outcome := gate.judge(testCase.group, testCase.lease, testCase.held)
		if outcome != testCase.want || hint != testCase.hinting {
			t.Errorf("%s: outcome %s hint %d, want %s hint %d", testCase.name, outcome, hint, testCase.want, testCase.hinting)
		}
	}
	// The view has not said the timeline either: not executable, whatever
	// the record says, because the third check compares two numbers.
	unsaid := entry
	unsaid.Assignment.TimelineRecordRevision = 0
	gate.attach(mapView{"qg-1": unsaid})
	if hint, outcome := gate.judge("qg-1", good, true); outcome != viewGateTimelineUnsaid || hint != 0 {
		t.Errorf("view without a timeline revision: outcome %s hint %d, want %s", outcome, hint, viewGateTimelineUnsaid)
	}
}

// hintCapturingExecutor reports the hint its context carried as the result's
// reason, so a test can read what execution would have read with.
type hintCapturingExecutor struct{}

func (hintCapturingExecutor) Execute(ctx context.Context, _ execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	return execution.SlotExecutionResult{ReasonCode: execution.ReasonCode("hint:" + strconv.FormatUint(controlplane.TimelineRevisionHint(ctx), 10))}, nil
}

type hintCapturingCatalog struct {
	productionPhaseTwoSlotCatalog
	hints []uint64
}

func (catalog *hintCapturingCatalog) NextSlotAfter(ctx context.Context, _ execution.QueryGroupIdentity, at execution.EvaluationTime) (execution.EvaluationTime, error) {
	catalog.hints = append(catalog.hints, controlplane.TimelineRevisionHint(ctx))
	return at + 60, nil
}

// The gated catalog carries the hint on a read the gate lets through and
// refuses by name one it does not (batch 4b: the Runner ends the round as
// view_not_executable rather than reading the control plane the old way),
// decides per read, counts the latest outcome per Query Group for the
// receipt, and forgets a Query Group let go.
func TestTheGatedCatalogHintsOnlyWhenTheGateLetsTheReadThrough(t *testing.T) {
	gate := newViewExecutionGate()
	entry := viewstream.Entry{QueryGroup: "qg-1", Content: &viewstream.Content{ObjectDigest: "obj-a"},
		Assignment: viewstream.Assignment{DesiredWorkerID: "w1", Revision: 3, ContentScope: "obj-a", TimelineRecordRevision: 12}}
	gate.attach(mapView{"qg-1": entry})
	store := newViewGateTestStore(t)
	session := openViewGateTestSession(t, store, "qg-1", 12, "obj-a")
	next := &hintCapturingCatalog{}
	catalog := &viewGatedCatalog{next: next, gate: gate, queryGroup: "qg-1", session: session}
	if _, err := catalog.NextSlotAfter(context.Background(), "qg-1", 60); err != nil {
		t.Fatal(err)
	}
	if len(next.hints) != 1 || next.hints[0] != 12 {
		t.Fatalf("a read the gate let through carried hints %v, want [12]", next.hints)
	}
	if gate.SwitchedQueryGroups([]execution.QueryGroupIdentity{"qg-1"}) != 1 || gate.Counts()[string(viewGateExecutable)] != 1 {
		t.Fatalf("after an executable read the gate counts %d switched, %v", gate.SwitchedQueryGroups([]execution.QueryGroupIdentity{"qg-1"}), gate.Counts())
	}
	// Asked about a version that does not name qg-1, the executable qg-1
	// does not count: the count is of the version's entries.
	if gate.SwitchedQueryGroups([]execution.QueryGroupIdentity{"qg-9"}) != 0 {
		t.Fatal("a Query Group the version does not name counted toward its switched")
	}
	// The view moves on to a timeline the record has not confirmed: the next
	// read is refused by the gate's word, reaches no catalog, and the count
	// falls, with no new version needed.
	moved := entry
	moved.Assignment.TimelineRecordRevision = 13
	gate.attach(mapView{"qg-1": moved})
	var refused *scheduler.ViewNotExecutableError
	if _, err := catalog.NextSlotAfter(context.Background(), "qg-1", 120); !errors.As(err, &refused) || refused.Reason != string(viewGateTimelineMismatch) {
		t.Fatalf("a read the gate refused returned %v, want ViewNotExecutableError{%s}", err, viewGateTimelineMismatch)
	}
	if len(next.hints) != 1 {
		t.Fatalf("a refused read reached the catalog: hints %v", next.hints)
	}
	// Execution under the same gate is refused the same way, and carries the
	// hint when let through.
	executor := &viewGatedExecutor{next: hintCapturingExecutor{}, gate: gate, queryGroup: "qg-1", session: session}
	if _, err := executor.Execute(context.Background(), execution.SlotExecutionRequest{}); !errors.As(err, &refused) {
		t.Fatalf("execution under a refusing gate returned %v, want ViewNotExecutableError", err)
	}
	gate.attach(mapView{"qg-1": entry})
	result, err := executor.Execute(context.Background(), execution.SlotExecutionRequest{})
	if err != nil || result.ReasonCode != "hint:12" {
		t.Fatalf("execution under a passing gate = (%+v, %v), want the hint 12 in its context", result, err)
	}
	gate.attach(mapView{"qg-1": moved})
	if _, err := catalog.NextSlotAfter(context.Background(), "qg-1", 120); err == nil {
		t.Fatal("the gate let a read through after the view moved again")
	}
	if gate.SwitchedQueryGroups([]execution.QueryGroupIdentity{"qg-1"}) != 0 || gate.Counts()[string(viewGateTimelineMismatch)] != 1 {
		t.Fatalf("after a refused read the gate counts %d switched, %v", gate.SwitchedQueryGroups([]execution.QueryGroupIdentity{"qg-1"}), gate.Counts())
	}
	gate.forget("qg-1")
	if counts := gate.Counts(); counts[string(viewGateTimelineMismatch)] != 0 {
		t.Fatalf("a forgotten Query Group still counts: %v", counts)
	}
}

// When the view names a newer timeline revision than the lease last
// brought, the record has moved and the lease has not caught up: the gate
// renews the lease once, now, and judges again on what the record says -
// executable when the record agrees with the view, refused when the view
// is ahead of the record too. A cutover moves the records and the view
// within a second and the leases within a renewal interval; without the
// early renewal every Query Group rechecked in that interval was refused,
// a burst per Worker per cutover. Without a renewal to make, the gate
// refuses as before.
func TestTheGateRenewsTheLeaseOnceWhenTheViewIsAheadOfIt(t *testing.T) {
	ctx := context.Background()
	gate := newViewExecutionGate()
	entry := viewstream.Entry{QueryGroup: "qg-1", Content: &viewstream.Content{ObjectDigest: "obj-a"},
		Assignment: viewstream.Assignment{DesiredWorkerID: "w1", Revision: 3, ContentScope: "obj-a", TimelineRecordRevision: 12}}
	gate.attach(mapView{"qg-1": entry})
	store := newViewGateTestStore(t)
	session, authority := openViewGateTestSessionWithAuthority(t, store, "qg-1", 12, "obj-a")
	now := time.UnixMilli(1_700_000_000_000)
	renewals := 0
	renew := func(ctx context.Context) error {
		renewals++
		return session.Renew(ctx, now.Add(time.Duration(renewals)*time.Second), time.Minute)
	}
	next := &hintCapturingCatalog{}
	catalog := &viewGatedCatalog{next: next, gate: gate, queryGroup: "qg-1", session: session, renew: renew}

	// The record moves to 13 (a cutover stamped it) and the view says so;
	// the lease still says 12 until its next renewal.
	record, err := store.ReadAssignment(ctx, "qg-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
		QueryGroup: "qg-1", DesiredWorkerID: "worker-1", PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
		ContentScope: "obj-a", TimelineRecordRevision: 13, ExpectedRecordRevision: record.RecordRevision,
	}); err != nil {
		t.Fatal(err)
	}
	moved := entry
	moved.Assignment.TimelineRecordRevision = 13
	gate.attach(mapView{"qg-1": moved})
	if lease, _ := session.Current(); lease.TimelineRecordRevision != 12 {
		t.Fatalf("the lease already says %d; the case needs it behind the view", lease.TimelineRecordRevision)
	}
	if _, err := catalog.NextSlotAfter(ctx, "qg-1", 60); err != nil {
		t.Fatalf("a read with the view ahead of the lease and the record agreeing = %v, want the lease renewed and the read let through", err)
	}
	if renewals != 1 || len(next.hints) != 1 || next.hints[0] != 13 {
		t.Fatalf("renewals %d, hints %v: want one early renewal and the read hinted at the record's 13", renewals, next.hints)
	}
	if lease, _ := session.Current(); lease.TimelineRecordRevision != 13 {
		t.Fatalf("the lease says %d after the early renewal, want 13", lease.TimelineRecordRevision)
	}
	if made, settled, failed := gate.Renewals(); made != 1 || settled != 1 || failed != 0 {
		t.Fatalf("gate renewals = (%d, %d, %d), want one made and settled", made, settled, failed)
	}
	if counts := gate.Counts(); counts[string(viewGateExecutable)] != 1 {
		t.Fatalf("gate counts after the settled renewal = %v", counts)
	}

	// The view moves on to 14 with the record still at 13: the renewal
	// brings 13 back, and the read is refused by the gate's word.
	ahead := entry
	ahead.Assignment.TimelineRecordRevision = 14
	gate.attach(mapView{"qg-1": ahead})
	var refused *scheduler.ViewNotExecutableError
	if _, err := catalog.NextSlotAfter(ctx, "qg-1", 120); !errors.As(err, &refused) || refused.Reason != string(viewGateTimelineMismatch) {
		t.Fatalf("a read with the view ahead of the record = %v, want ViewNotExecutableError{%s}", err, viewGateTimelineMismatch)
	}
	if made, settled, failed := gate.Renewals(); renewals != 2 || made != 2 || settled != 1 || failed != 0 {
		t.Fatalf("renewals %d, gate (%d, %d, %d): want the renewal made and not settled", renewals, made, settled, failed)
	}
	// A renewal that fails says nothing about the view against the record:
	// refused as it stood, counted as failed rather than unsettled.
	gate.attach(mapView{"qg-1": moved})
	failing := &viewGatedCatalog{next: next, gate: gate, queryGroup: "qg-1", session: session,
		renew: func(context.Context) error { return errors.New("store unreachable") }}
	// The lease says 13 and the view 13 now; make the view ahead again so
	// the gate has a renewal to make.
	gate.attach(mapView{"qg-1": ahead})
	if _, err := failing.NextSlotAfter(ctx, "qg-1", 120); !errors.As(err, &refused) {
		t.Fatalf("a read whose renewal failed = %v, want refused as it stood", err)
	}
	if made, settled, failed := gate.Renewals(); made != 3 || settled != 1 || failed != 1 {
		t.Fatalf("gate renewals after a failed one = (%d, %d, %d), want it counted as failed, not unsettled", made, settled, failed)
	}
	// The lease ahead of the view - the delta has not arrived - is left to
	// the delta: refused, and no renewal made.
	behind := entry
	behind.Assignment.TimelineRecordRevision = 12
	gate.attach(mapView{"qg-1": behind})
	if _, err := catalog.NextSlotAfter(ctx, "qg-1", 120); !errors.As(err, &refused) {
		t.Fatalf("a read with the lease ahead of the view = %v, want a refusal", err)
	}
	if renewals != 2 {
		t.Fatalf("renewals %d: a lease ahead of the view must not renew", renewals)
	}
	// And with nothing to renew with, the same read is refused outright.
	gate.attach(mapView{"qg-1": ahead})
	plain := &viewGatedCatalog{next: next, gate: gate, queryGroup: "qg-1", session: session}
	if _, err := plain.NextSlotAfter(ctx, "qg-1", 120); !errors.As(err, &refused) || renewals != 2 {
		t.Fatalf("a read without a renewal to make = %v (renewals %d), want refused without renewing", err, renewals)
	}
}

func newViewGateTestStore(t *testing.T) *ownership.RedisStore {
	t.Helper()
	_, client := startPhaseTwoRedis(t)
	store, err := ownership.NewRedisStoreWithClient(client, "alarmd:test:gate:ownership")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// openViewGateTestSession places the Query Group on worker-1 with the given
// content scope and timeline revision, then opens worker-1's session on it,
// so the session's lease carries both from the record.
func openViewGateTestSession(t *testing.T, store *ownership.RedisStore, queryGroup execution.QueryGroupIdentity, timeline uint64, scope string) *ownership.Session {
	t.Helper()
	session, _ := openViewGateTestSessionWithAuthority(t, store, queryGroup, timeline, scope)
	return session
}

// openViewGateTestSessionWithAuthority is openViewGateTestSession with the
// Leader authority the record was published under, for a case that moves
// the record afterwards.
func openViewGateTestSessionWithAuthority(t *testing.T, store *ownership.RedisStore, queryGroup execution.QueryGroupIdentity, timeline uint64, scope string) (*ownership.Session, ownership.PublicationAuthority) {
	t.Helper()
	ctx := context.Background()
	now := time.UnixMilli(1_700_000_000_000)
	authority, err := store.AcquireControlLeader(ctx, "control-1", now, 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishAssignment(ctx, authority, ownership.AssignmentDecision{
		QueryGroup: queryGroup, DesiredWorkerID: "worker-1", PlacementReason: ownership.PlacementRendezvous, DecidedAt: now,
		ContentScope: scope, TimelineRecordRevision: timeline,
	}); err != nil {
		t.Fatal(err)
	}
	session, err := ownership.OpenSession(ctx, store, queryGroup, "worker-1", now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Release(ctx) })
	return session, authority
}

// A refusal from the gate reaches the log on both paths with the gate's
// word for which check failed: the source's schedule_due line and the
// executor's slot_completed line both say retrying VIEW_NOT_EXECUTABLE and
// carry the word, and neither reads as a failure of this deployment. Before
// this, the source observer matched only a retrying or blocked source and
// wrote nothing, and the refusal at execution was an internal_unknown
// failure with the error swallowed.
func TestARefusalFromTheGateIsObservedByNameOnBothPaths(t *testing.T) {
	refusal := &scheduler.ViewNotExecutableError{Reason: "scope_mismatch"}
	var observations []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observations = append(observations, observation)
	})
	source := observedProductionSlotSource{
		next: slotSourceFunc(func(context.Context, execution.QueryGroupIdentity) (scheduler.FrozenSlot, bool, scheduler.SlotDueFacts, error) {
			return scheduler.FrozenSlot{}, false, scheduler.SlotDueFacts{}, refusal
		}),
		observer: observer,
	}
	if _, _, _, err := source.Next(context.Background(), "query-group-1"); !errors.Is(err, refusal) {
		t.Fatalf("Next() error = %v, want the refusal passed through", err)
	}
	due := observationAt(t, observations, observability.StageScheduleDue)
	if due.Result != observability.ResultRetrying || due.ReasonCode != observability.ReasonCode(contract.ReasonViewNotExecutable) ||
		due.Err == nil || !strings.Contains(due.Err.Error(), "scope_mismatch") || due.Trace.QueryGroupKey != "query-group-1" {
		t.Fatalf("schedule_due line = %+v, want retrying %s carrying the gate's word", due, contract.ReasonViewNotExecutable)
	}

	observations = nil
	executor := observedProductionSlotExecutor{
		next: slotExecutorFunc(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
			return execution.SlotExecutionResult{}, refusal
		}),
		observer: observer,
	}
	if _, err := executor.Execute(context.Background(), execution.SlotExecutionRequest{}); !errors.Is(err, refusal) {
		t.Fatalf("Execute() error = %v, want the refusal passed through", err)
	}
	completed := observationAt(t, observations, observability.StageSlotCompleted)
	if completed.Result != observability.ResultRetrying || completed.ReasonCode != observability.ReasonCode(contract.ReasonViewNotExecutable) ||
		completed.Err == nil || !strings.Contains(completed.Err.Error(), "scope_mismatch") {
		t.Fatalf("slot_completed line = %+v, want retrying %s carrying the gate's word", completed, contract.ReasonViewNotExecutable)
	}
}

func observationAt(t *testing.T, observations []observability.Observation, stage observability.Stage) observability.Observation {
	t.Helper()
	for _, observation := range observations {
		if observation.Stage == stage {
			return observation
		}
	}
	t.Fatalf("no %s observation in %+v", stage, observations)
	return observability.Observation{}
}
