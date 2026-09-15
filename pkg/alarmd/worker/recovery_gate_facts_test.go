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
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The counts the evaluator keeps of held and passed RECOVERY envelopes reach
// the observer as one fact per cause, zero counts left out, and only for the
// Plan the observation is about.
func TestRecoveryGateFactsCarryEveryNonZeroCause(t *testing.T) {
	identity := execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "7"}
	due := execution.DuePlan{Identity: identity}
	evaluated := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{{
		Plan: identity,
		RecoveryGate: execution.RecoveryGateCounts{
			HeldLevelUnavailable: 2, HeldLevelRecovering: 1, SentPastLevelWithoutRecovery: 3,
		},
	}}}
	want := []observability.RecoveryGateFact{
		{Cause: observability.RecoveryGateLevelUnavailable, Records: 2},
		{Cause: observability.RecoveryGateLevelRecovering, Records: 1},
		{Cause: observability.RecoveryGateLevelWithoutRecovery, Records: 3},
	}
	if got := recoveryGateFacts(due, evaluated); !reflect.DeepEqual(got, want) {
		t.Fatalf("facts = %+v, want %+v", got, want)
	}

	evaluated.Plans[0].RecoveryGate = execution.RecoveryGateCounts{HeldLevelRecovering: 4}
	if got := recoveryGateFacts(due, evaluated); !reflect.DeepEqual(got, []observability.RecoveryGateFact{{Cause: observability.RecoveryGateLevelRecovering, Records: 4}}) {
		t.Fatalf("facts with one cause = %+v, want only that cause", got)
	}

	evaluated.Plans[0].RecoveryGate = execution.RecoveryGateCounts{}
	if got := recoveryGateFacts(due, evaluated); got != nil {
		t.Fatalf("facts with nothing gated = %+v, want none", got)
	}

	other := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", BusinessID: "2", StrategyID: "8"}}
	evaluated.Plans[0].RecoveryGate = execution.RecoveryGateCounts{HeldLevelUnavailable: 1}
	if got := recoveryGateFacts(other, evaluated); got != nil {
		t.Fatalf("facts for another Plan = %+v, want none", got)
	}
}
