// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package throttle

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

const (
	testCPUEnter = 0.8
	testCPUExit  = 0.7
	testCPUHard  = 0.9
	testMemEnter = 0.85
	testMemExit  = 0.78
	testMemHard  = 0.92
	testBreachN  = 2
)

// cpuAtRatio / memAtRatio 把「过载比例 r」转成对应水位，方便 dropProbability 用例。
// r=0 → enter 线，r=1 → hard 线，r=0.5 → 中点。
func cpuAtRatio(r float64) float64 { return testCPUEnter + (testCPUHard-testCPUEnter)*r }

func memAtRatio(r float64) float64 { return testMemEnter + (testMemHard-testMemEnter)*r }

func TestManagerStateHysteresis(t *testing.T) {
	manager := newManager(testConfig())
	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.01, CPUFast: 0.5})
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))

	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.02, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))
	assert.Equal(t, ActionShed, manager.decide(define.RecordTraces, func() float64 { return 0 }))

	// 滞回带（exit < x < enter）保持 Shedding
	manager.Publish(WaterLevel{CPUSlow: testCPUExit + 0.05, CPUFast: 0.5})
	manager.Publish(WaterLevel{CPUSlow: testCPUExit + 0.05, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))

	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.01, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))
	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.02, CPUFast: 0.5})
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))
}

func TestManagerHardOpen(t *testing.T) {
	manager := newManager(testConfig())
	manager.Publish(WaterLevel{CPUSlow: 0.5, CPUFast: testCPUHard + 0.05})
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))

	manager.Publish(WaterLevel{CPUSlow: 0.5, CPUFast: testCPUHard + 0.06})
	assert.Equal(t, StateOpen, manager.State(define.RecordTraces))
	assert.Equal(t, ActionOpen, manager.Decide(define.RecordTraces))

	// 第 1 次 CPU 硬线回落计数未满 breach_n → 仍 Open，避免硬线边沿抖动。
	manager.Publish(WaterLevel{CPUSlow: testCPUExit + 0.05, CPUFast: 0.5})
	assert.Equal(t, StateOpen, manager.State(define.RecordTraces))

	// 第 2 次硬线回落 → 退出 Open；CPUSlow 仍在 cpu_exit 之上，按瞬时水位走 Shedding。
	manager.Publish(WaterLevel{CPUSlow: testCPUExit + 0.05, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))
}

func TestManagerMemOpenAndRuleDisabled(t *testing.T) {
	enabled := false
	config := testConfig()
	config.Rules = map[string]RuleConfig{
		define.RecordMetrics.S(): {Enabled: &enabled},
	}
	manager := newManager(config)

	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: 0.1, Mem: testMemHard + 0.05, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(define.RecordTraces))
	assert.Equal(t, StateNormal, manager.State(define.RecordMetrics))
	assert.Equal(t, ActionAdmit, manager.Decide(define.RecordMetrics))
}

func TestManagerMemSoftHysteresis(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces

	// 内存默认单次越 mem_enter → 进入 Shedding
	manager.Publish(WaterLevel{Mem: testMemEnter + 0.01, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))

	// 滞回带（exit < x < enter）保持 Shedding
	manager.Publish(WaterLevel{Mem: testMemExit + 0.04, MemValid: true})
	manager.Publish(WaterLevel{Mem: testMemExit + 0.04, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))

	// 内存默认单次跌破 mem_exit，CPU 也满足退出门控 → 回到 Normal
	manager.Publish(WaterLevel{Mem: testMemExit - 0.01, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerSoftEntryByEitherSignal(t *testing.T) {
	traces := define.RecordTraces

	// 仅 CPU 高（mem 不报）也能进 Shedding
	cpu := newManager(testConfig())
	cpu.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.01})
	cpu.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.02})
	assert.Equal(t, StateShedding, cpu.State(traces))

	// 仅 mem 高（CPU 静默）也能独立进 Shedding
	mem := newManager(testConfig())
	mem.Publish(WaterLevel{Mem: testMemEnter + 0.01, MemValid: true})
	assert.Equal(t, StateShedding, mem.State(traces))
}

