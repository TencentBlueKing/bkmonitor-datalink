// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A Slot attempt reports what it waited on, both times, however fast it was.
//
// The measurement has to be unconditional at the site. An attempt that stalls
// produces no failure -- nothing went wrong, it is simply still waiting -- so
// there is nothing to hang a report on except the wait itself, and a report
// emitted only when something goes wrong is absent in exactly the state it
// exists to describe. In the production evidence, twenty-two seconds passed
// between a Slot beginning and its query being planned with no line of any
// kind in between, and the answer to "which of these was it in" had to be
// guessed from the two timestamps on either side.
//
// Asserted on a healthy, fast attempt on purpose: the slow case cannot be
// reached without making a test wait, and if the fast case reported nothing
// the mechanism would be missing precisely where it is cheapest to have.
func TestASlotAttemptReportsEachWaitItPassedThrough(t *testing.T) {
	var observed []observability.Observation
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observed = append(observed, observation)
	})
	fixture := newFixtureWithObserver(t, true, "", observer)

	result, err := fixture.coordinator.Execute(context.Background(), slotRequest(execution.OperationNormal))
	if err != nil || !result.Completed {
		t.Fatalf("Execute() = (%+v, %v), want a completed Slot", result, err)
	}

	waits := map[string]int{}
	for _, observation := range observed {
		if observation.SlotWait == nil {
			continue
		}
		if observation.Stage != observability.StageSlotWait {
			t.Fatalf("a slot wait was reported under stage %s", observation.Stage)
		}
		if observation.Duration < 0 {
			t.Fatalf("slot wait %s has a negative duration %s", observation.SlotWait.Wait, observation.Duration)
		}
		waits[observation.SlotWait.Wait]++
	}
	for _, wait := range []string{observability.SlotWaitProgressBegin, observability.SlotWaitFinalization} {
		if waits[wait] != 1 {
			t.Fatalf("waits = %v, want exactly one %s. A wait nothing measures is a Slot attempt that can "+
				"stall with no line anywhere saying what it is in", waits, wait)
		}
	}
}
