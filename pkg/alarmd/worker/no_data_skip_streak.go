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
	"sync"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// noDataPersistentSkipRounds is how many consecutive Slots a Plan has to go
// without its no-data detection running before that stops being occasional.
//
// Skipping one round is allowed by design and happens for ordinary reasons: a
// query that came back partial, a CMDB index still warming. What the rounds
// counter exists to separate is the other thing -- a Plan that skips every
// round, which every one of those reasons also produces when it does not
// clear. Three is the smallest number that cannot be a single event, and it is
// counted in rounds rather than in time because that is what the condition is
// about: a 10 second Plan and a 60 second one have both stopped detecting for
// three of their own rounds.
const noDataPersistentSkipRounds = 3

// noDataSkipStreaks remembers, per Plan, how many Slots in a row have ended
// without that Plan's no-data detection running.
//
// It exists because the outcome buckets cannot answer the question they most
// need to. One Slot's SKIPPED_HOSTS_UNRESOLVED and a Plan's thousandth
// consecutive one are the same increment on the same series, so a deployment
// where one Plan has silently stopped detecting for a day reads exactly like
// one where a hundred Plans each missed a round. A count of skips is a rate; a
// streak is a state, and only the state says anything has stopped.
//
// Held on the coordinator, which is one per process, so the streak survives
// Slots. A Plan that moves to another replica leaves its entry behind: the
// entry is never read as a level, only as the gate on an increment that
// requires the Plan to run here again, so a stale one costs a map entry and
// nothing else. The map is bounded by the Plans this replica has owned.
type noDataSkipStreaks struct {
	mu      sync.Mutex
	rounds  map[execution.PlanIdentity]int
	crossed map[execution.PlanIdentity]bool
}

// record takes one Plan's outcome for one Slot and reports whether this is the
// round on which it became a persistent stall.
//
// The report is once per stall, not once per round. A Plan stuck for a
// thousand rounds is one thing that has stopped, and counting it a thousand
// times would turn the count of stalls into a count of rounds -- which is the
// reading the outcome buckets already give and the one that cannot be acted
// on. It becomes reportable again only after the Plan evaluates.
func (streaks *noDataSkipStreaks) record(plan execution.PlanIdentity, outcome nodata.SlotOutcome) bool {
	streaks.mu.Lock()
	defer streaks.mu.Unlock()
	if streaks.rounds == nil {
		streaks.rounds = map[execution.PlanIdentity]int{}
		streaks.crossed = map[execution.PlanIdentity]bool{}
	}
	if outcome == nodata.OutcomeEvaluated {
		delete(streaks.rounds, plan)
		delete(streaks.crossed, plan)
		return false
	}
	streaks.rounds[plan]++
	if streaks.rounds[plan] < noDataPersistentSkipRounds || streaks.crossed[plan] {
		return false
	}
	streaks.crossed[plan] = true
	return true
}

// recordNoDataOutcome files one Plan's outcome for this Slot and reports a
// stall the round it becomes one.
//
// The outcome reaches the Slot's partition here and nowhere else, so the
// partition and the streak cannot disagree about what happened to a Plan.
func (stream *streamedExecution) recordNoDataOutcome(
	ctx context.Context, due execution.DuePlan, outcome nodata.SlotOutcome,
) {
	stream.noDataOutcomes = append(stream.noDataOutcomes, outcome)
	if !stream.coordinator.noDataSkips.record(due.Identity, outcome) {
		return
	}
	stream.coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
		Operation: observability.Operation(stream.request.Operation),
		Direction: observability.DirectionInternal, Result: observability.ResultDegraded,
		ReasonCode: observability.ReasonCode(outcome),
		Trace: observability.TraceFields{
			StrategyID: due.Identity.StrategyID, BusinessID: due.Identity.BusinessID,
		},
		NoDataStall: &observability.NoDataStallFacts{Outcome: string(outcome)},
	})
}
