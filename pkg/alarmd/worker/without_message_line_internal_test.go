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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// reportingSink reports a batch the way the production sink does: every event
// its resolved protocol has no message for is counted without one, by format
// and kind, from the same rule. refuseFirst refuses before reporting, as the
// sink does when the lease cannot carry the batch.
type reportingSink struct {
	refuseFirst error
	calls       int
	received    [][]contract.TriggerEventV1
}

func (sink *reportingSink) WriteBatch(ctx context.Context, events []contract.TriggerEventV1) error {
	sink.calls++
	sink.received = append(sink.received, events)
	if sink.refuseFirst != nil {
		return sink.refuseFirst
	}
	counts := map[[2]string]int64{}
	published := 0
	for index := range events {
		format := contract.OutputWireFormatOf(&events[index])
		if contract.EventHasMessage(format, events[index].EventKind) {
			published++
			continue
		}
		counts[[2]string{format, events[index].EventKind}]++
	}
	buckets := make([]observability.OutputWithoutMessage, 0, len(counts))
	for key, n := range counts {
		buckets = append(buckets, observability.OutputWithoutMessage{Format: key[0], EventKind: key[1], Events: n})
	}
	observability.ReportOutputWrite(ctx, published, len(events)-published, buckets)
	return nil
}

func ackedLine(t *testing.T, events []contract.TriggerEventV1, dropped []execution.EventWithoutMessage, sink *reportingSink) observability.Observation {
	t.Helper()
	var acked *observability.Observation
	coordinator := &SlotExecutionCoordinator{ports: Ports{Events: sink,
		Observer: observability.ObserverFunc(func(ctx context.Context, observation observability.Observation) {
			if observation.Stage == observability.StageEventACKed {
				// The emitter names the strategy on the context, and every
				// observer reads it from there.
				observation.Trace = observability.TraceFieldsFromContext(ctx)
				acked = &observation
			}
		}),
	}}
	plan := execution.PlanIdentity{StrategyID: "4101", BusinessID: "2"}
	_ = coordinator.writeEvents(context.Background(), execution.OperationNormal, plan, events, dropped)
	if acked == nil {
		t.Fatal("no event_acked line")
	}
	return *acked
}

// The line a Slot's write produces is the same, count for count, whether the
// Python-compatible recoveries travel to the sink as events - as before - or
// stay behind as identities and are added back at the write. What the line
// counts is what was decided and what the protocol did with it, and neither
// changed. A native RECOVERY in the same batch is an event either way.
func TestTheLineCountsTheSameWhetherTheRecoveriesTravelOrStayBehind(t *testing.T) {
	legacy := &contract.LegacyEventContext{Configuration: &contract.FrozenLegacyOutput{}}
	python := func(kind, record string) contract.TriggerEventV1 {
		return contract.TriggerEventV1{WireFormat: contract.WireFormatPythonCompatible, EventKind: kind, LegacyOutput: legacy,
			RecordRef: contract.TriggerRecordRefV1{RecordID: record}, PlanRef: contract.RuntimePlanRefV1{StrategyID: "4101"}, BusinessID: "2"}
	}
	native := contract.TriggerEventV1{WireFormat: contract.WireFormatStandardRawEvent, EventKind: contract.TriggerEventRecovery,
		RecordRef: contract.TriggerRecordRefV1{RecordID: "n"}, PlanRef: contract.RuntimePlanRefV1{StrategyID: "4101"}, BusinessID: "2"}

	for _, testCase := range []struct {
		name   string
		before []contract.TriggerEventV1
	}{
		{"mixed batch", []contract.TriggerEventV1{python(contract.TriggerEventAbnormal, "a"),
			python(contract.TriggerEventRecovery, "b"), python(contract.TriggerEventRecovery, "c"), native}},
		{"recoveries only", []contract.TriggerEventV1{python(contract.TriggerEventRecovery, "b"), python(contract.TriggerEventRecovery, "c")}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var kept []contract.TriggerEventV1
			var dropped []execution.EventWithoutMessage
			for index := range testCase.before {
				if contract.DroppedAtSink(&testCase.before[index]) {
					event := testCase.before[index]
					dropped = append(dropped, execution.EventWithoutMessage{
						Record:    execution.RecordAnchor{RecordID: event.RecordRef.RecordID},
						EventKind: event.EventKind, Format: contract.OutputWireFormatOf(&event)})
					continue
				}
				kept = append(kept, testCase.before[index])
			}
			if len(dropped) == 0 {
				t.Fatal("setup: nothing stays behind, so this case compares nothing")
			}
			beforeSink, afterSink := &reportingSink{}, &reportingSink{}
			before := ackedLine(t, testCase.before, nil, beforeSink)
			after := ackedLine(t, kept, dropped, afterSink)

			if before.Counts.Events != after.Counts.Events {
				t.Errorf("events on the line %d before, %d after", before.Counts.Events, after.Counts.Events)
			}
			if !reflect.DeepEqual(before.OutputWireFormats, after.OutputWireFormats) {
				t.Errorf("by format %v before, %v after", before.OutputWireFormats, after.OutputWireFormats)
			}
			if !reflect.DeepEqual(before.OutputEventKinds, after.OutputEventKinds) {
				t.Errorf("by kind %v before, %v after", before.OutputEventKinds, after.OutputEventKinds)
			}
			if !reflect.DeepEqual(before.OutputWrite, after.OutputWrite) {
				t.Errorf("sink report %+v before, %+v after", before.OutputWrite, after.OutputWrite)
			}
			if before.Trace.StrategyID != after.Trace.StrategyID || after.Trace.StrategyID != "4101" {
				t.Errorf("strategy on the line %q before, %q after", before.Trace.StrategyID, after.Trace.StrategyID)
			}
			// The sink is asked either way, even with nothing left to send:
			// its admission against the lease stays where it was.
			if afterSink.calls != 1 {
				t.Errorf("sink asked %d times, want once whatever is left", afterSink.calls)
			}
			for _, event := range afterSink.received[0] {
				if contract.DroppedAtSink(&event) {
					t.Errorf("the sink still received %s under %s", event.EventKind, event.WireFormat)
				}
			}
		})
	}
}

