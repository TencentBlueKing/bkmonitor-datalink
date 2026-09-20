// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The single-worker fake knows no index: rounds are accepted and nothing is
// ever readable, which is the shape of a store an older Leader wrote.
func (store *fakePhaseTwoOwnershipStore) PublishAssignmentIndex(context.Context, ownership.PublicationAuthority, time.Time, []ownership.AssignedSetWrite) (ownership.AssignmentIndexPublication, error) {
	return ownership.AssignmentIndexPublication{Round: 1}, nil
}

func (store *fakePhaseTwoOwnershipStore) ReadAssignmentIndex(context.Context) (ownership.AssignmentIndex, error) {
	return ownership.AssignmentIndex{}, ownership.ErrAssignmentIndexAbsent
}

func (store *fakePhaseTwoOwnershipStore) ReadAssignedSet(context.Context, string) (ownership.AssignedSet, error) {
	return ownership.AssignedSet{}, ownership.ErrAssignedSetAbsent
}

func (store *rebalanceOwnershipStore) PublishAssignmentIndex(_ context.Context, _ ownership.PublicationAuthority, _ time.Time, sets []ownership.AssignedSetWrite) (ownership.AssignmentIndexPublication, error) {
	if store.indexErr != nil {
		return ownership.AssignmentIndexPublication{}, store.indexErr
	}
	recorded := make([]ownership.AssignedSetWrite, len(sets))
	copy(recorded, sets)
	store.indexRounds = append(store.indexRounds, recorded)
	publication := ownership.AssignmentIndexPublication{Round: uint64(len(store.indexRounds)), Missing: store.indexMissingOnce}
	store.indexMissingOnce = nil
	return publication, nil
}

func (store *rebalanceOwnershipStore) ReadAssignmentIndex(context.Context) (ownership.AssignmentIndex, error) {
	if store.indexErr != nil {
		return ownership.AssignmentIndex{}, store.indexErr
	}
	if store.index == nil {
		return ownership.AssignmentIndex{}, ownership.ErrAssignmentIndexAbsent
	}
	return *store.index, nil
}

func (store *rebalanceOwnershipStore) ReadAssignedSet(_ context.Context, workerID string) (ownership.AssignedSet, error) {
	set, ok := store.sets[workerID]
	if !ok {
		return ownership.AssignedSet{}, ownership.ErrAssignedSetAbsent
	}
	return set, nil
}

func newIndexOwnershipHarness(t *testing.T, now time.Time, store *rebalanceOwnershipStore) (*productionPhaseTwoOwnership, *[]observability.Observation) {
	t.Helper()
	limits := validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	eligibility, err := scheduler.NewStaticWorkerEligibility(ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	observations := &[]observability.Observation{}
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
		ControlLeaderTTL: time.Minute, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			*observations = append(*observations, observation)
		}), Reconcile: reconciler, Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute,
		QueryDeadlineReserve: 5 * time.Second, SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute, SettlingWait: 30 * time.Second, LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return production, observations
}

func indexObservations(observations []observability.Observation, stage observability.Stage) []observability.Observation {
	var found []observability.Observation
	for _, observation := range observations {
		if observation.Stage == stage {
			found = append(found, observation)
		}
	}
	return found
}

func newIndexStore(now time.Time, groups []execution.QueryGroupIdentity) *rebalanceOwnershipStore {
	worker := func(id string) ownership.WorkerRegistration {
		return ownership.WorkerRegistration{
			WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Minute),
		}
	}
	store := &rebalanceOwnershipStore{
		fakePhaseTwoOwnershipStore: &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})},
		workers:                    []ownership.WorkerRegistration{worker("worker-1"), worker("worker-2")},
		assignments:                map[execution.QueryGroupIdentity]ownership.AssignmentRecord{},
		sets:                       map[string]ownership.AssignedSet{},
	}
	for _, queryGroup := range groups {
		store.assignments[queryGroup] = ownership.AssignmentRecord{
			QueryGroup: queryGroup, DesiredWorkerID: "worker-1", AssignmentGeneration: 1, RecordRevision: 1,
			ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now,
		}
	}
	return store
}

