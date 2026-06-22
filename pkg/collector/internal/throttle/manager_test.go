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

func TestManagerStateHysteresis(t *testing.T) {
	manager := newManager(testConfig())
	manager.Publish(WaterLevel{CPUSlow: 0.81, CPUFast: 0.5})
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))

	manager.Publish(WaterLevel{CPUSlow: 0.82, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))
	assert.Equal(t, ActionShed, manager.decide(define.RecordTraces, func() float64 { return 0 }))

	manager.Publish(WaterLevel{CPUSlow: 0.75, CPUFast: 0.5})
	manager.Publish(WaterLevel{CPUSlow: 0.75, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))

	manager.Publish(WaterLevel{CPUSlow: 0.69, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))
	manager.Publish(WaterLevel{CPUSlow: 0.68, CPUFast: 0.5})
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))
}

func TestManagerHardOpen(t *testing.T) {
	manager := newManager(testConfig())
	manager.Publish(WaterLevel{CPUSlow: 0.5, CPUFast: 0.95})
	assert.Equal(t, StateNormal, manager.State(define.RecordTraces))

	manager.Publish(WaterLevel{CPUSlow: 0.5, CPUFast: 0.96})
	assert.Equal(t, StateOpen, manager.State(define.RecordTraces))
	assert.Equal(t, ActionOpen, manager.Decide(define.RecordTraces))

	// 第 1 次硬线回落计入 hardClearHits，但 breach_n=2 未满 → 仍 Open，避免硬线边沿抖动。
	manager.Publish(WaterLevel{CPUSlow: 0.75, CPUFast: 0.5})
	assert.Equal(t, StateOpen, manager.State(define.RecordTraces))

	// 第 2 次硬线回落 → 退出 Open；CPUSlow=0.75 > cpu_exit=0.7，按软线水位走 Shedding。
	manager.Publish(WaterLevel{CPUSlow: 0.75, CPUFast: 0.5})
	assert.Equal(t, StateShedding, manager.State(define.RecordTraces))
}

