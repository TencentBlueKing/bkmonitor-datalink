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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// rebalanceOwnershipStore holds several ready workers and several
// Assignments, which the single-worker fake cannot express.
type rebalanceOwnershipStore struct {
	*fakePhaseTwoOwnershipStore
	workers     []ownership.WorkerRegistration
	assignments map[execution.QueryGroupIdentity]ownership.AssignmentRecord
	listErr     error
	listCalls   int
	published   int
	// decisions records every publication in order; beforePublish runs
	// before each one and may move the record under the round; publishErr
	// is returned by every publication when set.
	decisions     []ownership.AssignmentDecision
	beforePublish func(ownership.AssignmentDecision)
	publishErr    error

	reads            int
	readErr          error
	indexRounds      [][]ownership.AssignedSetWrite
	indexMissingOnce []string
	indexErr         error
	index            *ownership.AssignmentIndex
	sets             map[string]ownership.AssignedSet
}

func (store *rebalanceOwnershipStore) ListReadyWorkers(
	context.Context, time.Time,
) ([]ownership.WorkerRegistration, ownership.ControlReadStats, error) {
	store.listCalls++
	if store.listErr != nil {
		return nil, ownership.ControlReadStats{}, store.listErr
	}
	return append([]ownership.WorkerRegistration(nil), store.workers...),
		ownership.ControlReadStats{Keys: len(store.workers), RoundTrips: 1}, nil
}

// reads counts what the round spent on Assignment records, so the batched
// read adds one rather than one per Query Group: a round that stopped
// batching reads as the old number, which is the thing worth noticing.
func (store *rebalanceOwnershipStore) ReadAssignments(
	_ context.Context,
	queryGroups []execution.QueryGroupIdentity,
) (map[execution.QueryGroupIdentity]ownership.AssignmentRecord, ownership.ControlReadStats, error) {
	store.reads++
	if store.readErr != nil {
		return nil, ownership.ControlReadStats{}, store.readErr
	}
	found := make(map[execution.QueryGroupIdentity]ownership.AssignmentRecord, len(queryGroups))
	for _, queryGroup := range queryGroups {
		if record, ok := store.assignments[queryGroup]; ok {
			found[queryGroup] = record
		}
	}
	return found, ownership.ControlReadStats{Keys: len(queryGroups), RoundTrips: 1}, nil
}

func (store *rebalanceOwnershipStore) ReadAssignment(_ context.Context, queryGroup execution.QueryGroupIdentity) (ownership.AssignmentRecord, error) {
	store.reads++
	record, ok := store.assignments[queryGroup]
	if !ok {
		return ownership.AssignmentRecord{}, ownership.ErrAssignmentAbsent
	}
	return record, nil
}

func (store *rebalanceOwnershipStore) PublishAssignment(
	_ context.Context,
	authority ownership.PublicationAuthority,
	decision ownership.AssignmentDecision,
) (ownership.AssignmentRecord, error) {
	store.published++
	store.decisions = append(store.decisions, decision)
	if store.beforePublish != nil {
		store.beforePublish(decision)
	}
	if store.publishErr != nil {
		return ownership.AssignmentRecord{}, store.publishErr
	}
	current, exists := store.assignments[decision.QueryGroup]
	// The store's own rule: the decision names the revision it read, and a
	// record that moved since is refused rather than overwritten.
	if current.RecordRevision != decision.ExpectedRecordRevision {
		return ownership.AssignmentRecord{}, ownership.ErrAssignmentConflict
	}
	if exists && current.DesiredWorkerID == decision.DesiredWorkerID {
		return current, nil
	}
	record := ownership.AssignmentRecord{
		QueryGroup: decision.QueryGroup, DesiredWorkerID: decision.DesiredWorkerID,
		AssignmentGeneration: current.AssignmentGeneration + 1, RecordRevision: current.RecordRevision + 1,
		ControlEpoch: authority.Fence.OwnerEpoch, PlacementReason: decision.PlacementReason, AssignedAt: decision.DecidedAt,
	}
	store.assignments[decision.QueryGroup] = record
	return record, nil
}

