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
	"sort"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// appliedPlanRecorder is what one attempt at one Slot got as far as writing.
//
// It exists because the fact has to be collected deep in the state apply and
// used at the very end of the attempt, and because it is worth nothing at all
// unless the attempt then fails: an attempt that finishes writes its Progress,
// and the Progress is the fact. So it is per attempt, created and read by
// Execute, and everything in between only adds to it.
//
// Carried on the context rather than threaded through the call chain. The
// chain between Execute and the state apply is long and passes through code
// that has no business knowing about this; a parameter there would be a fact
// about failure bookkeeping stated in a dozen signatures that do not otherwise
// mention it.
type appliedPlanRecorder struct {
	mutex sync.Mutex
	plans map[execution.PlanIdentity]struct{}
	// incomplete is the Plans this attempt did not write whole: a key failed,
	// was refused, was never sent, or was already there from an earlier
	// attempt. A Plan here is never applied whatever else was counted for it:
	// a mark that said otherwise would have the Slot finalized as evaluated
	// with some of the Plan's series one Slot behind.
	incomplete map[execution.PlanIdentity]struct{}
	// committed is set when Progress was written. An attempt that got that far
	// has recorded the Slot properly and must leave no mark: the mark exists
	// only to answer a question the Progress would otherwise have answered.
	committed bool
}

type appliedPlanContextKey struct{}

// withAppliedPlans starts recording for one attempt.
func withAppliedPlans(ctx context.Context) (context.Context, *appliedPlanRecorder) {
	recorder := &appliedPlanRecorder{plans: map[execution.PlanIdentity]struct{}{}, incomplete: map[execution.PlanIdentity]struct{}{}}
	return context.WithValue(ctx, appliedPlanContextKey{}, recorder), recorder
}

func appliedPlansFrom(ctx context.Context) *appliedPlanRecorder {
	recorder, _ := ctx.Value(appliedPlanContextKey{}).(*appliedPlanRecorder)
	return recorder
}

// recordApplied notes a Plan whose every key this attempt wrote. The caller
// decides that from the count of APPLIED keys against the Plan's list, once
// every chunk has run; a Plan with any key failed, refused, unsent, or
// already applied by an earlier attempt goes to recordIncomplete instead, and
// stays there.
//
// Only APPLIED counts, never ALREADY_APPLIED. An already-applied key was
// written by an earlier attempt, and if that attempt also failed to commit
// Progress then it left its own mark; counting it here would let an attempt
// claim credit for work it did not do, which matters because the count is
// compared against the whole due set to decide whether a Slot still owes a
// gap.
func (recorder *appliedPlanRecorder) recordApplied(plan execution.PlanIdentity) {
	if recorder == nil {
		return
	}
	recorder.mutex.Lock()
	recorder.plans[plan] = struct{}{}
	recorder.mutex.Unlock()
}

// recordIncomplete notes a Plan that this attempt did not get whole into the
// store. It is final for the attempt: nothing recorded for the Plan before or
// after makes it applied.
func (recorder *appliedPlanRecorder) recordIncomplete(plan execution.PlanIdentity) {
	if recorder == nil {
		return
	}
	recorder.mutex.Lock()
	recorder.incomplete[plan] = struct{}{}
	recorder.mutex.Unlock()
}

// progressCommitted says this attempt wrote the Slot down properly.
func (recorder *appliedPlanRecorder) progressCommitted() {
	if recorder == nil {
		return
	}
	recorder.mutex.Lock()
	recorder.committed = true
	recorder.mutex.Unlock()
}

