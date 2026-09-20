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

type WindowLogLimiterConfig struct {
	Window    time.Duration
	MaxEvents int
}

// LogAdmission is the result of asking a limiter whether one observation may
// be logged. Suppressed counts the lines this bucket dropped since its last
// admitted line; SuppressedEvicted counts lines dropped by scope buckets of
// the same reason that were evicted before they could report.
type LogAdmission struct {
	Allowed           bool
	Suppressed        uint64
	SuppressedEvicted uint64
}

// RepeatedLogLimiter is the admission contract BoundedLogPolicy uses for
// repeated transitions and exceptional results.
type RepeatedLogLimiter interface {
	Admit(Observation) LogAdmission
}

// WindowLogLimiter is a concurrency-safe, constant-memory fixed-window
// limiter. Its window and capacity must be supplied by the caller; M8 does not
// define production defaults before G3 calibration.
type WindowLogLimiter struct {
	mu sync.Mutex

	window    time.Duration
	maxEvents int
	now       func() time.Time
	buckets   map[logBucketKey]windowLogBucket
}

type logBucketKey struct {
	reason ReasonCode
	stage  Stage
}

type windowLogBucket struct {
	windowStart time.Time
	used        int
}

func NewWindowLogLimiter(config WindowLogLimiterConfig) (*WindowLogLimiter, error) {
	return newWindowLogLimiter(config, time.Now)
}

func newWindowLogLimiter(config WindowLogLimiterConfig, now func() time.Time) (*WindowLogLimiter, error) {
	if config.Window <= 0 {
		return nil, errors.New("observability: log limiter window must be positive")
	}
	if config.MaxEvents <= 0 {
		return nil, errors.New("observability: log limiter capacity must be positive")
	}
	if now == nil {
		return nil, errors.New("observability: log limiter clock is required")
	}
	buckets := make(map[logBucketKey]windowLogBucket, len(AllLogReasons())+len(AllStages()))
	for _, reason := range AllLogReasons() {
		buckets[logBucketKey{reason: reason}] = windowLogBucket{}
	}
	for _, stage := range AllStages() {
		buckets[logBucketKey{reason: ReasonNone, stage: stage}] = windowLogBucket{}
	}
	return &WindowLogLimiter{window: config.Window, maxEvents: config.MaxEvents, now: now, buckets: buckets}, nil
}

func (l *WindowLogLimiter) Allow(observation Observation) bool {
	if l == nil {
		return false
	}
	observation = NormalizeObservation(observation)
	key := limiterBucketKey(observation)
	l.mu.Lock()
	defer l.mu.Unlock()
	// Sample in admission order so delayed callers cannot look like a clock rollback.
	now := l.now()
	bucket, ok := l.buckets[key]
	if !ok {
		return false
	}
	if bucket.windowStart.IsZero() || now.Before(bucket.windowStart) || now.Sub(bucket.windowStart) >= l.window {
		bucket.windowStart = now
		bucket.used = 0
	}
	if bucket.used >= l.maxEvents {
		return false
	}
	bucket.used++
	l.buckets[key] = bucket
	return true
}

// Admit keeps the phase-one behaviour: fixed reason/stage buckets and no
// suppressed-count reporting.
func (l *WindowLogLimiter) Admit(observation Observation) LogAdmission {
	return LogAdmission{Allowed: l.Allow(observation)}
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

// ScopedLogLimiter is a concurrency-safe fixed-window limiter with two bucket
// layers. Observations that carry a Query Group coordinate are limited per
// (reason or stage, query group) so one noisy object cannot hide every other
// object's coordinates; observations without a Query Group fall back to the
// fixed reason/stage buckets. Every bucket counts the lines it suppressed and
// reports that count on the next admitted line so readers can tell how many
// events were merged. Memory is bounded by MaxScopes scope buckets plus the
// fixed reason/stage set.
type ScopedLogLimiter struct {
	mu sync.Mutex

	window    time.Duration
	maxEvents int
	maxScopes int
	now       func() time.Time

	fixed   map[logBucketKey]*scopedLogBucket
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
	fixed := make(map[logBucketKey]*scopedLogBucket, len(AllLogReasons())+len(AllStages()))
	for _, reason := range AllLogReasons() {
		fixed[logBucketKey{reason: reason}] = &scopedLogBucket{}
	}
	for _, stage := range AllStages() {
		fixed[logBucketKey{reason: ReasonNone, stage: stage}] = &scopedLogBucket{}
	}
	return &ScopedLogLimiter{
		window: config.Window, maxEvents: config.MaxEvents, maxScopes: config.MaxScopes, now: now,
		fixed:   fixed,
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
	if _, known := l.fixed[reasonKey]; !known {
		return LogAdmission{}
	}
	now := l.now()
	bucket := l.bucketFor(reasonKey, observation.Trace.QueryGroupKey)
	if bucket.windowStart.IsZero() || now.Before(bucket.windowStart) || now.Sub(bucket.windowStart) >= l.window {
		bucket.windowStart = now
		bucket.used = 0
	}
	if bucket.used >= l.maxEvents {
		bucket.suppressed++
		return LogAdmission{}
	}
	bucket.used++
	admission := LogAdmission{Allowed: true, Suppressed: bucket.suppressed, SuppressedEvicted: l.evicted[reasonKey]}
	bucket.suppressed = 0
	delete(l.evicted, reasonKey)
	return admission
}

func (l *ScopedLogLimiter) bucketFor(reasonKey logBucketKey, scope string) *scopedLogBucket {
	if scope == "" {
		return l.fixed[reasonKey]
	}
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