func TestManagerMemSlotBreachNControlsSoftTransitions(t *testing.T) {
	config := testConfig()
	config.Thresholds.CPU.BreachN = 1
	config.Thresholds.Mem.BreachN = 2
	manager := newManager(config)
	traces := define.RecordTraces

	manager.Publish(WaterLevel{Mem: testMemEnter + 0.01, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))

	manager.Publish(WaterLevel{Mem: testMemEnter + 0.02, MemValid: true})
	require.Equal(t, StateShedding, manager.State(traces))

	manager.Publish(WaterLevel{Mem: testMemExit - 0.01, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))

	manager.Publish(WaterLevel{Mem: testMemExit - 0.02, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerExitRequiresBothCPUAndMem(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces

	// CPU 与 mem 同时越线 → Shedding
	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.01, Mem: testMemEnter + 0.01, MemValid: true})
	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.01, Mem: testMemEnter + 0.01, MemValid: true})
	require.Equal(t, StateShedding, manager.State(traces))

	// 仅 CPU 跌回 exit 线下，mem 仍在 mem_exit 线上 → 不能退出 Shedding
	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05, Mem: testMemEnter + 0.01, MemValid: true})
	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05, Mem: testMemEnter + 0.01, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))

	// mem 也跌回 exit 线下，默认 mem.breach_n=1 → Normal
	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05, Mem: testMemExit - 0.01, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerMemOpenExitUsesDefaultMemSlotBreachN(t *testing.T) {
	config := testConfig()
	disabled := false
	config.Thresholds.CPU.Enabled = &disabled
	manager := newManager(config)
	traces := define.RecordTraces

	// mem 单次越线即触发 Open
	manager.Publish(WaterLevel{Mem: testMemHard + 0.03, MemValid: true})
	require.Equal(t, StateOpen, manager.State(traces))

	// CPU disabled 不阻塞恢复；内存默认 breach_n=1，单次硬线清除即可退出 Open。
	manager.Publish(WaterLevel{Mem: testMemExit - 0.20, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerOpenExitUsesMemSlotBreachNForMemClear(t *testing.T) {
	config := testConfig()
	config.Thresholds.CPU.BreachN = 1
	config.Thresholds.Mem.BreachN = 2
	manager := newManager(config)
	traces := define.RecordTraces

	manager.Publish(WaterLevel{Mem: testMemHard + 0.03, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
	manager.Publish(WaterLevel{Mem: testMemHard + 0.03, MemValid: true})
	require.Equal(t, StateOpen, manager.State(traces))

	manager.Publish(WaterLevel{Mem: testMemExit - 0.20, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(traces))

	manager.Publish(WaterLevel{Mem: testMemExit - 0.20, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerOpenExitWaitsForAllEnabledSlots(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces

	// 第 1 帧：mem 单次越硬线触发 Open，CPU hard 只命中 1 次。
	manager.Publish(WaterLevel{CPUFast: testCPUHard + 0.03, Mem: testMemHard + 0.03, MemValid: true})
	require.Equal(t, StateOpen, manager.State(traces))

	// 第 2 帧：CPU hard 满 breach_n；mem 已清除但还要等 CPU 清除。
	manager.Publish(WaterLevel{CPUFast: testCPUHard + 0.03, Mem: testMemExit - 0.20, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(traces))

	manager.Publish(WaterLevel{CPUFast: 0.5, Mem: testMemExit - 0.20, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(traces))

	manager.Publish(WaterLevel{CPUFast: 0.5, Mem: testMemExit - 0.20, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerMemOpenExitWaitsForCPUHardClear(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces

	manager.Publish(WaterLevel{CPUFast: 0.1, Mem: testMemHard + 0.03, MemValid: true})
	require.Equal(t, StateOpen, manager.State(traces))

	manager.Publish(WaterLevel{CPUFast: 0.1, Mem: testMemExit - 0.20, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(traces))

	manager.Publish(WaterLevel{CPUFast: 0.1, Mem: testMemExit - 0.20, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerMemInvalidIgnoresSoftEntry(t *testing.T) {
	manager := newManager(testConfig())

	// MemValid=false 时即使 Mem 数值很高也不参与软进入与硬熔断，避免读不到 cgroup 配额时误丢。
	manager.Publish(WaterLevel{Mem: 0.99, MemValid: false})
	manager.Publish(WaterLevel{Mem: 0.99, MemValid: false})
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))
}

func TestManagerOpenExitToSheddingByMem(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces

	// mem 单次越硬线 → Open
	manager.Publish(WaterLevel{Mem: testMemHard + 0.03, MemValid: true})
	require.Equal(t, StateOpen, manager.State(traces))

	// 第 1 次回落时 CPU hard clear 还未满足 breach_n，继续 Open。
	manager.Publish(WaterLevel{Mem: testMemExit + 0.04, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(traces))

	// 第 2 次回落满足所有 enabled slot 的 hard clear；mem 仍高于 mem_exit → Shedding。
	manager.Publish(WaterLevel{Mem: testMemExit + 0.04, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))
}

func TestManagerDisabledSignalsDoNotParticipate(t *testing.T) {
	disabled := false
	config := testConfig()
	config.Thresholds.CPU.Enabled = &disabled
	config.Thresholds.Mem.Enabled = &disabled
	manager := newManager(config)
	traces := define.RecordTraces

	for i := 0; i < testBreachN+2; i++ {
		manager.Publish(WaterLevel{
			CPUSlow:  testCPUEnter + 0.20,
			CPUFast:  testCPUHard + 0.20,
			Mem:      testMemHard + 0.20,
			MemValid: true,
		})
	}
	assert.Equal(t, StateNormal, manager.State(traces))
	assert.Equal(t, ActionAdmit, manager.Decide(traces))
	assert.InDelta(t, 0.0, manager.dropProbability(traces), 0.001)
}

func TestManagerMemDisabledAllowsCPUOnlyDecision(t *testing.T) {
	disabled := false
	config := testConfig()
	config.Thresholds.Mem.Enabled = &disabled
	manager := newManager(config)
	traces := define.RecordTraces

	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: 0.1, Mem: testMemHard + 0.20, MemValid: true})
	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: 0.1, Mem: testMemHard + 0.20, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))

	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.01, CPUFast: 0.1, Mem: testMemHard + 0.20, MemValid: true})
	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.02, CPUFast: 0.1, Mem: testMemHard + 0.20, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))
}

func TestManagerMemDisabledUsesCPUOnlyDropProbability(t *testing.T) {
	disabled := false
	dropMin := 0.0
	dropMax := 1.0
	config := testConfig()
	config.Thresholds.Mem.Enabled = &disabled
	config.Rules = map[string]RuleConfig{
		"default": {DropMin: &dropMin, DropMax: &dropMax},
	}
	manager := newManager(config)
	traces := define.RecordTraces

	manager.Publish(WaterLevel{CPUSlow: cpuAtRatio(0.3), Mem: memAtRatio(1.0), MemValid: true})
	assert.InDelta(t, 0.3, manager.dropProbability(traces), 0.001)
}

func TestManagerMemDisabledDoesNotBlockCPUOpenExit(t *testing.T) {
	disabled := false
	config := testConfig()
	config.Thresholds.Mem.Enabled = &disabled
	manager := newManager(config)
	traces := define.RecordTraces

	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: testCPUHard + 0.05, Mem: testMemHard + 0.20, MemValid: true})
	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: testCPUHard + 0.06, Mem: testMemHard + 0.20, MemValid: true})
	require.Equal(t, StateOpen, manager.State(traces))

	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: 0.1, Mem: testMemHard + 0.20, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(traces))

	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: 0.1, Mem: testMemHard + 0.20, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerCPUDisabledAllowsMemOnlyDecision(t *testing.T) {
	disabled := false
	config := testConfig()
	config.Thresholds.CPU.Enabled = &disabled
	manager := newManager(config)
	traces := define.RecordTraces

	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.20, CPUFast: testCPUHard + 0.20})
	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.20, CPUFast: testCPUHard + 0.20})
	assert.Equal(t, StateNormal, manager.State(traces))

	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.20, CPUFast: testCPUHard + 0.20, Mem: testMemEnter + 0.01, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))
}

