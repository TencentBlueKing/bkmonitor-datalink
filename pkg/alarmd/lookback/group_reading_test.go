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
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func groupSample(f *fixture, name string, hold time.Duration, first func(int64) []*execution.Dataset, later func(int64) []*execution.Dataset) {
	f.t.Helper()
	qg := execution.QueryGroupIdentity(name)
	if _, exists := f.engine.GroupReading(qg); exists {
		f.rest(qg)
	}
	slot := f.clock.now().Unix()
	q := query(name, slot, minute, sourceLog)
	q.Contract.ReadHoldMillis = hold.Milliseconds()
	f.capture(q, first(slot)...)
	readAt := f.clock.now()
	for rung, pending := f.rung(qg); pending; rung, pending = f.rung(qg) {
		f.recheck(sourceLog, readAt, rung, minute, full(later(slot)...), RecheckCompared)
	}
}

func TestGroupReadingsSeparateSameSourceGroupsAndFrozenReadHolds(t *testing.T) {
	f := newFixture(t)
	firstAt := f.clock.now()
	value := func(text string) func(int64) []*execution.Dataset {
		return func(slot int64) []*execution.Dataset { return []*execution.Dataset{point(slot, text)} }
	}
	groupSample(f, "qg", 0, value("1"), value("2"))
	groupSample(f, "qg-b", 0, value("3"), value("3"))
	a, found := f.engine.GroupReading("qg")
	b, foundB := f.engine.GroupReading("qg-b")
	if !found || !foundB || a.Source != sourceLog || b.Source != sourceLog || !a.Since.Equal(firstAt) || !b.Since.After(a.Since) {
		t.Fatalf("groups a=%+v b=%+v", a, b)
	}
	if a.Compared["h0"][RungNames[0]] != 1 || a.ChangedWindows["h0"][RungNames[0]] != 1 ||
		b.Compared["h0"][RungNames[0]] != 1 || b.ChangedWindows["h0"][RungNames[0]] != 0 ||
		a.Classes[ClassWindowReadEarly] != 1 || b.Classes[ClassComplete] != 1 {
		t.Fatalf("same-source groups were not counted independently: a=%+v b=%+v", a, b)
	}
	groupSample(f, "qg", time.Minute, value("2"), value("2"))
	after, _ := f.engine.GroupReading("qg")
	unchanged, _ := f.engine.GroupReading("qg-b")
	if after.Compared["h_positive"][RungNames[0]] != 1 || after.ChangedWindows["h_positive"][RungNames[0]] != 0 ||
		!reflect.DeepEqual(after.Compared["h0"], a.Compared["h0"]) || !reflect.DeepEqual(after.ChangedWindows["h0"], a.ChangedWindows["h0"]) ||
		after.Classes[ClassComplete] != 1 || !reflect.DeepEqual(unchanged, b) {
		t.Fatalf("positive hold changed h0 or another group: after=%+v b=%+v", after, unchanged)
	}
	// Mutating a returned dictionary cannot alter the engine's counters.
	after.Compared["h_positive"][RungNames[0]], after.Classes[ClassComplete] = 999, 999
	copy, _ := f.engine.GroupReading("qg")
	if copy.Compared["h_positive"][RungNames[0]] != 1 || copy.Classes[ClassComplete] != 1 {
		t.Fatal("a GroupReading exposed mutable engine state")
	}
}

