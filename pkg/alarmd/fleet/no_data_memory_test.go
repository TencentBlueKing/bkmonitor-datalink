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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// memoryRefused is the observation the worker emits when the store will not
// take a Plan's absence memory: its own stage, degraded, the store's reason,
// the Plan's identity on the trace, the object on the context -- the round
// itself is reported separately and is fine.
func memoryRefused(ctx context.Context, tracker *Tracker, bytes int) {
	tracker.Observe(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataMemoryRefused,
		Result: observability.ResultDegraded, ReasonCode: "STATE_BUDGET_EXCEEDED", Direction: observability.DirectionInternal,
		Trace:               observability.TraceFields{StrategyID: "s-1", BusinessID: "2"},
		NoDataMemoryRefusal: &observability.NoDataMemoryRefusalFacts{Reason: "STATE_BUDGET_EXCEEDED", Record: "GROUPS", Groups: bytes, Limit: 65536},
	})
}

// A refused absence memory is listed on its own, with the store's reason,
// the numbers it compared and since when -- and not as an anomaly: the
// object's rounds complete, so it is healthy by every column, which is the
// whole reason the loss needs a line of its own.
func TestARefusedAbsenceMemoryIsListedApartFromTheColumns(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-memory"})
	for round := 0; round < 3; round++ {
		tracker.Observe(ctx, observability.Observation{ProgressCompletionKind: "FULL_COMPLETED",
			Trace: observability.TraceFields{EvaluationTime: int64(100 + 60*round)}})
		memoryRefused(ctx, tracker, 70000+round)
		at.at = at.at.Add(time.Minute)
	}
	if rows := tracker.Anomalies(); len(rows) != 0 {
		t.Fatalf("anomalies = %+v, want none: the rounds completed", rows)
	}
	rows := tracker.NoDataMemory()
	if len(rows) != 1 {
		t.Fatalf("no-data memory rows = %+v, want the one object", rows)
	}
	row := rows[0]
	if row.QueryGroup != "qg-memory" || row.Kind != KindNoDataMemoryRefused || row.ReasonCode != "STATE_BUDGET_EXCEEDED" {
		t.Fatalf("row = %+v, want the object under its kind with the store's reason", row)
	}
	memory := row.NoDataMemory
	if memory == nil || memory.Refusals != 3 || !memory.FirstAt.Equal(now) || !memory.LastAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("memory = %+v, want three refusals from %v to %v", memory, now, now.Add(2*time.Minute))
	}
	if memory.Record != "GROUPS" || memory.Groups != 70002 || memory.Limit != 65536 || memory.Plan.StrategyID != "s-1" {
		t.Fatalf("memory = %+v, want the latest measurement and the Plan", memory)
	}
	if !row.Since.Equal(now) || row.Consecutive != 3 || len(row.Strategies) != 1 || row.Strategies[0].StrategyID != "s-1" {
		t.Fatalf("row = %+v, want since the first refusal, the count, and the Plan as its strategy", row)
	}
	// Healthy rounds after it do not end it: nothing has said the write went
	// through, and a round completing is not that.
	tracker.Observe(ctx, observability.Observation{ProgressCompletionKind: "FULL_COMPLETED",
		Trace: observability.TraceFields{EvaluationTime: 400}})
	if rows := tracker.NoDataMemory(); len(rows) != 1 || rows[0].NoDataMemory.Refusals != 3 {
		t.Fatalf("rows after a healthy round = %+v, want the refusal still listed, unchanged", rows)
	}
	// A refusal that names no Plan is still a refusal on the object: it is
	// recorded, with no strategy to show for it.
	other := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-unnamed"})
	tracker.Observe(other, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataMemoryRefused, Result: observability.ResultDegraded,
		ReasonCode: "STATE_CORRUPT", NoDataMemoryRefusal: &observability.NoDataMemoryRefusalFacts{Reason: "STATE_CORRUPT"},
	})
	rows = tracker.NoDataMemory()
	if len(rows) != 2 || rows[1].QueryGroup != "qg-unnamed" || rows[1].NoDataMemory.Reason != "STATE_CORRUPT" || len(rows[1].Strategies) != 0 {
		t.Fatalf("rows = %+v, want the unnamed refusal listed too, without a strategy", rows)
	}
}

