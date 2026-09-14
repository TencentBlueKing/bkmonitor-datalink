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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// The worker drives the open alert copy at three points, and the order is
// the point: the copy is told the Plan before the evaluation so its strategy
// is in the next read; the evaluation request carries the copy so the
// trigger can ask it; and the copy is told the envelopes only after the sink
// ACKed them, never on a batch the sink refused. What the copy answers is
// the trigger's and the copy's own tests; this one is the wiring.
func TestWorkerDrivesTheOpenAlertCopyAroundTheEvaluation(t *testing.T) {
	t.Run("told the Plan before, told the envelopes after the ACK, outcomes reach the observer", func(t *testing.T) {
		header, batches, completion := workerG4MultiLevelStreamFixture(t, false)
		var completed []observability.Observation
		ports, evaluator, coordinator := workerG4CoordinatorWithObserver(t, observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observation = observability.NormalizeObservation(observation)
			if observation.Component == observability.ComponentEvaluation && observation.Stage == observability.StageEvaluationCompleted {
				completed = append(completed, observation)
			}
		}))
		evaluator.mutate = func(result *execution.EvaluationResult) {
			// The fixture's Plan is on the compatibility protocol, so the
			// real second gate answers legacy_protocol; the count is what
			// has to travel, whichever outcome it carries.
			result.Plans[0].OpenAlertGate.LegacyProtocol = 1
		}
		ports.executeOverride = streamExecution(header, batches, completion)

		result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
		if err != nil || !result.Completed {
			t.Fatalf("Execute() result=%+v error=%v", result, err)
		}
		if len(evaluator.requests) == 0 || evaluator.requests[0].OpenAlerts == nil {
			t.Fatal("the evaluation request did not carry the copy; the trigger would count not_configured")
		}
		if len(ports.trackedPlans) == 0 || ports.trackedPlans[0].StrategyID == "" {
			t.Fatalf("tracked plans = %+v, want the due Plan's identity", ports.trackedPlans)
		}
		if ports.eventCount == 0 {
			t.Fatal("the fixture sent no envelope; the ACK path was not exercised")
		}
		if len(ports.acknowledged) != ports.eventCount {
			t.Fatalf("acknowledged %d envelopes to the copy, the sink took %d", len(ports.acknowledged), ports.eventCount)
		}
		if ports.acknowledged[0].EventKind != contract.TriggerEventAbnormal {
			t.Fatalf("acknowledged kind = %s, want the fixture's ABNORMAL envelope", ports.acknowledged[0].EventKind)
		}
		want := []string{"track after state_load", "ack after event_ack"}
		if !reflect.DeepEqual(ports.openAlertCalls, want) {
			t.Fatalf("copy calls = %v, want %v", ports.openAlertCalls, want)
		}
		if len(completed) != 1 {
			t.Fatalf("evaluation observations=%d, want one", len(completed))
		}
		wantFacts := []observability.OpenAlertGateFact{{Outcome: observability.OpenAlertGateLegacyProtocol, Records: 1}}
		if !reflect.DeepEqual(completed[0].OpenAlertGates, wantFacts) {
			t.Fatalf("observed gate facts=%+v, want %+v", completed[0].OpenAlertGates, wantFacts)
		}
	})

	t.Run("a batch the sink refused is not told to the copy", func(t *testing.T) {
		header, batches, completion := workerG4MultiLevelStreamFixture(t, false)
		ports, _, coordinator := workerG4Coordinator(t)
		ports.failStage = "event_ack"
		ports.executeOverride = streamExecution(header, batches, completion)

		result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
		if err != nil || result.Completed || result.ReasonCode != contract.ReasonOutputACKUnknown {
			t.Fatalf("Execute() result=%+v error=%v, want the Slot left retrying on an unknown ACK", result, err)
		}
		if ports.eventCount == 0 {
			t.Fatal("the sink was never asked; the refusal path was not exercised")
		}
		if len(ports.acknowledged) != 0 {
			t.Fatalf("the copy was told %d envelopes the sink refused", len(ports.acknowledged))
		}
	})
}

// The constructor refuses a worker without the copy: on a production worker
// a missing copy would send every RECOVERY envelope and count it as
// not_configured, which is a wiring fault best refused at boot.
func TestCoordinatorRequiresTheOpenAlertCopy(t *testing.T) {
	ports, evaluator, _ := workerG4Coordinator(t)
	_, err := worker.NewSlotExecutionCoordinator(worker.Ports{
		Finalization: ports, Activation: ports, Query: ports, Sequencer: ports, Evaluator: evaluator,
		Admission: ports, GapGuard: ports, Events: ports, State: ports, Progress: ports,
		Observer: observability.ObserverFunc(func(context.Context, observability.Observation) {}),
	}, worker.ProvisionalBudget{MaxSeries: 100, MaxRetainedBytes: 1 << 20, MaxStateMutations: 100, MaxEvents: 100, MaxGapMutations: 10})
	if err == nil {
		t.Fatal("a coordinator without the open alert copy was constructed")
	}
}
