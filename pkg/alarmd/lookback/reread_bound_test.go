// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
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

// A group read here answers how long its query takes, never less than
// RecheckTimeout; a group never read here, as one just taken over, answers
// nothing rather than a guess.
func TestRereadBoundIsTheGroupsOwnReading(t *testing.T) {
	f := newFixture(t)
	if _, ok := f.engine.RereadBound("qg"); ok {
		t.Fatal("a group never read has a reading")
	}
	q := heldQuery(f)
	f.engine.mu.Lock()
	state := f.engine.groupLocked(q, f.clock.now())
	f.engine.mu.Unlock()
	if _, ok := f.engine.RereadBound("qg"); ok {
		t.Fatal("a group with no whole first read has a reading")
	}
	f.engine.mu.Lock()
	state.requests.add(8 * time.Second)
	f.engine.mu.Unlock()
	if got, ok := f.engine.RereadBound("qg"); !ok || got != 8*time.Second {
		t.Fatalf("slow group: %v %v, want 8s", got, ok)
	}
	f.engine.mu.Lock()
	state.requests = durations{}
	state.requests.add(2 * time.Second)
	f.engine.mu.Unlock()
	if got, ok := f.engine.RereadBound("qg"); !ok || got != RecheckTimeout {
		t.Fatalf("quick group: %v %v, want RecheckTimeout", got, ok)
	}
	var none *Engine
	if _, ok := none.RereadBound("qg"); ok {
		t.Fatal("no engine has a reading")
	}
}

// The query duration is the group's reading as measured, with no floor: a
// quick query reads as quick. A group never read, or never read whole, has
// none.
func TestQueryDurationIsTheGroupsReadingAsMeasured(t *testing.T) {
	f := newFixture(t)
	if _, ok := f.engine.QueryDuration("qg"); ok {
		t.Fatal("a group never read has a reading")
	}
	q := heldQuery(f)
	f.engine.mu.Lock()
	state := f.engine.groupLocked(q, f.clock.now())
	f.engine.mu.Unlock()
	if _, ok := f.engine.QueryDuration("qg"); ok {
		t.Fatal("a group with no whole first read has a reading")
	}
	f.engine.mu.Lock()
	state.firstReads.add(300 * time.Millisecond)
	state.requests.add(300 * time.Millisecond)
	f.engine.mu.Unlock()
	if got, ok := f.engine.QueryDuration("qg"); !ok || got != 300*time.Millisecond {
		t.Fatalf("quick group: %v %v, want 300ms", got, ok)
	}
	if got, _ := f.engine.RereadBound("qg"); got != RecheckTimeout {
		t.Fatalf("the reread bound %v, want RecheckTimeout still", got)
	}
	var none *Engine
	if _, ok := none.QueryDuration("qg"); ok {
		t.Fatal("no engine has a reading")
	}
}

// A Slot read in slices is one first read: its query takes from its first
// slice's start to its last slice's end, slices read side by side not
// summed, while one request read again is bounded by its longest slice.
func TestASlicedSlotIsOneFirstRead(t *testing.T) {
	f := newFixture(t)
	q := heldQuery(f)
	start := f.clock.now()
	before := f.engine.Stats().Sources[sourceLog].FirstReads
	slice := func(pattern string) *Read {
		sliced := q
		sliced.Slice = sliceOf(pattern)
		return f.engine.Begin(sliced)
	}
	a := slice("^a$")
	f.clock.set(start.Add(10 * time.Second))
	b := slice("^b$")
	a.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	f.clock.set(start.Add(30 * time.Second))
	b.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	if got, ok := f.engine.QueryDuration("qg"); !ok || got != 30*time.Second {
		t.Fatalf("query duration %v %v, want the Slot's 30 s", got, ok)
	}
	if got, _ := f.engine.RereadBound("qg"); got != max(RecheckTimeout, 20*time.Second) {
		t.Fatalf("reread bound %v, want the longest slice's 20 s", got)
	}
	if got := f.engine.Stats().Sources[sourceLog].FirstReads - before; got != 1 {
		t.Fatalf("first reads counted %d, want the Slot's one", got)
	}
	// The next Slot's slices are a reading of their own.
	next := q
	next.Contract.Slot.EvaluationTime += 60
	next.Slice = sliceOf("^a$")
	f.clock.set(start.Add(time.Minute))
	read := f.engine.Begin(next)
	f.clock.set(start.Add(time.Minute + 5*time.Second))
	read.Complete(execution.ProviderCompletion{Completeness: execution.CompletenessFull}, nil)
	f.engine.mu.Lock()
	readings := f.engine.groups["qg"].firstReads
	f.engine.mu.Unlock()
	if readings.count != 2 || readings.longest() != 30*time.Second {
		t.Fatalf("readings %+v, want the two Slots' 30 s and 5 s", readings)
	}
}
