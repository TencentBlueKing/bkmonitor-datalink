// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sourceliveness

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Judge is what a no-data end asks: the verdict on a Level's source, and a
// probe started ahead for one due next round. Checker is the one.
type Judge interface {
	Judge(subject Subject, facts execution.QueryPlanFacts, judged int64) Verdict
	Ahead(facts execution.QueryPlanFacts, lastRealPoint, judged, spanSeconds int64)
}

var _ Judge = (*Checker)(nil)

// Querier runs one probe against the query service.
type Querier interface {
	ProbeSourceLiveness(ctx context.Context, probe Probe) Answer
}

// Observer hears what the Checker decides: every verdict a no-data end was
// decided on, and every probe that ran with how long it took.
type Observer interface {
	SourceLivenessChecked(kind Kind, verdict Verdict)
	SourceLivenessProbed(kind Kind, elapsed time.Duration)
	// SourceLivenessDeferred hears a probe not started because MaxInFlight
	// probes were running: its round reads timeout, and asks again later.
	SourceLivenessDeferred(kind Kind)
	// SourceLivenessEvicted hears the oldest kept answer dropped to keep a
	// newer source's past MaxAnswers.
	SourceLivenessEvicted()
	// SourceLivenessHeldEvicted hears the held Level asked about least
	// recently let go to keep a new one past MaxHeldLevels.
	SourceLivenessHeldEvicted()
}

// ProbeTimeout bounds one probe. It runs beside the round, never inside it,
// so it cannot hold a Slot; it is still shorter than the query service's own
// 30 s limit (unify-query service/tsdb/hook.go), so a probe this checker
// gave up on is not still running there, and an answer slower than this is
// unknown (timeout).
const ProbeTimeout = 20 * time.Second

// MaxInFlight bounds the probes running at once in one process. A probe is
// asked only for a no-data end that is due or due next round, at most one per
// source and period, so the bound is reached only by many distinct sources
// going quiet together. A probe not started then is deferred - counted, so
// the bound being reached is seen - and its round reads timeout: no answer in
// time, though none was asked for. The next round asks again.
const MaxInFlight = 16

// MaxAnswers bounds the answers kept, one per source: a few hundred bytes
// each. Past it the oldest answer - the one whose window ends first - makes
// room for the new one, counted: an answer dropped instead would leave its
// source unknown by timeout on every round, its probe answering for nothing.
const MaxAnswers = 4096

// sourceOf is a Key without the end of its window: one answer is kept per
// source and window start - the first empty period's - the newest. A start
// is a series' last real point, so the series of one source that stopped
// together, as in an outage, share it.
type sourceOf struct {
	TenantID, SpaceScope           string
	GlobalBusiness                 bool
	DataSource, TableID, FieldName string
	StepSeconds, Start             int64
}

func (key Key) source() sourceOf {
	return sourceOf{TenantID: key.TenantID, SpaceScope: key.SpaceScope, GlobalBusiness: key.GlobalBusiness,
		DataSource: key.DataSource, TableID: key.TableID, FieldName: key.FieldName, StepSeconds: key.StepSeconds, Start: key.Start}
}

type answered struct {
	key     Key
	verdict Verdict
}

// Checker answers whether a source is alive for a no-data end, without ever
// waiting on the query service. A probe runs in the background, at most one
// per source and period, and its verdict is kept in this process only - no
// Redis command, no state shared between replicas; each replica that owns
// such a Level probes its source once a period. A round reads the newest
// verdict of its source if it is of its own period or the one before;
// otherwise the source has not answered in time this round (unknown by
// timeout: the Level stays open)
// and the probe it starts answers a later round.
//
// A Level whose end is due next round has its source probed ahead (Ahead),
// so that the round its end is due reads a verdict of the period before and
// ends on time; a probe that has not come back by then holds the end one
// round, never fails the round.
type Checker struct {
	querier  Querier
	observer Observer
	base     context.Context
	timeout  time.Duration
	now      func() time.Time

	mu       sync.Mutex
	answers  map[sourceOf]answered
	inflight map[Key]struct{}
	running  sync.WaitGroup
	// maxInFlight is MaxInFlight and maxAnswers MaxAnswers; a test sets
	// them lower.
	maxInFlight, maxAnswers int
	held                    heldLevels
}

// NewChecker is a Checker probing through querier for as long as base
// lives. A nil observer hears nothing.
func NewChecker(base context.Context, querier Querier, observer Observer) (*Checker, error) {
	if base == nil || querier == nil {
		return nil, errors.New("alarmd sourceliveness: a checker needs a context and a querier")
	}
	checker := &Checker{querier: querier, observer: observer, base: base, timeout: ProbeTimeout, now: time.Now,
		answers: map[sourceOf]answered{}, inflight: map[Key]struct{}{},
		maxInFlight: MaxInFlight, maxAnswers: MaxAnswers, held: newHeldLevels()}
	if observer != nil {
		checker.held.evicted = observer.SourceLivenessHeldEvicted
	}
	return checker, nil
}

// WithClock is the Checker judging time by now, for the held Levels' ages.
func (checker *Checker) WithClock(now func() time.Time) *Checker {
	if now != nil {
		checker.now = now
	}
	return checker
}

