// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

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

// An open that takes the Query Group's lease and then cannot build its
// runner - here a source refused for keeping snapshots no longer than a
// publication may be late - lets the lease go, and a release that fails is in the error the
// open returns, beside what stopped it: dropped, the lease stayed held to
// its TTL with nothing to say so.
func TestAnOpenThatFailsAfterTakingTheLeaseSaysWhenItCouldNotLetItGo(t *testing.T) {
	refused := errors.New("store unanswered")
	for _, releaseErr := range []error{nil, refused} {
		now := time.Unix(1_700_000_000, 0)
		limits := validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
		flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
		if err != nil {
			t.Fatal(err)
		}
		store := &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{}), releaseErr: releaseErr}
		eligibility, err := scheduler.NewStaticWorkerEligibility(ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
		if err != nil {
			t.Fatal(err)
		}
		reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
		if err != nil {
			t.Fatal(err)
		}
		production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
			QueryCooldowns: &countingCooldownStore{},
			Store:          store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
			Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
			ControlLeaderTTL: time.Minute, Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
			Reconcile: reconciler, Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute, QueryDeadlineReserve: 5 * time.Second,
			SnapshotRetention: time.Minute, PublicationDelayAllowance: time.Hour, SettlingWait: 30 * time.Second, LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
		})
		if err != nil {
			t.Fatalf("newProductionPhaseTwoOwnership() error = %v", err)
		}
		if err := production.RegisterWorker(context.Background(), ownership.WorkerRegistration{
			WorkerID: "worker-1", AssignmentReadiness: ownership.WorkerReady,
			DependencyStatus: ownership.DependencyHealthy, DeploymentProfile: "shadow",
			CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Minute),
		}); err != nil {
			t.Fatal(err)
		}
		if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Minute); err != nil || !leader {
			t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
		}
		if err := production.PublishAssignments(context.Background(), []execution.QueryGroupIdentity{"query-group-1"}, now); err != nil {
			t.Fatal(err)
		}
		runner, err := production.OpenQueryGroup(context.Background(), "query-group-1", now, time.Minute)
		if runner != nil || !errors.Is(err, scheduler.ErrSnapshotRetentionInsufficient) || store.releaseCalls != 1 {
			t.Fatalf("release error %v: OpenQueryGroup() = (%v, %v), %d releases; want refused for its source and the lease let go",
				releaseErr, runner, err, store.releaseCalls)
		}
		if releaseErr != nil && !errors.Is(err, releaseErr) {
			t.Fatalf("OpenQueryGroup() error = %v, want the failed release in it", err)
		}
	}
}

// A released session drops its Query Group's witnesses from Release, which
// every release path reaches after its Slot drain, and not before: opening
// drops nothing, and the drop names the Query Group and how far the session
// ran - here no Slot, so the store keeps any Slot's witnesses but none.
// That the drain comes first is the bundle's order (handover and decline drop
// the State cache, then wait, then call Release); this holds the drop to
// Release, the one place that order puts it after the wait.
func TestReleasingAQueryGroupDropsItsWitnessesFromReleaseOnly(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limits := validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(limits, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	store := &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})}
	eligibility, err := scheduler.NewStaticWorkerEligibility(ownership.WorkerCompatibility{DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities"})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	type drop struct {
		queryGroup    execution.QueryGroupIdentity
		upTo          execution.EvaluationTime
		leaseReleased int
	}
	var drops []drop
	production, err := newProductionPhaseTwoOwnership(productionPhaseTwoOwnershipDependencies{
		QueryCooldowns: &countingCooldownStore{},
		DropRuntimeWitnesses: func(queryGroup execution.QueryGroupIdentity, upTo execution.EvaluationTime) {
			drops = append(drops, drop{queryGroup, upTo, store.releaseCalls})
		},
		Store: store, WorkerID: "worker-1", Catalog: unavailableSlotCatalog{},
		Progress: unavailableScheduleProgress{}, Executor: rejectingSlotExecutor{}, Now: func() time.Time { return now },
		ControlLeaderTTL: time.Minute, Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
		Reconcile: reconciler, Flights: flights, RecoveryLimits: limits, PostRecoveryTerminalDelay: time.Minute, QueryDeadlineReserve: 5 * time.Second,
		SnapshotRetention: time.Hour, PublicationDelayAllowance: time.Minute, SettlingWait: 30 * time.Second, LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
	})
	if err != nil {
		t.Fatalf("newProductionPhaseTwoOwnership() error = %v", err)
	}
	if err := production.RegisterWorker(context.Background(), ownership.WorkerRegistration{
		WorkerID: "worker-1", AssignmentReadiness: ownership.WorkerReady,
		DependencyStatus: ownership.DependencyHealthy, DeploymentProfile: "shadow",
		CapabilitiesDigest: "capabilities", ExpiresAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	if leader, err := production.TryAcquireControlLeader(context.Background(), now, time.Minute); err != nil || !leader {
		t.Fatalf("TryAcquireControlLeader() leader=%v error=%v", leader, err)
	}
	if err := production.PublishAssignments(context.Background(), []execution.QueryGroupIdentity{"query-group-1"}, now); err != nil {
		t.Fatal(err)
	}
	runner, err := production.OpenQueryGroup(context.Background(), "query-group-1", now, time.Minute)
	if err != nil {
		t.Fatalf("OpenQueryGroup() error = %v", err)
	}
	if len(drops) != 0 {
		t.Fatalf("opening dropped witnesses: %+v", drops)
	}
	if err := runner.Release(context.Background()); err != nil {
		t.Fatalf("Release() error = %v", err)
	}
	if want := []drop{{"query-group-1", 0, 0}}; !reflect.DeepEqual(drops, want) {
		t.Fatalf("drops = %+v, want %+v: once, for this Query Group, before the lease went", drops, want)
	}
}

// How far a session ran is the latest Slot it took to execution, failed ones
// included - their load left witnesses too - and an earlier Slot run after it,
// a replay or a supplement, does not pull it back.
func TestSessionSlotsRemembersTheLatestSlotTakenToExecution(t *testing.T) {
	slots := &sessionSlots{next: rejectingSlotExecutor{}}
	if got := slots.last(); got != 0 {
		t.Fatalf("last() before any Slot = %d, want 0", got)
	}
	for _, at := range []execution.EvaluationTime{120, 180, 60} {
		request := execution.SlotExecutionRequest{Contract: execution.FrozenExecutionContractRef{
			Slot: execution.SlotIdentity{QueryGroup: "query-group-1", EvaluationTime: at}}}
		if _, err := slots.Execute(context.Background(), request); !errors.Is(err, errRejectingExecutorReached) {
			t.Fatalf("Execute(%d) error = %v, want the wrapped executor's", at, err)
		}
	}
	if got := slots.last(); got != 180 {
		t.Fatalf("last() = %d, want 180", got)
	}
}
