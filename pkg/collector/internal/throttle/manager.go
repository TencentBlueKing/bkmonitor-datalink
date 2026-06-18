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
	"math/rand"
	"sync/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// Action 是单次请求的裁决结果。
type Action uint8

const (
	ActionAdmit Action = iota // 放行
	ActionShed                // 分级丢弃：按概率丢
	ActionOpen                // 熔断：全拒
)

func (a Action) S() string {
	switch a {
	case ActionShed:
		return "shed"
	case ActionOpen:
		return "open"
	default:
		return "admit"
	}
}

// State 是单类数据状态机的当前态，由背景回路推进，请求路径只读不转移。
type State uint32

const (
	StateNormal   State = iota // 正常，全放行
	StateShedding              // 分级丢弃，按 p_drop 概率丢
	StateOpen                  // 熔断，全拒
)

type WaterLevel struct {
	CPUSlow  float64
	CPUFast  float64
	Mem      float64
	MemValid bool
}

type Manager struct {
	enabled bool
	config  Config
	rules   map[define.RecordType]Rule
	states  map[define.RecordType]*stateSlot
	level   atomic.Pointer[WaterLevel]
}

type stateSlot struct {
	state      atomic.Uint32
	enterCount int // 慢信号连续越进入线的次数
	exitCount  int // 慢信号连续跌破退出线的次数
	hardCount  int // 快信号连续越熔断线的次数
}

var (
	globalManager atomic.Pointer[Manager]
	globalSampler atomic.Pointer[ResourceSampler]
)

func Init(config Config) error {
	config = normalizeConfig(config)
	if err := validateConfig(config); err != nil {
		return err
	}

	manager := newManager(config)
	var sampler *ResourceSampler
	if config.Enabled {
		sampler = NewResourceSampler(NewCgroupReader(), config, manager)
		// 冷启动启动时采样。
		sampler.tick()
		sampler.Start()
		logger.Infof("throttle enabled, sample_interval=%s", config.SampleInterval)
	}

	// 先让新单例就位（新 sampler 已绑新 manager 在发布），最后才停旧 sampler，确保平滑过度。
	oldSampler := globalSampler.Swap(sampler)
	globalManager.Store(manager)
	if oldSampler != nil {
		oldSampler.Stop()
	}
	return nil
}

func Stop() {
	oldSampler := globalSampler.Swap(nil)
	globalManager.Store(newDisabledManager())
	if oldSampler != nil {
		oldSampler.Stop()
	}
}

func GlobalManager() *Manager {
	manager := globalManager.Load()
	if manager == nil {
		return newDisabledManager()
	}
	return manager
}

func newDisabledManager() *Manager {
	config := normalizeConfig(Config{})
	return newManager(config)
}

func newManager(config Config) *Manager {
	m := &Manager{
		enabled: config.Enabled,
		config:  config,
		rules:   buildRules(config.Rules),
		states:  make(map[define.RecordType]*stateSlot, len(throttleRecordTypes)),
	}
	for _, rt := range throttleRecordTypes {
		m.states[rt] = &stateSlot{}
	}
	return m
}

// Publish 接收一帧水位。
func (m *Manager) Publish(level WaterLevel) {
	if m == nil || !m.enabled {
		return
	}
	current := level
	// 保存快照。
	m.level.Store(&current)
	// 记录指标。
	observeWaterLevel(current)
	// 推进状态机。
	m.updateStates(&current)
}

// Decide 是请求路径入口，按数据类型给裁决。
func (m *Manager) Decide(rt define.RecordType) Action {
	return m.decide(rt, rand.Float64)
}

func (m *Manager) decide(rt define.RecordType, random func() float64) Action {
	// 关闭、未注册或该类型豁免，一律放行。
	if m == nil || !m.enabled {
		return ActionAdmit
	}
	rule, ok := m.rules[rt]
	if !ok || !rule.Enabled {
		return ActionAdmit
	}
	slot := m.states[rt]
	if slot == nil {
		return ActionAdmit
	}

	switch State(slot.state.Load()) {
	case StateNormal:
		return ActionAdmit
	case StateShedding:
		// 计算流控概率。
		p := m.dropProbability(rt)
		if p <= 0 {
			return ActionAdmit
		}
		if p >= 1 || random() < p {
			return ActionShed
		}
	case StateOpen:
		return ActionOpen
	}

	return ActionAdmit
}

