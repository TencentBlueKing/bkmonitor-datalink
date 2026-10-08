// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Slot frozen from its schedule knows the Slot the schedule has after it,
// and none when its Segment ends first.
func TestAFrozenSlotKnowsTheSlotAfterIt(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(200, 0))
	slot, due, _, err := source.Next(context.Background(), "query-group-1")
	if err != nil || !due || slot.FollowingSlot != slot.Contract.Slot.EvaluationTime+60 {
		t.Fatalf("Next() = (%+v, %t, %v), want the Slot one interval after", slot, due, err)
	}
	end := slot.Contract.Slot.EvaluationTime + 1
	schedule.Segment.End = &end
	if got := followingSlot(schedule, slot.Contract.Slot.EvaluationTime); got != 0 {
		t.Fatalf("following Slot past the Segment's end = %d, want none", got)
	}
}

// The Runner hands the query layer the Slot after the one it executes on
// the context, not on the request.
func TestTheRunnerHandsTheExecutorTheSlotAfter(t *testing.T) {
	now := time.UnixMilli(1_700_000_000_000)
	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	slot := frozenSlot("query-group-1")
	slot.FollowingSlot = slot.Contract.Slot.EvaluationTime + 60
	executor := &followingRecordingExecutor{}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: fence, deadline: now.Add(time.Minute)}, &fakeSlotSource{slot: slot},
		executor, NewFlightCoordinator(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.RunOne(context.Background()); err != nil {
		t.Fatalf("RunOne() error = %v", err)
	}
	if executor.seen != slot.FollowingSlot {
		t.Fatalf("executor saw following Slot %d, want %d", executor.seen, slot.FollowingSlot)
	}
}

type followingRecordingExecutor struct{ seen execution.EvaluationTime }

func (executor *followingRecordingExecutor) Execute(ctx context.Context, _ execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
	executor.seen = execution.FollowingSlotOf(ctx)
	return execution.SlotExecutionResult{Completed: true, Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone}, nil
}