// After each reconcile round the Control Leader writes the index for every
// ready worker, rewriting a set only when its content changed since this
// Leader last wrote it; a set the store found missing is rewritten in one
// follow-up round.
func TestProductionPhaseTwoOwnershipWritesTheAssignmentIndexOnlyWhenSetsChange(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	groups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"}
	store := newIndexStore(now, groups)
	production, observations := newIndexOwnershipHarness(t, now, store)
	if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Minute); err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
	}
	round := func() {
		t.Helper()
		if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
			t.Fatalf("PublishAssignments() error = %v", err)
		}
	}
	round()
	round()
	if len(store.indexRounds) != 2 {
		t.Fatalf("index rounds = %d, want one per reconcile round", len(store.indexRounds))
	}
	wantFirst := []ownership.AssignedSetWrite{
		{WorkerID: "worker-1", QueryGroups: groups, Rewrite: true},
		{WorkerID: "worker-2", QueryGroups: nil, Rewrite: true},
	}
	sortWrites(store.indexRounds[0])
	if !reflect.DeepEqual(store.indexRounds[0], wantFirst) {
		t.Fatalf("first round = %+v, want %+v", store.indexRounds[0], wantFirst)
	}
	for _, write := range store.indexRounds[1] {
		if write.Rewrite {
			t.Fatalf("second round rewrote %q although nothing changed: %+v", write.WorkerID, store.indexRounds[1])
		}
	}
	// One Query Group moves: both affected sets are rewritten, nothing else.
	record := store.assignments["query-group-3"]
	record.DesiredWorkerID = "worker-2"
	store.assignments["query-group-3"] = record
	round()
	third := store.indexRounds[2]
	sortWrites(third)
	wantThird := []ownership.AssignedSetWrite{
		{WorkerID: "worker-1", QueryGroups: groups[:2], Rewrite: true},
		{WorkerID: "worker-2", QueryGroups: groups[2:], Rewrite: true},
	}
	if !reflect.DeepEqual(third, wantThird) {
		t.Fatalf("round after a move = %+v, want %+v", third, wantThird)
	}
	// A set the store reports missing is rewritten by a follow-up round
	// carrying every worker, so nobody loses their index entry.
	store.indexMissingOnce = []string{"worker-2"}
	round()
	if len(store.indexRounds) != 5 {
		t.Fatalf("index rounds after a missing set = %d, want a follow-up round", len(store.indexRounds))
	}
	followUp := store.indexRounds[4]
	sortWrites(followUp)
	if len(followUp) != 2 || followUp[0].Rewrite || !followUp[1].Rewrite || followUp[1].WorkerID != "worker-2" {
		t.Fatalf("follow-up round = %+v, want worker-2 rewritten and worker-1 kept", followUp)
	}
	written := indexObservations(*observations, observability.StageAssignmentIndexWritten)
	if len(written) != 4 {
		t.Fatalf("written observations = %d, want one per reconcile round", len(written))
	}
	last := written[3].AssignmentIndex
	if written[3].Result != observability.ResultSuccess || last == nil || last.Round != 5 || last.Workers != 2 || last.Rewritten != 1 || last.Missing != 1 {
		t.Fatalf("last written facts = %+v (%s), want round 5, 2 workers, 1 rewritten, 1 missing", last, written[3].Result)
	}
	// A failed write is reported and does not fail the reconcile round.
	store.indexErr = errors.New("registry unavailable")
	if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
		t.Fatalf("PublishAssignments() with a failing index write error = %v, want nil", err)
	}
	written = indexObservations(*observations, observability.StageAssignmentIndexWritten)
	if written[len(written)-1].Result != observability.ResultFailed {
		t.Fatalf("failed index write observed as %s", written[len(written)-1].Result)
	}
}

func sortWrites(writes []ownership.AssignedSetWrite) {
	for i := range writes {
		for j := i + 1; j < len(writes); j++ {
			if writes[j].WorkerID < writes[i].WorkerID {
				writes[i], writes[j] = writes[j], writes[i]
			}
		}
	}
	for i := range writes {
		groups := writes[i].QueryGroups
		for a := range groups {
			for b := a + 1; b < len(groups); b++ {
				if groups[b] < groups[a] {
					groups[a], groups[b] = groups[b], groups[a]
				}
			}
		}
	}
}