func (m *Manager) State(rt define.RecordType) State {
	if m == nil {
		return StateNormal
	}
	slot := m.states[rt]
	if slot == nil {
		return StateNormal
	}
	return State(slot.state.Load())
}

func (m *Manager) Level() *WaterLevel {
	if m == nil {
		return nil
	}
	return m.level.Load()
}

func (m *Manager) dropProbability(rt define.RecordType) float64 {
	level := m.Level()
	if level == nil {
		return 0
	}
	rule := m.rules[rt]
	thresholds := m.config.Thresholds
	// 将当前 CPU 慢信号转换成一个 0 ~ 1 的「过载程度」，计算水位在「进入线 -> 熔断线」范围的哪个百分比位置（0 ～ 1）。
	// 假设 CPUEnter = 0.80，CPUHard  = 0.95
	// CPUSlow = 0.80  => t = 0 ｜ CPUSlow = 0.875 => t = 0.5 ｜ CPUSlow = 0.95  => t = 1
	t := clamp((level.CPUSlow-thresholds.CPUEnter)/(thresholds.CPUHard-thresholds.CPUEnter), 0, 1)
	// 根据 t 决策丢弃概率，越接近 CPUHard，丢弃概率越高，假设 DropMin = 0.5，DropMax = 1：
	// CPUSlow = 0.875 => t = 0.5 => 0.25，即 25 % 的丢弃概率。
	return rule.DropMin + (rule.DropMax-rule.DropMin)*t
}

func (m *Manager) updateStates(level *WaterLevel) {
	for _, rt := range throttleRecordTypes {
		rule := m.rules[rt]
		slot := m.states[rt]
		if slot == nil {
			continue
		}
		if !rule.Enabled {
			slot.store(StateNormal)
			observeState(rt, StateNormal)
			continue
		}
		m.updateState(slot, level)
		observeState(rt, State(slot.state.Load()))
	}
}

func (m *Manager) updateState(slot *stateSlot, level *WaterLevel) {
	thresholds := m.config.Thresholds
	// 快信号连续触发熔断线判断，一次没有越过即清空计数。
	if level.CPUFast >= thresholds.CPUHard {
		slot.hardCount++
	} else {
		slot.hardCount = 0
	}

	// CPU：连续 BreachN 越线｜内存：由于 OOM 会导致重启（不可逆），一次越线即短路。
	hardOpen := slot.hardCount >= thresholds.BreachN
	memOpen := level.MemValid && level.Mem >= thresholds.MemHard
	if hardOpen || memOpen {
		// 进入熔断后清空计数器。
		slot.enterCount = 0
		slot.exitCount = 0
		slot.store(StateOpen)
		return
	}

	state := State(slot.state.Load())
	if state == StateOpen {
		// 走到该分支说明当前负载低于熔断线，根据慢信号当前的水位转移至下一个状态。
		if level.CPUSlow > thresholds.CPUExit {
			slot.store(StateShedding)
		} else {
			slot.store(StateNormal)
		}
		return
	}

	// 分级用慢信号（平滑），进入线高于退出线形成滞回带（如 enter=0.8 / exit=0.7），升过 0.8 才开始丢数据、跌回 0.7 恢复正常。
	// 滞回带的作用是防抖，避免短期抖动导致负载未到达，便开始丢弃数据。
	// 进入线、退出线各自累计「连续命中次数」，任一帧不满足就清零。
	if level.CPUSlow > thresholds.CPUEnter {
		slot.enterCount++
	} else {
		slot.enterCount = 0
	}
	if level.CPUSlow < thresholds.CPUExit {
		slot.exitCount++
	} else {
		slot.exitCount = 0
	}

	switch state {
	case StateShedding:
		// exitCount 连续达标才从 Shedding 回 Normal。
		if slot.exitCount >= thresholds.BreachN {
			slot.enterCount = 0
			slot.store(StateNormal)
		}
	default:
		// 走到该分支说明此时状态是 Normal，判断是否需要向 Shedding 转移。
		if slot.enterCount >= thresholds.BreachN {
			slot.exitCount = 0
			slot.store(StateShedding)
		}
	}
}

func (s *stateSlot) store(state State) {
	s.state.Store(uint32(state))
}

func clamp(v, min, max float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
