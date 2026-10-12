// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"container/list"
	"errors"
	"sync"
	"time"
)

// PacingLogBudget is how many lines a bucket keeps per window.
type PacingLogBudget struct {
	Window    time.Duration
	MaxEvents int
}

// LogAdmission is the result of asking a limiter whether one observation may
// be logged. Suppressed counts the lines this bucket dropped since its last
// admitted line; SuppressedEvicted counts lines dropped by scope buckets of
// the same reason that were evicted before they could report. Sampled marks
// a line admitted under the hourly sample (RoutineLogSample): the one line
// its bucket keeps per hour, written so that a reader can tell a quiet
// emitter from a dead one.
type LogAdmission struct {
	Allowed           bool
	Suppressed        uint64
	SuppressedEvicted uint64
	Sampled           bool
	// Candidate is an observation the policy decides a line for: written,
	// held back by its limiter, or counted and not written. A routine
	// success outside the workflow stages is not one.
	Candidate bool
	// Unwritten is a workflow stage's routine success, or a Control Leader
	// round that changed nothing: counted, never written.
	Unwritten bool
}

// RoutineLogSample is the budget of every result that is neither a success
// nor a failure -- degraded, retrying, paused, terminal, skipped, rejected:
// one line per (reason, stage, Query Group) per hour, carrying how many were
// merged into it since the previous one. Those results repeat every Slot
// while a condition lasts, and every one of them is already counted by its
// stage's metrics; the line is the positive control, so that a reading of
// none over a window can show the emitter was alive. A failure keeps the
// limiter's own, shorter window. The bound is fixed here and not a
// deployment parameter: the operator does not know better than the program
// how often a repeating word needs to be seen.
var RoutineLogSample = PacingLogBudget{Window: time.Hour, MaxEvents: 1}

// RepeatedLogLimiter is the admission contract BoundedLogPolicy uses for
// repeated transitions and exceptional results.
type RepeatedLogLimiter interface {
	Admit(Observation) LogAdmission
}

type logBucketKey struct {
	reason ReasonCode
	stage  Stage
}

func limiterBucketKey(observation Observation) logBucketKey {
	key := logBucketKey{reason: observation.ReasonCode}
	if observation.stageReasonBucket {
		key.reason = ReasonNone
		key.stage = observation.Stage
	}
	return key
}

// ScopedLogLimiterConfig bounds a limiter that buckets by reason (or stage)
// plus an object scope. MaxScopes caps the number of live scope buckets across
// all reasons; the least recently used bucket is evicted when the cap is hit.
type ScopedLogLimiterConfig struct {
	Window    time.Duration
	MaxEvents int
	MaxScopes int
}

// ScopedLogLimiter is a concurrency-safe fixed-window limiter. Observations
// are limited per (reason or stage, stage, Query Group), failures apart from
// the stage's other results, so one noisy object cannot hide every other
// object's coordinates and one stage cannot hide another's; a line with no
// Query Group has its stage's bucket. Every bucket counts the lines it
// suppressed and reports that count on the next admitted line so readers can
// tell how many events were merged. Memory is bounded by MaxScopes buckets;
// the closed set of reasons and stages is what a bucket may be keyed by.
type ScopedLogLimiter struct {
	mu sync.Mutex

	window    time.Duration
	maxEvents int
	maxScopes int
	now       func() time.Time

	known   map[logBucketKey]struct{}
	scoped  map[scopedLogBucketKey]*list.Element
	order   *list.List
	evicted map[logBucketKey]uint64
}

type scopedLogBucketKey struct {
	reason logBucketKey
	scope  string
}

type scopedLogBucket struct {
	key         scopedLogBucketKey
	windowStart time.Time
	used        int
	suppressed  uint64
}

func NewScopedLogLimiter(config ScopedLogLimiterConfig) (*ScopedLogLimiter, error) {
	return newScopedLogLimiter(config, time.Now)
}

