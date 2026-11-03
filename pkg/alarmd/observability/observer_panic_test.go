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
	"testing"
)

func panicsSince(before map[string]uint64) map[string]uint64 {
	delta := map[string]uint64{}
	for name, count := range ObserverPanicCounts() {
		if count != before[name] {
			delta[name] = count - before[name]
		}
	}
	return delta
}

// A panic in one observer of a fan-out is counted under its name, and every
// observer after it still receives the observation - also inside a fan-out
// that is itself a member, and also when the panicking member forwards to
// that inner fan-out before it panics, as the tracker does.
func TestAPanickingObserverLeavesTheOthersTheirObservation(t *testing.T) {
	received := map[string]int{}
	receiving := func(name string) Observer {
		return ObserverFunc(func(context.Context, Observation) { received[name]++ })
	}
	panicking := ObserverFunc(func(context.Context, Observation) { panic("observer defect") })
	inner := Multi(Named(ObserverRecorder, panicking), Named(ObserverLogging, receiving("logging")))
	tracker := ObserverFunc(func(ctx context.Context, observation Observation) {
		defer inner.Observe(ctx, observation)
		panic("tracker defect")
	})
	fanOut := Multi(Named(ObserverTracker, tracker), Named(ObserverAdditional, panicking),
		Named(ObserverTargetFlow, receiving("target_flow")), panicking, receiving("last"))

	before := ObserverPanicCounts()
	fanOut.Observe(context.Background(), Observation{Component: ComponentRuntime})
	if received["logging"] != 1 || received["target_flow"] != 1 || received["last"] != 1 {
		t.Fatalf("an observer after a panicking one missed the observation: %v", received)
	}
	want := map[string]uint64{ObserverRecorder: 1, ObserverTracker: 1, ObserverAdditional: 1, ObserverUnnamed: 1}
	got := panicsSince(before)
	if len(got) != len(want) {
		t.Fatalf("panics counted %v, want %v", got, want)
	}
	for name, n := range want {
		if got[name] != n {
			t.Fatalf("panics counted %v, want %v", got, want)
		}
	}
}

// A name outside the closed list, and the entry's own name, count as an
// unnamed member; a nil observer stays out of the fan-out.
func TestAnObserverIsCountedOnlyUnderANameOfTheList(t *testing.T) {
	panicking := ObserverFunc(func(context.Context, Observation) { panic("observer defect") })
	if Named(ObserverRecorder, nil) != nil {
		t.Fatal("a nil observer was given a name instead of staying nil")
	}
	before := ObserverPanicCounts()
	Multi(Named("made_up", panicking), Named(ObserverEntry, panicking), Named(ObserverLogging, nil)).Observe(context.Background(), Observation{})
	if got := panicsSince(before); len(got) != 1 || got[ObserverUnnamed] != 2 {
		t.Fatalf("panics counted %v, want two under %s", got, ObserverUnnamed)
	}
	if counts := ObserverPanicCounts(); len(counts) != len(ObserverNames) {
		t.Fatalf("counts name %d observers, want every one of %d", len(counts), len(ObserverNames))
	}
}
