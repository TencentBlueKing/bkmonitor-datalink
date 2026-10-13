// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

type blockedRoleControl struct {
	*fakePhaseTwoControl
	entered, returned, unblock chan struct{}
	ignoreCancel               bool
}

func (control *blockedRoleControl) Refresh(ctx context.Context) (phaseTwoControlRefreshResult, error) {
	close(control.entered)
	defer close(control.returned)
	if control.ignoreCancel {
		<-control.unblock
	} else {
		<-ctx.Done()
	}
	return phaseTwoControlRefreshResult{}, ctx.Err()
}

type roleStoppingOwnership struct {
	*fakePhaseTwoOwnership
	stopped  chan struct{}
	released atomic.Bool
}

func (owner *roleStoppingOwnership) MaintainControlLeader(ctx context.Context, _, _ time.Duration) error {
	<-ctx.Done()
	close(owner.stopped)
	return ctx.Err()
}

func (owner *roleStoppingOwnership) ReleaseControlLeader(context.Context) error {
	owner.released.Store(true)
	return nil
}

func TestControlRoleStopsRenewalAndWaitsForControlWritesBeforeRelease(t *testing.T) {
	for _, ignoreCancel := range []bool{false, true} {
		t.Run(fmt.Sprintf("ignore_cancel=%v", ignoreCancel), func(t *testing.T) {
			cfg := validGoAccessRuntimeConfig()
			cfg.Roles = roles.Set{roles.Control}
			cfg.ShutdownTimeout = config.Duration(50 * time.Millisecond)
			cfg.PhaseTwo.Control.RefreshInterval = config.Duration(5 * time.Millisecond)
			cfg.PhaseTwo.Control.ReconcileInterval = config.Duration(time.Hour)
			control := &blockedRoleControl{fakePhaseTwoControl: &fakePhaseTwoControl{},
				entered: make(chan struct{}), returned: make(chan struct{}), unblock: make(chan struct{}), ignoreCancel: ignoreCancel}
			owner := &roleStoppingOwnership{fakePhaseTwoOwnership: &fakePhaseTwoOwnership{}, stopped: make(chan struct{})}
			bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
			if err := bundle.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- bundle.runControlRole(ctx) }()
			select {
			case <-control.entered:
			case <-time.After(time.Second):
				t.Fatal("control round did not enter")
			}
			cancel()
			select {
			case err := <-done:
				if ignoreCancel != errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("shutdown error = %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("control role shutdown exceeded its budget")
			}
			select {
			case <-owner.stopped:
			case <-time.After(time.Second):
				t.Fatal("control lease renewal continued")
			}
			if owner.released.Load() == ignoreCancel {
				t.Fatal("lease release did not wait for control writes to stop")
			}
			if ignoreCancel {
				close(control.unblock)
			}
			select {
			case <-control.returned:
			case <-time.After(time.Second):
				t.Fatal("control round leaked after unblocking")
			}
			if err := waitPhaseTwoGroup(context.Background(), &bundle.maintenanceWG); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestControlReadinessRemainsSeparateWhenWorkerAssignmentCannotBeRead(t *testing.T) {
	cfg := validGoAccessRuntimeConfig()
	app, err := newPhaseTwoApplication(cfg)
	if err != nil {
		t.Fatal(err)
	}
	control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"role-qg"}}
	owner := &fakePhaseTwoOwnership{}
	owner.injectFailure("assigned", errors.New("assignment unavailable"), -1)
	bundle := mustPhaseTwoWorkerBundle(t, cfg, app.health, control, owner)
	if err := bundle.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot := app.HealthSnapshot()
	if snapshot.Ready || snapshot.RoleReadiness["worker"] != "not_ready" ||
		(snapshot.RoleReadiness["control"] != "ready" && snapshot.RoleReadiness["control"] != "degraded") {
		t.Fatalf("control status inherited Worker assignment readiness: %+v", snapshot)
	}
	if err := bundle.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestBusinessRoleCombinationsKeepExecutionAndControlOwnershipSeparate(t *testing.T) {
	for _, selected := range []roles.Set{nil, {roles.Control}, {roles.Worker},
		{roles.Control, roles.Worker}, {roles.Control, roles.Channel}, {roles.Worker, roles.Channel},
		{roles.Control, roles.Worker, roles.Channel}} {
		t.Run(selected.String(), func(t *testing.T) {
			cfg := validGoAccessRuntimeConfig()
			cfg.Roles = selected
			control := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{"role-test-qg"}}
			owner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{"role-test-qg"}, runner: newFakePhaseTwoQueryGroup()}
			bundle := mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, owner)
			if err := bundle.Start(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := bundle.refreshAndReconcile(context.Background(), true); err != nil {
				t.Fatal(err)
			}
			if cfg.HasRole(roles.Control) {
				if control.initialRefreshCalls != 1 || control.refreshCalls == 0 || owner.publishAssignmentCount() == 0 {
					t.Fatal("control role did not publish through the existing coordinator")
				}
			} else if control.initialRefreshCalls != 0 || control.refreshCalls != 0 || owner.controlLeaderCount() != 0 || owner.publishAssignmentCount() != 0 {
				t.Fatal("worker-only process acquired or refreshed the control authority")
			}
			if cfg.HasRole(roles.Worker) {
				if len(owner.registrationStates()) == 0 || owner.opens != 1 {
					t.Fatal("worker role did not register and open its assignment")
				}
			} else if len(owner.registrationStates()) != 0 || owner.opens != 0 || len(bundle.runners) != 0 {
				t.Fatal("control-only process entered the Worker population or acquired a QG lease")
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if err := bundle.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExpectedWorkerContractExcludesRoleAndResourceQuotaWhileRetainingLegacyDigest(t *testing.T) {
	control := validGoAccessRuntimeConfig()
	control.Roles = roles.Set{roles.Control}
	worker := control
	worker.Roles = roles.Set{roles.Worker}
	worker.PhaseTwo.Worker.ID = "other-worker"
	worker.PhaseTwo.Coordinator.MaxEvents++
	want, err := phaseTwoExpectedWorkerCompatibility(control)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := phaseTwoWorkerRegistration(worker, ownership.WorkerReady, time.Now(), nil, nil, viewStreamIdentity{})
	if err != nil {
		t.Fatal(err)
	}
	if want.CapabilitiesDigest == registration.CapabilitiesDigest {
		t.Fatal("legacy resource-sensitive digest changed its semantics")
	}
	if !want.Matches(registration.Compatibility()) {
		t.Fatal("role or resource quota changed the execution contract")
	}
	old := registration.Compatibility()
	old.ExecutionContractDigest = ""
	if want.Matches(old) {
		t.Fatal("legacy registration bypassed its original digest gate")
	}
	old.CapabilitiesDigest = want.CapabilitiesDigest
	if !want.Matches(old) {
		t.Fatal("compatible legacy registration was refused")
	}
}
