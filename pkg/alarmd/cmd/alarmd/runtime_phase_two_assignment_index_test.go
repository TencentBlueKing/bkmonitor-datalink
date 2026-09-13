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
		QueryDeadlineReserve: 5 * time.Second, SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute,
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

// A worker shadows the index beside the per-record read: it reads the index
// every round, its own set only when the index says the set changed, and
// classifies the comparison; the returned set still comes from the records.
func TestProductionPhaseTwoOwnershipShadowsTheAssignmentIndexAgainstTheRecords(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	groups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"}
	store := newIndexStore(now, groups)
	production, observations := newIndexOwnershipHarness(t, now, store)
	read := func() *observability.AssignmentIndexFacts {
		t.Helper()
		assigned, err := production.AssignedQueryGroups(context.Background(), groups)
		if err != nil || !reflect.DeepEqual(assigned, groups) {
			t.Fatalf("AssignedQueryGroups() = %v, %v; want the records' set", assigned, err)
		}
		reads := indexObservations(*observations, observability.StageAssignmentIndexRead)
		return reads[len(reads)-1].AssignmentIndex
	}
	want := func(t *testing.T, got *observability.AssignmentIndexFacts, result string, stale int, setRead bool, candidates int, shadow string, difference int) {
		t.Helper()
		if got == nil || got.Result != result || got.StaleRounds != stale || got.SetRead != setRead || got.Candidates != candidates ||
			got.Shadow != shadow || got.Difference != difference || got.Assigned != 3 {
			t.Fatalf("index read facts = %+v, want result=%s stale=%d set_read=%v candidates=%d shadow=%s difference=%d",
				got, result, stale, setRead, candidates, shadow, difference)
		}
	}
	// No index yet: nothing to compare, nothing counted against it.
	want(t, read(), observability.AssignmentIndexMissing, 0, false, 0, observability.AssignmentIndexShadowSkipped, 0)

	store.index = &ownership.AssignmentIndex{Round: 7, ControlEpoch: 1, SetRounds: map[string]uint64{"worker-1": 7, "worker-2": 7}}
	store.sets["worker-1"] = ownership.AssignedSet{WorkerID: "worker-1", Round: 7, QueryGroups: append(groups[:3:3], "query-group-elsewhere")}
	want(t, read(), observability.AssignmentIndexFresh, 0, true, 3, observability.AssignmentIndexShadowAgreed, 0)
	// Same round again: stale, and the cached set is used without a read.
	want(t, read(), observability.AssignmentIndexStale, 1, false, 3, observability.AssignmentIndexShadowAgreed, 0)
	want(t, read(), observability.AssignmentIndexStale, 2, false, 3, observability.AssignmentIndexShadowAgreed, 0)
	// The round advances but this worker's set did not: fresh, no set read.
	store.index.Round = 8
	want(t, read(), observability.AssignmentIndexFresh, 0, false, 3, observability.AssignmentIndexShadowAgreed, 0)
	// The set changed while the records did not: read again, and the
	// difference counts against the index.
	store.index.Round, store.index.SetRounds["worker-1"] = 9, 9
	store.sets["worker-1"] = ownership.AssignedSet{WorkerID: "worker-1", Round: 9, QueryGroups: groups[:2]}
	want(t, read(), observability.AssignmentIndexFresh, 0, true, 2, observability.AssignmentIndexShadowDisagreed, 1)
	// A set older than the round the index names is not trusted.
	store.index.Round, store.index.SetRounds["worker-1"] = 10, 10
	want(t, read(), observability.AssignmentIndexInvalid, 0, true, 0, observability.AssignmentIndexShadowSkipped, 0)
	// The index no longer names this worker: an empty candidate set, and the
	// records changing this round make the difference transient.
	delete(store.index.SetRounds, "worker-1")
	store.index.Round = 11
	delete(store.assignments, "query-group-3")
	shortened := groups[:2]
	assigned, err := production.AssignedQueryGroups(context.Background(), groups)
	if err != nil || !reflect.DeepEqual(assigned, shortened) {
		t.Fatalf("AssignedQueryGroups() after a record left = %v, %v", assigned, err)
	}
	reads := indexObservations(*observations, observability.StageAssignmentIndexRead)
	last := reads[len(reads)-1].AssignmentIndex
	if last.Result != observability.AssignmentIndexFresh || last.Candidates != 0 || last.Assigned != 2 ||
		last.Shadow != observability.AssignmentIndexShadowTransient || last.Difference != 2 {
		t.Fatalf("index read facts after the records changed = %+v, want an unnamed worker with a transient difference of 2", last)
	}
	// A read that fails is reported as such.
	store.indexErr = errors.New("registry unavailable")
	if _, err := production.AssignedQueryGroups(context.Background(), groups); err != nil {
		t.Fatalf("AssignedQueryGroups() with a failing index read error = %v, want the records to still answer", err)
	}
	reads = indexObservations(*observations, observability.StageAssignmentIndexRead)
	if reads[len(reads)-1].Result != observability.ResultFailed || reads[len(reads)-1].AssignmentIndex.Result != observability.AssignmentIndexInvalid {
		t.Fatalf("failed index read observed as %+v", reads[len(reads)-1])
	}
}
