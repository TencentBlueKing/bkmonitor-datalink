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
	"math"
	"math/rand"
	"sync/atomic"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

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
	CPU      float64
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

// stateSlot 保存单类数据的状态机以及各信号的连续命中计数。
type stateSlot struct {
	state            atomic.Uint32
	cpuEnterHits     int // cpuSlow > cpu_enter（进入降级）
	cpuExitHits      int // cpuSlow < cpu_exit（恢复正常）
	cpuHardHits      int // cpuFast >= cpu_hard（进入熔断）
	cpuHardClearHits int // cpuFast < cpu_hard（退出熔断）
	memEnterHits     int // mem > mem_enter（进入降级，内存采样无效时忽略）
	memExitHits      int // mem < mem_exit（恢复正常，内存采样无效时视为正常）
	memHardClearHits int // mem < mem_hard（退出熔断，内存采样无效时视为正常）
	openByCPU        bool
	openByMem        bool
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
	if !config.Enabled {
		Stop()
		return nil
	}

	manager := newManager(config)
	sampler := NewResourceSampler(NewCgroupReader(), config, manager)
	// 冷启动启动时采样。
	sampler.tick()
	sampler.Start()
	logger.Infof("throttle enabled, sample_interval=%s", config.SampleInterval)

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
	globalManager.Store(nil)
	if oldSampler != nil {
		oldSampler.Stop()
	}
}

func Enabled() bool {
	manager := globalManager.Load()
	return manager != nil && manager.enabled
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
	observeWaterLevel(current, m.config.Thresholds)
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

	// 将当前 CPU 慢信号转换成一个 0 ~ 1 的「过载程度」，计算水位在「进入线 -> 熔断线」范围的哪个百分比位置（0 ～ 1）。
	// 假设 CPUEnter = 0.80，CPUHard  = 0.95，CPUSlow = 0.80  => t = 0 ｜ CPUSlow = 0.875 => t = 0.5 ｜ CPUSlow = 0.95  => t = 1
	// 根据 t 决策丢弃概率，越接近 CPUHard，丢弃概率越高，假设 DropMin = 0.5，DropMax = 1：CPUSlow = 0.875 => t = 0.5 => 0.25，即 25 % 的丢弃概率。
	th := m.config.Thresholds
	tMem := 0.0
	if level.MemValid {
		tMem = overloadRatio(level.Mem, th.MemEnter, th.MemHard)
	}
	t := math.Max(tMem, overloadRatio(level.CPUSlow, th.CPUEnter, th.CPUHard))

	rule := m.rules[rt]
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
	th := m.config.Thresholds
	n := th.BreachN
	memN := th.MemBreachN

	// 推进所有信号的连续命中计数：每帧只统计、不转移。
	memSafe := !level.MemValid || level.Mem < th.MemHard
	tickHits(&slot.cpuHardHits, level.CPUFast >= th.CPUHard)
	tickHits(&slot.cpuHardClearHits, level.CPUFast < th.CPUHard)
	tickHits(&slot.memHardClearHits, memSafe)
	tickHits(&slot.cpuEnterHits, level.CPUSlow > th.CPUEnter)
	tickHits(&slot.cpuExitHits, level.CPUSlow < th.CPUExit)
	tickHits(&slot.memEnterHits, level.MemValid && level.Mem > th.MemEnter)
	tickHits(&slot.memExitHits, !level.MemValid || level.Mem < th.MemExit)

	// -> 熔断：CPU 快信号连续越线，或内存单次越线（OOM 不可逆，等不起连续）。
	cpuOpen := slot.cpuHardHits >= n
	memOpen := level.MemValid && level.Mem >= th.MemHard
	if cpuOpen || memOpen {
		slot.resetEnterHits()
		slot.resetExitHits()
		slot.markOpenCauses(cpuOpen, memOpen)
		slot.store(StateOpen)
		return
	}

	state := State(slot.state.Load())
	if state == StateOpen {
		// 走到该分支说明当前负载低于熔断线，根据慢信号当前的水位转移至下一个状态。
		if !slot.openCausesCleared(n, memN) {
			return
		}

		if softAboveExit(level, th) {
			// 熔断 -> 降级：还没恢复到正常。
			slot.store(StateShedding)
		} else {
			// 熔断 -> 正常。
			slot.store(StateNormal)
		}
		return
	}

	enterMet := slot.cpuEnterHits >= n || slot.memEnterHits >= memN
	exitMet := slot.cpuExitHits >= n && slot.memExitHits >= memN

	switch state {
	case StateNormal:
		if enterMet {
			// 正常 -> 降级：CPU、内存任一连续越 enter（降级线）即进入，内存使用独立门控。
			slot.resetExitHits()
			slot.store(StateShedding)
		}
	case StateShedding:
		if exitMet {
			// 降级 -> 正常：CPU 与内存「同时」满足各自连续门控后退出。
			slot.store(StateNormal)
		}
	}
}

// softAboveExit 判定熔断退出后是否仍高于 exit 线。
func softAboveExit(level *WaterLevel, th ThresholdConfig) bool {
	if level.CPUSlow > th.CPUExit {
		return true
	}
	// 内存只在有效时纳入，invalid 视为安全（与 dropProbability、tickHits 一致）。
	return level.MemValid && level.Mem > th.MemExit
}

func (s *stateSlot) resetEnterHits() {
	s.cpuEnterHits = 0
	s.memEnterHits = 0
}

func (s *stateSlot) resetExitHits() {
	s.cpuExitHits = 0
	s.memExitHits = 0
}

func (s *stateSlot) markOpenCauses(cpuOpen, memOpen bool) {
	if cpuOpen {
		s.openByCPU = true
		s.cpuHardClearHits = 0
	}
	if memOpen {
		s.openByMem = true
		s.memHardClearHits = 0
	}
}

func (s *stateSlot) openCausesCleared(cpuN, memN int) bool {
	if s.openByCPU && s.cpuHardClearHits < cpuN {
		return false
	}
	if s.openByMem && s.memHardClearHits < memN {
		return false
	}
	return s.openByCPU || s.openByMem
}

func (s *stateSlot) resetOpenCauses() {
	s.openByCPU = false
	s.openByMem = false
}

func (s *stateSlot) store(state State) {
	if state != StateOpen {
		s.resetOpenCauses()
	}
	s.state.Store(uint32(state))
}

// tickHits 推进一个连续命中计数：满足条件则 +1，否则归零。
func tickHits(c *int, hit bool) {
	if hit {
		*c++
		return
	}
	*c = 0
}

// overloadRatio 把当前水位归一化到 [0, 1]。
func overloadRatio(value, enter, hard float64) float64 {
	span := hard - enter
	if span <= 0 {
		return 0
	}
	return clamp((value-enter)/span, 0, 1)
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
