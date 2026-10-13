// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// runControlRole starts no Slot dispatcher or QG lease owner. The control
// refresh/assignment and shutdown paths are shared with a combined process.
func (bundle *phaseTwoWorkerBundle) runControlRole(ctx context.Context) error {
	roundCtx, stopRounds := context.WithCancel(ctx)
	defer stopRounds()
	done := make(chan error, 1)
	// Control rounds are leader tasks just like lease maintenance. Shutdown
	// waits for both before releasing the existing lease; a dependency that
	// ignores cancellation leaves the lease to expire instead.
	bundle.mu.Lock()
	if bundle.closed || bundle.draining {
		bundle.mu.Unlock()
		return errPhaseTwoWorkerDraining
	}
	bundle.maintenanceWG.Add(1)
	bundle.mu.Unlock()
	go func() {
		defer bundle.maintenanceWG.Done()
		done <- bundle.runControlRounds(roundCtx)
	}()
	var runErr error
	select {
	case <-ctx.Done():
		runErr = ctx.Err()
	case runErr = <-done:
	}
	stopRounds()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), bundle.dependencies.Config.ShutdownTimeout.Duration())
	defer cancel()
	shutdownErr := bundle.Shutdown(shutdownCtx)
	if errors.Is(runErr, context.Canceled) && ctx.Err() != nil {
		runErr = nil
	}
	return errors.Join(runErr, shutdownErr)
}

func (bundle *phaseTwoWorkerBundle) runControlRounds(ctx context.Context) error {
	refresh := time.NewTicker(bundle.dependencies.Config.PhaseTwo.Control.RefreshInterval.Duration())
	reconcile := time.NewTicker(bundle.dependencies.Config.PhaseTwo.Control.ReconcileInterval.Duration())
	defer refresh.Stop()
	defer reconcile.Stop()
	bundle.liveness.start(livenessLoopControl, controlLoopStallBound)
	var runErr error
	for runErr == nil {
		select {
		case <-ctx.Done():
			runErr = ctx.Err()
		case <-refresh.C:
			began := bundle.liveness.clock()
			runErr = bundle.refreshAndReconcile(ctx, true)
			bundle.liveness.turned(livenessLoopControl, began)
		case <-reconcile.C:
			began := bundle.liveness.clock()
			bundle.probeControlRedis(ctx)
			runErr = bundle.refreshAndReconcile(ctx, false)
			bundle.liveness.turned(livenessLoopControl, began)
		}
	}
	return runErr
}

// The legacy digest remains unchanged for older leaders and registrations.
// This additional contract describes executable formats and algorithms, not
// the resource quota assigned to this particular process. The legacy digest
// is retired with the fallback in ownership.WorkerCompatibility.Matches:
// once the previous upstream snapshot declares ExecutionContractDigest.
func phaseTwoExecutionContractDigest() (string, error) {
	semantics, err := state.RuntimeStateSemantics()
	if err != nil {
		return "", err
	}
	return contract.DeriveCanonicalDigestV2("alarmd-worker-execution-contract-v1", struct {
		SchemaVersion string               `json:"schema_version"`
		Algorithms    string               `json:"algorithm_registry"`
		State         state.StateSemantics `json:"state_semantics"`
	}{schemaVersion, strategy.NewDefaultAlgorithmCompilerRegistry().CapabilityDigest(), semantics})
}

// Keep configuration inputs visible to callers that need the legacy contract;
// no worker registration is manufactured to obtain a controller's eligibility.
func phaseTwoExpectedWorkerCompatibility(cfg config.Config) (ownership.WorkerCompatibility, error) {
	legacy, err := phaseTwoCapabilitiesDigest(cfg)
	if err != nil {
		return ownership.WorkerCompatibility{}, err
	}
	execution, err := phaseTwoExecutionContractDigest()
	if err != nil {
		return ownership.WorkerCompatibility{}, err
	}
	return ownership.WorkerCompatibility{DeploymentProfile: cfg.DeploymentProfile(),
		CapabilitiesDigest: legacy, ExecutionContractDigest: execution}, nil
}