func TestGroupClassesAndIgnoredFindingsAreClosedAndIndependent(t *testing.T) {
	f := newFixture(t)
	groupSample(f, "qg", 0, func(slot int64) []*execution.Dataset {
		return []*execution.Dataset{point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "1"})}
	}, func(slot int64) []*execution.Dataset {
		return []*execution.Dataset{point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "3"})}
	})
	groupSample(f, "qg-b", 0, func(slot int64) []*execution.Dataset {
		return []*execution.Dataset{point(slot, "1")}
	}, func(int64) []*execution.Dataset { return nil })
	a, _ := f.engine.GroupReading("qg")
	b, _ := f.engine.GroupReading("qg-b")
	if a.Classes[ClassPartialRevised] != 1 || a.ReadHoldIgnored[ClassPartialRevised] != 1 || a.ReadHoldIgnored[IgnoredNoWholeWindowArrival] != 0 ||
		b.Classes[ClassPartialRevised] != 0 || b.ReadHoldIgnored[ClassPartialRevised] != 0 || b.ReadHoldIgnored[IgnoredNoWholeWindowArrival] != 1 {
		t.Fatalf("closed classes/ignored findings leaked between groups: a=%+v b=%+v", a, b)
	}
	if len(a.Classes) != len(SampleClasses) || len(a.ReadHoldIgnored) != len(ReadHoldIgnoredReasons) || len(a.EarlierReads) != len(EarlierReadOutcomes) {
		t.Fatal("a zero cell is missing from a closed dictionary")
	}
}

func TestGroupEarlierOutcomesAndBytesUseTheExistingReads(t *testing.T) {
	f := newFixture(t)
	f.engine.options.OnEarlierRead = func(EarlierReadEvidence) {}
	q := heldQuery(f)
	f.engine.Prepare(q)
	trial := f.group("qg").prepared
	f.clock.set(trial.at)
	f.answers <- full(point(int64(q.Contract.Slot.EvaluationTime), "1"))
	f.engine.StepEarly(context.Background())
	f.waitFor(func(Stats) bool {
		reading, _ := f.engine.GroupReading("qg")
		return reading.EarlierReadBytes == 100
	})
	f.clock.set(q.ReadyAt)
	read := f.engine.Begin(q)
	read.Series(point(int64(q.Contract.Slot.EvaluationTime), "1"), 100)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	other := heldQuery(f)
	other.Contract.Slot.QueryGroup, other.Spec.Digest = "qg-b", "qg-b-query"
	f.engine.Prepare(other)
	f.clock.set(f.group("qg-b").prepared.at)
	f.set(func() { f.refuse = "waiters" })
	f.engine.StepEarly(context.Background())
	f.clock.set(other.ReadyAt)
	read = f.engine.Begin(other)
	read.Series(point(int64(other.Contract.Slot.EvaluationTime), "1"), 100)
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	a, _ := f.engine.GroupReading("qg")
	b, _ := f.engine.GroupReading("qg-b")
	if a.EarlierReads[EarlierEqual] != 1 || a.EarlierReads[EarlierPermitRefused] != 0 || a.EarlierReadBytes != 100 ||
		b.EarlierReads[EarlierEqual] != 0 || b.EarlierReads[EarlierPermitRefused] != 1 || b.EarlierReadBytes != 0 {
		t.Fatalf("earlier outcomes/bytes leaked: a=%+v b=%+v", a, b)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.specs) != 1 {
		t.Fatalf("group counters added reads: %d", len(f.specs))
	}
}

func TestGroupReadingIsOwnedAndForgetClearsTheCumulativeFacts(t *testing.T) {
	f := newFixture(t)
	groupSample(f, "qg", 0, func(slot int64) []*execution.Dataset { return []*execution.Dataset{point(slot, "1")} },
		func(slot int64) []*execution.Dataset { return []*execution.Dataset{point(slot, "2")} })
	before, _ := f.engine.GroupReading("qg")
	f.set(func() { f.owned["qg"] = false })
	if _, found := f.engine.GroupReading("qg"); found {
		t.Fatal("an unowned group supplied a reading")
	}
	f.engine.Forget("qg")
	f.set(func() { f.owned["qg"] = true })
	if _, found := f.engine.GroupReading("qg"); found {
		t.Fatal("Forget retained a group's counters")
	}
	f.capture(query("qg", f.clock.now().Unix(), minute, sourceLog), point(f.clock.now().Unix(), "2"))
	after, found := f.engine.GroupReading("qg")
	if !found || !after.Since.After(before.Since) || after.Classes[ClassWindowReadEarly] != 0 || after.Compared["h0"][RungNames[0]] != 0 {
		t.Fatalf("reowned group inherited old counters: %+v", after)
	}
	if f.engine.Stats().Sources[sourceLog].Classes[ClassWindowReadEarly] != 1 {
		t.Fatal("Forget incorrectly cleared the source aggregate")
	}
}

