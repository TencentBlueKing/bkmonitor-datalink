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
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The round line says when a refusal was the view's one wait - the Worker
// holds the lease and its view has not carried the Query Group yet - from
// the source and from the executor alike, and wrapped or not. The fleet
// reads it there to keep that wait apart from a dependency that is down.
// Any other refusal of the view is not the wait.
func TestTheRoundLineNamesTheViewsWaitAndNothingElse(t *testing.T) {
	waiting := &ViewNotExecutableError{Reason: "not_in_view", AwaitingView: true}
	mismatch := &ViewNotExecutableError{Reason: "scope_mismatch"}
	for _, c := range []struct {
		name        string
		atExecution bool
		refusal     error
		want        bool
	}{
		{name: "at the source, the wait", refusal: waiting, want: true},
		{name: "at execution, the wait", atExecution: true, refusal: waiting, want: true},
		{name: "at the source, the wait wrapped", refusal: fmt.Errorf("gated read: %w", waiting), want: true},
		{name: "at the source, a scope mismatch", refusal: mismatch},
		{name: "at execution, a scope mismatch", atExecution: true, refusal: mismatch},
	} {
		t.Run(c.name, func(t *testing.T) {
			clock := newMutableClock(time.Unix(200, 0))
			slot := frozenSlot("query-group-1")
			source := &fakeSlotSource{slot: slot}
			executor := &refusingExecutor{}
			if c.atExecution {
				executor.err = c.refusal
			} else {
				source.err = c.refusal
			}
			flights, err := NewFlightCoordinatorWithRecovery(testRecoveryLimits(), clock.Now)
			if err != nil {
				t.Fatal(err)
			}
			var lines []observability.Observation
			flights.observer = observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				if o.Stage == observability.StageRunnerReturned {
					lines = append(lines, o)
				}
			})
			fence := execution.OwnerFence{QueryGroup: "query-group-1", OwnerID: "worker-1", OwnerEpoch: 1, LeaseToken: "token-1"}
			if c.atExecution {
				fence = slot.Dispatch.OwnerFence
			}
			runner, err := NewRunner("query-group-1", &fakeSession{fence: fence}, source, executor, flights, clock.Now)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := runner.RunOne(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(lines) != 1 || lines[0].RunOutcome != "view_not_executable" {
				t.Fatalf("round lines = %+v, want one view_not_executable", lines)
			}
			if lines[0].AwaitingView != c.want {
				t.Fatalf("the round line's AwaitingView = %t, want %t", lines[0].AwaitingView, c.want)
			}
		})
	}
}
