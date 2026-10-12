// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lookback

import (
	"container/heap"
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ReadHoldEvidence says when a confirmed whole window first held its final
// values, measured from the original window end, including source delay.
// Contract carries the hold frozen for the formal first read.
type ReadHoldEvidence struct {
	Contract   execution.FrozenExecutionContractRef
	ArrivalAge time.Duration
	Rung       string
	Buckets    []int64
}

// EarlierReadEvidence compares a candidate h/2 read with the formal first
// read of the same Slot. An unobserved read supplies no decrease evidence.
type EarlierReadEvidence struct {
	Contract      execution.FrozenExecutionContractRef
	CandidateHold time.Duration
	Observed      bool
	Equal         bool
	Outcome       string
}

const (
	EarlierEqual           = "equal"
	EarlierDifferent       = "different"
	EarlierPermitRefused   = "permit_refused"
	EarlierMemoryRefused   = "memory_refused"
	EarlierReadFailed      = "read_failed"
	EarlierFirstIncomplete = "first_read_incomplete"
	EarlierOwnerLost       = "owner_lost"
	EarlierOvertaken       = "overtaken"
)

var EarlierReadOutcomes = []string{EarlierEqual, EarlierDifferent, EarlierPermitRefused, EarlierMemoryRefused,
	EarlierReadFailed, EarlierFirstIncomplete, EarlierOwnerLost, EarlierOvertaken}

// ReadHoldIgnoredReasons count findings that cannot raise a whole-window hold.
var ReadHoldIgnoredReasons = []string{ClassPartialRevised, IgnoredNoWholeWindowArrival}

// IgnoredNoWholeWindowArrival is a sample classed window_read_early in which
// no series arrived whole after the first read -- one went, or one changed
// without arriving -- so no arrival age can be read from it. It is not two
// later reads disagreeing.
const IgnoredNoWholeWindowArrival = "no_whole_window_arrival"

type ignoredReadHold struct {
	evidence ReadHoldEvidence
	reason   string
}

func firstReadAge(candidate *sample) time.Duration {
	ready := candidate.readyAt
	if ready.IsZero() {
		ready = candidate.readAt
	}
	return ready.Sub(candidate.windowEnd)
}

func (engine *Engine) recordIgnoredLocked(candidate *sample, reason string) {
	engine.counts.holdIgnored[key2(candidate.source, reason)]++
	if engine.options.OnReadHoldIgnored != nil {
		engine.ignoredEvidence = append(engine.ignoredEvidence, ignoredReadHold{reason: reason, evidence: ReadHoldEvidence{
			Contract: candidate.contract, ArrivalAge: candidate.completion}})
	}
}

func wholeWindowArrival(candidate *sample) bool {
	return candidate.emptyFirstRead && len(candidate.last) > 0 || candidate.existingArrived > 0
}

func (engine *Engine) recordReadHoldLocked(candidate *sample) {
	class, _ := classOf(candidate)
	if engine.options.OnWholeWindowReadEarly == nil || candidate.holdReported || candidate.lastChange < 0 || class != ClassWindowReadEarly || !wholeWindowArrival(candidate) {
		return
	}
	candidate.holdReported = true
	engine.holdEvidence = append(engine.holdEvidence, ReadHoldEvidence{Contract: candidate.contract,
		ArrivalAge: candidate.completion, Rung: RungNames[candidate.lastChange], Buckets: append([]int64(nil), candidate.lastBuckets...)})
}

type earlierSample struct {
	query         Query
	source        string
	at            time.Time
	readyAt       time.Time
	candidateHold time.Duration
	keptFrom      int64
	summary       readSummary
	running       bool
	done          bool
	formal        bool
	outcome       string
	cancel        context.CancelFunc
	group         *group
}

func (engine *Engine) groupLocked(query Query, now time.Time) *group {
	qg := query.Contract.Slot.QueryGroup
	state := engine.groups[qg]
	if state == nil {
		state = &group{depth: 1, rest: RungSteps[0], sinceProbe: probeEvery - 2}
		state.reading.since = now
		if engine.options.ProbeFirstSamples {
			state.sinceProbe = probeEvery - 1
		}
		if !engine.options.UnspreadFirstSamples {
			state.nextAt = now.Add(time.Duration(spreadFraction(qg) * float64(restCap)))
		}
		engine.groups[qg] = state
	}
	state.source = sourceOf(query.Spec.PlanFacts)
	state.step = time.Duration(query.Spec.PlanFacts.StepMillis) * time.Millisecond
	return state
}

// Prepare sees a normal, non-retry query before readiness waits. It reserves
// exactly the sample Begin would take, once per Slot, so the h/2 read uses
// existing sampling rather than an independent query schedule. Begin then
// continues that reservation even when readiness has been retried.
func (engine *Engine) Prepare(query Query) {
	if engine == nil || engine.options.OnEarlierRead == nil || query.Operation != execution.OperationNormal || query.Secondary ||
		query.AttemptNo != 1 || query.Spec.PlanFacts.StepMillis <= 0 || query.Contract.ReadHoldMillis <= 0 || query.ReadyAt.IsZero() {
		return
	}
	if !engine.options.Owns(query.Contract.Slot.QueryGroup) {
		return
	}
	now := engine.options.Now()
	hold := time.Duration(query.Contract.ReadHoldMillis) * time.Millisecond
	base := hold
	if engine.options.CurrentReadHold != nil {
		base = engine.options.CurrentReadHold(query.Contract.Slot.QueryGroup)
	}
	if base <= 0 {
		return
	}
	engine.mu.Lock()
	state := engine.groupLocked(query, now)
	if previous := state.prepared; previous != nil {
		if previous.query.Contract.Slot == query.Contract.Slot {
			// The same Slot prepared again, its readiness deferred and
			// retried: the reservation stands.
			engine.mu.Unlock()
			return
		}
		// A frozen Slot that never reached a first read supplies no evidence.
		if !previous.formal {
			previous.done, previous.outcome = true, EarlierOvertaken
			if previous.cancel != nil {
				previous.cancel()
			}
			engine.counts.earlierReads[key2(previous.source, EarlierOvertaken)]++
			previous.group.reading.earlier[wordIndex(EarlierReadOutcomes, EarlierOvertaken)]++
		}
		state.prepared = nil
	}
	if state.capturing || state.sample != nil || now.Before(state.nextAt) {
		engine.mu.Unlock()
		return
	}
	candidate := execution.LoweredReadHold(base, state.step)
	baseline := query.ReadyAt.Add(-hold)
	readyAt := baseline.Add(candidate)
	start := readyAt.Add(-RecheckTimeout)
	if start.Before(baseline) {
		start = baseline
	}
	state.prepared = &earlierSample{query: query, source: state.source, candidateHold: candidate,
		group: state,
		// Start within the existing recheck budget before the candidate
		// readiness point. Data already whole here proves it whole at h/2;
		// starting after that point cannot prove a decrease.
		at: start, readyAt: readyAt, keptFrom: tailFrom(query.Spec.LogicalWindow, state.step, tailSteps)}
	if now.UnixMilli() > readyAt.UnixMilli() {
		state.prepared.done, state.prepared.outcome = true, EarlierOvertaken
	}
	heap.Push(&engine.earlyWork, earlyWork{at: start, trial: state.prepared})
	engine.mu.Unlock()
	engine.poke()
}

func (read *Read) completeEarlier(completion execution.ProviderCompletion, err error) {
	trial := read.earlier
	if trial == nil {
		return
	}
	engine := read.engine
	owned := engine.options.Owns(trial.query.Contract.Slot.QueryGroup)
	candidateCurrent := true
	if engine.options.CurrentReadHold != nil {
		hold := engine.options.CurrentReadHold(trial.query.Contract.Slot.QueryGroup)
		candidateCurrent = hold > 0 && execution.LoweredReadHold(hold, trial.group.step) == trial.candidateHold
	}
	engine.mu.Lock()
	outcome := trial.outcome
	switch {
	case !owned:
		outcome = EarlierOwnerLost
	case err != nil || completion.Completeness != execution.CompletenessFull || read.summary == nil || read.summary.faulted:
		outcome = EarlierFirstIncomplete
	case outcome == "" && !candidateCurrent:
		outcome = EarlierOvertaken
	case outcome == "":
		outcome = EarlierEqual
		if len(compareSummaries(trial.summary, trimSummary(read.summary.buckets, trial.keptFrom))) > 0 {
			outcome = EarlierDifferent
		}
	}
	trial.summary = nil
	if state := engine.groups[trial.query.Contract.Slot.QueryGroup]; state != nil && state.prepared == trial {
		state.prepared = nil
	}
	engine.counts.earlierReads[key2(trial.source, outcome)]++
	trial.group.reading.earlier[wordIndex(EarlierReadOutcomes, outcome)]++
	engine.mu.Unlock()
	engine.options.OnEarlierRead(EarlierReadEvidence{Contract: trial.query.Contract, CandidateHold: trial.candidateHold,
		Observed: outcome == EarlierEqual || outcome == EarlierDifferent, Equal: outcome == EarlierEqual, Outcome: outcome})
}

func (engine *Engine) publishReadHold() {
	if engine.options.OnWholeWindowReadEarly == nil && engine.options.OnReadHoldIgnored == nil {
		return
	}
	engine.mu.Lock()
	evidence := engine.holdEvidence
	engine.holdEvidence = nil
	ignored := engine.ignoredEvidence
	engine.ignoredEvidence = nil
	engine.mu.Unlock()
	for _, item := range evidence {
		if engine.options.Owns(item.Contract.Slot.QueryGroup) {
			engine.options.OnWholeWindowReadEarly(item)
		}
	}
	for _, item := range ignored {
		if engine.options.Owns(item.evidence.Contract.Slot.QueryGroup) {
			engine.options.OnReadHoldIgnored(item.evidence, item.reason)
		}
	}
}

// earlyWork is shared by directed early reads and h/2 sample reads. Both
// use the engine loop's existing wakeup, minimum heap and lookback permits.
type earlyWork struct {
	at    time.Time
	slot  *directedSlot
	trial *earlierSample
}
type earlyHeap []earlyWork

func (queue earlyHeap) Len() int { return len(queue) }
func (queue earlyHeap) Less(i, j int) bool {
	if !queue[i].at.Equal(queue[j].at) {
		return queue[i].at.Before(queue[j].at)
	}
	deadline := func(work earlyWork) time.Time {
		if work.slot != nil {
			return work.slot.early.latest
		}
		return work.trial.readyAt
	}
	return deadline(queue[i]).Before(deadline(queue[j]))
}
func (queue earlyHeap) Swap(i, j int)   { queue[i], queue[j] = queue[j], queue[i] }
func (queue *earlyHeap) Push(value any) { *queue = append(*queue, value.(earlyWork)) }
func (queue *earlyHeap) Pop() any {
	last := len(*queue) - 1
	value := (*queue)[last]
	(*queue)[last] = earlyWork{}
	*queue = (*queue)[:last]
	return value
}

func (engine *Engine) earlyQueueLocked(now time.Time, free int) time.Time {
	directed, wake := engine.dueEarlyLocked(now, free)
	for _, slot := range directed {
		if !slot.early.queued {
			slot.early.queued = true
			heap.Push(&engine.earlyWork, earlyWork{at: now, slot: slot})
		}
	}
	for engine.earlyWork.Len() > 0 {
		work := engine.earlyWork[0]
		if work.trial != nil && (work.trial.done || work.trial.formal) || work.slot != nil && (work.slot.dropped || work.slot.early.done) {
			heap.Pop(&engine.earlyWork)
			continue
		}
		if wake.IsZero() || work.at.Before(wake) {
			wake = work.at
		}
		break
	}
	return wake
}

func (engine *Engine) earlierRead(ctx context.Context, trial *earlierSample, release func(), yield <-chan struct{}) {
	budget := min(RecheckTimeout, max(trial.readyAt.Sub(engine.options.Now()), 0))
	readCtx, cancel := context.WithTimeout(ctx, budget)
	engine.mu.Lock()
	trial.cancel = cancel
	if engine.options.Now().UnixMilli() > trial.readyAt.UnixMilli() && !trial.formal && !trial.done {
		trial.done, trial.outcome = true, EarlierOvertaken
	}
	skipped := trial.formal || trial.done
	if skipped {
		cancel()
	}
	engine.mu.Unlock()
	back := make(chan struct{})
	if yield != nil {
		go func() {
			select {
			case <-yield:
				cancel()
			case <-back:
			}
		}()
	}
	sink := &earlierSink{summary: newSummarizer(trial.query.Spec.PlanFacts.Normalization.CanonicalValueField),
		admit: engine.options.Memory, keptFrom: trial.keptFrom}
	// Even an empty summary must have passed the memory line.
	sink.reserve(64)
	lookback, known := queryLookback(trial.query.Spec.PlanFacts)
	if !known {
		lookback = time.Duration(trial.query.Spec.PlanFacts.StepMillis) * time.Millisecond
	}
	from := max(trial.query.Spec.LogicalWindow.Start, trial.keptFrom-int64(lookback/time.Second))
	var completion execution.ProviderCompletion
	var err error
	if !sink.refused && !skipped {
		completion, err = engine.options.Recheck(readCtx, tailSpec(trial.query.Spec, from), sink)
	}
	finished := engine.options.Now()
	close(back)
	release()
	cancel()
	engine.mu.Lock()
	engine.counts.earlierBytes[trial.source] += sink.bytes
	trial.group.reading.earlierBytes += sink.bytes
	trial.running, trial.cancel = false, nil
	if !trial.done && !trial.formal {
		trial.done = true
		switch {
		case finished.After(trial.readyAt):
			trial.outcome = EarlierOvertaken
		case sink.refused:
			trial.outcome = EarlierMemoryRefused
		case err != nil || completion.Completeness != execution.CompletenessFull || sink.summary.faulted || closed(yield):
			trial.outcome = EarlierReadFailed
		default:
			trial.summary = sink.summary.buckets
		}
	}
	engine.mu.Unlock()
	engine.poke()
}

// earlierSink keeps only the compared tail, admitted before it grows. The
// summary remains live until the formal first read has been compared.
type earlierSink struct {
	summary  *summarizer
	admit    func(uint64) bool
	keptFrom int64
	granted  int
	refused  bool
	bytes    uint64
}

func (sink *earlierSink) reserve(want int) {
	if sink.refused || want <= sink.granted {
		return
	}
	capacity := max(sink.granted, 64)
	for capacity < want {
		capacity *= 2
	}
	if sink.admit != nil && !sink.admit(uint64(capacity-sink.granted)*summaryEntryBytes) {
		sink.refused, sink.summary.buckets = true, nil
		return
	}
	sink.granted = capacity
}

func (sink *earlierSink) ConsumeProviderSeries(_ context.Context, batch execution.ProviderSeriesBatch) error {
	sink.bytes += batch.Delivery.Bytes
	if sink.refused || sink.summary.faulted || batch.Dataset == nil {
		return nil
	}
	// The provider may read a lookback before the compared tail. Summing
	// one view at a time avoids retaining or allocating another dataset.
	for index := 0; index < batch.Dataset.Len(); index++ {
		record, _ := batch.Dataset.Record(index)
		if record.SourceTime() < sink.keptFrom {
			continue
		}
		if _, known := sink.summary.buckets[record.SourceTime()]; !known {
			sink.reserve(len(sink.summary.buckets) + 1)
			if sink.refused {
				return nil
			}
		}
		series := hashString(record.DimensionIdentityDigest())
		sink.summary.addRecord(record, series, mix(series))
		if sink.summary.faulted {
			return nil
		}
	}
	return nil
}
