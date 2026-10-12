// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// supplementComponents is the component that reports each stage of a
// supplement: an observation under any other is not the stage at all.
var supplementComponents = map[Stage]Component{
	StageSlotStarted: ComponentScheduler, StageSlotCompleted: ComponentScheduler, StageQueryCompleted: ComponentAccess,
	StageEvaluationCompleted: ComponentEvaluation, StageGapLoaded: ComponentState, StageStatePreflight: ComponentState,
	StageStateAdmission: ComponentState, StageStateApplied: ComponentState, StageEventACKed: ComponentOutput,
}

// supplementRun is every observation a supplement of an earlier Slot
// reports, in order, under the given operation: its start, the gap and state
// it loaded, its evaluation, the state it admitted and applied, its events,
// a query that failed, and its completion, which completes no Slot -- with
// the supplemented Slot's older time and revisions, and work on each.
func supplementRun(queryGroup string, operation Operation) []Observation {
	trace := TraceFields{QueryGroupKey: queryGroup, EvaluationTime: 470, QueryRevision: "query-v0"}
	run := []Observation{}
	for _, stage := range []Stage{StageSlotStarted, StageGapLoaded, StageStatePreflight, StageEvaluationCompleted,
		StageStateAdmission, StageStateApplied, StageEventACKed, StageQueryCompleted, StageSlotCompleted} {
		observation := Observation{Component: supplementComponents[stage], Stage: stage, Operation: operation, Result: ResultSuccess,
			Duration: time.Second, Trace: trace, Counts: Counts{Records: 3, Events: 2, Keys: 4}}
		switch stage {
		case StageQueryCompleted:
			observation.Result = ResultFailed
			observation.QueryFailure = &QueryFailureFacts{Stage: "provider", Category: "provider_transport", Code: "QUERY_TIMEOUT"}
		case StageSlotCompleted:
			observation.ExecuteOutcome = "incomplete"
		}
		run = append(run, observation)
	}
	return run
}

// The cost summary reads rounds: supplements between them leave every row as
// it was. The same observations as a round's change the rows, which is what
// they did before.
func TestTheCostSummaryLeavesSupplementsOut(t *testing.T) {
	snapshot := func(extra []Observation) string {
		c, now, _ := costFixture()
		for _, stage := range []Stage{StageSlotStarted, StageEvaluationCompleted, StageSlotCompleted, StageProgressCommitted} {
			c.Observe(context.Background(), costObservation(stage))
		}
		for _, observation := range extra {
			c.Observe(context.Background(), observation)
		}
		c.Publish(*now)
		encoded, _ := json.Marshal(c.Snapshot())
		return string(encoded)
	}
	rounds := snapshot(nil)
	if withSupplements := snapshot(supplementRun("shared", OperationSupplement)); withSupplements != rounds {
		t.Fatalf("supplements changed the cost summary:\n%s\nwant\n%s", withSupplements, rounds)
	}
	if asRounds := snapshot(supplementRun("shared", OperationNormal)); asRounds == rounds {
		t.Fatal("the same observations as a round changed nothing: the fixture does not reach the rows")
	}
}

// The target flow records a selected object's runs: a supplement of it is
// not one, and writes nothing.
func TestTheTargetFlowRecordsNoSupplement(t *testing.T) {
	for operation, want := range map[Operation]bool{OperationSupplement: false, OperationNormal: true} {
		f, _ := newTestFlow(t)
		ctx := f.Context(context.Background(), flowQG)
		for _, observation := range supplementRun(flowQG, operation) {
			f.Observe(ctx, observation)
		}
		if recorded := f.records > 0; recorded != want {
			t.Errorf("operation %s wrote %d records, want records %v", operation, f.records, want)
		}
	}
}