func newScopedLogLimiter(config ScopedLogLimiterConfig, now func() time.Time) (*ScopedLogLimiter, error) {
	if config.Window <= 0 {
		return nil, errors.New("observability: log limiter window must be positive")
	}
	if config.MaxEvents <= 0 {
		return nil, errors.New("observability: log limiter capacity must be positive")
	}
	if config.MaxScopes <= 0 {
		return nil, errors.New("observability: log limiter scope bound must be positive")
	}
	if now == nil {
		return nil, errors.New("observability: log limiter clock is required")
	}
	known := make(map[logBucketKey]struct{}, len(AllLogReasons())+len(AllStages()))
	for _, reason := range AllLogReasons() {
		known[logBucketKey{reason: reason}] = struct{}{}
	}
	for _, stage := range AllStages() {
		known[logBucketKey{reason: ReasonNone, stage: stage}] = struct{}{}
	}
	return &ScopedLogLimiter{
		window: config.Window, maxEvents: config.MaxEvents, maxScopes: config.MaxScopes, now: now,
		known:   known,
		scoped:  make(map[scopedLogBucketKey]*list.Element, config.MaxScopes),
		order:   list.New(),
		evicted: make(map[logBucketKey]uint64),
	}, nil
}

func (l *ScopedLogLimiter) Admit(observation Observation) LogAdmission {
	if l == nil {
		return LogAdmission{}
	}
	observation = NormalizeObservation(observation)
	reasonKey := limiterBucketKey(observation)
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, listed := l.known[reasonKey]; !listed {
		return LogAdmission{}
	}
	now := l.now()
	// A failure keeps the limiter's window and capacity; any other result
	// keeps the hourly sample, and its admitted line is marked as that sample.
	window, maxEvents := l.window, l.maxEvents
	sampled := !failedResult(observation.Result)
	if sampled {
		window, maxEvents = RoutineLogSample.Window, RoutineLogSample.MaxEvents
	}
	// A bucket is per stage as well: a Slot that fails writes its query's
	// line and its own, and two stages sharing one bucket would hand a
	// reader who greps one stage nothing in the windows the other stage's
	// line came first -- the same reading a dead emitter gives.
	// A line with no Query Group has a bucket per stage too, rather than the
	// one bucket every stage's line without one shared. Failures keep a
	// bucket apart from the stage's other results: the two have different
	// windows, and a recovery must not be counted against the failures before
	// it.
	scope := observation.Trace.QueryGroupKey + "\x00" + string(observation.Stage)
	if !sampled {
		scope += "\x00failed"
	}
	bucket := l.bucketFor(reasonKey, scope)
	if bucket.windowStart.IsZero() || now.Before(bucket.windowStart) || now.Sub(bucket.windowStart) >= window {
		bucket.windowStart = now
		bucket.used = 0
	}
	if bucket.used >= maxEvents {
		bucket.suppressed++
		return LogAdmission{}
	}
	bucket.used++
	admission := LogAdmission{Allowed: true, Suppressed: bucket.suppressed, SuppressedEvicted: l.evicted[reasonKey], Sampled: sampled}
	bucket.suppressed = 0
	delete(l.evicted, reasonKey)
	return admission
}

func (l *ScopedLogLimiter) bucketFor(reasonKey logBucketKey, scope string) *scopedLogBucket {
	key := scopedLogBucketKey{reason: reasonKey, scope: scope}
	if element, ok := l.scoped[key]; ok {
		l.order.MoveToFront(element)
		return element.Value.(*scopedLogBucket)
	}
	for l.order.Len() >= l.maxScopes {
		oldest := l.order.Back()
		if oldest == nil {
			break
		}
		victim := oldest.Value.(*scopedLogBucket)
		if victim.suppressed > 0 {
			l.evicted[victim.key.reason] += victim.suppressed
		}
		l.order.Remove(oldest)
		delete(l.scoped, victim.key)
	}
	bucket := &scopedLogBucket{key: key}
	l.scoped[key] = l.order.PushFront(bucket)
	return bucket
}

// ScopeBuckets reports the number of live scope buckets; it exists for tests
// and diagnostics.
func (l *ScopedLogLimiter) ScopeBuckets() int {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.order.Len()
}
