// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"sort"
	"sync"
	"time"
)

// How the Console's own speed bounds a request to it, for every caller of one
// client: calibration, the absent-close round, the drawer's reads. Nothing
// here is configured; the deadline follows what the Console has taken.
const (
	// consoleLatencySamples is how many recent requests the tail is read
	// from: enough that one slow answer moves the p90 but does not own it.
	consoleLatencySamples = 64
	// consoleLatencyColdStart is how many requests must be timed before the
	// p90 is trusted; below it the deadline is the caller's coverage.
	consoleLatencyColdStart = 8
	// consoleDeadlineTail is how many times the observed p90 one request may
	// take: p90 is where requests already are, and four times it lets a slow
	// answer finish without the deadline becoming the usual end of a request.
	consoleDeadlineTail = 4
)

// ConsoleLatency is the durations of a Console client's latest requests,
// recorded by the client itself: every request that got an HTTP answer,
// whatever its status, and every request cut by its own deadline, at what it
// took -- so the tail can rise past a deadline that cut it. A request that
// failed before any answer is counted apart (Unanswered): a burst of refused
// connections must not pull the tail to milliseconds and cut the Console's
// real answers once it is back.
type ConsoleLatency struct {
	mu      sync.Mutex
	samples []time.Duration
	next    int
	// unanswered counts the requests that failed before the Console
	// answered and before their deadline -- a connection refused, a name
	// that did not resolve, a reset -- which say nothing of how long the
	// Console takes and are kept out of the samples.
	unanswered uint64
}

// NoteUnanswered counts one request that failed before any answer and before
// its deadline.
func (latency *ConsoleLatency) NoteUnanswered() {
	latency.mu.Lock()
	latency.unanswered++
	latency.mu.Unlock()
}

// Unanswered is how many requests failed before any answer and before their
// deadline.
func (latency *ConsoleLatency) Unanswered() uint64 {
	latency.mu.Lock()
	defer latency.mu.Unlock()
	return latency.unanswered
}

// Observe records one request's duration.
func (latency *ConsoleLatency) Observe(took time.Duration) {
	latency.mu.Lock()
	defer latency.mu.Unlock()
	if len(latency.samples) < consoleLatencySamples {
		latency.samples = append(latency.samples, took)
		return
	}
	latency.samples[latency.next] = took
	latency.next = (latency.next + 1) % consoleLatencySamples
}

// Deadline is one request's deadline for work whose own deadline is coverage
// (calibration: the calibration interval; the absent-close round: the next
// tick): consoleDeadlineTail times the p90 of the latest requests, never more
// than coverage, past which waiting gains the work nothing. Before
// consoleLatencyColdStart requests are timed it is coverage itself.
func (latency *ConsoleLatency) Deadline(coverage time.Duration) time.Duration {
	latency.mu.Lock()
	sorted := append([]time.Duration(nil), latency.samples...)
	latency.mu.Unlock()
	if len(sorted) < consoleLatencyColdStart {
		return coverage
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	p90 := sorted[(len(sorted)*9+9)/10-1]
	return min(coverage, consoleDeadlineTail*p90)
}

type requestCoverageKey struct{}

// WithRequestCoverage asks the Console client to bound each of the call's
// requests by its Latency().Deadline(coverage), inside ctx's own deadline: a
// call that takes several requests gets a deadline per request, not one
// shared by them.
func WithRequestCoverage(ctx context.Context, coverage time.Duration) context.Context {
	return context.WithValue(ctx, requestCoverageKey{}, coverage)
}

func requestCoverage(ctx context.Context) (time.Duration, bool) {
	coverage, ok := ctx.Value(requestCoverageKey{}).(time.Duration)
	return coverage, ok && coverage > 0
}
