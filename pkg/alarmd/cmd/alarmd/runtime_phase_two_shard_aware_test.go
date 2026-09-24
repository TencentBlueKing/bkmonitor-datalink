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
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The Leader's round decides the split gate on the ready set it places
// with, and its facts name the ready workers that do not declare the split
// contract - on the log line and on the fleet snapshot alike. A roll is
// watched by that list going 0 -> n -> 0: a registration written by a
// build from before the contract declares nothing, so the gate closes on
// it and opens again once it is replaced; nothing asks for a split yet, so
// nothing is held, and the count of held splits reads zero.
func TestTheRoundNamesEveryReadyWorkerThatDoesNotDeclareTheSplitContract(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	worker := func(id string, aware bool) ownership.WorkerRegistration {
		registration := ownership.WorkerRegistration{
			WorkerID: id, AssignmentReadiness: ownership.WorkerReady, DependencyStatus: ownership.DependencyHealthy,
			DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Hour),
			Capabilities: []string{ownership.CapabilityContentScope},
		}
		if aware {
			registration.Capabilities = append(registration.Capabilities, ownership.CapabilityShardAware)
		}
		return registration
	}
	groups := []execution.QueryGroupIdentity{"query-group-1", "query-group-2"}
	store := &rebalanceOwnershipStore{
		fakePhaseTwoOwnershipStore: &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})},
		workers:                    []ownership.WorkerRegistration{worker("worker-1", true), worker("worker-2", false), worker("worker-3", true)},
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
	eligibility, err := scheduler.NewStaticWorkerEligibility(ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	var observations []observability.Observation
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
		ControlLeaderTTL: time.Hour, Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observations = append(observations, observation)
		}), Reconcile: reconciler, Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute,
		QueryDeadlineReserve: 5 * time.Second, SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute,
		SettlingWait: 30 * time.Second, LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
		Costs: scheduler.NewCostLedger(func() time.Time { return now }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Hour); err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
	}
	lastGate := func(t *testing.T) *observability.ShardAwareFacts {
		t.Helper()
		var found *observability.ShardAwareFacts
		for _, observation := range observations {
			if observation.Stage == observability.StageRebalancePlanned && observation.Rebalance != nil {
				found = observation.Rebalance.ShardAware
			}
		}
		if found == nil {
			t.Fatal("the round's facts carry no split gate")
		}
		return found
	}
	// Mid-roll: one ready worker is on a build from before the contract.
	if err := production.PublishAssignments(context.Background(), groups, now); err != nil {
		t.Fatalf("PublishAssignments(round 1) error = %v", err)
	}
	if gate := lastGate(t); gate.Ready != 3 || !reflect.DeepEqual(gate.Unaware, []string{"worker-2"}) || gate.SplitsHeld != 0 {
		t.Fatalf("round 1 gate = %+v, want three ready, worker-2 unaware, nothing held", gate)
	}
	if published := production.LastRebalance(); published == nil || published.ShardAware == nil || !reflect.DeepEqual(published.ShardAware.Unaware, []string{"worker-2"}) {
		t.Fatalf("fleet facts = %+v, want the same replica named on the snapshot", published)
	}
	// The roll completes: the replaced replica declares, the list is empty.
	store.workers[1] = worker("worker-2", true)
	at := now.Add(40 * time.Second)
	if err := production.PublishAssignments(context.Background(), groups, at); err != nil {
		t.Fatalf("PublishAssignments(round 2) error = %v", err)
	}
	if gate := lastGate(t); gate.Ready != 3 || len(gate.Unaware) != 0 {
		t.Fatalf("round 2 gate = %+v, want nobody unaware after the roll", gate)
	}
	if published := production.LastRebalance(); published == nil || published.ShardAware == nil || len(published.ShardAware.Unaware) != 0 || published.ShardAware.Ready != 3 {
		t.Fatalf("fleet facts after the roll = %+v", published)
	}
}

// This build declares the split contract in its registration, beside the
// content contract: a Leader reading a fleet of this build admits a split.
func TestThisBuildDeclaresTheSplitContract(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	registration, err := phaseTwoWorkerRegistration(cfg, ownership.WorkerReady, time.Unix(1_700_000_000, 0), nil, nil, viewStreamIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if !registration.Declares(ownership.CapabilityShardAware) || !registration.Declares(ownership.CapabilityContentScope) {
		t.Fatalf("registration declares %v, want both contracts", registration.Capabilities)
	}
}

// Losing the Control Leader authority tells whoever asked to be told. The
// readings that belong to the role are taken off the scrape there; the six
// paths that give the authority up all go through this one method, so the
// call has to be in it and not in each of them.
func TestGivingUpTheControlLeaderAuthorityReportsTheStepDown(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	store := &rebalanceOwnershipStore{
		fakePhaseTwoOwnershipStore: &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})},
		assignments:                map[execution.QueryGroupIdentity]ownership.AssignmentRecord{},
	}
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
	steppedDown := 0
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
		ControlLeaderTTL: time.Hour, Observer: observability.NopObserver{}, Reconcile: reconciler, Flights: flights,
		RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute, QueryDeadlineReserve: 5 * time.Second,
		SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute, SettlingWait: 30 * time.Second,
		LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
		Costs: scheduler.NewCostLedger(func() time.Time { return now }), SteppedDownAsLeader: func() { steppedDown++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	authority, err := production.ensureControlAuthority(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if steppedDown != 0 {
		t.Fatalf("acquiring the authority reported %d step-downs", steppedDown)
	}
	// Another authority's fence does not give this one up.
	production.clearControlAuthority(ownership.PublicationAuthority{Fence: execution.OwnerFence{QueryGroup: "another", OwnerID: "worker-9"}})
	if steppedDown != 0 {
		t.Fatalf("another fence's release reported %d step-downs", steppedDown)
	}
	production.clearControlAuthority(authority)
	if steppedDown != 1 {
		t.Fatalf("giving up the authority reported %d step-downs, want one", steppedDown)
	}
}