func TestManagerMemOpenAndRuleDisabled(t *testing.T) {
	enabled := false
	config := testConfig()
	config.Rules = map[string]RuleConfig{
		define.RecordMetrics.S(): {Enabled: &enabled},
	}
	manager := newManager(config)

	manager.Publish(WaterLevel{CPUSlow: 0.1, CPUFast: 0.1, Mem: 0.99, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(define.RecordTraces))
	assert.Equal(t, StateNormal, manager.State(define.RecordMetrics))
	assert.Equal(t, ActionAdmit, manager.Decide(define.RecordMetrics))
}

func TestManagerMemSoftHysteresis(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces

	// 第 1 次越 mem_enter（0.85），breach_n=2 未满 → 仍 Normal
	manager.Publish(WaterLevel{Mem: 0.86, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))

	// 第 2 次越 mem_enter → 进入 Shedding
	manager.Publish(WaterLevel{Mem: 0.87, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))

	// 滞回带内（mem_exit=0.78 < 0.82 < 0.85）保持 Shedding
	manager.Publish(WaterLevel{Mem: 0.82, MemValid: true})
	manager.Publish(WaterLevel{Mem: 0.82, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))

	// 第 1 次跌破 mem_exit，breach_n 未满 → 仍 Shedding
	manager.Publish(WaterLevel{Mem: 0.77, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))

	// 第 2 次跌破 mem_exit → 回到 Normal
	manager.Publish(WaterLevel{Mem: 0.76, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerSoftEntryByEitherSignal(t *testing.T) {
	traces := define.RecordTraces

	// 仅 CPU 高（mem 不报）也能进 Shedding
	cpu := newManager(testConfig())
	cpu.Publish(WaterLevel{CPUSlow: 0.81})
	cpu.Publish(WaterLevel{CPUSlow: 0.82})
	assert.Equal(t, StateShedding, cpu.State(traces))

	// 仅 mem 高（CPU 静默）也能独立进 Shedding
	mem := newManager(testConfig())
	mem.Publish(WaterLevel{Mem: 0.86, MemValid: true})
	mem.Publish(WaterLevel{Mem: 0.87, MemValid: true})
	assert.Equal(t, StateShedding, mem.State(traces))
}

func TestManagerExitRequiresBothCPUAndMem(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces

	// CPU 与 mem 同时越线 → Shedding
	manager.Publish(WaterLevel{CPUSlow: 0.81, Mem: 0.86, MemValid: true})
	manager.Publish(WaterLevel{CPUSlow: 0.81, Mem: 0.86, MemValid: true})
	require.Equal(t, StateShedding, manager.State(traces))

	// 仅 CPU 跌回 exit 线下，mem 仍在 mem_exit 线上 → 不能退出 Shedding
	manager.Publish(WaterLevel{CPUSlow: 0.65, Mem: 0.86, MemValid: true})
	manager.Publish(WaterLevel{CPUSlow: 0.65, Mem: 0.86, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))

	// mem 也跌回 exit 线下，连续 breach_n 次 → Normal
	manager.Publish(WaterLevel{CPUSlow: 0.65, Mem: 0.77, MemValid: true})
	manager.Publish(WaterLevel{CPUSlow: 0.65, Mem: 0.77, MemValid: true})
	assert.Equal(t, StateNormal, manager.State(traces))
}

func TestManagerOpenExitRequiresBreachN(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces

	// mem 单次越线即触发 Open
	manager.Publish(WaterLevel{Mem: 0.95, MemValid: true})
	require.Equal(t, StateOpen, manager.State(traces))

	// 第 1 次硬线回落不足以退出 Open
	manager.Publish(WaterLevel{Mem: 0.5, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(traces))

	// 第 2 次硬线回落 → 退出 Open；软线均低于 exit → Normal
	manager.Publish(WaterLevel{Mem: 0.5, MemValid: true})
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
	manager.Publish(WaterLevel{Mem: 0.95, MemValid: true})
	require.Equal(t, StateOpen, manager.State(traces))

	// 第 1 次硬线回落，breach_n 未满 → 仍 Open
	manager.Publish(WaterLevel{Mem: 0.82, MemValid: true})
	assert.Equal(t, StateOpen, manager.State(traces))

	// 第 2 次硬线回落 → 退出 Open；mem=0.82 仍 > mem_exit=0.78 → Shedding（按本帧瞬时水位决定）
	manager.Publish(WaterLevel{Mem: 0.82, MemValid: true})
	assert.Equal(t, StateShedding, manager.State(traces))
}

func TestManagerMemInvalidAllowsSoftExit(t *testing.T) {
	manager := newManager(testConfig())
	traces := define.RecordTraces

	manager.Publish(WaterLevel{CPUSlow: 0.81})
	manager.Publish(WaterLevel{CPUSlow: 0.82})
	require.Equal(t, StateShedding, manager.State(traces))

	// CPU 跌回 exit 线下、内存读不到（MemValid=false 视为安全），仍能退出 Shedding。
	manager.Publish(WaterLevel{CPUSlow: 0.65})
	manager.Publish(WaterLevel{CPUSlow: 0.65})
	assert.Equal(t, StateNormal, manager.State(traces))
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

	// CPU=0.85，t_cpu=(0.85-0.8)/(0.9-0.8)=0.5，mem 不报 → t=0.5，p=0.2+0.6*0.5=0.5
	manager.Publish(WaterLevel{CPUSlow: 0.85, CPUFast: 0.5})
	assert.InDelta(t, 0.5, manager.dropProbability(define.RecordTraces), 0.001)

	// CPU=0.95 已顶到 cpu_hard，t=1，p=drop_max
	manager.Publish(WaterLevel{CPUSlow: 0.95, CPUFast: 0.5})
	assert.InDelta(t, 0.8, manager.dropProbability(define.RecordTraces), 0.001)
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

	// 单 CPU 高（cpu_enter=0.8、cpu_hard=0.9）：t_cpu=0.5、t_mem=0 → p=0.5
	manager.Publish(WaterLevel{CPUSlow: 0.85})
	assert.InDelta(t, 0.5, manager.dropProbability(traces), 0.001)

	// 单 mem 高（mem_enter=0.85、mem_hard=0.92）：t_mem=0.5、t_cpu=0 → p=0.5
	manager.Publish(WaterLevel{Mem: 0.885, MemValid: true})
	assert.InDelta(t, 0.5, manager.dropProbability(traces), 0.001)

	// 双高时取 max：t_cpu=0.5、t_mem≈0.7 → p≈0.7（mem 主导）
	manager.Publish(WaterLevel{CPUSlow: 0.85, Mem: 0.899, MemValid: true})
	assert.InDelta(t, 0.7, manager.dropProbability(traces), 0.01)

	// MemValid=false 时 mem 不参与计算
	manager.Publish(WaterLevel{Mem: 0.99, MemValid: false})
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
			CPUEnter: 0.8,
			CPUExit:  0.7,
			CPUHard:  0.9,
			MemHard:  0.92,
			BreachN:  2,
		},
	})
}
