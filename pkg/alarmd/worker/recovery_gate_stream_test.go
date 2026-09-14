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
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A held record reaches the Worker as RECOVERY outcomes with no envelope. The
// Worker runs the result contract on it before anything is written; on the
// first build with the gate that check refused every held record, the Slot
// retried the same refusal until its budget ran out, the record's state was
// never written, and the hold never reached the held counter because the
// evaluation carrying it was thrown away. Through the real coordinator: a
// held record completes, its state is applied, and its hold reaches the
// observer; the same record without the hold stated on its outcomes is
// still refused, which is the released behaviour, pinned.
func TestHeldRecoveryRecordCompletesThroughTheWorkerAndReachesTheObserver(t *testing.T) {
	hold := func(result *execution.EvaluationResult, state bool) int {
		converted := 0
		for index := range result.Plans[0].LevelOutcomes {
			outcome := &result.Plans[0].LevelOutcomes[index]
			if outcome.Outcome != execution.LevelOutcomeNormal && outcome.Outcome != execution.LevelOutcomeAbnormal {
				continue
			}
			// Both a NORMAL and an ANOMALOUS Level fact admit a RECOVERY
			// outcome, so the record can be turned into the held shape without
			// touching its state facts; the envelope the evaluator built for
			// it is what the hold withholds.
			outcome.Outcome = execution.LevelOutcomeRecovery
			outcome.EnvelopeHeld = state
			converted++
		}
		for index := range result.Plans[0].StateResults {
			result.Plans[0].StateResults[index].Events = nil
		}
		result.Plans[0].RecoveryGate.HeldLevelUnavailable = 1
		return converted
	}

	t.Run("held on its outcomes: completes and is observed", func(t *testing.T) {
		header, batches, completion := workerG4MultiLevelStreamFixture(t, false)
		var completed []observability.Observation
		ports, evaluator, coordinator := workerG4CoordinatorWithObserver(t, observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
			observation = observability.NormalizeObservation(observation)
			if observation.Component == observability.ComponentEvaluation && observation.Stage == observability.StageEvaluationCompleted {
				completed = append(completed, observation)
			}
		}))
		converted := 0
		evaluator.mutate = func(result *execution.EvaluationResult) { converted = hold(result, true) }
		// No Plan gap guard: an active guard forbids RECOVERY before the
		// envelope rule is ever reached, and the held shape needs RECOVERY.
		ports.gapMissing = true
		ports.executeOverride = streamExecution(header, batches, completion)

		result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
		if err != nil || !result.Completed {
			t.Fatalf("Execute() result=%+v error=%v, want the held record to complete", result, err)
		}
		if converted == 0 {
			t.Fatal("fixture produced no NORMAL outcome to hold; the test did not exercise the held shape")
		}
		if ports.stateApplyCalls != 1 || ports.eventCount != 0 {
			t.Fatalf("state applies=%d events=%d, want the record's state written and no envelope sent", ports.stateApplyCalls, ports.eventCount)
		}
		if len(completed) != 1 {
			t.Fatalf("evaluation observations=%d, want one", len(completed))
		}
		want := []observability.RecoveryGateFact{{Cause: observability.RecoveryGateLevelUnavailable, Records: 1}}
		if !reflect.DeepEqual(completed[0].RecoveryGates, want) {
			t.Fatalf("observed gate facts=%+v, want %+v", completed[0].RecoveryGates, want)
		}
	})

	t.Run("held but not stated on its outcomes: still refused", func(t *testing.T) {
		header, batches, completion := workerG4MultiLevelStreamFixture(t, false)
		ports, evaluator, coordinator := workerG4Coordinator(t)
		evaluator.mutate = func(result *execution.EvaluationResult) { hold(result, false) }
		ports.gapMissing = true
		ports.executeOverride = streamExecution(header, batches, completion)

		result, err := coordinator.Execute(context.Background(), workerSlotRequest(header.Contract))
		if err == nil || result.Completed || !strings.Contains(err.Error(), "require exactly one TriggerEvent envelope") {
			t.Fatalf("Execute() result=%+v error=%v, want the contract's envelope refusal", result, err)
		}
		if ports.stateApplyCalls != 0 {
			t.Fatalf("state applies=%d, want none for a refused evaluation", ports.stateApplyCalls)
		}
	})
}
