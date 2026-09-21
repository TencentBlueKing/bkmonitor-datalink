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
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// seriesCensus is where a Slot's series went: how many it meant to evaluate,
// how many it read Runtime State for, and how many it wrote.
//
// It exists because the first attempt to size the population that needs
// renewal inferred it from two rates a deployment already published --
// state_load minus state_apply -- and that difference is not the population.
// It also holds series short-circuited before the loaded views, series already
// applied by an earlier attempt of the same Slot, and series that never had a
// key. Sizing a mechanism from it put the estimate two orders of magnitude out
// and the mechanism shipped renewing almost nothing.
//
// The two differences answer different questions and neither can be derived
// from the other:
//
//   - Due minus Read is the series a Slot meant to evaluate and did not read.
//     A series whose PRIMARY input is incomplete is skipped before the State
//     preflight, so it is neither read nor written and its key ages the whole
//     time -- and a renewal that hangs on the read cannot reach it.
//   - Read minus Written is the series that were read and not written, which
//     is the population the renewal does cover.
//
// Counted at the three places the decisions are made, and reported on every
// Slot that had anything due. A count that only appears when it is non-zero
// cannot say the difference between "nothing was frozen" and "nothing was
// looked at", which is exactly the reading that had to be chased through a
// deployment.
type seriesCensus struct {
	Due     int
	Read    int
	Written int
}

// frozenSeriesOf is the series of one Plan that this Slot read and is not
// going to write.
//
// Read means the key was found: the three FOUND statuses are exactly the ones
// whose view carries the persisted apply version, and that version is the age
// the renewal decides from. A missing key has nothing to keep alive, and a
// corrupt or unreadable one is not something to extend the life of -- it is
// either about to be overwritten or better left to expire.
//
// Not going to write means the evaluation produced no mutation for it. That
// covers both halves of the same situation: a Level the trigger froze, and a
// Level frozen because its inputs were incomplete. It also, deliberately,
// covers the mutations that were produced and then found already applied --
// those keys were written by an earlier attempt at this same Slot, moments
// ago, so they have a full life and the age gate would decline them anyway.
//
// The series whose view came back equal to this Slot's own apply version never
// reach here at all: that comparison short-circuits the evaluation, so the
// series is neither in the loaded views nor in the results. Same reasoning --
// it was written by an earlier attempt at this Slot and is as fresh as a key
// gets.
func frozenSeriesOf(
	plan execution.PlanIdentity, loaded execution.StatePreflightResult, results []execution.StateEvaluation,
) []execution.FrozenSeriesState {
	if len(loaded.Items) == 0 {
		return nil
	}
	written := make(map[execution.StateKeyIdentity]struct{}, len(results))
	for _, result := range results {
		written[result.Mutation.Identity] = struct{}{}
	}
	frozen := make([]execution.FrozenSeriesState, 0, len(loaded.Items))
	for _, view := range loaded.Items {
		if view.Identity.Plan != plan {
			continue
		}
		switch view.Status {
		case execution.StateFoundReady, execution.StateFoundWarming, execution.StateFoundGapped:
		default:
			continue
		}
		if _, writing := written[view.Identity]; writing {
			continue
		}
		frozen = append(frozen, execution.FrozenSeriesState{
			Identity: view.Identity, LastApplied: view.PersistedApplyVersion.EvaluationTime,
		})
	}
	if len(frozen) == 0 {
		return nil
	}
	return frozen
}

// renewFrozenState keeps this Plan's frozen series alive and says what happened.
//
// It cannot fail the Slot. A renewal is bookkeeping about keys that were
// already read and evaluated; the alternative -- a Slot that retries because
// an expiry could not be extended -- would turn a mechanism that exists to
// remove retries into one that causes them. A failure is counted as FAILED
// against the whole batch and the Slot carries on.
//
// Chunked at the store's own per-call limit for the same reason the apply path
// is: one request must not grow with the size of a query group.
func (coordinator *SlotExecutionCoordinator) renewFrozenState(
	ctx context.Context,
	request execution.SlotExecutionRequest,
	retention []execution.StateRetentionRequirement,
	frozen []execution.FrozenSeriesState,
	facts *observability.FrozenStateRenewalFacts,
) {
	if coordinator == nil || len(frozen) == 0 {
		return
	}
	// The real clock, as the rest of this file's neighbours use. What a test
	// steers is the age, by stamping a fixture's last write in the past; the
	// store takes the instant from the request so its own tests can put a key
	// past half its life without waiting out half its life.
	now := time.Now()
	limit := int(coordinator.applyChunkItems(coordinator.budget.MaxStateMutations))
	if limit <= 0 {
		limit = len(frozen)
	}
	for start := 0; start < len(frozen); start += limit {
		end := start + limit
		if end > len(frozen) {
			end = len(frozen)
		}
		chunk := frozen[start:end]
		result, err := coordinator.ports.State.RenewFrozenRuntime(ctx, execution.FrozenStateRenewalRequest{
			Contract: request.Contract, Retention: retention, Items: chunk, Now: now,
		})
		if err == nil {
			err = execution.ValidateFrozenStateRenewal(execution.FrozenStateRenewalRequest{
				Contract: request.Contract, Retention: retention, Items: chunk, Now: now,
			}, result)
		}
		if err != nil {
			facts.Record(0, 0, 0, len(chunk))
			continue
		}
		facts.Record(result.Tally())
	}
}

// observeFrozenStateRenewal reports the Slot's renewals, once, with the
// population beside the outcomes.
//
// Silent when nothing was frozen, because a counter with nothing to add says
// the same thing by not moving. What makes an all-zero reading legible is the
// pair of rates the deployment already publishes: worker_work_total's
// state_load minus state_apply is how many keys are read and not written, and
// this family has to account for the difference. The two disagreeing is the
// reading that says the candidate set is wrong.
func (coordinator *SlotExecutionCoordinator) observeFrozenStateRenewal(
	ctx context.Context, operation execution.Operation, facts observability.FrozenStateRenewalFacts,
) {
	if coordinator == nil || facts.Empty() {
		return
	}
	result := observability.Result(observability.ResultSuccess)
	reason := observability.ReasonCode(observability.ReasonNone)
	if facts.Failed > 0 {
		result = observability.ResultDegraded
		reason = observability.ReasonCode(contract.ReasonRedisUnavailable)
	}
	recorded := facts
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageFrozenStateRenewed,
		Operation: observability.Operation(operation), Direction: observability.DirectionInternal,
		Result: result, ReasonCode: reason, FrozenStateRenewal: &recorded,
	})
}
