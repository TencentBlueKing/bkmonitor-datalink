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
	"errors"
	"sync"
	"time"
)

type WindowLogLimiterConfig struct {
	Window    time.Duration
	MaxEvents int
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
	key := logBucketKey{reason: observation.ReasonCode}
	if observation.stageReasonBucket {
		key.reason = ReasonNone
		key.stage = observation.Stage
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
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
