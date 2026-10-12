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
	"context"
	"sync/atomic"
)

// Observation is a fail-open side channel: an observer that panics must not
// take the path it observes down with it, and must not take the other
// observers down either. A fan-out (Multi) therefore gives each of its
// observers the observation on its own, and a panic in one is recovered
// there and counted under that observer's name; the observers after it
// still receive the observation. Recovered and not counted, as it was, a
// panic was invisible, and every observer after the panicking one silently
// missed every observation it panicked on.

// The names an observer is counted under. Closed: a name outside the list
// counts as ObserverUnnamed.
const (
	ObserverTracker        = "tracker"
	ObserverRecorder       = "recorder"
	ObserverLogging        = "logging"
	ObserverAdditional     = "additional"
	ObserverTargetFlow     = "target_flow"
	ObserverRejectionTally = "rejection_tally"
	ObserverCostSummary    = "cost_summary"
	ObserverRetainedPeaks  = "retained_peaks"
	// ObserverUnnamed is an observer a fan-out was given without a name.
	ObserverUnnamed = "unnamed"
	// ObserverEntry is a panic outside any fan-out member: in the
	// observation's normalization, or in an observer a caller handed the
	// observation to directly.
	ObserverEntry = "entry"
)

// ObserverNames is every name a panic is counted under.
var ObserverNames = []string{ObserverTracker, ObserverRecorder, ObserverLogging, ObserverAdditional, ObserverTargetFlow,
	ObserverRejectionTally, ObserverCostSummary, ObserverRetainedPeaks, ObserverUnnamed, ObserverEntry}

var observerPanics = func() map[string]*atomic.Uint64 {
	counts := make(map[string]*atomic.Uint64, len(ObserverNames))
	for _, name := range ObserverNames {
		counts[name] = new(atomic.Uint64)
	}
	return counts
}()

// ObserverPanicCounts is how many panics each observer raised since the
// process started, every name present.
func ObserverPanicCounts() map[string]uint64 {
	counts := make(map[string]uint64, len(observerPanics))
	for name, count := range observerPanics {
		counts[name] = count.Load()
	}
	return counts
}

// RecoverObserverPanic recovers a panic and counts it under name. It has to
// be the deferred call itself (defer RecoverObserverPanic(name)): recover
// stops a panic only when the deferred function calls it directly.
func RecoverObserverPanic(name string) {
	if recover() == nil {
		return
	}
	count, known := observerPanics[name]
	if !known {
		count = observerPanics[ObserverUnnamed]
	}
	count.Add(1)
}

// namedObserver is an observer a fan-out counts its panics under name.
type namedObserver struct {
	name     string
	observer Observer
}

func (named namedObserver) Observe(ctx context.Context, observation Observation) {
	named.observer.Observe(ctx, observation)
}

// Named gives an observer the name its panics are counted under in a
// fan-out. A nil observer stays nil, so Multi still leaves it out.
func Named(name string, observer Observer) Observer {
	if observer == nil {
		return nil
	}
	if _, known := observerPanics[name]; !known || name == ObserverEntry {
		name = ObserverUnnamed
	}
	return namedObserver{name: name, observer: observer}
}

// observeIsolated gives one fan-out member the observation, recovering and
// counting a panic in it under name so the members after it still receive
// theirs. The observation travels by pointer to the member's own Observe:
// it is large, and each member takes one copy of it, as before the
// isolation.
func observeIsolated(ctx context.Context, name string, observer Observer, observation *Observation) {
	defer RecoverObserverPanic(name)
	observer.Observe(ctx, *observation)
}
