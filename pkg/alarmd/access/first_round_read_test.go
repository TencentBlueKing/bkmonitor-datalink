// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package access

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The scheduler runs a Slot's first round when it reckons access will read the
// primary query, by execution.ReadWaitWithinQueryBudget and with no prepared plan
// in hand. This is the other side of that reckoning: what access prepares for the
// primary is that instant, and a dependency over an earlier window is ready before
// it. A round at that instant finds nothing still to wait for.
func TestAMixedSlotAtItsPrimaryReadWaitsForNothing(t *testing.T) {
	const settling = 30 * time.Second
	contractRef, frozen, evaluationTime := historyAheadExecution(t)
	prepared, err := prepare(contractRef, frozen, settling, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	var primary, dependency int64
	for _, query := range prepared.Queries {
		for _, requirement := range query.Requirements {
			if requirement.Role == execution.InputRolePrimary {
				primary = query.ReadyAtUnixMilli
			} else {
				dependency = query.ReadyAtUnixMilli
			}
		}
	}
	consumer := frozen.Requirements[0].Consumers[0]
	budget := time.Duration(consumer.ConsumerDeadlineUnixMilli-evaluationTime.UnixMilli()-consumer.DownstreamExecutionReserveMilliSec) * time.Millisecond
	want := evaluationTime.Add(execution.ReadWaitWithinQueryBudget(contractRef.Slot.QueryGroup, budget, settling)).UnixMilli()
	if primary != want {
		t.Fatalf("the primary is read at %d, the reckoning says %d", primary, want)
	}
	if dependency == 0 || dependency >= primary {
		t.Fatalf("the dependency is ready at %d and the primary at %d, so this is not a mixed Slot", dependency, primary)
	}

	source, err := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, &recordingQueryPermits{}, Config{MinReadyDelay: settling})
	if err != nil {
		t.Fatal(err)
	}
	source.now = func() time.Time { return time.UnixMilli(primary) }
	source.wait = func(context.Context, time.Duration) error {
		t.Fatal("a Slot at its primary's read waited in place")
		return nil
	}
	completion, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1}, &retainingConsumer{})
	if err != nil {
		t.Fatal(err)
	}
	if completion.InPlaceWait != nil {
		t.Fatalf("in-place wait %+v at the primary's read", completion.InPlaceWait)
	}
}

// With a source delay the window is older by the delay and access adds the delay
// back, so the read is the same instant as without one. The scheduler reckons
// neither, and is right in both.
func TestThePrimaryReadIsTheSameWithASourceDelay(t *testing.T) {
	const settling = 30 * time.Second
	contractRef, frozen := frozenExecution(t)
	slot := int64(contractRef.Slot.EvaluationTime)
	requirement := frozen.Requirements[0]
	reserve := requirement.Consumers[0].DownstreamExecutionReserveMilliSec
	for _, interval := range []int64{60, 300, 3600} {
		for _, delay := range []int64{0, 120} {
			schedule := execution.ScheduleSpec{EvaluationIntervalSeconds: interval, Timezone: "UTC"}
			window := execution.QueryWindow{Start: slot - interval - delay, End: slot - delay}
			got, err := frozenConsumerReadyAt(contractRef, requirement, window, schedule, settling, time.Duration(reserve)*time.Millisecond, delay)
			if err != nil {
				t.Fatal(err)
			}
			budget := time.Duration(interval)*time.Second - time.Duration(reserve)*time.Millisecond
			want := slot*1000 + execution.ReadWaitWithinQueryBudget(contractRef.Slot.QueryGroup, budget, settling).Milliseconds() + contractRef.ReadHoldMillis
			if got != want {
				t.Fatalf("interval %d delay %d: access reads at %d, the reckoning says %d", interval, delay, got, want)
			}
		}
	}
}
