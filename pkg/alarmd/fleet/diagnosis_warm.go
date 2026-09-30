// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// DiagnosisTiming is where one diagnosis page's time went, in milliseconds.
// Universe and View are present only on a page that read them: a later page
// of the same diagnosis answers from its cache, and a zero there would say it
// read them for nothing. Progress is present only where progress is wired.
// The response's own encoding and the hop from a follower are not in it;
// the follower's leader_forward_duration_seconds is the whole, and the
// difference is those two.
type DiagnosisTiming struct {
	UniverseMillis *int64 `json:"universe,omitempty"`
	ViewMillis     *int64 `json:"view,omitempty"`
	RowsMillis     int64  `json:"rows"`
	ProgressMillis *int64 `json:"progress,omitempty"`
}

// DiagnosisWarm is the warm-up this process ran when it took the catalog
// over: a first page read in full and thrown away, and how long each part
// of it took. That timing is the one a cold first page would have paid.
type DiagnosisWarm struct {
	At     time.Time       `json:"at"`
	Timing DiagnosisTiming `json:"timing_ms"`
	// Error is the read's failure word, when the universe could not be read.
	Error string `json:"error,omitempty"`
}

// DiagnosisWarmer runs one first page when this process becomes the one
// that answers diagnoses, so the first person to ask after a hand-over does
// not pay for the process's first reads.
//
// Each diagnosis keeps its own read by its id, and a new diagnosis always
// reads afresh; that is the contract, and warming the cache would not reach
// it. What is cold after a hand-over is underneath: the stores' connections,
// the fleet service's cached replica list and denominator, and the first
// decode of every replica's snapshot. The warm-up goes through exactly the
// readers a first page goes through, so it warms exactly those.
type DiagnosisWarmer struct {
	service    *Service
	lookup     StrategyLookupFunc
	universe   UniverseReader
	progress   ProgressReader
	now        func() time.Time
	stallAfter time.Duration

	// answering is whether the last Tick found the catalog here. A warm-up
	// runs on the tick it turns true, once per hand-over.
	answering atomic.Bool
	mu        sync.Mutex
	last      *DiagnosisWarm
}

// NewDiagnosisWarmer takes the same readers WithDiagnosis answers from.
func NewDiagnosisWarmer(service *Service, lookup StrategyLookupFunc, universe UniverseReader, progress ProgressReader,
	now func() time.Time, stallAfter time.Duration) *DiagnosisWarmer {
	if now == nil {
		now = time.Now
	}
	return &DiagnosisWarmer{service: service, lookup: lookup, universe: universe, progress: progress, now: now, stallAfter: stallAfter}
}

// Tick is called on a cadence. It returns the warm-up to run when this
// process has just come to hold the catalog, and nil otherwise; the caller
// decides where it runs, so a slow read does not hold the cadence up.
func (warmer *DiagnosisWarmer) Tick() func(context.Context) {
	if warmer == nil || warmer.lookup == nil || warmer.universe == nil {
		return nil
	}
	if !warmer.lookup("0").Available {
		warmer.answering.Store(false)
		return nil
	}
	if !warmer.answering.CompareAndSwap(false, true) {
		return nil
	}
	return func(ctx context.Context) { warmer.Warm(ctx) }
}

// Warm reads one first page, bounded as a first page's read is, and keeps
// its timing.
func (warmer *DiagnosisWarmer) Warm(ctx context.Context) DiagnosisWarm {
	at := warmer.now()
	readCtx, cancel := context.WithTimeout(ctx, DiagnosisReadTimeout)
	defer cancel()
	entry := readDiagnosisEntry(readCtx, warmer.service, warmer.universe, at, warmer.stallAfter)
	warm := DiagnosisWarm{At: at, Timing: entry.timing(), Error: entry.readError}
	if entry.readError == "" {
		ctx := newDiagnosisContext(entry.view, "", at)
		started := time.Now()
		page := buildDiagnosisPage(entry.universe, "", DiagnosisPageRows, func(id string) DiagnosisRow {
			return diagnoseStrategy(id, warmer.lookup(id), ctx)
		})
		warm.Timing.RowsMillis = time.Since(started).Milliseconds()
		if warmer.progress != nil {
			started = time.Now()
			_, _, _ = warmer.progress(readCtx, pageQueryGroups(page.Rows))
			warm.Timing.ProgressMillis = millisOf(time.Since(started))
		}
	}
	warmer.mu.Lock()
	warmer.last = &warm
	warmer.mu.Unlock()
	return warm
}

// Last is the latest warm-up, or nil before the first.
func (warmer *DiagnosisWarmer) Last() *DiagnosisWarm {
	if warmer == nil {
		return nil
	}
	warmer.mu.Lock()
	defer warmer.mu.Unlock()
	if warmer.last == nil {
		return nil
	}
	last := *warmer.last
	return &last
}

func millisOf(duration time.Duration) *int64 {
	millis := duration.Milliseconds()
	return &millis
}
