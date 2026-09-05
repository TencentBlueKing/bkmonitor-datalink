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
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type blockedWakeControl struct {
	*fakePhaseTwoControl
	entered chan struct{}
	calls   atomic.Int64
}

func (control *blockedWakeControl) Refresh(ctx context.Context) (phaseTwoControlRefreshResult, error) {
	if control.calls.Add(1) == 1 {
		close(control.entered)
	}
	<-ctx.Done()
	return phaseTwoControlRefreshResult{}, ctx.Err()
}

type blockedWakeOwnership struct {
	*fakePhaseTwoOwnership
	entered chan struct{}
	calls   atomic.Int64
}

func (owner *blockedWakeOwnership) AssignedQueryGroups(ctx context.Context, groups []execution.QueryGroupIdentity) ([]execution.QueryGroupIdentity, error) {
	call := owner.calls.Add(1)
	if call == 1 {
		return owner.fakePhaseTwoOwnership.AssignedQueryGroups(ctx, groups)
	}
	if call == 2 {
		close(owner.entered)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestPhaseTwoWorkerBundleNormalTicksContinueDuringBlockedControl(t *testing.T) {
	for _, stage := range []string{"refresh", "reconcile"} {
		t.Run(stage, func(t *testing.T) {
			cfg := validGoAccessRuntimeConfig()
			cfg.PhaseTwo.Scheduler.TickInterval = config.Duration(time.Millisecond)
			cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Hour)
			cfg.PhaseTwo.Control.ReconcileInterval = config.Duration(time.Hour)
			qg := execution.QueryGroupIdentity("healthy-normal")
			entered := make(chan struct{})
			baseControl := &fakePhaseTwoControl{queryGroups: []execution.QueryGroupIdentity{qg}}
			baseOwner := &fakePhaseTwoOwnership{assigned: []execution.QueryGroupIdentity{qg}}
			var calls atomic.Int64
			changed := make(chan struct{}, 1)
			baseOwner.runner = &callbackPhaseTwoQueryGroup{run: func(context.Context) (execution.SlotExecutionResult, bool, error) {
				calls.Add(1)
				select {
				case changed <- struct{}{}:
				default:
				}
				return execution.SlotExecutionResult{}, true, nil
			}}
			control := &blockedWakeControl{fakePhaseTwoControl: baseControl, entered: entered}
			owner := &blockedWakeOwnership{fakePhaseTwoOwnership: baseOwner, entered: entered}
			var bundle *phaseTwoWorkerBundle
			if stage == "refresh" {
				cfg.PhaseTwo.Control.RefreshInterval = config.Duration(20 * time.Millisecond)
				bundle = mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), control, baseOwner)
			} else {
				cfg.PhaseTwo.Control.ReconcileInterval = config.Duration(20 * time.Millisecond)
				bundle = mustPhaseTwoWorkerBundle(t, cfg, newPhaseTwoApplicationHealth(), baseControl, owner)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- bundle.Run(ctx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("Run(cancel) = %v", err)
					}
				case <-time.After(time.Second):
					t.Error("Run did not join scheduler after cancellation")
				}
			}()
			waitSignal(t, entered, "blocked "+stage)
			before := calls.Load()
			// Eight generations cannot come from the one buffered pre-control wake
			// or the single active QG. Wait on progress, not a fixed execution speed.
			deadline := time.NewTimer(time.Second)
			defer deadline.Stop()
			for calls.Load() < before+8 {
				select {
				case <-changed:
				case <-deadline.C:
					t.Fatalf("normal scheduling stopped during %s: calls %d -> %d", stage, before, calls.Load())
				}
			}
			if stage == "refresh" && control.calls.Load() != 1 {
				t.Fatal("refresh reentered while blocked")
			}
			if stage == "reconcile" && owner.calls.Load() != 2 {
				t.Fatal("reconcile reentered while blocked")
			}
		})
	}
}
