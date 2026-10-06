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
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// collectArrivals returns a Source wired to record readiness arrivals, and the
// slice they land in.
func collectArrivals(t *testing.T, frozen FrozenPlan) (*Source, *[]observability.SlotReadinessFacts) {
	t.Helper()
	arrivals := &[]observability.SlotReadinessFacts{}
	observer := observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
		if o.Component != observability.ComponentAccess || o.Stage != observability.StageSlotReadinessArrival {
			return
		}
		if o.SlotReadiness != nil {
			*arrivals = append(*arrivals, *o.SlotReadiness)
		}
	})
	source, err := NewSource(staticFrozenPlan{plan: frozen}, &fakeProvider{}, &recordingQueryPermits{},
		Config{MinReadyDelay: 30 * time.Second, Observer: observer})
	if err != nil {
		t.Fatal(err)
	}
	source.wait = func(context.Context, time.Duration) error { return nil }
	return source, arrivals
}

// readinessAtThirtySeconds shapes the fixture so its one query becomes readable
// thirty seconds after the Slot's evaluation time.
func readinessAtThirtySeconds(t *testing.T) (execution.FrozenExecutionContractRef, FrozenPlan, time.Time) {
	t.Helper()
	contractRef, frozen := frozenExecution(t)
	evaluationTime := time.UnixMilli(int64(contractRef.Slot.EvaluationTime) * 1000)
	frozen.DuePlans[0].CompletionDeadlineUnixMilli = evaluationTime.Add(time.Minute).UnixMilli()
	frozen.DuePlans[0].ScheduleSpec.EvaluationIntervalSeconds = 60
	frozen.Requirements[0].Consumers[0].ConsumerDeadlineUnixMilli = frozen.DuePlans[0].CompletionDeadlineUnixMilli
	return bindFrozenDueDigest(t, contractRef, frozen), frozen, evaluationTime
}

// Arriving late is the half of the timing question nothing reports. A deferral
// says an execution came too early; nothing at all says it came long after the
// data was sitting there. A scheduler that stops arriving early by arriving
// late would show every deferral gone and read as a success, which is why this
// figure has to exist before anything is changed to reduce deferrals.
func TestArrivalRecordsHowLongTheDataHadBeenReadable(t *testing.T) {
	contractRef, frozen, evaluationTime := readinessAtThirtySeconds(t)
	source, arrivals := collectArrivals(t, frozen)
	// Readable at +30s; this execution turns up at +45s.
	source.now = func() time.Time { return evaluationTime.Add(45 * time.Second) }

	_, _ = source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
	}, &recordingConsumer{})

	if len(*arrivals) != 1 {
		t.Fatalf("arrivals=%d, want exactly one for one execution", len(*arrivals))
	}
	facts := (*arrivals)[0]
	if facts.Boundary != observability.ReadinessBoundaryUnified || !facts.Slack {
		t.Fatalf("facts=%+v, want a measured arrival against one readiness moment", facts)
	}
	if facts.SlackSeconds != 15 {
		t.Fatalf("slack=%v seconds, want the 15 the data spent readable before anything read it",
			facts.SlackSeconds)
	}
}

// An execution turned away for being early has not done the round yet; it comes
// back and arrives again. Recording the turn-away as an arrival would count one
// round of work twice, and would put the early arrival and the later real one
// in the same distribution, where they pull in opposite directions and cancel.
func TestATurnedAwayArrivalIsNotRecorded(t *testing.T) {
	contractRef, frozen, evaluationTime := readinessAtThirtySeconds(t)
	source, arrivals := collectArrivals(t, frozen)
	// Readable at +30s; this execution turns up at the evaluation time itself
	// and is deferred.
	source.now = func() time.Time { return evaluationTime }

	_, err := source.Execute(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationNormal, AttemptNo: 1,
	}, &recordingConsumer{})
	if _, deferred := ReadinessDeferredAt(err); !deferred {
		t.Fatalf("Execute(early) error=%v, want the readiness deferral this case is about", err)
	}
	if len(*arrivals) != 0 {
		t.Fatalf("arrivals=%+v, want none: nothing was read, so nothing arrived late", *arrivals)
	}
}

// Recovery and replay run deliberately behind their own clock. Folding them in
// would move this distribution for a reason that says nothing about how well
// normal scheduling is timed -- and the whole point of the figure is to judge
// exactly that.
func TestRecoveryArrivalsAreNotRecorded(t *testing.T) {
	contractRef, frozen, evaluationTime := readinessAtThirtySeconds(t)
	source, arrivals := collectArrivals(t, frozen)
	source.now = func() time.Time { return evaluationTime.Add(45 * time.Second) }
	prepared, err := prepare(contractRef, frozen, 30*time.Second, true)
	if err != nil {
		t.Fatal(err)
	}

	source.observeSlotReadiness(context.Background(), execution.QueryExecutionRequest{
		Contract: contractRef, Operation: execution.OperationReplay, AttemptNo: 1,
	}, prepared, evaluationTime.Add(45*time.Second))

	if len(*arrivals) != 0 {
		t.Fatalf("arrivals=%+v, want none for a recovery operation", *arrivals)
	}
}

// Queries that disagree about when their data is readable give the Slot no one
// readiness moment, so there is no one answer for how late it arrived. Stating
// an average over the disagreement would invent the boundary this case says
// does not exist -- but the executions still have to be counted, or the
// histogram is read as covering a population it only samples.
func TestAMixedBoundaryIsCountedWithoutInventingASlack(t *testing.T) {
	now := time.UnixMilli(1_800_000_000_000)
	unified := []PlannedQuery{
		{Requirements: []execution.DataRequirement{{RequirementID: "first"}}, ReadyAtUnixMilli: now.UnixMilli()},
		{Requirements: []execution.DataRequirement{{RequirementID: "second"}}, ReadyAtUnixMilli: now.UnixMilli()},
	}
	if at, kind := sharedReadinessBoundary(unified); kind != observability.ReadinessBoundaryUnified || !at.Equal(now) {
		t.Fatalf("sharedReadinessBoundary(agreeing)=%s/%s, want the common moment", at, kind)
	}
	mixed := []PlannedQuery{
		{Requirements: []execution.DataRequirement{{RequirementID: "first"}}, ReadyAtUnixMilli: now.UnixMilli()},
		{Requirements: []execution.DataRequirement{{RequirementID: "second"}}, ReadyAtUnixMilli: now.Add(time.Minute).UnixMilli()},
	}
	if _, kind := sharedReadinessBoundary(mixed); kind != observability.ReadinessBoundaryMixed {
		t.Fatalf("sharedReadinessBoundary(disagreeing)=%s, want mixed", kind)
	}
	if _, kind := sharedReadinessBoundary([]PlannedQuery{{}}); kind != observability.ReadinessBoundaryNone {
		t.Fatalf("sharedReadinessBoundary(no requirements)=%s, want none", kind)
	}

	// A mixed Slot is normalized to carry no slack, whatever was measured.
	facts := observability.NormalizeObservation(observability.Observation{
		Component: observability.ComponentAccess, Stage: observability.StageSlotReadinessArrival,
		SlotReadiness: &observability.SlotReadinessFacts{
			Boundary: observability.ReadinessBoundaryMixed, SlackSeconds: 12, Slack: true,
		},
	}).SlotReadiness
	if facts.Slack || facts.SlackSeconds != 0 {
		t.Fatalf("facts=%+v, want no slack asserted where there is no single boundary", facts)
	}
}
