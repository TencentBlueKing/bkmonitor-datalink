// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

func controlOnlyOwnershipDependencies(t *testing.T) (productionPhaseTwoOwnershipDependencies, *fakePhaseTwoOwnershipStore) {
	t.Helper()
	now := time.Unix(1_700_000_000, 0)
	store := &fakePhaseTwoOwnershipStore{now: now, renewed: make(chan struct{})}
	eligibility, err := scheduler.NewStaticWorkerEligibility(ownership.WorkerCompatibility{
		DeploymentProfile: "shadow", CapabilitiesDigest: "capabilities",
	})
	if err != nil {
		t.Fatal(err)
	}
	reconciler, err := scheduler.NewReconciler(scheduler.NewRouter(eligibility), store)
	if err != nil {
		t.Fatal(err)
	}
	return productionPhaseTwoOwnershipDependencies{
		Roles: roles.Set{roles.Control}, Store: store, WorkerID: "control-1",
		Catalog: unavailableSlotCatalog{}, Progress: unavailableScheduleProgress{}, Now: func() time.Time { return now },
		Observer: observability.NopObserver{}, Reconcile: reconciler, ControlLeaderTTL: time.Minute,
		LeaseTTL: 30 * time.Second, ReconcileInterval: 5 * time.Second, ContentScopes: noContentScopes,
	}, store
}

func TestControlRoleOwnershipCoordinatesWithoutExecutionDependencies(t *testing.T) {
	dependencies, store := controlOnlyOwnershipDependencies(t)
	runtime, err := newProductionPhaseTwoOwnership(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.flights != nil || runtime.dependencies.Executor != nil {
		t.Fatal("control-only ownership acquired local execution dependencies")
	}
	ctx, now := context.Background(), dependencies.Now()
	if acquired, err := runtime.TryAcquireControlLeader(ctx, now, time.Minute); err != nil || !acquired {
		t.Fatalf("TryAcquireControlLeader() = %v, %v", acquired, err)
	}
	if err := runtime.PublishAssignments(ctx, nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.OpenQueryGroup(ctx, "group-1", now, time.Minute); err == nil || !strings.Contains(err.Error(), "worker role") {
		t.Fatalf("OpenQueryGroup() error = %v, want worker role refusal", err)
	}
	if err := runtime.RegisterWorker(ctx, ownership.WorkerRegistration{WorkerID: "control-1"}); err == nil || !strings.Contains(err.Error(), "worker_role_required") {
		t.Fatalf("RegisterWorker() error = %v, want worker_role_required", err)
	}
	if store.acquireLeaderCalls != 1 || store.acquireLeaseCalls != 0 || store.worker.WorkerID != "" {
		t.Fatalf("control/lease calls and worker registration = %d/%d/%q, want 1/0/empty", store.acquireLeaderCalls, store.acquireLeaseCalls, store.worker.WorkerID)
	}
}

func TestWorkerRoleOwnershipRequiresExecutionAndRefusesControlMutation(t *testing.T) {
	dependencies, store := controlOnlyOwnershipDependencies(t)
	dependencies.Roles = roles.Set{roles.Worker}
	dependencies.Reconcile, dependencies.ContentScopes = nil, nil
	dependencies.ControlLeaderTTL, dependencies.LeaseTTL, dependencies.ReconcileInterval = 0, 0, 0
	if _, err := newProductionPhaseTwoOwnership(dependencies); err == nil || !strings.Contains(err.Error(), "execution dependencies") {
		t.Fatalf("constructor without executor/flights = %v", err)
	}
	dependencies.Executor = rejectingSlotExecutor{}
	dependencies.RecoveryLimits = validGoAccessRuntimeConfig().PhaseTwo.Scheduler.RecoveryLimits()
	flights, err := scheduler.NewFlightCoordinatorWithRecovery(dependencies.RecoveryLimits, dependencies.Now)
	if err != nil {
		t.Fatal(err)
	}
	dependencies.Flights = flights
	dependencies.PostRecoveryTerminalDelay, dependencies.QueryDeadlineReserve = time.Minute, 5*time.Second
	dependencies.SnapshotRetention, dependencies.PublicationDelayAllowance, dependencies.SettlingWait = time.Hour, time.Minute, 30*time.Second
	runtime, err := newProductionPhaseTwoOwnership(dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.TryAcquireControlLeader(context.Background(), dependencies.Now(), 0); err == nil || !strings.Contains(err.Error(), "control role") {
		t.Fatalf("TryAcquireControlLeader() error = %v", err)
	}
	if err := runtime.PublishAssignments(context.Background(), nil, dependencies.Now()); err == nil || !strings.Contains(err.Error(), "control role") {
		t.Fatalf("PublishAssignments() error = %v", err)
	}
	if store.acquireLeaderCalls != 0 || store.publishAssignmentCalls != 0 {
		t.Fatalf("worker-only ownership wrote control state: %d leader calls, %d assignment calls", store.acquireLeaderCalls, store.publishAssignmentCalls)
	}
}

func TestWorkerRoleControlReadsActiveCatalogWithoutPublicationDependencies(t *testing.T) {
	publication := controlplane.SnapshotPublicationRef{SnapshotRevision: "snapshot-1", PublicationEpoch: 1}
	now := time.Unix(1_700_000_000, 0)
	repository := &fakeProductionCatalogRepository{
		activation: controlplane.ActivationState{RecordRevision: 1, Current: publication},
		snapshot:   controlplane.PublishedSnapshot{Publication: publication, QueryGroups: []controlplane.QueryGroup{{Identity: "group-1"}}},
		versionTag: "version-1", versionKnown: true, successMarks: []time.Time{now},
	}
	control, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Roles: roles.Set{roles.Worker}, Repository: repository, Schedules: &fakeScheduleProjection{}, Progress: &fakeProductionProgressReader{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	active, err := control.LoadActive(ctx)
	if err != nil || !reflect.DeepEqual(active.QueryGroups, []execution.QueryGroupIdentity{"group-1"}) {
		t.Fatalf("LoadActive() = %+v, %v", active, err)
	}
	if version, known, err := control.ControlVersion(ctx); err != nil || !known || version != "version-1" {
		t.Fatalf("ControlVersion() = %q, %v, %v", version, known, err)
	}
	if at, known, err := control.SourceRefreshSuccessAt(ctx); err != nil || !known || !at.Equal(now) {
		t.Fatalf("SourceRefreshSuccessAt() = %v, %v, %v", at, known, err)
	}
	for _, refresh := range []func(context.Context) (phaseTwoControlRefreshResult, error){control.InitialRefresh, control.Refresh} {
		if _, err := refresh(ctx); err == nil || !strings.Contains(err.Error(), "control_role_required") {
			t.Fatalf("refresh() error = %v, want control_role_required", err)
		}
	}
	if repository.renewCalls != 0 || repository.headerRebuilds != 0 || len(repository.successMarks) != 1 {
		t.Fatal("a worker-only catalog read wrote publication state")
	}
	if _, err := newProductionPhaseTwoControl(productionPhaseTwoControlDependencies{
		Repository: repository, Schedules: &fakeScheduleProjection{}, Progress: &fakeProductionProgressReader{},
	}); err == nil {
		t.Fatal("omitted roles changed legacy publication dependency validation")
	}
}