// The Control Leader rebalances what rendezvous placement leaves uneven.
// Placement is sticky: a Query Group keeps its holder while that holder is
// ready, so a replica that comes back after a crash or a rolling update
// owns nothing until something moves its share to it. On 2026-09-16 one of
// two replicas held 2372 Query Groups and the other 3 for as long as it
// took the next rollout to reshuffle them, with the plan computed on every
// round and published on none.
//
// The round plans over the owners it just reconciled and publishes the
// moves as REBALANCE decisions naming the record revision it read; the
// index is written from the owners after the moves. It publishes nothing
// while the ready set changed within the stabilisation window -- a lease
// TTL plus two rounds -- so a rolling update settles first and converges
// once, and a new Leader waits out one window before it moves anything.
func TestProductionPhaseTwoOwnershipPublishesRebalanceMovesOnceTheReadySetIsStable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := func(id string) ownership.WorkerRegistration {
		return ownership.WorkerRegistration{
			WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Hour),
		}
	}
	groups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"}
	newStore := func() *rebalanceOwnershipStore {
		store := &rebalanceOwnershipStore{
			fakePhaseTwoOwnershipStore: &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})},
			workers:                    []ownership.WorkerRegistration{worker("worker-1"), worker("worker-2")},
			assignments:                map[execution.QueryGroupIdentity]ownership.AssignmentRecord{},
		}
		for _, queryGroup := range groups {
			store.assignments[queryGroup] = ownership.AssignmentRecord{
				QueryGroup: queryGroup, DesiredWorkerID: "worker-1", AssignmentGeneration: 1, RecordRevision: 1,
				ControlEpoch: 1, PlacementReason: ownership.PlacementRendezvous, AssignedAt: now,
			}
		}
		return store
	}
	// The stabilisation window the constructor below is given: lease TTL 30s
	// plus two reconcile intervals of 5s.
	const stabilisation = 40 * time.Second
	newOwnership := func(t *testing.T, store *rebalanceOwnershipStore) (*productionPhaseTwoOwnership, *[]observability.Observation) {
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
			ControlLeaderTTL: time.Hour, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				*observations = append(*observations, observation)
			}), Reconcile: reconciler, Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute,
			QueryDeadlineReserve: 5 * time.Second, SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute,
			SettlingWait: 30 * time.Second, LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
		})
		if err != nil {
			t.Fatal(err)
		}
		if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Hour); err != nil || !leader {
			t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
		}
		return production, observations
	}
	lastPlanned := func(t *testing.T, observations []observability.Observation) *observability.RebalanceFacts {
		t.Helper()
		var found *observability.RebalanceFacts
		for _, observation := range observations {
			if observation.Stage == observability.StageRebalancePlanned && observation.Result == observability.ResultSuccess {
				found = observation.Rebalance
			}
		}
		if found == nil {
			t.Fatal("no rebalance observation")
		}
		return found
	}
	owners := func(store *rebalanceOwnershipStore) map[string]int {
		counts := map[string]int{}
		for _, record := range store.assignments {
			counts[record.DesiredWorkerID]++
		}
		return counts
	}
	setOf := func(round []ownership.AssignedSetWrite, workerID string) []execution.QueryGroupIdentity {
		for _, write := range round {
			if write.WorkerID == workerID {
				return write.QueryGroups
			}
		}
		return nil
	}

	t.Run("a new Leader plans at once and publishes after one window", func(t *testing.T) {
		store := newStore()
		production, observations := newOwnership(t, store)
		if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
			t.Fatalf("PublishAssignments(round 1) error = %v", err)
		}
		facts := lastPlanned(t, *observations)
		if facts.PlannedMoves != 1 || facts.PublishedMoves != 0 || !facts.Paused || facts.PausedForSeconds != stabilisation.Seconds() {
			t.Fatalf("round 1 facts = %+v, want one planned move, none published, paused for the whole window", facts)
		}
		if store.published != 0 || !reflect.DeepEqual(owners(store), map[string]int{"worker-1": 3}) {
			t.Fatalf("round 1 published %d, owners %v: a Leader's first round must not move anything", store.published, owners(store))
		}
		// The same round's census of the content scope, from the records it
		// read: workers that declare no capability make it a withdrawing
		// round, and none of the three records names a content.
		if scope := production.LastAssignmentScope(); scope == nil || scope.Policy != fleet.AssignmentScopePolicyWithdrawn ||
			scope.Total != 3 || scope.Declared != 0 || scope.Undeclared != 3 || !scope.At.Equal(now) || !scope.Consistent() {
			t.Fatalf("round 1 assignment scope = %+v, want a withdrawing round over three undeclared records", scope)
		}

		at := now.Add(stabilisation)
		if err := production.PublishAssignments(context.Background(), groups, at); err != nil {
			t.Fatalf("PublishAssignments(round 2) error = %v", err)
		}
		facts = lastPlanned(t, *observations)
		if facts.PlannedMoves != 1 || facts.PublishedMoves != 1 || facts.Paused || facts.Conflicts != 0 {
			t.Fatalf("round 2 facts = %+v, want the one planned move published", facts)
		}
		moved := store.assignments["query-group-1"]
		if moved.DesiredWorkerID != "worker-2" || moved.PlacementReason != ownership.PlacementRebalance ||
			moved.RecordRevision != 2 || moved.AssignmentGeneration != 2 || moved.AssignedAt != at {
			t.Fatalf("moved record = %+v, want worker-2 under REBALANCE at revision 2", moved)
		}
		if len(store.decisions) != 1 || store.decisions[0].ExpectedRecordRevision != 1 {
			t.Fatalf("decisions = %+v, want one naming the revision the round read", store.decisions)
		}
		if !reflect.DeepEqual(owners(store), map[string]int{"worker-1": 2, "worker-2": 1}) {
			t.Fatalf("owners after round 2 = %v", owners(store))
		}
		// The index this round wrote is the owners after the move, so the
		// new holder reads its Query Group on its next tick and the old one
		// releases it.
		round := store.indexRounds[len(store.indexRounds)-1]
		if !reflect.DeepEqual(setOf(round, "worker-2"), []execution.QueryGroupIdentity{"query-group-1"}) ||
			!reflect.DeepEqual(setOf(round, "worker-1"), []execution.QueryGroupIdentity{"query-group-2", "query-group-3"}) {
			t.Fatalf("index after round 2 = %+v, want the moved Query Group under worker-2", round)
		}
		if fleetFacts := production.LastRebalance(); fleetFacts == nil || fleetFacts.Shadow || fleetFacts.PublishedMoves != 1 {
			t.Fatalf("fleet rebalance facts = %+v, want a published, non-shadow round", fleetFacts)
		}

		// 2/1 is parity for three Query Groups: nothing more to move, and
		// the moved record stays where it is on the rounds that follow.
		if err := production.PublishAssignments(context.Background(), groups, at.Add(5*time.Second)); err != nil {
			t.Fatalf("PublishAssignments(round 3) error = %v", err)
		}
		if facts = lastPlanned(t, *observations); facts.PlannedMoves != 0 || facts.PublishedMoves != 0 {
			t.Fatalf("round 3 facts = %+v, want nothing planned at parity", facts)
		}
		if store.assignments["query-group-1"] != moved {
			t.Fatalf("moved record changed on a round with nothing to do: %+v", store.assignments["query-group-1"])
		}
	})
	t.Run("a worker joining pauses the writer for one window and then receives its share", func(t *testing.T) {
		store := newStore()
		production, observations := newOwnership(t, store)
		at := now
		for _, step := range []time.Duration{0, stabilisation} {
			at = now.Add(step)
			if err := production.PublishAssignments(context.Background(), groups, at); err != nil {
				t.Fatalf("PublishAssignments(%s) error = %v", step, err)
			}
		}
		if !reflect.DeepEqual(owners(store), map[string]int{"worker-1": 2, "worker-2": 1}) {
			t.Fatalf("owners before the join = %v", owners(store))
		}
		store.workers = append(store.workers, worker("worker-3"))
		at = at.Add(5 * time.Second)
		if err := production.PublishAssignments(context.Background(), groups, at); err != nil {
			t.Fatalf("PublishAssignments(join) error = %v", err)
		}
		facts := lastPlanned(t, *observations)
		if facts.PlannedMoves != 1 || facts.PublishedMoves != 0 || !facts.Paused || facts.PausedForSeconds != stabilisation.Seconds() {
			t.Fatalf("join round facts = %+v, want the move planned and the round paused for a full window", facts)
		}
		half := at.Add(stabilisation / 2)
		if err := production.PublishAssignments(context.Background(), groups, half); err != nil {
			t.Fatalf("PublishAssignments(half window) error = %v", err)
		}
		if facts = lastPlanned(t, *observations); !facts.Paused || facts.PausedForSeconds != (stabilisation/2).Seconds() {
			t.Fatalf("half-window facts = %+v, want still paused with half the window left", facts)
		}
		settled := at.Add(stabilisation)
		if err := production.PublishAssignments(context.Background(), groups, settled); err != nil {
			t.Fatalf("PublishAssignments(settled) error = %v", err)
		}
		if facts = lastPlanned(t, *observations); facts.PublishedMoves != 1 || facts.Paused {
			t.Fatalf("settled round facts = %+v, want the move published", facts)
		}
		if !reflect.DeepEqual(owners(store), map[string]int{"worker-1": 1, "worker-2": 1, "worker-3": 1}) {
			t.Fatalf("owners after the joined worker received its share = %v", owners(store))
		}
	})
	t.Run("a record that moved under the round is skipped, not overwritten", func(t *testing.T) {
		store := newStore()
		production, observations := newOwnership(t, store)
		store.beforePublish = func(decision ownership.AssignmentDecision) {
			// Another writer got there first: the record's revision advanced
			// past the one the round read.
			record := store.assignments[decision.QueryGroup]
			record.RecordRevision++
			store.assignments[decision.QueryGroup] = record
		}
		if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
			t.Fatalf("PublishAssignments(round 1) error = %v", err)
		}
		if err := production.PublishAssignments(context.Background(), groups, now.Add(stabilisation)); err != nil {
			t.Fatalf("PublishAssignments(round 2) error = %v, want the conflict skipped", err)
		}
		facts := lastPlanned(t, *observations)
		if facts.PlannedMoves != 1 || facts.PublishedMoves != 0 || facts.Conflicts != 1 {
			t.Fatalf("facts = %+v, want one planned move refused as a conflict", facts)
		}
		if got := store.assignments["query-group-1"].DesiredWorkerID; got != "worker-1" {
			t.Fatalf("conflicting record moved to %q", got)
		}
		if len(store.indexRounds) != 2 {
			t.Fatalf("index rounds = %d, want the round to finish and write its index", len(store.indexRounds))
		}
	})
	t.Run("a stale fence ends the round before the index", func(t *testing.T) {
		store := newStore()
		production, observations := newOwnership(t, store)
		if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
			t.Fatalf("PublishAssignments(round 1) error = %v", err)
		}
		store.publishErr = ownership.ErrStaleFence
		err := production.PublishAssignments(context.Background(), groups, now.Add(stabilisation))
		if !errors.Is(err, ownership.ErrStaleFence) {
			t.Fatalf("PublishAssignments(stale) error = %v, want the stale fence", err)
		}
		if facts := lastPlanned(t, *observations); facts.PublishedMoves != 0 {
			t.Fatalf("facts = %+v, want nothing published under a stale fence", facts)
		}
		if len(store.indexRounds) != 1 {
			t.Fatalf("index rounds = %d, want the stale round to write no index", len(store.indexRounds))
		}
		production.mu.Lock()
		authority := production.authority
		production.mu.Unlock()
		if authority.Fence.QueryGroup != "" {
			t.Fatalf("authority kept after a stale fence: %+v", authority)
		}
	})
	t.Run("a failed ready-set read is reported as a failed round", func(t *testing.T) {
		store := newStore()
		production, observations := newOwnership(t, store)
		store.listErr = errors.New("registry unavailable")
		if err := production.PublishAssignments(context.Background(), groups, now); err == nil || !errors.Is(err, store.listErr) {
			// The round lists the ready set once, before any Query Group, and fails there.
			t.Fatalf("PublishAssignments() error = %v, want the registry failure", err)
		}
		for _, observation := range *observations {
			if observation.Stage == observability.StageRebalancePlanned {
				t.Fatalf("rebalance observation %+v, want none when the round failed", observation)
			}
		}
		if store.listCalls != 1 || store.published != 0 {
			t.Fatalf("listed %d times and published %d after a failed listing, want one failed listing and no publication", store.listCalls, store.published)
		}
	})
}