// A worker answers from the index: it reads the index every round, its own
// set only when the index says the set changed, confirms every candidate it
// does not yet hold against its record before returning it, and confirms
// every held Query Group the index dropped against its record before
// releasing it. The record wins whenever the two disagree. Without a usable
// index every record is read, as before the index existed.
func TestProductionPhaseTwoOwnershipAnswersFromTheAssignmentIndexAndConfirmsChangesAgainstRecords(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	groups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"}
	store := newIndexStore(now, groups)
	production, observations := newIndexOwnershipHarness(t, now, store)
	read := func(population []execution.QueryGroupIdentity) ([]execution.QueryGroupIdentity, *observability.AssignmentIndexFacts, observability.Result) {
		t.Helper()
		store.reads = 0
		assigned, err := production.AssignedQueryGroups(context.Background(), population)
		if err != nil {
			t.Fatalf("AssignedQueryGroups() error = %v", err)
		}
		reads := indexObservations(*observations, observability.StageAssignmentIndexRead)
		last := reads[len(reads)-1]
		return assigned, last.AssignmentIndex, last.Result
	}
	type want struct {
		assigned                                       []execution.QueryGroupIdentity
		result                                         string
		setRead, fullRead                              bool
		candidates, opened, rejected, released, retain int
		reads                                          int
	}
	check := func(t *testing.T, step string, assigned []execution.QueryGroupIdentity, facts *observability.AssignmentIndexFacts, w want) {
		t.Helper()
		if !reflect.DeepEqual(assigned, w.assigned) || facts == nil || facts.Result != w.result || facts.SetRead != w.setRead ||
			facts.FullRead != w.fullRead || facts.Candidates != w.candidates || facts.Opened != w.opened || facts.Rejected != w.rejected ||
			facts.Released != w.released || facts.Retained != w.retain || facts.Reads != w.reads || store.reads != w.reads {
			t.Fatalf("%s: assigned=%v facts=%+v store reads=%d, want %+v", step, assigned, facts, store.reads, w)
		}
	}
	setIndex := func(round uint64, setRound *uint64, set []execution.QueryGroupIdentity) {
		if store.index == nil {
			store.index = &ownership.AssignmentIndex{ControlEpoch: 1, SetRounds: map[string]uint64{"worker-2": 1}}
		}
		store.index.Round = round
		if setRound == nil {
			delete(store.index.SetRounds, "worker-1")
		} else {
			store.index.SetRounds["worker-1"] = *setRound
			store.sets["worker-1"] = ownership.AssignedSet{WorkerID: "worker-1", Round: *setRound, QueryGroups: set}
		}
	}
	round := func(value uint64) *uint64 { return &value }

	// No index yet: every record is read, and the held set is built from it.
	assigned, facts, _ := read(groups)
	check(t, "no index", assigned, facts, want{assigned: groups, result: observability.AssignmentIndexMissing, fullRead: true, reads: 3})
	// The index names what is already held: nothing is read.
	setIndex(7, round(7), groups)
	assigned, facts, _ = read(groups)
	check(t, "index matches held", assigned, facts, want{assigned: groups, result: observability.AssignmentIndexFresh, setRead: true, candidates: 3})
	assigned, facts, _ = read(groups)
	check(t, "same round", assigned, facts, want{assigned: groups, result: observability.AssignmentIndexStale, candidates: 3})
	// A Query Group moves away and the index follows: released after one read.
	moved := store.assignments["query-group-3"]
	moved.DesiredWorkerID = "worker-2"
	store.assignments["query-group-3"] = moved
	setIndex(8, round(8), groups[:2])
	assigned, facts, _ = read(groups)
	check(t, "released", assigned, facts, want{assigned: groups[:2], result: observability.AssignmentIndexFresh, setRead: true, candidates: 2, released: 1, reads: 1})
	// The index names a Query Group whose record says otherwise: rejected,
	// not returned, and read again next round because it is still named.
	setIndex(9, round(9), groups)
	assigned, facts, _ = read(groups)
	check(t, "rejected", assigned, facts, want{assigned: groups[:2], result: observability.AssignmentIndexFresh, setRead: true, candidates: 3, rejected: 1, reads: 1})
	assigned, facts, _ = read(groups)
	check(t, "rejected again", assigned, facts, want{assigned: groups[:2], result: observability.AssignmentIndexStale, candidates: 3, rejected: 1, reads: 1})
	// The record comes back: opened after one read, with no set read since
	// the index did not change the set.
	moved.DesiredWorkerID = "worker-1"
	store.assignments["query-group-3"] = moved
	setIndex(10, round(9), groups)
	assigned, facts, _ = read(groups)
	check(t, "opened", assigned, facts, want{assigned: groups, result: observability.AssignmentIndexFresh, candidates: 3, opened: 1, reads: 1})
	// The index drops a Query Group the record still assigns here: retained.
	setIndex(11, round(11), groups[:2])
	assigned, facts, _ = read(groups)
	check(t, "retained", assigned, facts, want{assigned: groups, result: observability.AssignmentIndexFresh, setRead: true, candidates: 2, retain: 1, reads: 1})
	// The index no longer names this worker at all: every held Query Group
	// is confirmed against its record, and a Query Group that left the
	// population is released without a read.
	setIndex(12, nil, nil)
	assigned, facts, _ = read(groups[:2])
	check(t, "unnamed and shrunk population", assigned, facts, want{assigned: groups[:2], result: observability.AssignmentIndexFresh, released: 1, retain: 2, reads: 2})
	// A failing index read falls back to reading every record and is reported.
	store.indexErr = errors.New("registry unavailable")
	assigned, facts, result := read(groups[:2])
	check(t, "failed index read", assigned, facts, want{assigned: groups[:2], result: observability.AssignmentIndexInvalid, fullRead: true, reads: 2})
	if result != observability.ResultFailed {
		t.Fatalf("failed index read observed as %s", result)
	}
}
