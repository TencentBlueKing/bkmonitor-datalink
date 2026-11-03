// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// supplementOf is what the executor reports for a supplement of an earlier
// Slot: the Slot completion stage, the supplement's operation, and the
// execute outcome every supplement carries, since it completes no Slot.
func supplementOf(slot int64, operation observability.Operation) observability.Observation {
	return observability.Observation{Component: observability.ComponentScheduler, Stage: observability.StageSlotCompleted,
		Operation: operation, ExecuteOutcome: "incomplete", Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone,
		Trace: observability.TraceFields{StrategyID: "4101", BusinessID: "7", EvaluationTime: slot}}
}

// An object whose rounds end degraded, supplemented between them, keeps the
// line its rounds put it on and its latest round: a supplement is not a
// round. The same observation with any other operation is a round that
// reached execution and did not finish, which is what put supplemented
// objects on DEFECT -- the second case shows the fixture reaches it.
func TestASupplementIsNotARoundOfTheObject(t *testing.T) {
	for name, tc := range map[string]struct {
		operation observability.Operation
		defect    bool
	}{
		"supplement":                       {observability.OperationSupplement, false},
		"the same outcome of a real round": {observability.OperationNormal, true},
	} {
		tracker := newTracker(t, &clock{at: now})
		ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg"})
		const period, first = int64(60), int64(60_000)
		last := int64(0)
		for index := int64(0); index < int64(DefaultDegradedRounds)+2; index++ {
			last = first + index*period
			round(ctx, tracker, last, "COMPLETED_WITH_UNAVAILABLE", "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED", primary("FULL", "DATA"), nil)
			tracker.Observe(ctx, supplementOf(last-2*period, tc.operation))
		}
		rows := tracker.Anomalies()
		if len(rows) != 1 {
			t.Fatalf("%s: rows %+v, want the object's one row", name, rows)
		}
		onDefect := rows[0].ReasonCode == "incomplete" || rows[0].Finding.Check == CheckDefect
		if onDefect != tc.defect {
			t.Errorf("%s: reason %s under %s, want DEFECT from the outcome %v", name, rows[0].ReasonCode, rows[0].Finding.Check, tc.defect)
		}
		if state := tracker.groups["qg"]; !tc.defect && state.lastRoundSlot != last {
			t.Errorf("%s: latest round %d, want the latest round's Slot %d, not the supplemented one", name, state.lastRoundSlot, last)
		}
	}
}

// fullSupplement is every observation a supplement of an earlier Slot
// reports, in order, under the given operation: its start, the gap scope and
// state it loaded, its evaluation, the state it admitted and applied, its
// events (one write refused), a query that failed, and its completion, which
// completes no Slot.
func fullSupplement(slot int64, operation observability.Operation) []observability.Observation {
	trace := observability.TraceFields{StrategyID: "4101", BusinessID: "7", EvaluationTime: slot}
	components := map[observability.Stage]observability.Component{
		observability.StageSlotStarted: observability.ComponentScheduler, observability.StageSlotCompleted: observability.ComponentScheduler,
		observability.StageQueryCompleted: observability.ComponentAccess, observability.StageEvaluationCompleted: observability.ComponentEvaluation,
		observability.StageGapLoaded: observability.ComponentState, observability.StageStatePreflight: observability.ComponentState,
		observability.StageStateAdmission: observability.ComponentState, observability.StageStateApplied: observability.ComponentState,
		observability.StageEventACKed: observability.ComponentOutput,
	}
	of := func(stage observability.Stage) observability.Observation {
		return observability.Observation{Component: components[stage], Stage: stage, Operation: operation,
			Result: observability.ResultSuccess, ReasonCode: observability.ReasonNone, Trace: trace}
	}
	gap := of(observability.StageGapLoaded)
	gap.GapProgress = &observability.GapProgressFacts{Scope: "window", Status: "HELD", Required: 5, Observed: 2}
	refused := of(observability.StageEventACKed)
	refused.Result, refused.Err = observability.ResultFailed, errors.New("sink refused the write")
	failed := of(observability.StageQueryCompleted)
	failed.Result = observability.ResultFailed
	failed.QueryFailure = &observability.QueryFailureFacts{Stage: "provider", Category: "provider_transport", Code: "QUERY_TIMEOUT"}
	completed := of(observability.StageSlotCompleted)
	completed.ExecuteOutcome = "incomplete"
	completed.SlotBudgetUsage = &observability.SlotBudgetUsageFacts{RetainedBytes: 99, RetainedShareBytes: 100}
	return []observability.Observation{of(observability.StageSlotStarted), gap, of(observability.StageStatePreflight),
		of(observability.StageEvaluationCompleted), of(observability.StageStateAdmission), of(observability.StageStateApplied),
		of(observability.StageEventACKed), refused, failed, completed}
}

// Every reading the tracker gives is the same with supplements between its
// rounds as without, whichever stage of a supplement it is: the tracker
// leaves out the whole supplement, not its completion alone. The same
// observations under a round's operation change the readings, which shows
// they reach the tracker.
func TestAFullSupplementLeavesEveryTrackerReadingAsItsRoundsLeftIt(t *testing.T) {
	readings := func(operation observability.Operation) string {
		tracker := newTracker(t, &clock{at: now})
		ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg"})
		const period, first = int64(60), int64(60_000)
		for index := int64(0); index < int64(DefaultDegradedRounds)+2; index++ {
			slot := first + index*period
			round(ctx, tracker, slot, "COMPLETED_WITH_UNAVAILABLE", "LEVEL_OUTCOME_UNKNOWN", "HISTORY_GAPPED", primary("FULL", "DATA"), nil)
			if operation != "" {
				for _, observation := range fullSupplement(slot-2*period, operation) {
					tracker.Observe(ctx, observation)
				}
			}
		}
		encoded, err := json.Marshal(map[string]any{
			"anomalies": tracker.Anomalies(), "undecidable": tracker.Undecidable(), "by_design": tracker.ByDesign(),
			"demoted": tracker.Demoted(), "demotion_flow": tracker.DemotionFlow(), "determined": tracker.Determined(),
			"tracked": tracker.Tracked(), "no_data": tracker.NoData(), "no_data_memory": tracker.NoDataMemory(),
			"gap_skips": tracker.GapSkips(), "pruned_skips": tracker.PrunedSkips(), "recovered": tracker.Recovered(),
			"retained_share": tracker.RetainedShare(), "round_memory": tracker.RoundMemory(),
			"bookkeeping_abandoned": tracker.BookkeepingAbandoned(), "no_data_tracking": tracker.NoDataTrackingSummary(),
		})
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	rounds := readings("")
	if withSupplements := readings(observability.OperationSupplement); withSupplements != rounds {
		t.Fatalf("supplements changed the tracker's readings:\n%s\nwant\n%s", withSupplements, rounds)
	}
	if asRounds := readings(observability.OperationNormal); asRounds == rounds {
		t.Fatal("the same observations as a round's changed nothing: the fixture does not reach the tracker")
	}
}
