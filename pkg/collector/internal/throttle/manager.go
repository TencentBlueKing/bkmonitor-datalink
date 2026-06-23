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
	signals []signalSpec
	states  map[define.RecordType]*recordState
	level   atomic.Pointer[WaterLevel]
}

const (
	signalCPU = "cpu"
	signalMem = "mem"
)

type signalSpec struct {
	name   string
	config ThresholdSlotConfig
	sample func(*WaterLevel) slotSample
}

type slotSample struct {
	slow  float64
	fast  float64
	valid bool
}

// recordState 保存单类数据状态机。每个 slot 承载一种资源信号的阈值与连续命中计数。
type recordState struct {
	state atomic.Uint32
	slots map[string]*stateSlot
}

// stateSlot 保存单个资源信号的阈值与连续命中计数。
type stateSlot struct {
	enabled bool
	enter   float64
	exit    float64
	hard    float64
	breachN int

	enterHits     int
	exitHits      int
	hardHits      int
	hardClearHits int
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
	config = normalizeConfig(config)
	signals := buildSignals(config.Thresholds)
	m := &Manager{
		enabled: config.Enabled,
		config:  config,
		rules:   buildRules(config.Rules),
		signals: signals,
		states:  make(map[define.RecordType]*recordState, len(throttleRecordTypes)),
	}
	for _, rt := range throttleRecordTypes {
		m.states[rt] = newRecordState(signals)
	}
	return m
}

func buildSignals(thresholds ThresholdConfig) []signalSpec {
	return []signalSpec{
		{
			name:   signalCPU,
			config: thresholds.CPU,
			sample: func(level *WaterLevel) slotSample {
				return slotSample{slow: level.CPUSlow, fast: level.CPUFast, valid: true}
			},
		},
		{
			name:   signalMem,
			config: thresholds.Mem,
			sample: func(level *WaterLevel) slotSample {
				return slotSample{slow: level.Mem, fast: level.Mem, valid: level.MemValid}
			},
		},
	}
}

func newRecordState(signals []signalSpec) *recordState {
	r := &recordState{
		slots: make(map[string]*stateSlot, len(signals)),
	}
	for _, signal := range signals {
		r.slots[signal.name] = newStateSlot(signal.config)
	}
	return r
}

