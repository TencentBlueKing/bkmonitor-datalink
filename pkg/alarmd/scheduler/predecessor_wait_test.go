// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/readhold"
)

// awaitingReadHolds answers every new Slot that it waits on a predecessor.
type awaitingReadHolds struct {
	testReadHolds
	awaiting *readhold.AwaitingPredecessorError
}

func (holds *awaitingReadHolds) SlotReadHold(context.Context, execution.FrozenQueryGroupSchedule, execution.EvaluationTime, execution.OwnerFence) (time.Duration, error) {
	return 0, holds.awaiting
}

func (holds *awaitingReadHolds) PeekSlotReadHold(execution.FrozenQueryGroupSchedule, execution.EvaluationTime) (time.Duration, error) {
	return 0, readhold.ErrNotSeeded
}

// A Slot whose group waits on a predecessor is not frozen and not a failure:
// the round answers it not due until the wait's next look, naming the Slot
// and the wait, and the Runner's bound carries the wait for a reader - a
// schedule bound, not a backoff.
func TestASlotWaitingOnAPredecessorIsNotDueUntilItsNextLook(t *testing.T) {
	schedule := schedulerSchedule(t, 60, 60, nil, "snapshot-1", 1)
	catalog := &fakeSlotCatalog{t: t, schedules: []execution.FrozenQueryGroupSchedule{schedule}}
	source := newProductionSlotSourceForTest(t, catalog, missingProgress(), time.Unix(160, 0))
	awaiting := &readhold.AwaitingPredecessorError{QueryGroup: "query-group-1", Predecessor: "query-group-0",
		Reason: readhold.PredecessorRecordOpen, SinceUnixMilli: 150_000, UntilUnixMilli: 700_000, RetryAt: time.UnixMilli(164_500)}
	source.readHolds = &awaitingReadHolds{awaiting: awaiting}
	slot, due, facts, err := source.Next(context.Background(), "query-group-1")
	if err != nil || due || facts.AwaitingPredecessor != awaiting || facts.NotDueUntilUnix != 165 || facts.NextSlotUnix != 60 {
		t.Fatalf("a waiting Slot = (%+v, due %t, %+v, %v), want not due until 165 naming Slot 60 and the wait", slot.Contract, due, facts, err)
	}

	fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
	runner, err := NewRunner("query-group-1", &fakeSession{fence: fence}, &fakeSlotSource{facts: facts}, &blockingExecutor{}, NewFlightCoordinator(),
		func() time.Time { return time.Unix(160, 0) })
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runner.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	if bound := runner.DueBound(); bound.Verdict != DueVerdictNotDue || bound.Deferred || bound.AwaitingPredecessor != awaiting ||
		bound.NotDueUntilUnix != 165 {
		t.Fatalf("bound = %+v, want a not-due schedule bound until 165 carrying the wait", bound)
	}
}