// unrecorded is the Plans this attempt applied when it has something to leave
// behind, and nothing when it has not.
//
// Nothing to leave behind means either that no state was written -- the Slot
// failed before or during its evaluation, which is a real gap and should read
// as one -- or that Progress was committed, in which case the Slot is finished
// and the mark would be a key nobody reads.
func (recorder *appliedPlanRecorder) unrecorded() []execution.PlanIdentity {
	if recorder == nil {
		return nil
	}
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	if recorder.committed || len(recorder.plans) == 0 {
		return nil
	}
	plans := make([]execution.PlanIdentity, 0, len(recorder.plans))
	for plan := range recorder.plans {
		if _, partial := recorder.incomplete[plan]; partial {
			continue
		}
		plans = append(plans, plan)
	}
	if len(plans) == 0 {
		return nil
	}
	sort.Slice(plans, func(left, right int) bool { return lessPlanIdentity(plans[left], plans[right]) })
	return plans
}

// recordExecutionEvidence leaves behind what this attempt applied, when the
// attempt is ending without having written the Slot down.
//
// Everything here is deliberately unable to change the attempt's outcome. The
// mark is what lets a later query-free completion say "this Slot was already
// evaluated"; failing to leave one costs that completion its evidence and
// nothing else, so a failure is observed and dropped. Turning it into an error
// would mean a bookkeeping write deciding whether a Slot retries.
func (coordinator *SlotExecutionCoordinator) recordExecutionEvidence(
	ctx context.Context, request execution.SlotExecutionRequest, recorder *appliedPlanRecorder,
) {
	if coordinator == nil || coordinator.ports.ExecutionEvidence == nil {
		return
	}
	applied := recorder.unrecorded()
	if len(applied) == 0 {
		return
	}
	// The context the attempt ran on may already be cancelled -- that is one of
	// the ways an attempt ends without committing -- so the write gets its own
	// short-lived one. Without it the mark would be missing in exactly the case
	// it exists for.
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), executionEvidenceWriteTimeout)
	defer cancel()
	now := time.Now()
	// The mark lives to the Slot's keep-until, not its recovery-until: the
	// finalization that reads it runs after the replay window closes, and a
	// mark that ended where its reader begins was never there to be read.
	err := coordinator.ports.ExecutionEvidence.Record(
		writeCtx, request.Contract.Slot, execution.PlanIdentitiesOf(request.DuePlanTargets.Plans), applied,
		time.UnixMilli(request.KeepUntilUnixMilli), now,
	)
	result := observability.Result(observability.ResultSuccess)
	reason := observability.ReasonCode(observability.ReasonNone)
	if err != nil {
		result = observability.ResultDegraded
		reason = observability.ReasonCode(contract.ReasonRedisUnavailable)
	}
	coordinator.emitObservation(writeCtx, observability.Observation{
		Component: observability.ComponentProgress, Stage: observability.StageExecutionEvidenceWritten,
		Operation: observability.Operation(request.Operation),
		Direction: observability.DirectionInternal, Result: result, ReasonCode: reason,
		ExecutionEvidence: &observability.ExecutionEvidenceFacts{
			Kind:         string(execution.EvidenceStateApplied),
			PlansApplied: len(applied), PlansTotal: len(request.DuePlanTargets.Plans),
		},
	})
}

// executionEvidenceWriteTimeout bounds the one write an ending attempt makes.
// Short on purpose: the attempt is already over and the scheduler is waiting
// for its answer, and a mark that arrives late is worth less than a Slot that
// returns on time.
const executionEvidenceWriteTimeout = 2 * time.Second

// readExecutionEvidence is what a query-free completion can find out about an
// earlier attempt at the same Slot.
//
// A read failure is UNREADABLE and the completion goes ahead. Holding up a Slot
// that has already missed its window, because the record of a previous attempt
// could not be read, would turn a reading into a dependency.
func (coordinator *SlotExecutionCoordinator) readExecutionEvidence(
	ctx context.Context, duePlans []execution.PlanIdentity, slot execution.SlotIdentity,
) *execution.ExecutionEvidence {
	if coordinator == nil || coordinator.ports.ExecutionEvidence == nil {
		return nil
	}
	evidence, err := coordinator.ports.ExecutionEvidence.Read(ctx, slot, duePlans)
	if err != nil {
		return &execution.ExecutionEvidence{
			Kind: execution.EvidenceUnreadable, PlansTotal: len(duePlans),
		}
	}
	return &evidence
}