// memoryWritten is the observation the worker emits for every absence-memory
// write that was not refused: the store's outcome word, and whether the
// store now holds what the round wanted -- carried, not derived by readers.
func memoryWritten(ctx context.Context, tracker *Tracker, outcome string, stored bool) {
	result := observability.Result(observability.ResultDegraded)
	if stored {
		result = observability.Result(observability.ResultSuccess)
	}
	tracker.Observe(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataMemoryWritten, Result: result,
		Direction:         observability.DirectionInternal,
		Trace:             observability.TraceFields{StrategyID: "s-1", BusinessID: "2"},
		NoDataMemoryWrite: &observability.NoDataMemoryWriteFacts{Outcome: outcome, Stored: stored},
	})
}

// The refusal ends when a write for the Plan stores -- the emitter's stored
// flag, not the outcome's name: ALREADY_APPLIED is stored, STALE_VERSION is
// not. The row leaves and the recovery is on the ledger under the line it
// was listed on; a write that did not store changes nothing.
func TestAStoredWriteEndsTheRefusalAndIsItsRecovery(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-memory"})
	for round := 0; round < 3; round++ {
		memoryRefused(ctx, tracker, 70000)
		at.at = at.at.Add(time.Minute)
	}
	memoryWritten(ctx, tracker, "STALE_VERSION", false)
	if rows := tracker.NoDataMemory(); len(rows) != 1 || rows[0].NoDataMemory.Refusals != 3 {
		t.Fatalf("rows after a write that did not store = %+v, want the refusal still listed", rows)
	}
	if recovered := tracker.Recovered(); len(recovered) != 0 {
		t.Fatalf("recovered after a write that did not store = %+v, want nothing", recovered)
	}
	storedAt := at.at
	memoryWritten(ctx, tracker, "ALREADY_APPLIED", true)
	if rows := tracker.NoDataMemory(); len(rows) != 0 {
		t.Fatalf("rows after a stored write = %+v, want the refusal gone", rows)
	}
	recovered := tracker.Recovered()
	if len(recovered) != 1 || recovered[0].Check != CheckNoDataMemoryRefused || recovered[0].Key != "STATE_BUDGET_EXCEEDED" || recovered[0].Objects != 1 {
		t.Fatalf("recovered = %+v, want the one object under the refusal's line and reason", recovered)
	}
	if !recovered[0].FirstFailure.Equal(now) || !recovered[0].LastRecovery.Equal(storedAt) {
		t.Fatalf("recovered clocks = %+v, want the first refusal as onset and the stored write as recovery", recovered[0])
	}
	// A stored write for a different Plan of the same object does not end
	// a refusal that named another.
	memoryRefused(ctx, tracker, 70000)
	tracker.Observe(ctx, observability.Observation{
		Component: observability.ComponentState, Stage: observability.StageNoDataMemoryWritten, Result: observability.ResultSuccess,
		Trace:             observability.TraceFields{StrategyID: "s-other", BusinessID: "2"},
		NoDataMemoryWrite: &observability.NoDataMemoryWriteFacts{Outcome: "APPLIED", Stored: true},
	})
	if rows := tracker.NoDataMemory(); len(rows) != 1 {
		t.Fatalf("rows after another Plan's stored write = %+v, want the refusal still listed", rows)
	}
}

