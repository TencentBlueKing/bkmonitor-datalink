// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// noDataOutputIdentity is the identity a no-data event's fingerprint is hashed
// with: the output identity of the view execution.PlanViewFor hands the
// trigger for a no-data series, which reads every dimension the record
// carries. The Plan's own OutputIdentity reads only the query's identity
// fields, and two groups differing only in a no-data dimension would hash
// alike under it.
func noDataOutputIdentity(due execution.DuePlan) *contract.MonitorOutputIdentity {
	view, err := execution.PlanViewFor(due, execution.SeriesKindNoData)
	if err != nil || view.CompiledPlan == nil {
		return nil
	}
	return view.CompiledPlan.OutputIdentity()
}

// manualClosesFor binds a strategy's manual closes, as the no-data load read
// them, to the Plan that has to recognise them; nil when there are none.
func manualClosesFor(due execution.DuePlan, closedAt map[string]int64) *nodata.ManualCloses {
	if len(closedAt) == 0 {
		return nil
	}
	identity := noDataOutputIdentity(due)
	if identity == nil {
		return nil
	}
	strategyID, businessID := due.Identity.StrategyID, due.Identity.BusinessID
	return &nodata.ManualCloses{
		Fingerprint: func(group nodata.Group) (string, error) {
			return nodata.GroupFingerprint(strategyID, businessID, *identity, group)
		},
		ClosedAt: closedAt,
	}
}

// noDataAbsenceStarts is, per Plan, when the absence behind each of this
// Slot's absent no-data series began, by the series' dimension identity
// digest (which the event carries as RecordRef.DimensionIdentityDigest).
type noDataAbsenceStarts map[execution.PlanIdentity]map[string]int64

type noDataAbsenceStartsKey struct{}

// withNoDataAbsenceStarts carries this Slot's absence starts to the output,
// where a close recorded while the Slot ran is checked (decision-024).
func withNoDataAbsenceStarts(ctx context.Context, starts noDataAbsenceStarts) context.Context {
	if len(starts) == 0 {
		return ctx
	}
	return context.WithValue(ctx, noDataAbsenceStartsKey{}, starts)
}

func noDataAbsenceStartsFrom(ctx context.Context) noDataAbsenceStarts {
	starts, _ := ctx.Value(noDataAbsenceStartsKey{}).(noDataAbsenceStarts)
	return starts
}

// withoutManuallyClosed takes out of the accepted series' events the no-data
// alerts a user closed after the round that produced them had read its memory:
// the close was recorded, and from then on its absence must send nothing
// (decision-024 section 5.2). Only absent no-data events of this Slot are
// looked at, with one read of the strategy's closes for the fingerprints
// among them.
//
// The events are taken out of eventsByState, a copy of it when any is, so
// every reading of what the Plan sent - the batch, the series the sink did
// not write, the events a refused State leaves at the consumer - reads the
// same set. A series whose alert was dropped still moves its State: the
// absence was decided, and the user, not the sink, is why it was not sent.
//
// A read that fails lets the batch out as it is: a close that could not be
// confirmed must not hold back an alert nobody closed. The Plan's next Slot
// applies the close to its memory either way.
func (coordinator *SlotExecutionCoordinator) withoutManuallyClosed(
	ctx context.Context,
	operation execution.Operation,
	plan execution.PlanIdentity,
	accepted []execution.StateMutation,
	eventsByState map[execution.StateKeyIdentity][]contract.TriggerEventV1,
) map[execution.StateKeyIdentity][]contract.TriggerEventV1 {
	reader := coordinator.ports.NoDataManualCloses
	starts := noDataAbsenceStartsFrom(ctx)[plan]
	if reader == nil || len(starts) == 0 {
		return eventsByState
	}
	fingerprints := make([]string, 0)
	seen := make(map[string]struct{})
	for _, mutation := range accepted {
		for _, event := range eventsByState[mutation.Identity] {
			if !manualCloseCandidate(event, starts) {
				continue
			}
			if _, dup := seen[event.DedupeMD5]; !dup {
				seen[event.DedupeMD5] = struct{}{}
				fingerprints = append(fingerprints, event.DedupeMD5)
			}
		}
	}
	if len(fingerprints) == 0 {
		return eventsByState
	}
	closedAt, err := reader.ReadNoDataManualCloses(ctx, plan.TenantID, plan.StrategyID, fingerprints)
	if err != nil {
		coordinator.observeManualCloseCheck(ctx, operation, observability.NoDataManualCloseFacts{CheckFailed: true}, err)
		return eventsByState
	}
	if len(closedAt) == 0 {
		return eventsByState
	}
	closes := nodata.ManualCloses{ClosedAt: closedAt}
	var kept map[execution.StateKeyIdentity][]contract.TriggerEventV1
	var dropped uint32
	for _, mutation := range accepted {
		events := eventsByState[mutation.Identity]
		var left []contract.TriggerEventV1
		for index, event := range events {
			closed := manualCloseCandidate(event, starts) &&
				closes.Matches(event.DedupeMD5, starts[event.RecordRef.DimensionIdentityDigest])
			if closed && left == nil {
				left = append(make([]contract.TriggerEventV1, 0, len(events)), events[:index]...)
			}
			if closed {
				dropped++
				continue
			}
			if left != nil {
				left = append(left, event)
			}
		}
		if left == nil {
			continue
		}
		if kept == nil {
			kept = make(map[execution.StateKeyIdentity][]contract.TriggerEventV1, len(eventsByState))
			for identity, events := range eventsByState {
				kept[identity] = events
			}
		}
		kept[mutation.Identity] = left
	}
	if kept == nil {
		return eventsByState
	}
	coordinator.observeManualCloseCheck(ctx, operation, observability.NoDataManualCloseFacts{Dropped: dropped}, nil)
	return kept
}

// observeManualCloseCheck writes the check's line: only for a batch it held
// an alert back from or could not check, so the line is the event and its
// count the metric.
func (coordinator *SlotExecutionCoordinator) observeManualCloseCheck(
	ctx context.Context, operation execution.Operation, facts observability.NoDataManualCloseFacts, err error,
) {
	reason := observability.ReasonCode(observability.ReasonNone)
	if err != nil {
		// The check failed and the batch went on unchecked, which is the
		// design: the line is the check's failure, with its cause, and not
		// the Slot's.
		reason = observability.ReasonCode(contract.ReasonRedisUnavailable)
	}
	coordinator.emitObservation(ctx, observability.Observation{
		Component: observability.ComponentOutput, Stage: observability.StageNoDataManualCloseChecked,
		Operation: observability.Operation(operation),
		Result:    observability.Result(observability.ResultSuccess), ReasonCode: reason,
		Counts: observability.Counts{Events: int64(facts.Dropped)}, Err: err,
		NoDataManualClose: &facts,
	})
}

// manualCloseCandidate is an alert a manual close can stop: a no-data
// ABNORMAL event of a series this Slot found absent, with a fingerprint.
func manualCloseCandidate(event contract.TriggerEventV1, starts map[string]int64) bool {
	if event.EventKind != contract.TriggerEventAbnormal || event.DedupeMD5 == "" {
		return false
	}
	if _, tagged := event.RecordRef.Dimensions[contract.NoDataDimensionTag]; !tagged {
		return false
	}
	return starts[event.RecordRef.DimensionIdentityDigest] > 0
}
