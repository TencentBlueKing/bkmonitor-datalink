// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycle

import (
	"context"
	"time"

	"linkd/internal/store"
)

// PhaseDuration 是单次 ProcessEvent 中一个环节的独占耗时；未执行的环节为零。
type PhaseDuration struct {
	// Phase 使用固定环节名，不包含租户、实体或配置身份。
	Phase string
	// Duration 不含嵌套环节的耗时。
	Duration time.Duration
	// BatchWait 与 StorageQueue 属于 Duration，扣除两者后才是执行时间。
	BatchWait    time.Duration
	StorageQueue time.Duration
}

// PhaseObservation 覆盖一次 Event 尝试，包括失败、重放和尝试内的 CAS 重试。
type PhaseObservation struct {
	// EventSourceID 来自本次输入 Event，失败与重放也保留来源。
	EventSourceID string
	// Phases 的独占耗时之和等于 Duration，嵌套环节不重复计时。
	Phases []PhaseDuration
	// Steps 与 Phases 使用相同样本，按父环节独占记账。
	Steps []StepDuration
	// Duration 不含观察器自身上报耗时。
	Duration time.Duration
	// Result 与 Err 是 ProcessEvent 返回给调用方的原始结果。
	Result ProcessResult
	// Err 非 nil 时，所有分段都属于本次失败尝试。
	Err error
}

// PhaseObserver 接收单次 Event 的完整分段样本，不得阻塞或改变处理结果。
type PhaseObserver interface {
	Finished(context.Context, PhaseObservation)
}

// WithPhaseObserver 注入 Event 分段耗时观察器；nil 表示不采样。
func WithPhaseObserver(observer PhaseObserver) ProcessorOption {
	return func(p *Processor) { p.phaseObserver = observer }
}

const (
	phaseOther = iota
	phaseEnrich
	phasePolicySnapshot
	phasePlan
	phaseEventCommit
	phaseApply
	phaseHook
	phaseCount
)

// phaseTiming 仅属于一次串行 ProcessEvent 调用。切换时把时间记给当前环节，
// 返回时恢复父环节，避免 executePlan 与其内部 Hook / Event 写入重复计时。
// 不挂在共享 Processor 上，其他 fingerprint 的并发调用拥有独立实例。
type phaseTiming struct {
	stepDurations [len(phaseSteps)]time.Duration
	stepWaits     [len(phaseSteps)][2]time.Duration
	step          int
	waits         [phaseCount][2]time.Duration
	durations     [phaseCount]time.Duration
	current       int
	last          time.Time
	started       time.Time
}

func (t *phaseTiming) change(phase int, at time.Time) {
	elapsed := at.Sub(t.last)
	t.durations[t.current] += elapsed
	t.stepDurations[t.activeStep()] += elapsed
	t.current, t.last = phase, at
}

func (p *Processor) measurePhase(phase int) func() {
	if p.phaseTiming == nil {
		return func() {}
	}
	t := p.phaseTiming
	parent := t.current
	t.change(phase, time.Now())
	return func() { t.change(parent, time.Now()) }
}

// observeWait 由同步存储调用在原 goroutine 返回前上报，归入当时最内层环节。
func (t *phaseTiming) observeWait(kind store.WaitKind, duration time.Duration) {
	switch kind {
	case store.WaitBatch:
		t.waits[t.current][0] += duration
		t.stepWaits[t.activeStep()][0] += duration
	case store.WaitStorageQueue:
		t.waits[t.current][1] += duration
		t.stepWaits[t.activeStep()][1] += duration
	}
}

func (t *phaseTiming) observation(at time.Time, result ProcessResult, err error) PhaseObservation {
	t.change(phaseOther, at)
	names := [...]string{"other", "enrich", "policy_snapshot", "plan", "event_commit", "apply", "final_hook"}
	phases := make([]PhaseDuration, phaseCount)
	for i, name := range names {
		duration := t.durations[i]
		batch := min(duration, t.waits[i][0])
		queue := min(duration-batch, t.waits[i][1])
		phases[i] = PhaseDuration{Phase: name, Duration: duration, BatchWait: batch, StorageQueue: queue}
	}
	steps := make([]StepDuration, len(phaseSteps))
	for i, step := range phaseSteps {
		duration := t.stepDurations[i]
		batch := min(duration, t.stepWaits[i][0])
		queue := min(duration-batch, t.stepWaits[i][1])
		steps[i] = StepDuration{PhaseDuration: PhaseDuration{Phase: names[step.phase], Duration: duration, BatchWait: batch, StorageQueue: queue}, Step: step.name}
	}
	return PhaseObservation{Phases: phases, Steps: steps, Duration: at.Sub(t.started), Result: result, Err: err}
}