func newStateSlot(config ThresholdSlotConfig) *stateSlot {
	return &stateSlot{
		enabled: thresholdEnabled(config),
		enter:   config.Enter,
		exit:    config.Exit,
		hard:    config.Hard,
		breachN: config.BreachN,
	}
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
	record := m.states[rt]
	if record == nil {
		return ActionAdmit
	}

	switch State(record.state.Load()) {
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
	record := m.states[rt]
	if record == nil {
		return StateNormal
	}
	return State(record.state.Load())
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

	record := m.states[rt]
	if record == nil {
		return 0
	}

	rule := m.rules[rt]
	return rule.DropMin + (rule.DropMax-rule.DropMin)*m.maxSlotRatio(record, level)
}

func (m *Manager) updateStates(level *WaterLevel) {
	for _, rt := range throttleRecordTypes {
		rule := m.rules[rt]
		record := m.states[rt]
		if record == nil {
			continue
		}
		if !rule.Enabled {
			record.store(StateNormal)
			observeState(rt, StateNormal)
			continue
		}
		m.updateState(record, level)
		observeState(rt, State(record.state.Load()))
	}
}

func (m *Manager) updateState(record *recordState, level *WaterLevel) {
	m.tickSlots(record, level)
	state := State(record.state.Load())
	if m.anySlot(record, (*stateSlot).HardReached) {
		// -> 熔断
		if state != StateOpen {
			record.resetEnterHits()
			record.resetExitHits()
			record.resetHardClearHits()
		}
		record.store(StateOpen)
		return
	}

	if state == StateOpen {
		// 走到该分支说明当前负载低于熔断线，根据慢信号当前的水位转移至下一个状态。
		if !m.allSlots(record, (*stateSlot).HardCleared) {
			return
		}

		if m.anySlotSample(record, level, (*stateSlot).SlowAboveExit) {
			// 熔断 -> 降级：还没恢复到正常。
			record.store(StateShedding)
		} else {
			// 熔断 -> 正常。
			record.store(StateNormal)
		}
		return
	}

	switch state {
	case StateNormal:
		if m.anySlot(record, (*stateSlot).EnterReached) {
			// 正常 -> 降级：某个信号连续 N 次越过 enter（降级）线。
			record.resetExitHits()
			record.store(StateShedding)
		}
	case StateShedding:
		if m.allSlots(record, (*stateSlot).ExitReached) {
			// 降级 -> 正常：CPU 与内存「同时」满足各自连续门控后退出。
			record.store(StateNormal)
		}
	}
}

func (m *Manager) tickSlots(record *recordState, level *WaterLevel) {
	for _, signal := range m.signals {
		slot := record.slots[signal.name]
		if slot != nil {
			slot.Tick(signal.sample(level))
		}
	}
}

func (m *Manager) anySlot(record *recordState, match func(*stateSlot) bool) bool {
	for _, signal := range m.signals {
		slot := record.slots[signal.name]
		if slot != nil && match(slot) {
			return true
		}
	}
	return false
}

func (m *Manager) allSlots(record *recordState, match func(*stateSlot) bool) bool {
	for _, signal := range m.signals {
		slot := record.slots[signal.name]
		if slot != nil && !match(slot) {
			return false
		}
	}
	return true
}

func (m *Manager) anySlotSample(record *recordState, level *WaterLevel, match func(*stateSlot, slotSample) bool) bool {
	for _, signal := range m.signals {
		slot := record.slots[signal.name]
		if slot != nil && match(slot, signal.sample(level)) {
			return true
		}
	}
	return false
}

func (m *Manager) maxSlotRatio(record *recordState, level *WaterLevel) float64 {
	// 将当前 CPU 慢信号转换成一个 0 ~ 1 的「过载程度」，计算水位在「进入线 -> 熔断线」范围的哪个百分比位置（0 ～ 1）。
	// 假设 CPUEnter = 0.80，CPUHard  = 0.95，CPUSlow = 0.80  => t = 0 ｜ CPUSlow = 0.875 => t = 0.5 ｜ CPUSlow = 0.95  => t = 1
	// 根据 t 决策丢弃概率，越接近 CPUHard，丢弃概率越高，假设 DropMin = 0.5，DropMax = 1：CPUSlow = 0.875 => t = 0.5 => 0.25，即 25 % 的丢弃概率。
	ratio := 0.0
	for _, signal := range m.signals {
		slot := record.slots[signal.name]
		if slot != nil {
			ratio = math.Max(ratio, slot.Ratio(signal.sample(level)))
		}
	}
	return ratio
}

func (r *recordState) resetEnterHits() {
	for _, slot := range r.slots {
		slot.enterHits = 0
	}
}

func (r *recordState) resetExitHits() {
	for _, slot := range r.slots {
		slot.exitHits = 0
	}
}

func (r *recordState) resetHardClearHits() {
	for _, slot := range r.slots {
		slot.hardClearHits = 0
	}
}

func (r *recordState) store(state State) {
	r.state.Store(uint32(state))
}

func (s *stateSlot) Tick(sample slotSample) {
	if !s.enabled || !sample.valid {
		s.enterHits = 0
		s.hardHits = 0
		s.exitHits = s.breachN
		s.hardClearHits = s.breachN
		return
	}
	tickHits(&s.enterHits, sample.slow > s.enter)
	tickHits(&s.exitHits, sample.slow < s.exit)
	tickHits(&s.hardHits, sample.fast >= s.hard)
	tickHits(&s.hardClearHits, sample.fast < s.hard)
}

func (s *stateSlot) EnterReached() bool {
	return s.enabled && s.enterHits >= s.breachN
}

func (s *stateSlot) ExitReached() bool {
	return !s.enabled || s.exitHits >= s.breachN
}

func (s *stateSlot) HardReached() bool {
	return s.enabled && s.hardHits >= s.breachN
}

func (s *stateSlot) HardCleared() bool {
	return !s.enabled || s.hardClearHits >= s.breachN
}

func (s *stateSlot) SlowAboveExit(sample slotSample) bool {
	return s.enabled && sample.valid && sample.slow > s.exit
}

func (s *stateSlot) Ratio(sample slotSample) float64 {
	if !s.enabled || !sample.valid {
		return 0
	}

	// 把当前水位归一化到 [0, 1]。
	span := s.hard - s.enter
	if span <= 0 {
		return 0
	}
	return clamp((sample.slow-s.enter)/span, 0, 1)
}

// tickHits 推进一个连续命中计数：满足条件则 +1，否则归零。
func tickHits(c *int, hit bool) {
	if hit {
		*c++
		return
	}
	*c = 0
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
