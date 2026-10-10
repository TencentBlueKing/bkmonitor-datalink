// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

func roundStore(t *testing.T, now time.Time, workerIDs ...string) *countingAssignmentStore {
	t.Helper()
	workers := make([]ownership.WorkerRegistration, 0, len(workerIDs))
	for _, workerID := range workerIDs {
		workers = append(workers, rebalanceWorker(workerID, ownership.WorkerReady, now.Add(time.Minute), "shadow"))
	}
	return &countingAssignmentStore{
		workers: workers,
		records: map[execution.QueryGroupIdentity]ownership.AssignmentRecord{},
	}
}

func roundReconciler(t *testing.T, store *countingAssignmentStore) *Reconciler {
	t.Helper()
	reconciler, err := NewReconciler(NewRouter(nil), store)
	if err != nil {
		t.Fatal(err)
	}
	return reconciler
}

// The round reads every Assignment record once, not once per Query Group.
//
// This is the whole change, and it is asserted on the store's own call count
// rather than on a duration: a timing assertion passes on a fast machine
// whatever the code does, and the cost being removed here is a count of waits.
func TestAReconcileRoundReadsEveryAssignmentInOneCall(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := roundStore(t, now, "a", "b")
	reconciler := roundReconciler(t, store)
	ctx, authority := context.Background(), ownership.PublicationAuthority{}
	queryGroups := make([]execution.QueryGroupIdentity, 0, 64)
	for index := 0; index < 64; index++ {
		queryGroups = append(queryGroups, execution.QueryGroupIdentity(fmt.Sprintf("query-group-%02d", index)))
	}
	settled, stats, err := reconciler.ReconcileRound(ctx, authority, queryGroups, store.workers, now)
	if err != nil {
		t.Fatalf("ReconcileRound() error = %v", err)
	}
	if len(settled) != len(queryGroups) {
		t.Fatalf("settled %d Query Groups, want %d", len(settled), len(queryGroups))
	}
	if store.batchCalls != 1 {
		t.Fatalf("the round spent %d batched reads for %d Query Groups, want one", store.batchCalls, len(queryGroups))
	}
	if store.readCalls != 0 {
		t.Fatalf("the round fell back to %d one-at-a-time reads", store.readCalls)
	}
	// The stats are what the acceptance is read off, so they have to carry
	// both numbers: round trips alone cannot be told apart from a round with
	// nothing to read.
	if stats.RoundTrips != 1 || stats.Keys != len(queryGroups) {
		t.Fatalf("stats = %d round trips over %d keys, want 1 over %d", stats.RoundTrips, stats.Keys, len(queryGroups))
	}
}

// A batch read that fails fails the round, and publishes nothing.
//
// The failure mode this is here for is not a lost round. It is the round
// deciding that nobody currently holds any Query Group -- which is what an
// empty record set means -- and rendezvousing the entire population onto new
// owners while the records that said otherwise were simply unreadable.
func TestAFailedAssignmentBatchPublishesNothing(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := roundStore(t, now, "a", "b")
	// Every Query Group is currently held by a worker that is still ready,
	// so a round that could read the records would publish nothing at all.
	queryGroups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"}
	for _, queryGroup := range queryGroups {
		store.records[queryGroup] = ownership.AssignmentRecord{
			QueryGroup: queryGroup, DesiredWorkerID: "a", AssignmentGeneration: 1,
			RecordRevision: 7, ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now,
		}
	}
	reconciler := roundReconciler(t, store)
	ctx, authority := context.Background(), ownership.PublicationAuthority{}
	store.batchErr = errors.New("assignment read unavailable")
	_, _, err := reconciler.ReconcileRound(ctx, authority, queryGroups, store.workers, now)
	if !errors.Is(err, store.batchErr) {
		t.Fatalf("ReconcileRound() error = %v, want the read failure", err)
	}
	if store.published != 0 {
		t.Fatalf("a round whose Assignment read failed published %d Assignments; an unreadable record is not a "+
			"free Query Group, and republishing them moves the whole population", store.published)
	}
	// And with the read working, the same round publishes nothing either --
	// which is what makes the assertion above about the failure rather than
	// about there being nothing to do.
	store.batchErr = nil
	if _, _, err := reconciler.ReconcileRound(ctx, authority, queryGroups, store.workers, now); err != nil {
		t.Fatalf("ReconcileRound() error = %v", err)
	}
	if store.published != 0 {
		t.Fatalf("the readable round published %d Assignments over ready incumbents", store.published)
	}
}

