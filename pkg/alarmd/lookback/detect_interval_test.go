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
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// unalignedFacts is the query of a Plan detected more often than it
// aggregates: read from where its request starts, each clause shifted a
// step less a millisecond forward so its buckets are labelled at their start.
func unalignedFacts(t *testing.T, functions ...execution.QueryFunction) execution.QueryPlanFacts {
	t.Helper()
	built := facts(t, minute, "", execution.QueryClause{TimeAggregation: execution.QueryFunction{Method: "avg_over_time", Window: "60s"},
		Functions: functions, Offset: "59999ms", OffsetForward: "true"})
	built.NotTimeAlign, built.QueryRevision = true, ""
	rebuilt, err := execution.BuildQueryPlanFacts(built)
	if err != nil {
		t.Fatal(err)
	}
	return rebuilt
}

// A forward offset reads later data for the same bucket: it shortens how far
// back a tail read has to start, where a backward time shift lengthens it.
// Read as backward, the forward shift of an unaligned query would start its
// tail reads a window and a step early - off its own grid by the millisecond
// short of a whole step.
func TestAForwardOffsetShortensATailReadsLookback(t *testing.T) {
	window := execution.QueryFunction{Method: "avg_over_time", Window: "60s"}
	for _, test := range []struct {
		offset, forward string
		want            time.Duration
	}{
		{offset: "", forward: "false", want: time.Minute},
		{offset: "3600s", forward: "false", want: time.Minute + time.Hour},
		{offset: "59999ms", forward: "true", want: time.Millisecond},
		{offset: "3600s", forward: "true", want: 0},
	} {
		got, known := queryLookback(execution.QueryPlanFacts{QueryList: []execution.QueryClause{{TimeAggregation: window, Offset: test.offset, OffsetForward: test.forward}}})
		if !known || got != test.want {
			t.Fatalf("offset %q forward %s: lookback %v (known %t), want %v", test.offset, test.forward, got, known, test.want)
		}
	}
}

// A Plan detected more often than it aggregates reads unaligned windows, and
// its rechecks read the same buckets at the same phase as its first read: a
// tail read starts a whole number of data steps before the kept tail, so
// where nothing arrives late the samples complete with nothing changed.
func TestAnUnalignedQuerysRechecksReadTheFirstReadsBuckets(t *testing.T) {
	// A whole second off the minute grid, as a Slot every fifteen seconds is.
	for name, query := range map[string]execution.QueryPlanFacts{
		"a window": unalignedFacts(t),
		// A lookback of a minute and a half past the forward shift: half a
		// step off unless rounded up to whole steps.
		"a function window of ninety seconds": unalignedFacts(t, execution.QueryFunction{Method: "moving_avg", Window: "90s"}),
	} {
		t.Run(name, func(t *testing.T) { unalignedRechecksReadTheFirstReadsBuckets(t, query) })
	}
}

func unalignedRechecksReadTheFirstReadsBuckets(t *testing.T, query execution.QueryPlanFacts) {
	w := &windowed{clock: &clock{at: time.Unix(1_700_006_015, 0)}, step: 60, probeFirst: true}
	engine := w.engine(t)
	for range 3 {
		w.run(t, engine, 2*time.Hour, query)
	}
	source := engine.Stats().Sources[sourceTimeSeries]
	if source.Samples[OutcomeCompleted] != 3 || source.Classes[ClassComplete] != 3 || source.ChangedWindows[RungNames[0]] != 0 ||
		source.Classes[ClassWindowReadEarly] != 0 || source.Classes[ClassPartialRevised] != 0 {
		t.Fatalf("samples %v classes %v changed %v, want three complete samples and nothing changed",
			source.Samples, source.Classes, source.ChangedWindows)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, spec := range w.specs {
		if phase := (spec.ProviderRange.End - spec.ProviderRange.Start) % w.step; phase != 0 {
			t.Fatalf("a recheck read %+v, %d s off its first read's buckets", spec.ProviderRange, phase)
		}
	}
}

// The unit a suggested delay is rounded to is the one the delay was: the data
// step for an aligned query, and for an unaligned one the schedule's step
// when that is shorter, which the next Slot of its frozen schedule says.
func TestASuggestedDelayIsRoundedAsTheDelayWas(t *testing.T) {
	aligned := execution.QueryPlanFacts{}
	unaligned := execution.QueryPlanFacts{NotTimeAlign: true}
	for _, test := range []struct {
		facts           execution.QueryPlanFacts
		slot, following execution.EvaluationTime
		want            time.Duration
	}{
		{facts: aligned, slot: 1_700_000_000, following: 1_700_000_015, want: time.Minute},
		{facts: unaligned, slot: 1_700_000_000, following: 1_700_000_015, want: 15 * time.Second},
		{facts: unaligned, slot: 1_700_000_000, following: 1_700_000_120, want: time.Minute},
		{facts: unaligned, slot: 1_700_000_000, following: 0, want: time.Minute},
	} {
		if got := delayUnitOf(test.facts, time.Minute, test.slot, test.following); got != test.want {
			t.Fatalf("unaligned %t, next Slot after %ds: unit %v, want %v", test.facts.NotTimeAlign, test.following-test.slot, got, test.want)
		}
	}
}

// A group read unaligned every fifteen seconds over one-minute windows had its
// delay rounded to fifteen seconds, and so are the delays it is advised: the
// read-early advice and the late-past-round advice alike. Rounded to the data
// step, both would advise a delay a whole minute longer than the one that
// reads the window whole.
func TestASteppedGroupsAdviceIsRoundedToItsDelaysUnit(t *testing.T) {
	early := ReadEarlySample{CompletionAgeSeconds: 40}
	state := &group{source: sourceTimeSeries, step: time.Minute, delayUnit: 15 * time.Second,
		readEarly:     &readEarlyState{delaySeconds: 30, recent: []readEarlyEntry{{early: true, sample: early}, {early: true, sample: early}}},
		latePastRound: &latePastRoundState{consecutive: latePastRoundRepeat, delaySeconds: 30, samples: []LatePastRoundSample{{SeenAgeSeconds: 10}}},
		seriesLate:    &seriesLateState{}}
	if reading, ok := readingOf("qg", state); !ok || reading.SuggestedDelaySeconds != 75 {
		t.Fatalf("read-early advice %+v (reported %t), want 30 + 40 rounded to 75", reading, ok)
	}
	if reading, ok := latePastRoundOf("qg", state); !ok || reading.SuggestedDelaySeconds != 45 {
		t.Fatalf("late-past-round advice %+v (reported %t), want 30 + 10 rounded to 45", reading, ok)
	}
}
