// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import "time"

// StepDuration 是父环节中的独占子步骤；执行与两类等待互不重叠。
type StepDuration struct {
	PhaseDuration
	Step string
}

// 固定词表控制指标基数；每个父环节的 other 收容未细分的编排和校验。
var phaseSteps = [...]struct {
	phase int
	name  string
}{
	{phaseOther, "other"}, {phaseOther, "event_reload"},
	{phaseEnrich, "other"}, {phaseEnrich, "chain"}, {phaseEnrich, "persist"},
	{phaseEnrich, "strategy"}, {phaseEnrich, "resource"}, {phaseEnrich, "display"},
	{phaseEnrich, "metric"}, {phaseEnrich, "source"}, {phaseEnrich, "log"},
	{phaseEnrich, "k8s"}, {phaseEnrich, "apm"}, {phaseEnrich, "test"}, {phaseEnrich, "custom"}, {phaseEnrich, "cmdb-access"}, {phaseEnrich, "cloud_resource"},
	{phasePolicySnapshot, "other"}, {phasePolicySnapshot, "read"},
	{phasePlan, "other"}, {phasePlan, "active_lookup"}, {phasePlan, "content"},
	{phasePlan, "suppression"}, {phasePlan, "admission"}, {phasePlan, "merge"}, {phasePlan, "pending_changes"},
	{phaseEventCommit, "other"}, {phaseEventCommit, "policy_save"},
	{phaseEventCommit, "plan_save"}, {phaseEventCommit, "result_save"}, {phaseEventCommit, "plan_clear"},
	{phaseApply, "other"}, {phaseApply, "alert_read"}, {phaseApply, "alert_create"},
	{phaseApply, "alert_cas"}, {phaseApply, "action"}, {phaseApply, "bindings"},
	{phaseApply, "cache"}, {phaseApply, "logs"},
	{phaseHook, "other"}, {phaseHook, "projection"}, {phaseHook, "action"}, {phaseHook, "custom"},
}

func stepIndex(phase int, name string) int {
	for i, step := range phaseSteps {
		if step.phase == phase && step.name == name {
			return i
		}
	}
	panic("unregistered lifecycle timing step")
}

func (t *phaseTiming) activeStep() int {
	if phaseSteps[t.step].phase == t.current {
		return t.step
	}
	return stepIndex(t.current, "other")
}

// measureStep 仅在指定父阶段内采样；复用方法在控制面调用时不产生 Event 样本。
func (p *Processor) measureStep(phase int, name string) func() {
	if p.phaseTiming == nil || p.phaseTiming.current != phase {
		return func() {}
	}
	t := p.phaseTiming
	parent := t.step
	t.change(t.current, time.Now())
	t.step = stepIndex(phase, name)
	return func() { t.change(t.current, time.Now()); t.step = parent }
}

func (p *Processor) measureEnrichProcessor(name string) func() {
	switch name {
	case "strategy", "resource", "display", "metric", "source", "log", "k8s", "apm", "test", "cmdb-access", "cloud_resource":
	default:
		name = "custom"
	}
	return p.measureStep(phaseEnrich, name)
}