// Only the series whose mutation the store accepted send - and count - what
// they decided: a refused mutation's event and its kept identities both stay
// behind.
func TestOnlyAcceptedSeriesSendOrCountTheirOutputs(t *testing.T) {
	accepted := execution.StateMutation{Identity: execution.StateKeyIdentity{SeriesIdentityDigest: "accepted"}}
	refused := execution.StateKeyIdentity{SeriesIdentityDigest: "refused"}
	events := map[execution.StateKeyIdentity][]contract.TriggerEventV1{
		accepted.Identity: {{EventKind: contract.TriggerEventAbnormal}},
		refused:           {{EventKind: contract.TriggerEventAbnormal}},
	}
	dropped := map[execution.StateKeyIdentity][]execution.EventWithoutMessage{
		accepted.Identity: {{EventKind: contract.TriggerEventRecovery, Format: contract.WireFormatPythonCompatible}},
		refused:           {{EventKind: contract.TriggerEventRecovery, Format: contract.WireFormatPythonCompatible}},
	}
	sent, kept := outputsOf([]execution.StateMutation{accepted}, events, dropped)
	if len(sent) != 1 || len(kept) != 1 {
		t.Fatalf("sent %d events and %d identities, want the accepted series' one of each", len(sent), len(kept))
	}
}

// A sink that refuses before counting reported nothing about the batch, and
// the line says so the same way whether the recoveries travelled or not.
func TestARefusalBeforeTheSinkCountsReportsNothingEitherWay(t *testing.T) {
	legacy := &contract.LegacyEventContext{Configuration: &contract.FrozenLegacyOutput{}}
	recovery := contract.TriggerEventV1{WireFormat: contract.WireFormatPythonCompatible, EventKind: contract.TriggerEventRecovery,
		LegacyOutput: legacy, PlanRef: contract.RuntimePlanRefV1{StrategyID: "4101"}}
	refusal := errors.New("lease cannot carry the batch")
	before := ackedLine(t, []contract.TriggerEventV1{recovery}, nil, &reportingSink{refuseFirst: refusal})
	after := ackedLine(t, nil, []execution.EventWithoutMessage{{EventKind: contract.TriggerEventRecovery,
		Format: contract.WireFormatPythonCompatible}}, &reportingSink{refuseFirst: refusal})
	if before.OutputWrite != nil || after.OutputWrite != nil {
		t.Fatalf("sink report %+v before, %+v after, want none: the sink counted nothing", before.OutputWrite, after.OutputWrite)
	}
	if before.Err == nil || after.Err == nil || before.Counts.Events != after.Counts.Events {
		t.Fatalf("refusal %v/%v, events %d/%d: the refusal and the count stay as they were", before.Err, after.Err,
			before.Counts.Events, after.Counts.Events)
	}
}
