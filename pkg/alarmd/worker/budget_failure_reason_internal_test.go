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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A budget rejection says which kind it is, and the two kinds are different
// answers.
//
// A Slot whose own output is past a per-Slot cap is terminal: the retry
// recomputes the same Plans from the same frozen contract and produces the
// same output, so it crosses the same cap. A rejection on capacity shared with
// concurrent Slots is a pause: those Slots finish and give their share back.
// Both reached the page as internal_unknown, which is neither, and which reads
// as a defect in this build rather than as the two things it is.
func TestABudgetRejectionNamesWhetherRetryingCanHelp(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{
			name: "the Slot's own output is past a per-Slot cap",
			err: (&streamedExecution{request: execution.SlotExecutionRequest{Operation: execution.OperationNormal}}).
				slotBudgetRejection(observability.CapacityBudgetStateMutations,
					effectCounts{states: 100}, effectCounts{states: 1}, ProvisionalBudget{MaxStateMutations: 100}),
			want: contract.ReasonSlotBudgetExceeded,
		},
		{
			name: "shared capacity is full",
			err:  &provisionalBudgetExceededError{budget: observability.CapacityBudgetStateMutations},
			want: contract.ReasonResourceHardStop,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got observability.Observation
			coordinator := &SlotExecutionCoordinator{ports: Ports{
				Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					got = observability.NormalizeObservation(observation)
				}),
			}}
			coordinator.observeQueryFailure(context.Background(), execution.OperationNormal, time.Now(), "execute", test.err)

			if got.ReasonCode != observability.ReasonCode(test.want) {
				t.Fatalf("reason_code = %q, want %q", got.ReasonCode, test.want)
			}
			// And the code a reader can parse, from the one place that maps a
			// budget's label spelling to its code spelling.
			want := observability.CapacityBudgetFailureCode(observability.CapacityBudgetStateMutations)
			if got.QueryFailure == nil || got.QueryFailure.Code != want {
				t.Fatalf("failure code = %+v, want %q", got.QueryFailure, want)
			}
			if !observability.ValidQueryFailureCode(got.QueryFailure.Code) {
				t.Fatalf("failure code %q does not parse; fleet reads it as OTHER", got.QueryFailure.Code)
			}
		})
	}
}
