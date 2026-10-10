// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// retentionRecordingGapStore answers every marker applied and keeps what each
// request said its markers live for.
type retentionRecordingGapStore struct {
	execution.GapGuardStore
	retentions []execution.GenerationRetention
}

func (store *retentionRecordingGapStore) ApplyGap(ctx context.Context, request execution.GapGuardApplyRequest) (execution.GapGuardApplyResult, error) {
	store.retentions = append(store.retentions, request.Retention)
	return store.GapGuardStore.ApplyGap(ctx, request)
}

// retentionRecordingNoDataStore does the same for no-data memory.
type retentionRecordingNoDataStore struct {
	answeringNoDataStore
	retentions []execution.GenerationRetention
}

func (store *retentionRecordingNoDataStore) ApplyNoData(ctx context.Context, request execution.NoDataApplyRequest) (execution.NoDataApplyResult, error) {
	store.retentions = append(store.retentions, request.Retention)
	return store.answeringNoDataStore.ApplyNoData(ctx, request)
}

// A round that evaluated its Plans writes their gap markers and loads them
// with each Plan's own retention, so the store gives the marker the lifetime
// the load renews it to; nothing on this path writes at the floor.
func TestAnEvaluatedRoundWritesItsGapMarkersWithThePlansRetention(t *testing.T) {
	fixture := newOutputIsolationFixture(t)
	recording := &retentionRecordingGapStore{GapGuardStore: fixture.coordinator.ports.GapGuard}
	fixture.coordinator.ports.GapGuard = recording
	if _, err := fixture.finalize(t); err != nil {
		t.Fatalf("finalize() error = %v", err)
	}
	failed := fixture.header.DuePlans[0]
	want, err := execution.DeriveStateRetentionRequirement(failed.CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(recording.retentions) == 0 {
		t.Fatal("setup: the round wrote no gap marker")
	}
	for _, retention := range recording.retentions {
		if retention.Unknown || !reflect.DeepEqual(retention.ByPlan[failed.Identity], want) {
			t.Fatalf("gap marker written with %+v, want the Plan's own retention %+v", retention, want)
		}
	}

	items, err := gapPreflightForHeader(fixture.header)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		due, ok := duePlan(fixture.header.DuePlans, item.Identity.Plan)
		if !ok {
			t.Fatalf("a gap load item for a Plan that is not due: %+v", item.Identity)
		}
		want, err := execution.DeriveStateRetentionRequirement(due.CompiledPlan)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(item.Retention, want) {
			t.Fatalf("gap load item retention %+v, want the Plan's own %+v", item.Retention, want)
		}
	}
}

// No-data memory is written with its own Plan's retention, and a memory for a
// Plan this round does not have is a wiring fault, not a write at a guess.
func TestNoDataMemoryIsWrittenWithItsPlansRetention(t *testing.T) {
	store := &retentionRecordingNoDataStore{answeringNoDataStore: answeringNoDataStore{status: execution.NoDataApplied}}
	coordinator := &SlotExecutionCoordinator{ports: Ports{
		NoData: store, Hosts: SharedHostBusiness,
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
	}}
	due := refusedMemoryDue(t)
	if err := coordinator.applyNoDataMemory(context.Background(), execution.SlotExecutionRequest{Operation: execution.OperationNormal},
		due, []execution.PlanNoDataMutation{refusedMemoryMutation(t)}); err != nil {
		t.Fatalf("applyNoDataMemory() error = %v", err)
	}
	want, err := execution.DeriveStateRetentionRequirement(due[0].CompiledPlan)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.retentions) != 1 || store.retentions[0].Unknown || !reflect.DeepEqual(store.retentions[0].ByPlan[due[0].Identity], want) {
		t.Fatalf("no-data memory written with %+v, want the Plan's own retention %+v", store.retentions, want)
	}

	if err := coordinator.applyNoDataMemory(context.Background(), execution.SlotExecutionRequest{Operation: execution.OperationNormal},
		nil, []execution.PlanNoDataMutation{refusedMemoryMutation(t)}); err == nil {
		t.Fatal("a memory for a Plan that is not due was written")
	}
}