func TestGroupCountersHaveOnlyClosedDimensions(t *testing.T) {
	var counts groupCounts
	for _, dimension := range []struct {
		name          string
		actual, words int
	}{
		{"compared hold", len(counts.compared), len(GroupReadHoldClasses)},
		{"changed hold", len(counts.changed), len(GroupReadHoldClasses)},
		{"compared rung", len(counts.compared[0]), len(RungNames)},
		{"changed rung", len(counts.changed[0]), len(RungNames)},
		{"classes", len(counts.classes), len(SampleClasses)},
		{"ignored", len(counts.ignored), len(ReadHoldIgnoredReasons)},
		{"earlier", len(counts.earlier), len(EarlierReadOutcomes)},
	} {
		if dimension.actual != dimension.words {
			t.Fatalf("fixed %s dimension = %d, labels = %d", dimension.name, dimension.actual, dimension.words)
		}
	}
	f := newFixture(t)
	f.capture(query("qg", f.clock.now().Unix(), minute, sourceLog), point(f.clock.now().Unix(), "1"))
	reading, _ := f.engine.GroupReading("qg")
	if len(reading.Compared) != len(GroupReadHoldClasses) || len(reading.ChangedWindows) != len(GroupReadHoldClasses) {
		t.Fatal("comparison dictionaries have an unbounded hold dimension")
	}
	for _, hold := range GroupReadHoldClasses {
		if len(reading.Compared[hold]) != len(RungNames) || len(reading.ChangedWindows[hold]) != len(RungNames) {
			t.Fatal("comparison dictionaries have an unbounded rung dimension")
		}
	}
}

func TestGroupReadingOwnershipCanReenterStatsAndForget(t *testing.T) {
	f := newFixture(t)
	f.capture(query("qg", f.clock.now().Unix(), minute, sourceLog), point(f.clock.now().Unix(), "1"))
	called := false
	f.engine.options.Owns = func(qg execution.QueryGroupIdentity) bool {
		if !called {
			called = true
			f.engine.Stats()
			f.engine.Forget(qg)
		}
		return false
	}
	done := make(chan bool, 1)
	go func() {
		_, found := f.engine.GroupReading("qg")
		done <- found
	}()
	select {
	case found := <-done:
		if found || !called {
			t.Fatal("an owner-lost group supplied a reading")
		}
	case <-time.After(time.Second):
		t.Fatal("GroupReading held the engine lock across its ownership callback")
	}
}

// GroupSources names the source of each group the lookback has seen, and
// leaves out one it has not.
func TestGroupSourcesNamesTheGroupsSeen(t *testing.T) {
	f := newFixture(t)
	f.classSample(0, []*execution.Dataset{point(f.clock.now().Unix(), "1")}, func(slot int64) []*execution.Dataset {
		return []*execution.Dataset{point(slot, "1")}
	})
	sources := f.engine.GroupSources([]execution.QueryGroupIdentity{"qg", "unseen"})
	if len(sources) != 1 || sources["qg"] != sourceLog {
		t.Fatalf("sources %v, want qg under %s and nothing for the group never seen", sources, sourceLog)
	}
	var none *Engine
	if got := none.GroupSources([]execution.QueryGroupIdentity{"qg"}); len(got) != 0 {
		t.Fatalf("a process without a lookback named %v", got)
	}
}

// A sample's hold of a part of a second counts as the whole second it
// reaches into, so the advice it goes into is never short.
func TestASamplesHoldIsCountedInWholeSecondsRoundedUp(t *testing.T) {
	for millis, want := range map[int64]int64{0: 0, 1: 1, 999: 1, 1000: 1, 45_000: 45, 45_500: 46} {
		if got := holdSeconds(millis); got != want {
			t.Errorf("holdSeconds(%d) = %d, want %d", millis, got, want)
		}
	}
}
