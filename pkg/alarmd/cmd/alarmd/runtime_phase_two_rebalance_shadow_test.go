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

// rebalanceOwnershipStore holds several ready workers and several
// Assignments, which the single-worker fake cannot express.
type rebalanceOwnershipStore struct {
	*fakePhaseTwoOwnershipStore
	workers     []ownership.WorkerRegistration
	assignments map[execution.QueryGroupIdentity]ownership.AssignmentRecord
	listErr     error
	listCalls   int
	published   int

	reads            int
	indexRounds      [][]ownership.AssignedSetWrite
	indexMissingOnce []string
	indexErr         error
	index            *ownership.AssignmentIndex
	sets             map[string]ownership.AssignedSet
}

func (store *rebalanceOwnershipStore) ListReadyWorkers(context.Context, time.Time) ([]ownership.WorkerRegistration, error) {
	store.listCalls++
	if store.listErr != nil {
		return nil, store.listErr
	}
	return append([]ownership.WorkerRegistration(nil), store.workers...), nil
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
	record := ownership.AssignmentRecord{
		QueryGroup: decision.QueryGroup, DesiredWorkerID: decision.DesiredWorkerID,
		AssignmentGeneration: 1, RecordRevision: 1, ControlEpoch: authority.Fence.OwnerEpoch,
		PlacementReason: decision.PlacementReason, AssignedAt: decision.DecidedAt,
	}
	store.assignments[decision.QueryGroup] = record
	return record, nil
}

// After reconciling every Query Group against one ready set the Control
// Leader plans one rebalance round over the owners it just confirmed and
// reports the plan; it publishes nothing for it. The ready set is listed
// once per round, and a listing that fails fails the round before any
// Query Group is touched, with nothing planned or published.
func TestProductionPhaseTwoOwnershipReportsARebalancePlanWithoutPublishingIt(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := func(id string) ownership.WorkerRegistration {
		return ownership.WorkerRegistration{
			WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Minute),
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
			ControlLeaderTTL: time.Minute, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				*observations = append(*observations, observation)
			}), Reconcile: reconciler, Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute,
			QueryDeadlineReserve: 5 * time.Second, SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute,
		})
		if err != nil {
			t.Fatal(err)
		}
		if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Minute); err != nil || !leader {
			t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
		}
		return production, observations
	}
	planned := func(observations []observability.Observation) []observability.Observation {
		var found []observability.Observation
		for _, observation := range observations {
			if observation.Stage == observability.StageRebalancePlanned {
				found = append(found, observation)
			}
		}
		return found
	}

	t.Run("a lopsided fleet yields a plan and no publication", func(t *testing.T) {
		store := newStore()
		production, observations := newOwnership(t, store)
		if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
			t.Fatalf("PublishAssignments() error = %v", err)
		}
		reports := planned(*observations)
		if len(reports) != 1 || reports[0].Component != observability.ComponentOwnership || reports[0].Result != observability.ResultSuccess {
			t.Fatalf("rebalance observations = %+v, want one successful report", reports)
		}
		want := &observability.RebalanceFacts{
			ReadyWorkers: 2, Assigned: 3, Target: 1, MostOwned: 3, LeastOwned: 0, Batch: 1, PlannedMoves: 1,
			Owned: []observability.RebalanceOwnedSample{{WorkerID: "worker-1", Owned: 3}, {WorkerID: "worker-2", Owned: 0}},
		}
		if !reflect.DeepEqual(reports[0].Rebalance, want) {
			t.Fatalf("rebalance facts = %+v, want %+v", reports[0].Rebalance, want)
		}
		if store.published != 0 {
			t.Fatalf("PublishAssignment called %d times; the plan must not publish", store.published)
		}
		if store.listCalls != 1 {
			t.Fatalf("ready set listed %d times for one round of %d Query Groups, want once", store.listCalls, len(groups))
		}
		for _, queryGroup := range groups {
			if store.assignments[queryGroup].DesiredWorkerID != "worker-1" {
				t.Fatalf("%s moved to %q during a shadow round", queryGroup, store.assignments[queryGroup].DesiredWorkerID)
			}
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
		if reports := planned(*observations); len(reports) != 0 {
			t.Fatalf("rebalance observations = %+v, want none when the round failed", reports)
		}
		if store.listCalls != 1 || store.published != 0 {
			t.Fatalf("listed %d times and published %d after a failed listing, want one failed listing and no publication", store.listCalls, store.published)
		}
	})
}
