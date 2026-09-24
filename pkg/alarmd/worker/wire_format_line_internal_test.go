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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// countingSink takes or refuses every batch, as told.
type countingSink struct{ err error }

func (sink countingSink) WriteBatch(context.Context, []contract.TriggerEventV1) error {
	return sink.err
}

// The event_acked line says how many of the batch went out as each wire
// format, from the word each event carries, whether or not the sink took
// the batch: a batch the broker refused still was what it was. An event
// with no word is counted under the fold's name, never under an empty key.
func TestTheACKLineCountsTheBatchByWireFormat(t *testing.T) {
	for _, testCase := range []struct {
		name string
		err  error
	}{
		{name: "taken", err: nil},
		{name: "refused", err: errors.New("broker refused")},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorded := []observability.Observation{}
			coordinator := &SlotExecutionCoordinator{ports: Ports{
				Events: countingSink{err: testCase.err},
				Observer: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
					recorded = append(recorded, observation)
				}),
			}}
			events := []contract.TriggerEventV1{
				{WireFormat: contract.WireFormatPythonCompatible, EventKind: contract.TriggerEventAbnormal, PlanRef: contract.RuntimePlanRefV1{StrategyID: "4101"}},
				{WireFormat: contract.WireFormatPythonCompatible, EventKind: contract.TriggerEventRecovery, PlanRef: contract.RuntimePlanRefV1{StrategyID: "4101"}},
				{WireFormat: contract.WireFormatStandardRawEvent, EventKind: contract.TriggerEventRecovery, PlanRef: contract.RuntimePlanRefV1{StrategyID: "4102"}},
				{EventKind: contract.TriggerEventAbnormal, PlanRef: contract.RuntimePlanRefV1{StrategyID: "4103"}},
			}
			err := coordinator.writeEvents(context.Background(), execution.OperationNormal, execution.PlanIdentity{}, events, nil)
			if (err != nil) != (testCase.err != nil) {
				t.Fatalf("writeEvents() error = %v, want an error iff the sink refused", err)
			}
			var acked *observability.Observation
			for index := range recorded {
				if recorded[index].Stage == observability.StageEventACKed {
					acked = &recorded[index]
				}
			}
			if acked == nil {
				t.Fatalf("no event_acked observation among %+v", recorded)
			}
			want := observability.OutputWireFormatCounts{
				contract.WireFormatPythonCompatible: 2, contract.WireFormatStandardRawEvent: 1, observability.WireFormatOther: 1,
			}
			if len(acked.OutputWireFormats) != len(want) {
				t.Fatalf("counts = %v, want %v", acked.OutputWireFormats, want)
			}
			for format, count := range want {
				if acked.OutputWireFormats[format] != count {
					t.Fatalf("counts = %v, want %v", acked.OutputWireFormats, want)
				}
			}
			// The same events once more by kind: the python recovery and
			// the standard recovery are told apart from the anomalies, and
			// the kinds under a format sum to the format's count.
			wantKinds := observability.OutputEventKindCounts{
				{Format: contract.WireFormatPythonCompatible, EventKind: contract.TriggerEventAbnormal}: 1,
				{Format: contract.WireFormatPythonCompatible, EventKind: contract.TriggerEventRecovery}: 1,
				{Format: contract.WireFormatStandardRawEvent, EventKind: contract.TriggerEventRecovery}: 1,
				{Format: observability.WireFormatOther, EventKind: contract.TriggerEventAbnormal}:       1,
			}
			if len(acked.OutputEventKinds) != len(wantKinds) {
				t.Fatalf("kinds = %v, want %v", acked.OutputEventKinds, wantKinds)
			}
			for key, count := range wantKinds {
				if acked.OutputEventKinds[key] != count {
					t.Fatalf("kinds = %v, want %v", acked.OutputEventKinds, wantKinds)
				}
			}
			byFormat := map[string]int64{}
			for key, count := range acked.OutputEventKinds {
				byFormat[key.Format] += count
			}
			for format, count := range acked.OutputWireFormats {
				if byFormat[format] != count {
					t.Fatalf("kinds under %s sum to %d, want the format's %d", format, byFormat[format], count)
				}
			}
		})
	}
}

