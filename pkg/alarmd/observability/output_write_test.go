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
	"testing"
)

// The sink's count travels through the context the caller gave it: reported,
// it is read back exactly, zero included; not reported, the reader gets nil
// and not a zero; reported into a context nobody prepared, it goes nowhere
// and breaks nothing.
func TestTheSinksCountTravelsThroughTheCallersContext(t *testing.T) {
	ctx, read := ContextWithOutputWriteReport(context.Background())
	if read() != nil {
		t.Fatal("a count nobody reported read as a count")
	}
	ReportOutputWrite(ctx, 0, 14, nil)
	facts := read()
	if facts == nil || facts.Published != 0 || facts.WithoutMessage != 14 {
		t.Fatalf("facts = %+v, want zero messages and fourteen events without one", facts)
	}
	// The sink reporting again -- a retry inside one call -- replaces, and the
	// copy a reader took stands.
	ReportOutputWrite(ctx, 3, 0, nil)
	if facts.Published != 0 || read().Published != 3 {
		t.Fatalf("earlier copy %+v, latest %+v", facts, read())
	}
	// A derived context still reaches the same report.
	child := ContextWithTraceFields(ctx, TraceFields{StrategyID: "1"})
	ReportOutputWrite(child, 5, 1, nil)
	if read().Published != 5 || read().WithoutMessage != 1 {
		t.Fatalf("report through a derived context = %+v", read())
	}
	ReportOutputWrite(context.Background(), 9, 9, nil)
	ReportOutputWrite(nil, 9, 9, nil) //nolint:staticcheck // a nil context is the case under test
}

// On the line, both numbers are present with their zeros: a success that
// handed the broker nothing is told from a write by "messages_published": 0,
// and a line from a sink that did not count carries neither key.
func TestTheEventAckedLineSaysHowManyMessagesLeft(t *testing.T) {
	base := Observation{Component: ComponentOutput, Stage: StageEventACKed, Result: ResultSuccess,
		Counts: Counts{Events: 14}, Trace: TraceFields{QueryGroupKey: "qg-1"}}
	counted := base
	counted.OutputWrite = &OutputWriteFacts{Published: 0, WithoutMessage: 14}
	event := renderObservation(t, counted)
	if event["messages_published"] != 0.0 || event["events_without_message"] != 14.0 {
		t.Fatalf("line = %v, want messages_published 0 and events_without_message 14", event)
	}
	if event["events"] != 14.0 {
		t.Fatalf("line lost the event count: %v", event)
	}
	uncounted := renderObservation(t, base)
	if _, present := uncounted["messages_published"]; present {
		t.Fatalf("a sink that did not count wrote a count: %v", uncounted)
	}
	if _, present := uncounted["events_without_message"]; present {
		t.Fatalf("a sink that did not count wrote a count: %v", uncounted)
	}
}

// The sweep's five numbers are on its line, zeros included: the line existed
// for a release with only its stage and result, and "swept" with nothing
// beside it could not be told from "swept nothing".
func TestTheSweepLineCarriesItsNumbers(t *testing.T) {
	// Five different numbers, so each key is pinned to its own field; then a
	// sweep whose last three are zero, so the zeros are pinned as present.
	event := renderObservation(t, Observation{Component: ComponentOwnership, Stage: StageAssignmentSwept, Result: ResultSuccess,
		Operation: OperationWrite, AssignmentSweep: &AssignmentSweepFacts{Scanned: 2407, Retired: 6, Reclaimed: 3, HeldByLease: 2, Changed: 1}})
	if event["assignment_sweep_scanned"] != 2407.0 || event["assignment_sweep_retired"] != 6.0 || event["assignment_sweep_reclaimed"] != 3.0 ||
		event["assignment_sweep_held_by_lease"] != 2.0 || event["assignment_sweep_changed"] != 1.0 {
		t.Fatalf("sweep line = %v, want the five numbers each under its own key", event)
	}
	quiet := renderObservation(t, Observation{Component: ComponentOwnership, Stage: StageAssignmentSwept, Result: ResultSuccess,
		Operation: OperationWrite, AssignmentSweep: &AssignmentSweepFacts{Scanned: 2401}})
	if quiet["assignment_sweep_retired"] != 0.0 || quiet["assignment_sweep_reclaimed"] != 0.0 || quiet["assignment_sweep_held_by_lease"] != 0.0 || quiet["assignment_sweep_changed"] != 0.0 {
		t.Fatalf("sweep line that found nothing = %v, want its zeros present", quiet)
	}
	bare := renderObservation(t, Observation{Component: ComponentOwnership, Stage: StageAssignmentSwept, Result: ResultSuccess, Operation: OperationWrite})
	if _, present := bare["assignment_sweep_scanned"]; present {
		t.Fatalf("a sweep line without facts wrote numbers: %v", bare)
	}
}
