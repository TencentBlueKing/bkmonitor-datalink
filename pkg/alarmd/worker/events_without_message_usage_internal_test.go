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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The completion row counts the events the Slot kept only as identities
// beside the events it holds, across every Plan and series of the Slot, so
// that Events plus EventsWithoutMessage reads the same across the change that
// stopped holding Python-compatible recoveries.
func TestTheCompletionRowCountsTheEventsKeptAsIdentities(t *testing.T) {
	recovery := execution.EventWithoutMessage{EventKind: contract.TriggerEventRecovery, Format: contract.WireFormatPythonCompatible}
	stream := &streamedExecution{coordinator: &SlotExecutionCoordinator{}}
	stream.effects.events = 1
	stream.evaluated = execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{
		{StateResults: []execution.StateEvaluation{
			{Events: []contract.TriggerEventV1{{EventKind: contract.TriggerEventAbnormal}}},
			{WithoutMessage: []execution.EventWithoutMessage{recovery, recovery}},
		}},
		{StateResults: []execution.StateEvaluation{{WithoutMessage: []execution.EventWithoutMessage{recovery}}}},
	}}
	usage := stream.budgetUsage()
	if usage.EventsWithoutMessage != 3 || usage.Events != 1 {
		t.Fatalf("usage events=%d without message=%d, want 1 held and 3 kept as identities across both Plans",
			usage.Events, usage.EventsWithoutMessage)
	}
}