// The evaluation line names the format the Plan's events go out as, resolved
// the way the sink resolves it: a Plan with no revision and no word is
// Python-compatible, and one with a revision and no word is the standard raw
// event. Two Plans that resolve differently, on both evaluation lines -- the
// per-series one and the completion-only one -- so a line that printed a
// constant, or the frozen word as written, would show.
func TestTheEvaluationLineNamesTheResolvedWireFormat(t *testing.T) {
	if got := planWireFormat(execution.DuePlan{}); got != "" {
		t.Fatalf("a due Plan without a compiled Plan names %q, want nothing", got)
	}
	unrevisioned := noDataWiredPlan(t)
	revisioned := unrevisioned
	revisioned.Identity.StrategyID = "4102"
	revisioned.CompiledPlan = noDataPreflightPlanWithRef(t,
		contract.StrategyRefV2{TenantID: "tenant", StrategyID: "4102", Revision: "strategy-v1", SnapshotRevision: 7}, nil)
	for _, testCase := range []struct {
		name string
		due  execution.DuePlan
		want string
	}{
		{name: "no revision, no word", due: unrevisioned, want: contract.WireFormatPythonCompatible},
		{name: "revision, no word", due: revisioned, want: contract.WireFormatStandardRawEvent},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := planWireFormat(testCase.due); got != testCase.want {
				t.Fatalf("planWireFormat = %q, want %q", got, testCase.want)
			}
			recorded := []observability.Observation{}
			stream := noDataWiredStream(t, testCase.due, &emptyNoDataStore{})
			stream.coordinator.ports.Observer = observability.ObserverFunc(
				func(_ context.Context, observation observability.Observation) {
					recorded = append(recorded, observation)
				})
			stream.observeCompletionOnlyPlan(context.Background(), testCase.due, execution.EvaluationResult{Result: observability.ResultSuccess})
			stream.observeEvaluationCompleted(context.Background(), time.Now(), testCase.due, "series-1", nil,
				execution.EvaluationResult{Result: observability.ResultSuccess})
			if len(recorded) != 2 {
				t.Fatalf("recorded %d observations, want the two evaluation lines", len(recorded))
			}
			for _, observation := range recorded {
				if observation.Stage != observability.StageEvaluationCompleted || observation.OutputWireFormat != testCase.want ||
					observation.Trace.StrategyID != testCase.due.Identity.StrategyID {
					t.Fatalf("evaluation line = %+v, want wire format %q beside strategy %s", observation, testCase.want, testCase.due.Identity.StrategyID)
				}
			}
		})
	}
}

// Every evaluation line of a Plan says how many series the Slot bound to it,
// and the completion-only line of a Plan bound to none says zero: the number
// that separates a guard warming from a guard with nothing to warm on. Two
// Plans with different counts, so a constant would show.
func TestTheEvaluationLineSaysHowManySeriesThePlanWasBoundTo(t *testing.T) {
	plan := noDataWiredPlan(t)
	other := plan
	other.Identity.StrategyID = "4102"
	other.CompiledPlan = noDataPreflightPlan(t, "4102", nil)
	recorded := []observability.Observation{}
	stream := noDataWiredStream(t, plan, &emptyNoDataStore{})
	stream.coordinator.ports.Observer = observability.ObserverFunc(
		func(_ context.Context, observation observability.Observation) {
			recorded = append(recorded, observation)
		})
	// The first Plan was bound two series this Slot; the other none.
	stream.planSeries = map[execution.PlanIdentity]map[execution.SeriesIdentityDigest]struct{}{
		plan.Identity: {"series-1": {}, "series-2": {}},
	}
	stream.observeEvaluationCompleted(context.Background(), time.Now(), plan, "series-1", nil,
		execution.EvaluationResult{Result: observability.ResultSuccess})
	stream.observeCompletionOnlyPlan(context.Background(), other, execution.EvaluationResult{Result: observability.ResultSuccess})
	if len(recorded) != 2 {
		t.Fatalf("recorded %d observations, want two evaluation lines", len(recorded))
	}
	if got := recorded[0].PlanSeriesMatched; got == nil || *got != 2 || recorded[0].Trace.StrategyID != plan.Identity.StrategyID {
		t.Fatalf("the bound Plan's line says %v series, want 2", got)
	}
	if got := recorded[1].PlanSeriesMatched; got == nil || *got != 0 || recorded[1].Trace.StrategyID != "4102" {
		t.Fatalf("the unbound Plan's completion-only line says %v series, want 0 and not nil: zero is the reading", got)
	}
}
