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
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// A Query Group lost here and opened again by the next reconcile, before the
// lost Runner's Release ran (stopLostQueryGroup / detachLostQueryGroup detach
// first and release after), must keep the successor's read-hold binding.
func TestALostRunnersReleaseLeavesItsSuccessorsReadHoldBound(t *testing.T) {
	ctx := context.Background()
	fixture := newRenewalTestFixture(t)
	holds, _, _ := runtimeTestHolds(t)
	holds.now = fixture.clock.Now
	fixture.production.dependencies.ReadHolds = holds
	qg := execution.QueryGroupIdentity("query-group-1")

	lost, err := fixture.production.OpenQueryGroup(ctx, qg, fixture.clock.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	successor, err := fixture.production.OpenQueryGroup(ctx, qg, fixture.clock.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holds.owner(qg); err != nil {
		t.Fatalf("successor not bound before the lost Runner released: %v", err)
	}
	if err := lost.Release(ctx); err != nil {
		t.Fatal(err)
	}
	lease, accepting := successor.(*productionPhaseTwoQueryGroup).session.Current()
	if !accepting {
		t.Fatal("successor session stopped accepting")
	}
	if _, err := holds.owner(qg); err != nil {
		t.Fatalf("the lost Runner's Release unbound its successor's read hold: owner() = %v", err)
	}
	if err := holds.PrepareSchedule(ctx, runtimeTestHoldSchedule(t, qg), lease.Fence); errors.Is(err, ownership.ErrStaleFence) {
		t.Fatalf("every Slot of the live successor is refused: PrepareSchedule = %v", err)
	}
}

// A Runner's own release, its binding and its gate outcome still the ones
// its session made, drops both: the read hold keeps no binding for the
// group, and the gate no longer counts it as executed from the view.
func TestARunnersOwnReleaseDropsItsBindingAndItsGateOutcome(t *testing.T) {
	ctx := context.Background()
	fixture := newRenewalTestFixture(t)
	holds, _, _ := runtimeTestHolds(t)
	holds.now = fixture.clock.Now
	fixture.production.dependencies.ReadHolds = holds
	gate := newViewExecutionGate()
	fixture.production.WithViewExecutionGate(gate)
	qg := execution.QueryGroupIdentity("query-group-1")
	runner, err := fixture.production.OpenQueryGroup(ctx, qg, fixture.clock.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	gate.record(qg, runner.(*productionPhaseTwoQueryGroup).session, viewGateExecutable)
	if err := runner.Release(ctx); err != nil {
		t.Fatal(err)
	}
	holds.mu.Lock()
	bound := holds.groups[qg] != nil
	holds.mu.Unlock()
	if bound {
		t.Fatal("the read hold kept the binding of a Runner that released it")
	}
	if switched := gate.SwitchedQueryGroups([]execution.QueryGroupIdentity{qg}); switched != 0 {
		t.Fatalf("the gate counts %d Query Groups executed from the view after their Runner released, want 0", switched)
	}
}
