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
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// The Worker's side of decision-016 batch 4: whether one Query Group is
// executed from the installed view, decided afresh at every catalog read a
// Slot makes, by three checks that need no state of their own.
//
//  1. The installed view carries the Query Group, with content.
//  2. The lease's renewal brought back the content scope the entry names -
//     or names it as pending, which is the Worker ahead of the record, not
//     behind it (016 section 7.1.1 item 5).
//  3. The lease's renewal brought back the timeline revision the entry
//     names, and it is not zero: zero is the record not saying.
//
// The first is the view's word, the second and third the record's, brought
// by the renewal. A view the record does not vouch for - a delta a
// partitioned old Leader pushed, an entry ahead of a record not yet stamped
// - fails on the record's side, and the Slot is not run: the round ends as
// view_not_executable with the gate's word, and the Runner comes back on
// its backoff, by which time a renewal or a delta has usually moved one
// side (batch 4b; batch 4a read the control plane the old way instead, and
// that binary is what a rollback is). When the three hold, the read
// carries the timeline revision as a hint and the catalog answers it
// without the activation header, which is the poll batch 4 removes; the
// cache and the body are the third party of check 3, and a body at another
// revision falls back to the header on its own
// (controlplane.WithTimelineRevisionHint).
//
// The outcomes are counted per Query Group so the receipt can say how many
// the Worker executes from the view, and by word so the drill can read
// which check is failing and for how long: timeline_stale only inside the
// view's propagation delay is the reading batch 4a is accepted on.
type viewExecutionGate struct {
	view installedView

	mu       sync.Mutex
	outcomes map[execution.QueryGroupIdentity]viewGateOutcome
	// renewals counts the leases renewed ahead of their interval because
	// the view said a newer timeline revision than the lease had brought,
	// and how many of those renewals settled the check. A cutover moves
	// the records and the view within a second and the leases within a
	// renewal interval; without this every Query Group rechecked in that
	// window was refused once or more, a burst per Worker per cutover.
	renewals, renewalsSettled, renewalsFailed uint64
}

// installedView is the one thing the gate asks of the view client.
type installedView interface {
	Entry(execution.QueryGroupIdentity) (viewstream.Entry, bool)
}

type viewGateOutcome string

const (
	viewGateExecutable       viewGateOutcome = "executable"
	viewGateNotInView        viewGateOutcome = "not_in_view"
	viewGateNoContent        viewGateOutcome = "no_content"
	viewGateScopeMismatch    viewGateOutcome = "scope_mismatch"
	viewGateTimelineUnsaid   viewGateOutcome = "timeline_unsaid"
	viewGateTimelineMismatch viewGateOutcome = "timeline_stale"
	viewGateNoLease          viewGateOutcome = "no_lease"
)

// viewGateOutcomes is the closed vocabulary the gate reports in, for the
// metric's series bound and for a reader who wants the list.
var viewGateOutcomes = []viewGateOutcome{
	viewGateExecutable, viewGateNotInView, viewGateNoContent, viewGateScopeMismatch, viewGateTimelineUnsaid, viewGateTimelineMismatch, viewGateNoLease,
}

func newViewExecutionGate() *viewExecutionGate {
	return &viewExecutionGate{outcomes: map[execution.QueryGroupIdentity]viewGateOutcome{}}
}

// attach gives the gate the installed view to read. The client is built
// with the gate as its switched source, so the gate exists first.
func (gate *viewExecutionGate) attach(view installedView) {
	gate.mu.Lock()
	gate.view = view
	gate.mu.Unlock()
}

func (gate *viewExecutionGate) installed() installedView {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.view
}

// judge is the three checks for one Query Group against the lease its
// session holds now. It returns the timeline revision to hint with when
// the Query Group is executed from the view, and the outcome either way.
func (gate *viewExecutionGate) judge(queryGroup execution.QueryGroupIdentity, lease ownership.Lease, held bool) (uint64, viewGateOutcome) {
	revision, outcome, _ := gate.judgeAgainstView(queryGroup, lease, held)
	return revision, outcome
}