func TestManagerCPUDisabledUsesMemOnlyDropProbability(t *testing.T) {
	disabled := false
	dropMin := 0.0
	dropMax := 1.0
	config := testConfig()
	config.Thresholds.CPU.Enabled = &disabled
	config.Rules = map[string]RuleConfig{
		"default": {DropMin: &dropMin, DropMax: &dropMax},
	}
	manager := newManager(config)
	traces := define.RecordTraces

	manager.Publish(WaterLevel{CPUSlow: cpuAtRatio(1.0), CPUFast: testCPUHard + 0.20, Mem: memAtRatio(0.4), MemValid: true})
	assert.InDelta(t, 0.4, manager.dropProbability(traces), 0.001)
}

func TestManagerMemInvalidAllowsSoftExit(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces

	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.01})
	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.02})
	require.Equal(t, StateShedding, manager.State(traces))

	// CPU 跌回 exit 线下、内存读不到（MemValid=false 视为安全），仍能退出 Shedding。
	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05})
	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerMemInvalidDoesNotWaitForMemBreachNOnSoftExit(t *testing.T) {
	config := testConfig()
	config.Thresholds.Mem.BreachN = 3
	manager := newManager(config)
	traces := define.RecordTraces

	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.01})
	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.02})
	require.Equal(t, StateShedding, manager.State(traces))

	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05, MemValid: false})
	assert.Equal(t, StateShedding, manager.State(traces))

	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05, MemValid: false})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerMemInvalidDoesNotWaitForMemBreachNOnHardClear(t *testing.T) {
	config := testConfig()
	config.Thresholds.Mem.BreachN = 3
	manager := newManager(config)
	traces := define.RecordTraces

	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: testCPUHard + 0.05, MemValid: false})
	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: testCPUHard + 0.06, MemValid: false})
	require.Equal(t, StateOpen, manager.State(traces))

	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: 0.1, MemValid: false})
	assert.Equal(t, StateOpen, manager.State(traces))

	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: 0.1, MemValid: false})
	assert.Equal(t, StateNormal, manager.State(traces))
}

