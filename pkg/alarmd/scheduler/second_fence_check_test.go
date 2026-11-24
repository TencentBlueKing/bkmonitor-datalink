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
	"errors"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// secondFenceStore fails the plain fence check -- the one a check just before
// the Slot used to take -- and answers the merged reading the round opens with.
type secondFenceStore struct {
	*countingOwnershipStore
	checkErr error
}

func (store *secondFenceStore) CheckFence(context.Context, execution.OwnerFence) error {
	return store.checkErr
}

// A round reads ownership once, when it opens, and takes no second fence check
// before its Slot: the Slot's first write runs the fence in its own script.
// Through the real session, a store whose plain fence check would fail is not
// asked, the Slot runs, and the session stays open. When a fence check ends
// the session is ownership's own test (TestAFenceCheckEndsTheSessionOnlyOnTheStoresAnswer).
func TestARoundTakesNoFenceCheckBeforeItsSlot(t *testing.T) {
	at := time.Unix(200, 0)
	slot := frozenSlot("query-group-1")
	for _, checkErr := range []error{errors.New("ownership store unreachable"), ownership.ErrNotDesired} {
		store := &secondFenceStore{countingOwnershipStore: &countingOwnershipStore{
			fence: slot.Dispatch.OwnerFence, deadline: at.Add(time.Hour), assignment: testAssignment("worker-1", 3),
		}, checkErr: checkErr}
		session, err := ownership.OpenSession(context.Background(), store, "query-group-1", "worker-1", at, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		executed := 0
		runner, err := NewRunner("query-group-1", session, &fakeSlotSource{slot: slot},
			workflowExecutor(func(context.Context, execution.SlotExecutionRequest) (execution.SlotExecutionResult, error) {
				executed++
				return execution.SlotExecutionResult{Completed: true}, nil
			}), NewFlightCoordinator(), func() time.Time { return at })
		if err != nil {
			t.Fatal(err)
		}
		if _, attempted, err := runner.RunOne(context.Background()); !attempted || err != nil || executed != 1 {
			t.Fatalf("plain check answering %v: RunOne() attempted=%t err=%v executed=%d, want the Slot run", checkErr, attempted, err, executed)
		}
		if session.Deadline().IsZero() {
			t.Fatalf("plain check answering %v: the session ended", checkErr)
		}
	}
}
