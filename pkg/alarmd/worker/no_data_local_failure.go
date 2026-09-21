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
	"errors"
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// noDataLocalFailure is a no-data round that failed in a way that must not
// reach the Slot.
//
// A Plan's no-data detection is one half of what that Plan does, and it is the
// half nobody asked for by name: a strategy is configured to compare a number
// against a threshold and to also say something when the number stops coming.
// Failing the Slot over the second takes the first down with it -- the
// threshold results this round already computed are discarded, the progress is
// not committed, and the Slot is retried to compute them again. On a condition
// that does not clear by itself, that is the strategy's ordinary detection
// stopped for as long as the condition lasts, over a no-data record nobody was
// looking at.
//
// So the failure becomes this Plan's outcome for this Slot and nothing more.
// Every one of them is already a named bucket, the partition still adds up, and
// a Plan failing this way every round is visible as that bucket rising rather
// than as a Query Group that will not advance.
type noDataLocalFailure struct {
	outcome nodata.SlotOutcome
	err     error
}

func (failure *noDataLocalFailure) Error() string {
	return fmt.Sprintf("alarmd worker: no-data round %s: %v", failure.outcome, failure.err)
}

func (failure *noDataLocalFailure) Unwrap() error { return failure.err }

// derivationFailed names a round that could not be worked out: its memory was
// not loaded, its Slot could not be turned into the evaluation's inputs, or the
// evaluation refused them.
func derivationFailed(err error) error {
	return &noDataLocalFailure{outcome: nodata.OutcomeSkippedDerivationFailed, err: err}
}

// outputFailed names a round that was decided and could not be turned into the
// series the ordinary evaluation reads.
func outputFailed(err error) error {
	return &noDataLocalFailure{outcome: nodata.OutcomeSkippedOutputFailed, err: err}
}

// noDataLocalOutcome reports the bucket a no-data failure belongs in, and false
// for an error that is not one of them.
//
// The second result is what keeps this from swallowing everything. Only the
// failures this package wrapped on purpose are contained; anything else -- a
// cancelled context, a store that is gone -- still reaches the Slot, because
// those are not facts about no-data detection and containing them would turn a
// dependency being down into a quiet per-Plan skip.
func noDataLocalOutcome(err error) (nodata.SlotOutcome, bool) {
	var local *noDataLocalFailure
	if errors.As(err, &local) {
		return local.outcome, true
	}
	return "", false
}

// observeNoDataLocalFailure reports one contained failure with the Plan it
// belongs to and what went wrong.
//
// The bucket alone is a count, and a count of skips does not say which Plan or
// why. This is the line somebody reads when that count starts rising, and it
// carries the error the Slot no longer carries: before this, the same fact
// arrived as a failed Slot, which at least named itself loudly. Containing the
// failure without saying anything would be the quieter, worse trade.
func (stream *streamedExecution) observeNoDataLocalFailure(
	ctx context.Context,
	due execution.DuePlan,
	outcome nodata.SlotOutcome,
	err error,
) {
	defer func() { _ = recover() }()
	stream.coordinator.ports.Observer.Observe(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		Result: observability.ResultDegraded, Operation: observability.Operation(stream.request.Operation),
		Direction:       observability.DirectionInternal,
		ReasonCode:      observability.ReasonCode(outcome),
		EvaluationOwner: costEvaluationOwner(due.Identity),
		Trace: observability.TraceFields{
			StrategyID: due.Identity.StrategyID, BusinessID: due.Identity.BusinessID,
		},
		Err: err,
	})
}