// Two Plans of one object refused, one of them storing: one Plan recovered
// and an object still listed, naming the Plan still refused -- not the whole
// object recovered on the strength of the wrong Plan's write. The recovery
// is recorded when the last refused Plan stores.
func TestOnePlanStoringDoesNotRecoverAnotherRefusedPlan(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-two"})
	refuse := func(strategy string, bytes int) {
		tracker.Observe(ctx, observability.Observation{
			Component: observability.ComponentState, Stage: observability.StageNoDataMemoryRefused, Result: observability.ResultDegraded,
			ReasonCode: "STATE_BUDGET_EXCEEDED", Trace: observability.TraceFields{StrategyID: strategy, BusinessID: "2"},
			NoDataMemoryRefusal: &observability.NoDataMemoryRefusalFacts{Reason: "STATE_BUDGET_EXCEEDED", Record: "GROUPS", Groups: bytes, Limit: 65536},
		})
	}
	store := func(strategy string) {
		tracker.Observe(ctx, observability.Observation{
			Component: observability.ComponentState, Stage: observability.StageNoDataMemoryWritten, Result: observability.ResultSuccess,
			Trace:             observability.TraceFields{StrategyID: strategy, BusinessID: "2"},
			NoDataMemoryWrite: &observability.NoDataMemoryWriteFacts{Outcome: "APPLIED", Stored: true},
		})
	}
	refuse("s-a", 70000)
	at.at = at.at.Add(time.Minute)
	refuse("s-b", 90000)
	rows := tracker.NoDataMemory()
	if len(rows) != 1 || rows[0].NoDataMemory.Plans != 2 || len(rows[0].Strategies) != 2 || rows[0].NoDataMemory.Plan.StrategyID != "s-b" || rows[0].Consecutive != 2 || !rows[0].Since.Equal(now) {
		t.Fatalf("rows = %+v / %+v, want one object with two refused Plans, the latest refusal shown, since the first", rows, rows[0].NoDataMemory)
	}
	at.at = at.at.Add(time.Minute)
	store("s-b")
	rows = tracker.NoDataMemory()
	if len(rows) != 1 || rows[0].NoDataMemory.Plans != 1 || rows[0].NoDataMemory.Plan.StrategyID != "s-a" || len(rows[0].Strategies) != 1 || rows[0].Strategies[0].StrategyID != "s-a" {
		t.Fatalf("rows after one Plan stored = %+v / %+v, want the object still listed for the other Plan", rows, rows[0].NoDataMemory)
	}
	if recovered := tracker.Recovered(); len(recovered) != 0 {
		t.Fatalf("recovered after one of two Plans stored = %+v, want nothing: the object has not recovered", recovered)
	}
	at.at = at.at.Add(time.Minute)
	store("s-a")
	if rows := tracker.NoDataMemory(); len(rows) != 0 {
		t.Fatalf("rows after both Plans stored = %+v, want the object gone", rows)
	}
	if recovered := tracker.Recovered(); len(recovered) != 1 || recovered[0].Objects != 1 || !recovered[0].LastRecovery.Equal(at.at) {
		t.Fatalf("recovered after both Plans stored = %+v, want the object recovered at the last Plan's write", recovered)
	}
}

// On the report the object is under its own line, on the work list, folded
// on the store's reason, and counted as this deployment's; the row reads
// completed for the round and the check for the loss.
func TestARefusedAbsenceMemoryIsItsOwnLineOnTheWorkList(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: "qg-memory"})
	memoryRefused(ctx, tracker, 70000)
	// Through the aggregate, the only way a replica's list reaches the page.
	snapshots := healthySnapshots()
	snapshots[0].NoDataMemory = tracker.NoDataMemory()
	aggregated := Aggregate(Expectation{QueryGroups: 949, Known: true}, snapshots, replicas(), now, freshness)
	view := &aggregated
	if len(view.NoDataMemory) != 1 {
		t.Fatalf("aggregated no-data memory rows = %+v, want the replica's one", view.NoDataMemory)
	}
	if finding := view.NoDataMemory[0].Finding; finding.Check != CheckNoDataMemoryRefused || finding.Owner != OwnerAlarmd ||
		finding.Group != "STATE_BUDGET_EXCEEDED" || finding.Result != ResultCompleted {
		t.Fatalf("finding = %+v, want this deployment's line, folded on the store's reason, the round completed", finding)
	}
	reports := ReportChecks(nil, nil, view, now)
	if len(reports) != 1 || reports[0].Code != CheckNoDataMemoryRefused || reports[0].Current != 1 || reports[0].Objects != 1 {
		t.Fatalf("reports = %+v, want the one line with the object current on it", reports)
	}
	todo := SummarizeTodo(reports, nil, view, now)
	if todo.Checks != 1 || todo.Objects != 1 {
		t.Fatalf("todo = %+v, want the line and its object counted as this deployment's", todo)
	}
	if listed := UnderCheck(CheckNoDataMemoryRefused, "", view, now); len(listed) != 1 || listed[0].QueryGroup != "qg-memory" {
		t.Fatalf("under the check = %+v, want the object", listed)
	}
}