// judgeAgainstView is judge with the view's own timeline revision for the
// Query Group beside the verdict: what a caller compares the lease against
// to know which side is behind when the two disagree.
func (gate *viewExecutionGate) judgeAgainstView(queryGroup execution.QueryGroupIdentity, lease ownership.Lease, held bool) (uint64, viewGateOutcome, uint64) {
	if !held {
		return 0, viewGateNoLease, 0
	}
	view := gate.installed()
	if view == nil {
		return 0, viewGateNotInView, 0
	}
	entry, inView := view.Entry(queryGroup)
	if !inView {
		return 0, viewGateNotInView, 0
	}
	viewRevision := entry.Assignment.TimelineRecordRevision
	// In the view without content: a draining Query Group, or one whose
	// Segment carries no object. It is never going to be executed from the
	// view, and a fleet with such entries never reaches switched == installed
	// - its own word, so a reader does not go looking at the stream for a
	// Query Group the stream delivered.
	if entry.Content == nil {
		return 0, viewGateNoContent, viewRevision
	}
	digest := string(entry.Content.ObjectDigest)
	if lease.ContentScope != digest && lease.PendingContentScope != digest {
		return 0, viewGateScopeMismatch, viewRevision
	}
	if lease.TimelineRecordRevision == 0 || viewRevision == 0 {
		return 0, viewGateTimelineUnsaid, viewRevision
	}
	if lease.TimelineRecordRevision != viewRevision {
		return 0, viewGateTimelineMismatch, viewRevision
	}
	return viewRevision, viewGateExecutable, viewRevision
}

// noteRenewal counts a lease renewal the gate made ahead of its interval:
// settled when the check passed on the renewed lease, failed when the
// renewal itself returned an error, unsettled otherwise.
func (gate *viewExecutionGate) noteRenewal(settled, failed bool) {
	gate.mu.Lock()
	gate.renewals++
	switch {
	case failed:
		gate.renewalsFailed++
	case settled:
		gate.renewalsSettled++
	}
	gate.mu.Unlock()
}

// Renewals is the gate's early lease renewals for the metrics: how many
// were made, how many settled the check, and how many failed to renew at
// all. A renewal that failed says nothing about the view against the
// record, so it is not counted among the unsettled.
func (gate *viewExecutionGate) Renewals() (made, settled, failed uint64) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.renewals, gate.renewalsSettled, gate.renewalsFailed
}

// record keeps the latest outcome for a Query Group this Worker runs.
func (gate *viewExecutionGate) record(queryGroup execution.QueryGroupIdentity, outcome viewGateOutcome) {
	gate.mu.Lock()
	gate.outcomes[queryGroup] = outcome
	gate.mu.Unlock()
}

// forget drops a Query Group the Worker no longer runs, so a released
// Query Group neither counts as executed from the view nor as short of it.
func (gate *viewExecutionGate) forget(queryGroup execution.QueryGroupIdentity) {
	gate.mu.Lock()
	delete(gate.outcomes, queryGroup)
	gate.mu.Unlock()
}

// SwitchedQueryGroups is how many of the given Query Groups - a version's
// own entries - this Worker executes from the view as of their latest Slot
// read; the receipt's count. Asked over the version's entries rather than
// over everything the gate remembers, because a Query Group a delta moved
// away stays executable in the gate until its next read, and it must not
// count for a version that no longer names it.
func (gate *viewExecutionGate) SwitchedQueryGroups(of []execution.QueryGroupIdentity) int {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	count := 0
	for _, queryGroup := range of {
		if gate.outcomes[queryGroup] == viewGateExecutable {
			count++
		}
	}
	return count
}

// Counts is the Query Groups by latest outcome, for the metric.
func (gate *viewExecutionGate) Counts() map[string]int {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	counts := make(map[string]int, len(viewGateOutcomes))
	for _, outcome := range viewGateOutcomes {
		counts[string(outcome)] = 0
	}
	for _, outcome := range gate.outcomes {
		counts[string(outcome)]++
	}
	return counts
}

// viewGatedCatalog is one Query Group's catalog reader: every read a Slot
// makes passes through the gate, and a read the gate lets through carries
// the timeline revision hint. The gate decides per read rather than per
// Slot because the reads of one Slot are few and a renewal can land
// between them; the hint each read carries is the one true at that read.
type viewGatedCatalog struct {
	next       productionPhaseTwoSlotCatalog
	gate       *viewExecutionGate
	queryGroup execution.QueryGroupIdentity
	session    *ownership.Session
	renew      leaseRenewal
}

// gated is the three checks for one read: the hinted context when they
// hold, the named refusal when they do not.
func (catalog *viewGatedCatalog) gated(ctx context.Context) (context.Context, error) {
	return gateContext(ctx, catalog.gate, catalog.queryGroup, catalog.session, catalog.renew)
}

// leaseRenewal renews the session's lease now, ahead of its interval.
type leaseRenewal func(context.Context) error

