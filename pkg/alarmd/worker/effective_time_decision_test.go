// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// The fixtures' Slot is evaluated at 10:40:00 UTC. A Level effective in a
// window is judged at the wall clock of the decision, as the platform's own
// detector judges it when it runs, not at the Slot's evaluation time: a Slot
// evaluated inside the window but decided after its end minute has passed
// would otherwise raise an alert the window's close retires at once.
func TestTheEffectiveTimeIsJudgedAtTheDecisionNotTheSlot(t *testing.T) {
	slot := time.Unix(1_788_000_000, 0).UTC()
	tests := []struct {
		name   string
		window string
		// clock is the wall clock on each read: the first when the Slot
		// begins, the last from then on.
		clock []time.Time
		fires bool
	}{
		{name: "decided inside the end minute", window: `{"start":"09:00","end":"10:40"}`,
			clock: []time.Time{slot.Add(5 * time.Second)}, fires: true},
		{name: "end minute passed before the Slot began", window: `{"start":"09:00","end":"10:40"}`,
			clock: []time.Time{slot.Add(61 * time.Second)}, fires: false},
		{name: "end minute passed between beginning and deciding", window: `{"start":"09:00","end":"10:40"}`,
			clock: []time.Time{slot.Add(50 * time.Second), slot.Add(65 * time.Second)}, fires: false},
		{name: "end minute over on the second after beginning", window: `{"start":"09:00","end":"10:40"}`,
			clock: []time.Time{slot.Add(59 * time.Second), slot.Add(60 * time.Second)}, fires: false},
		{name: "start minute reached between beginning and deciding", window: `{"start":"10:41","end":"12:00"}`,
			clock: []time.Time{slot.Add(50 * time.Second), slot.Add(65 * time.Second)}, fires: true},
		{name: "clock stepped back out of the window before deciding", window: `{"start":"10:41","end":"12:00"}`,
			clock: []time.Time{slot.Add(60 * time.Second), slot.Add(59 * time.Second)}, fires: false},
		{name: "Slot inside the window, decided before it starts", window: `{"start":"10:40","end":"12:00"}`,
			clock: []time.Time{slot.Add(-5 * time.Second)}, fires: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			header, batches := workerG4StreamFixture(t, strategy.DetectorKindSimpleRingRatio, windowed(test.window))
			dropped(t, batches)
			reads := 0
			now := func() time.Time {
				at := test.clock[min(reads, len(test.clock)-1)]
				reads++
				return at
			}
			ports, evaluator, coordinator := workerG4CoordinatorWith(t,
				observability.ObserverFunc(func(context.Context, observability.Observation) {}), nil,
				func(ports *worker.Ports) { ports.Now = now })
			ports.executeOverride = streamExecution(header, batches, fullCompletion(t, header, batches))

			result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
			if err != nil || !result.Completed {
				t.Fatalf("Execute() result=%+v error=%v evaluations=%+v", result, err, evaluator.results)
			}
			if len(evaluator.requests) != 1 {
				t.Fatalf("Evaluate requests=%d, want one", len(evaluator.requests))
			}
			decided := test.clock[len(test.clock)-1].Unix()
			if got := evaluator.requests[0].Header.DecisionTime; got != decided {
				t.Fatalf("DecisionTime=%d, want the decision's wall clock %d (Slot %d)", got, decided, slot.Unix())
			}
			fired := false
			for _, event := range ports.lastEvents {
				fired = fired || event.EventKind == contract.TriggerEventAbnormal
			}
			if fired != test.fires {
				t.Fatalf("abnormal event=%v, want %v: events=%+v", fired, test.fires, ports.lastEvents)
			}
		})
	}
}

// windowed makes the fixture's Level effective in one daily window of
// business-local (UTC) time.
func windowed(window string) func(*contract.EvaluationPlanV2) {
	return func(plan *contract.EvaluationPlanV2) {
		level := &plan.StrategyIR.Levels[0]
		level.TriggerPlan.Config = json.RawMessage(`{"window_size":1,"required_anomalies":1,"step_seconds":60,"timezone_ref":"BUSINESS_LOCAL","uptime":{"time_ranges":[` +
			window + `],"active_calendars":[],"calendars":[]}}`)
		plan.EffectiveTimeSnapshot = json.RawMessage(`{"schema_version":1,"status":"READY","business_timezone":"UTC","calendars":[]}`)
	}
}

// dropped makes the fixture's primary record 0.1 against its previous 1: a
// fall past the ring ratio's floor, so the Level is abnormal.
func dropped(t *testing.T, batches []execution.SeriesExecutionBatch) {
	t.Helper()
	for index, batch := range batches {
		if batch.Inputs[0].Role != execution.InputRolePrimary {
			continue
		}
		record := batch.Dataset.Records()[0]
		record.Values = map[string]json.RawMessage{"value": json.RawMessage(`0.1`)}
		dataset := execution.NewDataset([]contract.CanonicalRecordV2{record})
		view, err := execution.NewDatasetView(dataset, []uint32{0})
		if err != nil {
			t.Fatal(err)
		}
		batches[index].Dataset = dataset
		batches[index].Inputs[0].Dataset, batches[index].Inputs[0].View = dataset, view
	}
}