// What the batch found decides the same way reading one record at a time did:
// a ready incumbent is kept, a record that is not there is placed, and the
// revision the batch read is the one the publication is conditioned on.
func TestTheBatchedRoundKeepsIncumbentsAndCarriesTheirRevision(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := roundStore(t, now, "a", "b")
	store.records["held"] = ownership.AssignmentRecord{
		QueryGroup: "held", DesiredWorkerID: "a", AssignmentGeneration: 1,
		RecordRevision: 4, ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now,
	}
	// An incumbent that is no longer in the ready set: the record is there
	// and has a revision, and the round has to place it again against that
	// revision rather than against zero.
	store.records["stranded"] = ownership.AssignmentRecord{
		QueryGroup: "stranded", DesiredWorkerID: "gone", AssignmentGeneration: 1,
		RecordRevision: 9, ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now,
	}
	decisions := map[execution.QueryGroupIdentity]uint64{}
	store.observeDecision = func(decision ownership.AssignmentDecision) {
		decisions[decision.QueryGroup] = decision.ExpectedRecordRevision
	}
	reconciler := roundReconciler(t, store)
	settled, _, err := reconciler.ReconcileRound(
		context.Background(), ownership.PublicationAuthority{},
		[]execution.QueryGroupIdentity{"held", "stranded", "fresh"}, store.workers, now,
	)
	if err != nil {
		t.Fatalf("ReconcileRound() error = %v", err)
	}
	if settled["held"].DesiredWorkerID != "a" || settled["held"].RecordRevision != 4 {
		t.Fatalf("held = %+v, want the incumbent record untouched", settled["held"])
	}
	if _, republished := decisions["held"]; republished {
		t.Fatal("a ready incumbent was republished")
	}
	if got, ok := decisions["stranded"]; !ok || got != 9 {
		t.Fatalf("stranded published against expected revision %d (present %v), want the record's own 9; "+
			"conditioning on zero would overwrite a record that moved under the round", got, ok)
	}
	if got, ok := decisions["fresh"]; !ok || got != 0 {
		t.Fatalf("fresh published against expected revision %d (present %v), want zero for a record that is "+
			"absent rather than unreadable", got, ok)
	}
}

// The ready set is consulted by identity, and the lookup answers exactly what
// walking the slice answered -- including for a worker that is in the slice
// twice, where the first one wins.
func TestTheReadyWorkerIndexAnswersAsTheScanDid(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	ready := rebalanceWorker("a", ownership.WorkerReady, now.Add(time.Minute), "shadow")
	draining := rebalanceWorker("a", ownership.WorkerDraining, now.Add(time.Minute), "shadow")
	expired := rebalanceWorker("b", ownership.WorkerReady, now.Add(-time.Minute), "shadow")
	router := NewRouter(nil)
	for _, testCase := range []struct {
		name     string
		workers  []ownership.WorkerRegistration
		workerID string
		want     bool
	}{
		{"a ready worker is eligible", []ownership.WorkerRegistration{ready}, "a", true},
		{"a worker that is not in the set is not", []ownership.WorkerRegistration{ready}, "c", false},
		{"an expired registration is not", []ownership.WorkerRegistration{expired}, "b", false},
		{"a draining worker is not", []ownership.WorkerRegistration{draining}, "a", false},
		{"the first of a duplicated identity decides", []ownership.WorkerRegistration{ready, draining}, "a", true},
		{"and so does the first when it is the draining one", []ownership.WorkerRegistration{draining, ready}, "a", false},
		{"an empty worker id is never eligible", []ownership.WorkerRegistration{ready}, "", false},
	} {
		scanned := router.incumbentEligible("query-group-1", testCase.workerID, testCase.workers, now)
		indexed := router.incumbentEligibleIn("query-group-1", testCase.workerID, indexReadyWorkers(testCase.workers), now)
		if scanned != testCase.want || indexed != testCase.want {
			t.Fatalf("%s: scan = %v, index = %v, want %v", testCase.name, scanned, indexed, testCase.want)
		}
	}
}

// A ready set carrying the same worker twice is refused, and nothing is
// published.
//
// Select refuses a duplicate worker identity, and that refusal is a statement
// about the list it was handed. The round now also builds a map of that list
// to answer incumbent lookups, and a map cannot carry a duplicate -- so
// rebuilding Select's input from the map would drop the refusal silently,
// leaving the round to place Query Groups over a ready set it had quietly
// deduplicated. Nothing else in the suite notices: every other case supplies
// a well-formed set, where the slice and the map hold the same workers.
func TestARoundRefusesAReadySetThatNamesAWorkerTwice(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := roundStore(t, now, "a")
	duplicated := append(store.workers, store.workers[0])
	reconciler := roundReconciler(t, store)
	_, _, err := reconciler.ReconcileRound(
		context.Background(), ownership.PublicationAuthority{},
		[]execution.QueryGroupIdentity{"query-group-1", "query-group-2"}, duplicated, now,
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate worker identity") {
		t.Fatalf("ReconcileRound() over a ready set naming a worker twice error = %v, want the duplicate "+
			"identity refusal; Select is reading a list the round deduplicated for it", err)
	}
	if store.published != 0 {
		t.Fatalf("the refused round published %d Assignments", store.published)
	}
}