// gateContext judges one read. When the only thing wrong is that the view
// names a newer timeline revision than the lease last brought, the record
// has moved and the lease has not caught up yet - a cutover moves the
// records and the view within a second and the leases within a renewal
// interval - so the lease is renewed once, now, and the check is judged
// again on what the record says; the record is the authority and the
// renewal is only early. A lease that is ahead of the view is left to the
// delta, and every other mismatch is refused as it is. Without the early
// renewal, every Query Group rechecked in the interval after a cutover
// was refused once or more, a burst per Worker per cutover that the fleet
// read as blocked runs.
func gateContext(ctx context.Context, gate *viewExecutionGate, queryGroup execution.QueryGroupIdentity, session *ownership.Session, renew leaseRenewal) (context.Context, error) {
	lease, held := session.Current()
	revision, outcome, viewRevision := gate.judgeAgainstView(queryGroup, lease, held)
	if outcome == viewGateTimelineMismatch && lease.TimelineRecordRevision < viewRevision && renew != nil {
		err := renew(ctx)
		if err == nil {
			lease, held = session.Current()
			revision, outcome, _ = gate.judgeAgainstView(queryGroup, lease, held)
		}
		gate.noteRenewal(outcome == viewGateExecutable, err != nil)
	}
	gate.record(queryGroup, outcome)
	if revision == 0 {
		return ctx, &scheduler.ViewNotExecutableError{Reason: string(outcome)}
	}
	return controlplane.WithTimelineRevisionHint(ctx, revision), nil
}

func (catalog *viewGatedCatalog) ReadInitialFrozenSchedule(ctx context.Context, queryGroup execution.QueryGroupIdentity) (execution.FrozenQueryGroupSchedule, error) {
	ctx, err := catalog.gated(ctx)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return catalog.next.ReadInitialFrozenSchedule(ctx, queryGroup)
}

func (catalog *viewGatedCatalog) ReadFrozenSchedule(ctx context.Context, queryGroup execution.QueryGroupIdentity, at execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error) {
	ctx, err := catalog.gated(ctx)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return catalog.next.ReadFrozenSchedule(ctx, queryGroup, at)
}

func (catalog *viewGatedCatalog) ReadSuccessorFrozenSchedule(ctx context.Context, queryGroup execution.QueryGroupIdentity, at execution.EvaluationTime) (execution.FrozenQueryGroupSchedule, error) {
	ctx, err := catalog.gated(ctx)
	if err != nil {
		return execution.FrozenQueryGroupSchedule{}, err
	}
	return catalog.next.ReadSuccessorFrozenSchedule(ctx, queryGroup, at)
}

func (catalog *viewGatedCatalog) ReadScheduleRetirement(ctx context.Context, queryGroup execution.QueryGroupIdentity) (execution.EvaluationTime, bool, error) {
	ctx, err := catalog.gated(ctx)
	if err != nil {
		return 0, false, err
	}
	return catalog.next.ReadScheduleRetirement(ctx, queryGroup)
}

func (catalog *viewGatedCatalog) NextSlotAfter(ctx context.Context, queryGroup execution.QueryGroupIdentity, at execution.EvaluationTime) (execution.EvaluationTime, error) {
	ctx, err := catalog.gated(ctx)
	if err != nil {
		return 0, err
	}
	return catalog.next.NextSlotAfter(ctx, queryGroup, at)
}

func (catalog *viewGatedCatalog) FreezeSlotContract(ctx context.Context, request execution.FreezeSlotContractRequest) (execution.FrozenSlotContractFact, error) {
	ctx, err := catalog.gated(ctx)
	if err != nil {
		return execution.FrozenSlotContractFact{}, err
	}
	return catalog.next.FreezeSlotContract(ctx, request)
}

// viewGatedExecutor carries the hint into the execution of a Slot the source
// froze under it: the activation read and a re-freeze inside execution read
// the same timeline the source did, without the header. A Slot that reaches
// execution passed the gate at the source moments ago; a lease that moved
// since is refused here by name rather than executed the old way.
type viewGatedExecutor struct {
	next       scheduler.Executor
	gate       *viewExecutionGate
	queryGroup execution.QueryGroupIdentity
	session    *ownership.Session
	renew      leaseRenewal
}

func (executor *viewGatedExecutor) Execute(ctx context.Context, request execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	ctx, err := gateContext(ctx, executor.gate, executor.queryGroup, executor.session, executor.renew)
	if err != nil {
		return execution.SlotExecutionResult{}, err
	}
	return executor.next.Execute(ctx, request)
}