// 回归 1：Normal 期间 mem 长期低位让 memExitHits 累积，CPU 触发 enter 把状态机推到 Shedding 后，
// 必须先清掉旧的 exit 计数，再按 CPU breach_n 与内存 mem.breach_n 重新累计退出条件。
// 这条用来防 resetExitHits 没清 mem/cpu exit 计数的回归（曾经写错为清 enter 计数）。
func TestManagerEnterClearsExitHits(t *testing.T) {
	config := testConfig()
	manager := newManager(config)
	traces := define.RecordTraces
	slot := manager.states[traces].slots[signalMem]

	// Normal 期 mem 长期低位 → exitHits 累积超过默认 mem.breach_n。
	for i := 0; i < testBreachN+2; i++ {
		manager.Publish(WaterLevel{Mem: testMemExit - 0.05, MemValid: true})
	}
	require.Equal(t, StateNormal, manager.State(traces))
	require.GreaterOrEqual(t, slot.exitHits, config.Thresholds.Mem.BreachN)

	// CPU 单维触发 enter → Shedding
	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.01, Mem: testMemExit - 0.05, MemValid: true})
	manager.Publish(WaterLevel{CPUSlow: testCPUEnter + 0.02, Mem: testMemExit - 0.05, MemValid: true})
	require.Equal(t, StateShedding, manager.State(traces))
	// resetExitHits 必须清掉 mem 维残留计数，否则下一刻 CPU 一回落就会立即退 Normal。
	require.Equal(t, 0, slot.exitHits)

	// CPU 立刻跌回 exit 线下两帧：第 1 帧 CPU breach_n 未满，第 2 帧才正常退出。
	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05, Mem: testMemExit - 0.05, MemValid: true})
	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05, Mem: testMemExit - 0.05, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

// 回归 2：CPUFast 跨硬线、CPUSlow 同帧仍低于 cpu_exit（快慢分离）。
// 进 Open 时 cpuExitHits 当帧刚被 tickHits ++，必须由 resetExitHits 清掉，
// 否则 Open 退出后 cpuExitHits 带「虚高」计数，提前满足 exitMet。
func TestManagerOpenEntryClearsExitHits(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces
	slot := manager.states[traces].slots[signalCPU]

	// 先让 CPU 处于 cpu_exit 之下、cpu_fast 也低，预热 cpuExitHits 累积
	for i := 0; i < testBreachN+2; i++ {
		manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05, CPUFast: 0.1})
	}
	require.GreaterOrEqual(t, slot.exitHits, testBreachN)

	// 快慢分离：CPUFast 连续越 hard 触发 Open，CPUSlow 仍低于 exit
	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05, CPUFast: testCPUHard + 0.05})
	manager.Publish(WaterLevel{CPUSlow: testCPUExit - 0.05, CPUFast: testCPUHard + 0.05})
	require.Equal(t, StateOpen, manager.State(traces))
	// 进 Open 时 cpuExitHits 必须被清零，避免 Open 退出后带票退 Normal。
	assert.Equal(t, 0, slot.exitHits)
}

