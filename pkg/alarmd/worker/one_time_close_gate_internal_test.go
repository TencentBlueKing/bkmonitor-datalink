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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The set the one close of a dropped group is asked against lets the close
// through and says what it is, so the gate names the pass apart from one the
// consumer's copy vouched for; and the Plan's count of such passes reaches
// the observation beside the others.
func TestTheOneTimeCloseIsLetThroughAndCountedApart(t *testing.T) {
	var set contract.OpenAlertSet = oneTimeClose{}
	closer, marked := set.(contract.OneTimeCloseSet)
	if !set.Contains("tenant", "7", "fingerprint") || !marked || !closer.OneTimeClose() {
		t.Fatalf("oneTimeClose: contains %t, marked %t; want the close let through and marked as one-time",
			set.Contains("tenant", "7", "fingerprint"), marked)
	}
	due := execution.DuePlan{Identity: execution.PlanIdentity{TenantID: "tenant", StrategyID: "7"}}
	evaluated := execution.EvaluationResult{Plans: []execution.PlanEvaluationResult{{Plan: due.Identity,
		OpenAlertGate: execution.OpenAlertGateCounts{Passed: 1, PassedOneTimeClose: 2}}}}
	want := []observability.OpenAlertGateFact{
		{Outcome: observability.OpenAlertGatePassed, Records: 1},
		{Outcome: observability.OpenAlertGatePassedOneTimeClose, Records: 2},
	}
	if got := openAlertGateFacts(due, evaluated); !reflect.DeepEqual(got, want) {
		t.Fatalf("gate facts = %+v, want %+v", got, want)
	}
}
