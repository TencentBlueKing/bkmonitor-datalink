// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// A reconcile round says what it spent on the two control-plane reads.
//
// The acceptance for batching these reads is a number the round reports about
// itself. It cannot be a Redis client counter: that counts every command the
// process sends, from every caller, and cannot be divided back into rounds or
// into which read a command belonged to. Nor can it be a duration alone --
// a duration says a round was slow, not how many times it waited.
//
// So the round reports round trips beside keys, per read, and the reading is
// one sample wide: keys far above round trips is a batched read, keys equal to
// round trips is the shape this change removed.
func TestAReconcileRoundReportsWhatItsControlReadsSpent(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := func(id string) ownership.WorkerRegistration {
		return ownership.WorkerRegistration{
			WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Hour),
		}
	}
	groups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2", "query-group-3"}
	newRound := func(t *testing.T) (*productionPhaseTwoOwnership, *rebalanceOwnershipStore, *[]observability.Observation) {
		t.Helper()
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
		limits := validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
		flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		eligibility, err := scheduler.NewStaticWorkerEligibility(
			ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
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
		return production, store, observations
	}
	spent := func(t *testing.T, observations []observability.Observation) *observability.ControlReadFacts {
		t.Helper()
		var found *observability.ControlReadFacts
		for _, observation := range observations {
			if observation.Stage == observability.StageControlReadsSpent {
				found = observation.ControlReads
			}
		}
		if found == nil {
			t.Fatal("the round reported nothing about its control reads")
		}
		return found
	}

	t.Run("the round reports keys and round trips per read", func(t *testing.T) {
		production, store, observations := newRound(t)
		if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
			t.Fatalf("PublishAssignments() error = %v", err)
		}
		facts := spent(t, *observations)
		if facts.QueryGroups != len(groups) {
			t.Fatalf("reported %d Query Groups, want %d", facts.QueryGroups, len(groups))
		}
		if facts.AssignmentRoundTrips != 1 || facts.AssignmentKeys != len(groups) {
			t.Fatalf("assignment read = %d round trips over %d keys, want 1 over %d: this is the number the "+
				"batching is accepted on", facts.AssignmentRoundTrips, facts.AssignmentKeys, len(groups))
		}
		if facts.RegistryRoundTrips != 1 || facts.RegistryKeys != len(store.workers) {
			t.Fatalf("registry read = %d round trips over %d keys, want 1 over %d",
				facts.RegistryRoundTrips, facts.RegistryKeys, len(store.workers))
		}
		if facts.AssignmentMilliseconds < 0 || facts.RegistryMilliseconds < 0 {
			t.Fatalf("negative durations reported: %+v", facts)
		}
	})

	t.Run("a round whose assignment read failed still reports what it spent", func(t *testing.T) {
		production, store, observations := newRound(t)
		store.readErr = errors.New("assignment read unavailable")
		err := production.PublishAssignments(context.Background(), groups, now)
		if !errors.Is(err, store.readErr) {
			t.Fatalf("PublishAssignments() error = %v, want the read failure", err)
		}
		if store.published != 0 {
			t.Fatalf("a round whose Assignment read failed published %d Assignments", store.published)
		}
		// The round that failed is the one most worth seeing in the
		// distribution. Reporting only on success would leave exactly the
		// slow rounds out of it.
		facts := spent(t, *observations)
		if facts.QueryGroups != len(groups) {
			t.Fatalf("the failed round reported %d Query Groups, want %d", facts.QueryGroups, len(groups))
		}
		if facts.RegistryRoundTrips != 1 {
			t.Fatalf("the failed round reported %d registry round trips, want the one it actually spent "+
				"before the assignment read failed", facts.RegistryRoundTrips)
		}
	})
}
