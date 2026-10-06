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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A replay that is dispatched can be read, and still has room to run, before
// the replay window closes.
//
// The two halves of this inequality are derived in two packages that did not
// read each other. access decides when a Slot may be read; the scheduler
// decides how many grid points a Slot may fall behind before replaying it is
// abandoned. Nothing held the two together, and at a ten-second period they
// contradicted each other outright: a Slot that missed its live deadline was
// classified as replayable, told by access to wait until the thirtieth second,
// and by the thirtieth second was four grid points behind -- past the window
// -- so it was skipped. It could not be scheduled fast enough to escape,
// because the waiting was the thing consuming the window. Every ten-second
// strategy lost every round in which it missed its live deadline, and every
// signal said healthy: the query was never issued, so nothing failed.
//
// Driven through prepare with a recovery operation rather than through the
// settling-wait rule directly, because the rule was never the thing that was
// wrong -- the recovery path simply did not use it. A test that asked the rule
// would have passed throughout.
//
// The three numbers come from the deployment's own defaults rather than from
// literals here, so this states what the shipped configuration does.
func TestEveryPeriodLeavesADispatchedReplayTimeToRun(t *testing.T) {
	defaults := config.Default().PhaseTwo
	configured := defaults.Access.MinReadyDelay.Duration()
	reserve := defaults.Access.DownstreamExecutionReserve.Duration()
	replaySlots := int64(defaults.Scheduler.MaxReplaySlots)

	for _, interval := range []int64{10, 15, 20, 30, 60} {
		name := (time.Duration(interval) * time.Second).String()
		t.Run(name, func(t *testing.T) {
			contractRef, frozen := frozenExecution(t)
			if got := frozen.Requirements[0].Consumers[0].DownstreamExecutionReserveMilliSec; got != reserve.Milliseconds() {
				t.Fatalf("fixture reserve %dms is not the deployment's %s; this test would be measuring "+
					"something the deployment does not run", got, reserve)
			}
			evaluationMillis := int64(contractRef.Slot.EvaluationTime) * 1000
			spec := execution.DeriveScheduleSpec(interval)
			spec.Timezone = "UTC"
			frozen.DuePlans[0].ScheduleSpec = spec
			frozen.DuePlans[0].CompletionDeadlineUnixMilli = evaluationMillis + spec.CompletionOffsetSeconds()*1000
			frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
			contractRef = bindFrozenDueDigest(t, contractRef, frozen)

			// A replay, which is the operation that was getting a boundary of
			// its own.
			prepared, err := prepare(contractRef, frozen, configured, true)
			if err != nil {
				t.Fatalf("prepare(replay) error=%v", err)
			}
			if len(prepared.Queries) != 1 {
				t.Fatalf("queries=%d, want 1", len(prepared.Queries))
			}
			readAt := time.Duration(prepared.Queries[0].ReadyAtUnixMilli-evaluationMillis) * time.Millisecond
			// The scheduler abandons the Slot at its MaxReplaySlots-th grid
			// point: from that instant the distance is over the limit.
			window := time.Duration(replaySlots*interval) * time.Second
			if readAt+reserve >= window {
				t.Fatalf("interval=%ds: a replay may not be read until T+%s and needs %s to run, but it is "+
					"abandoned at T+%s. Every Slot of this period that misses its live deadline is "+
					"dispatched, made to wait, and then skipped for having waited -- on every round, at "+
					"any scheduling speed, with no query issued and nothing reporting a failure",
					interval, readAt, reserve, window)
			}
		})
	}
}