// WithProbeTimeout is the Checker bounding each probe by timeout instead of
// ProbeTimeout.
func (checker *Checker) WithProbeTimeout(timeout time.Duration) *Checker {
	if timeout > 0 {
		checker.timeout = timeout
	}
	return checker
}

// Subject is the Level a verdict is about: whose end it holds or lets go.
// NoDataSpanSeconds is how long the Level waits on no data before it ends,
// (R + T + 1) periods; LastRealPoint the source time of its series' last
// real point, which its no-data span is timed from.
type Subject struct {
	Plan              execution.PlanIdentity
	LevelID           uint32
	Series            execution.SeriesIdentityDigest
	NoDataSpanSeconds int64
	LastRealPoint     int64
}

// Judge is the verdict on a no-data end of subject, whose Plan's primary
// query has facts, due at the period judged: alive ends it, anything else
// keeps it open, and an unsupported source ends it as before. It never waits
// on a probe. Every verdict is counted.
func (checker *Checker) Judge(subject Subject, facts execution.QueryPlanFacts, judged int64) Verdict {
	probe, verdict, ok := Derive(facts, subject.LastRealPoint, judged, subject.NoDataSpanSeconds)
	if ok {
		verdict = checker.verdict(probe)
	}
	checker.held.note(subject, probe, facts, verdict, checker.now())
	if checker.observer != nil {
		checker.observer.SourceLivenessChecked(probe.Kind, verdict)
	}
	return verdict
}

// Ahead starts the probe of a source whose no-data end is due next round, so
// that round reads a verdict: the window that round would read, from the
// same first empty period, ending where this round's read ends - the next
// period is not readable yet. Never a window reaching back before the first
// empty period: the round's verdict is accepted only from a probe of the
// same start. It decides nothing and counts nothing.
func (checker *Checker) Ahead(facts execution.QueryPlanFacts, lastRealPoint, judged, spanSeconds int64) {
	probe, _, ok := Derive(facts, lastRealPoint, judged, spanSeconds)
	if !ok {
		return
	}
	checker.mu.Lock()
	defer checker.mu.Unlock()
	if kept, found := checker.answers[probe.Key.source()]; found && kept.key.End >= probe.Key.End {
		return
	}
	checker.startLocked(probe)
}

// verdict is the kept verdict of probe's source and window start when its
// window ends in probe's period or the one before, else unknown by timeout;
// a probe of this period is started unless its verdict is kept or it is
// running. Kept by start, an answer never comes from a window that begins
// before this Level's first empty period.
func (checker *Checker) verdict(probe Probe) Verdict {
	checker.mu.Lock()
	defer checker.mu.Unlock()
	kept, found := checker.answers[probe.Key.source()]
	if !found || kept.key.End < probe.Key.End {
		checker.startLocked(probe)
	}
	// Newer is as good: data a period later still says the source is alive.
	if found && kept.key.End >= probe.Key.End-probe.Key.StepSeconds {
		return kept.verdict
	}
	return Verdict{Result: ResultUnknown, Reason: ReasonTimeout}
}

func (checker *Checker) startLocked(probe Probe) {
	if _, running := checker.inflight[probe.Key]; running {
		return
	}
	if len(checker.inflight) >= checker.maxInFlight {
		if checker.observer != nil {
			checker.observer.SourceLivenessDeferred(probe.Kind)
		}
		return
	}
	checker.inflight[probe.Key] = struct{}{}
	checker.running.Add(1)
	go checker.run(probe)
}

func (checker *Checker) run(probe Probe) {
	defer checker.running.Done()
	ctx, cancel := context.WithTimeout(checker.base, checker.timeout)
	started := time.Now()
	answer := checker.querier.ProbeSourceLiveness(ctx, probe)
	if answer.Err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		answer.Timeout = true
	}
	elapsed := time.Since(started)
	cancel()
	verdict := Classify(answer)
	if checker.observer != nil {
		checker.observer.SourceLivenessProbed(probe.Kind, elapsed)
	}
	checker.mu.Lock()
	defer checker.mu.Unlock()
	delete(checker.inflight, probe.Key)
	source := probe.Key.source()
	if kept, found := checker.answers[source]; found && kept.key.End > probe.Key.End {
		return
	}
	if _, found := checker.answers[source]; !found && len(checker.answers) >= checker.maxAnswers {
		checker.evictOldestLocked()
	}
	checker.answers[source] = answered{key: probe.Key, verdict: verdict}
}

// evictOldestLocked drops the kept answer whose window ends first, counted.
// A scan of at most MaxAnswers entries, only when a new source arrives at
// the bound.
func (checker *Checker) evictOldestLocked() {
	var oldest sourceOf
	end, found := int64(0), false
	for source, kept := range checker.answers {
		if !found || kept.key.End < end {
			oldest, end, found = source, kept.key.End, true
		}
	}
	if !found {
		return
	}
	delete(checker.answers, oldest)
	if checker.observer != nil {
		checker.observer.SourceLivenessEvicted()
	}
}

// Wait waits for the probes running now. For tests and shutdown.
func (checker *Checker) Wait() { checker.running.Wait() }