func TestManagerDropProbability(t *testing.T) {
	dropMin := 0.2
	dropMax := 0.8
	config := testConfig()
	config.Rules = map[string]RuleConfig{
		"default": {
			DropMin: &dropMin,
			DropMax: &dropMax,
		},
	}
	manager := newManager(config)

	// CPU 在 enter 与 hard 中点（r=0.5）→ p = drop_min + (drop_max-drop_min)*0.5
	manager.Publish(WaterLevel{CPUSlow: cpuAtRatio(0.5), CPUFast: 0.5})
	assert.InDelta(t, dropMin+(dropMax-dropMin)*0.5, manager.dropProbability(define.RecordTraces), 0.001)

	// CPU 顶到 cpu_hard（r=1）→ p = drop_max
	manager.Publish(WaterLevel{CPUSlow: cpuAtRatio(1.0), CPUFast: 0.5})
	assert.InDelta(t, dropMax, manager.dropProbability(define.RecordTraces), 0.001)
}

func TestManagerDropProbabilityWithMem(t *testing.T) {
	dropMin := 0.0
	dropMax := 1.0
	config := testConfig()
	config.Rules = map[string]RuleConfig{
		"default": {DropMin: &dropMin, DropMax: &dropMax},
	}
	manager := newManager(config)
	traces := define.RecordTraces

	// 单 CPU 高（r=0.5）：t_cpu=0.5、t_mem=0 → p=0.5
	manager.Publish(WaterLevel{CPUSlow: cpuAtRatio(0.5)})
	assert.InDelta(t, 0.5, manager.dropProbability(traces), 0.001)

	// 单 mem 高（r=0.5）：t_mem=0.5、t_cpu=0 → p=0.5
	manager.Publish(WaterLevel{Mem: memAtRatio(0.5), MemValid: true})
	assert.InDelta(t, 0.5, manager.dropProbability(traces), 0.001)

	// 双高时取 max：t_cpu=0.5、t_mem=0.7 → p=0.7（mem 主导）
	manager.Publish(WaterLevel{CPUSlow: cpuAtRatio(0.5), Mem: memAtRatio(0.7), MemValid: true})
	assert.InDelta(t, 0.7, manager.dropProbability(traces), 0.001)

	// MemValid=false 时 mem 不参与计算，即使 Mem 数值很高
	manager.Publish(WaterLevel{Mem: 0.99, MemValid: false})
	assert.InDelta(t, 0.0, manager.dropProbability(traces), 0.001)

	// MemValid=true 但 Mem < MemEnter，tMem 被 clamp 到 0
	manager.Publish(WaterLevel{Mem: testMemEnter - 0.05, MemValid: true})
	assert.InDelta(t, 0.0, manager.dropProbability(traces), 0.001)
}

func TestInitDisabledClearsGlobalState(t *testing.T) {
	Stop()
	defer Stop()
	assert.False(t, Enabled())

	assert.NoError(t, Init(testConfig()))
	assert.True(t, Enabled())
	assert.NotNil(t, globalManager.Load())
	assert.NotNil(t, globalSampler.Load())

	assert.NoError(t, Init(Config{Enabled: false}))
	assert.False(t, Enabled())
	assert.Nil(t, globalManager.Load())
	assert.Nil(t, globalSampler.Load())
}

func TestGlobalManagerDisabledAdmitsWithoutSampler(t *testing.T) {
	Stop()
	defer Stop()

	manager := GlobalManager()
	assert.Equal(t, ActionAdmit, manager.Decide(define.RecordTraces))
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))
	assert.Nil(t, globalManager.Load())
	assert.Nil(t, globalSampler.Load())
}

func testConfig() Config {
	return normalizeConfig(Config{
		Enabled: true,
		Thresholds: ThresholdConfig{
			CPU: ThresholdSlotConfig{
				Enter:   testCPUEnter,
				Exit:    testCPUExit,
				Hard:    testCPUHard,
				BreachN: testBreachN,
			},
			Mem: ThresholdSlotConfig{
				Enter:   testMemEnter,
				Exit:    testMemExit,
				Hard:    testMemHard,
				BreachN: defaultMemBreachN,
			},
		},
	})
}
