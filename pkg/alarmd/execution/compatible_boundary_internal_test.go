// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The result contract asks the Plan the trigger asked whether a record lies
// between two aggregation boundaries. An anomaly the trigger left unbuilt
// there is the compatibility protocol having no message for it; the same
// anomaly on a boundary left unbuilt is an event the consumer never receives.
func TestTheResultContractAsksThePlanWhetherADroppedAnomalyIsOffItsBoundary(t *testing.T) {
	plan := compiledPlanForContractTest(t, func(p *contract.EvaluationPlanV2) {
		p.StrategyIR.ExecutionSemantics.AggregationInterval = 240
	})
	identity := PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	input := InternalExecution{DuePlans: []DuePlan{{Identity: identity, CompiledPlan: plan}}}
	dropped := func(source int64) PlanEvaluationResult {
		return PlanEvaluationResult{Plan: identity, StateResults: []StateEvaluation{{WithoutMessage: []EventWithoutMessage{{
			Record: RecordAnchor{RecordID: "r", SourceTime: source}, EventKind: contract.TriggerEventAbnormal, Format: contract.WireFormatPythonCompatible,
		}}}}}
	}
	const refused = "its protocol has a message for was not kept"
	if err := validateEventOutcomes(input, dropped(480), map[levelOutcomeIdentity]LevelOutcome{}); err == nil || !strings.Contains(err.Error(), refused) {
		t.Fatalf("an anomaly on the boundary left unbuilt: %v, want refused as one the protocol has a message for", err)
	}
	if err := validateEventOutcomes(input, dropped(420), map[levelOutcomeIdentity]LevelOutcome{}); err != nil && strings.Contains(err.Error(), refused) {
		t.Fatalf("an anomaly between boundaries left unbuilt was refused as one the protocol has a message for: %v", err)
	}
}
