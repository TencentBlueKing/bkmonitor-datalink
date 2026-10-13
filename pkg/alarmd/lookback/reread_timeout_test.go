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
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// budgets records how long each read of a query again was given, by the
// real clock its deadline is set on.
type budgets struct {
	mu   sync.Mutex
	read []time.Duration
}

func (b *budgets) watch(f *fixture) {
	recheck := f.engine.options.Recheck
	f.engine.options.Recheck = func(ctx context.Context, spec execution.PhysicalQuerySpec, sink execution.ProviderSeriesSink) (execution.ProviderCompletion, error) {
		if deadline, ok := ctx.Deadline(); ok {
			b.mu.Lock()
			b.read = append(b.read, time.Until(deadline))
			b.mu.Unlock()
		}
		return recheck(ctx, spec, sink)
	}
}

func (b *budgets) last(t *testing.T) time.Duration {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.read) == 0 {
		t.Fatal("no read of a query again was made")
	}
	return b.read[len(b.read)-1]
}

// near says whether a deadline read off the real clock was set to want: it
// can only have run down since, by the little the test took in between.
func near(got, want time.Duration) bool {
	return got <= want && got > want-time.Second
}

// timedFirstRead is queryGroup's formal first read of the Slot at slot,
// taking took and ending with completeness.
func (f *fixture) timedFirstRead(queryGroup string, slot int64, took time.Duration, completeness execution.Completeness) time.Time {
	f.t.Helper()
	begun := f.clock.now()
	read := f.engine.Begin(query(queryGroup, slot, minute, sourceLog))
	if read == nil {
		f.t.Fatalf("%s's first read at %d was not seen", queryGroup, slot)
	}
	read.Series(point(slot, "1"), 100)
	f.clock.set(begun.Add(took))
	read.Complete(execution.ProviderCompletion{Completeness: completeness}, nil)
	return begun
}

// A recheck is given what its Query Group's query has taken: a fixed five
// seconds cut off every recheck of a query slower than that, so the groups
// most expensive to read were the ones never measured. A first read that was
// not whole says nothing about how long a whole one takes, and a quick group
// keeps the floor.
func TestARecheckIsGivenWhatItsQueryHasTaken(t *testing.T) {
	f := newFixture(t)
	var given budgets
	given.watch(f)

	slot := f.clock.now().Unix()
	f.timedFirstRead("qg", slot, 50*time.Second, execution.CompletenessPartial)
	slot = f.clock.now().Unix()
	readAt := f.timedFirstRead("qg", slot, 30*time.Second, execution.CompletenessFull)
	f.recheck(sourceLog, readAt, 0, minute, full(point(slot, "1")), RecheckCompared)
	if got := given.last(t); !near(got, 30*time.Second) {
		t.Fatalf("a recheck of a query whose whole first read took 30s, and whose partial one 50s, was given %v", got)
	}

	slot = f.clock.now().Unix()
	readAt = f.timedFirstRead("qg-b", slot, time.Second, execution.CompletenessFull)
	f.recheck(sourceLog, readAt, 0, minute, full(point(slot, "1")), RecheckCompared)
	if got := given.last(t); !near(got, RecheckTimeout) {
		t.Fatalf("a recheck of a query that took 1s was given %v, want the %v floor", got, RecheckTimeout)
	}
}

// A directed Query Group's early read and its read at the rung read the same
// query again, and are given what it has taken too.
func TestADirectedGroupsReadsAreGivenWhatItsQueryHasTaken(t *testing.T) {
	f, supplement := earlyFixture(t, SupplementOutcome{Ran: true, Held: 50 * time.Millisecond,
		Facts: execution.SupplementFacts{Candidates: 1, Admitted: 1, Points: 1}})
	var given budgets
	given.watch(f)
	slot := f.clock.now().Unix()
	took := 20 * time.Second
	readAt := f.earlyFirstRead(slot, took)

	f.clock.set(f.engine.nextEarlyWake())
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].EarlyReads[EarlyBeforeNext] == 1 })
	if len(supplement.taken()) != 1 {
		t.Fatalf("jobs %+v, want the early read's supplement", supplement.taken())
	}
	if got := given.last(t); !near(got, took) {
		t.Fatalf("an early read of a query that took %v was given %v", took, got)
	}

	f.clock.set(readAt.Add(rungDelay(0, minute)))
	f.answers <- delivered(point(slot, "1"), dataset("h2", map[int64]string{slot - 60: "5"}))
	f.engine.Step(context.Background())
	f.waitFor(func(stats Stats) bool { return stats.Sources[sourceLog].SupplementWindows[DirectedSupplemented] == 1 })
	if got := given.last(t); !near(got, took) {
		t.Fatalf("a directed read of a query that took %v was given %v", took, got)
	}
}
