// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A provider lead widens what the provider is asked for, never what is
// accepted, and only as a whole number of steps on a primary requirement.
func TestAProviderLeadWidensOnlyTheRangeAskedInWholeSteps(t *testing.T) {
	plans, requirements := baseDuePlanAndRequirements()
	byPlan := map[execution.PlanIdentity]execution.DuePlan{}
	for _, plan := range plans {
		byPlan[plan.Identity] = plan
	}
	requirement := requirements[0]
	if err := requirement.Validate(byPlan); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	led := requirement
	led.ProviderLeadSeconds = 4 * led.StepMillis / 1000
	if err := led.Validate(byPlan); err != nil {
		t.Fatalf("a whole-step lead on a primary requirement refused: %v", err)
	}
	const at = execution.EvaluationTime(1_788_000_000)
	window, asked := led.AbsoluteWindow(at), led.ProviderWindow(at)
	if asked.End != window.End || window.Start-asked.Start != led.ProviderLeadSeconds {
		t.Fatalf("asked %+v for window %+v, want the window begun %d s earlier", asked, window, led.ProviderLeadSeconds)
	}
	if requirement.ProviderWindow(at) != requirement.AbsoluteWindow(at) {
		t.Fatal("a requirement without a lead asks for more than its window")
	}
	for name, mutate := range map[string]func(*execution.DataRequirement){
		"negative":     func(r *execution.DataRequirement) { r.ProviderLeadSeconds = -r.StepMillis / 1000 },
		"part of step": func(r *execution.DataRequirement) { r.ProviderLeadSeconds = r.StepMillis/1000 + 1 },
		"not a primary": func(r *execution.DataRequirement) {
			r.Role, r.ProviderLeadSeconds = execution.InputRoleAlgorithmDependency, r.StepMillis/1000
		},
	} {
		bad := requirement
		mutate(&bad)
		if err := bad.Validate(byPlan); err == nil {
			t.Errorf("%s lead accepted", name)
		}
	}
}
