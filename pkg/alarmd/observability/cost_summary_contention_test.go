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
	"fmt"
	"sync"
	"testing"
	"time"
)

// holdRanking makes the next Publish stop between its copy and its ranking,
// and returns once it has: the caller then acts while the ranking is held,
// and calls release to let it finish. The wait is on a channel the Publish
// closes, so nothing here depends on how long anything takes.
func holdRanking(t *testing.T, c *CostSummary, at time.Time) (release func()) {
	t.Helper()
	ranking, resume, published := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c.rankingStarted = func() {
		close(ranking)
		<-resume
	}
	go func() {
		c.Publish(at)
		close(published)
	}()
	<-ranking
	return func() {
		close(resume)
		<-published
		c.rankingStarted = nil
	}
}

// finishes fails the test when call does not return while the ranking is
// held. A correct summary returns at once; the bound only turns a holder
// that waits on the ranking into a failure instead of a hung test.
func finishes(t *testing.T, release func(), what string, call func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		call()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		release()
		t.Fatalf("%s waited for the publication's ranking", what)
	}
}

// The ranking is in proportion to the roster, and it was done holding the
// lock every observation takes: whatever arrived while it ran was dropped.
// Held at the ranking, every group's account is free -- an observation for
// any object is counted and none is lost -- and the published snapshot, the
// peaks and a reconciliation are all answered without waiting for it.
func TestObservationsAreCountedWhilePublishRanks(t *testing.T) {
	c, now, g := costFixture()
	other := g
	other.QueryGroupKey = "other"
	c.Reconcile([]CostGroup{g, other}, true)
	ctx := context.Background()

	release := holdRanking(t, c, *now)
	for _, key := range []string{"shared", "other"} {
		o := costObservation(StageSlotCompleted)
		o.Trace.QueryGroupKey = key
		finishes(t, release, "an observation", func() { c.Observe(ctx, o) })
	}
	finishes(t, release, "a snapshot read", func() { _ = c.Snapshot() })
	finishes(t, release, "a reconciliation", func() { c.Reconcile([]CostGroup{g, other}, true) })
	release()

	if dropped := c.Snapshot().Coverage.ContentionDroppedTotal; dropped != 0 {
		t.Fatalf("contention dropped %d observations made while the ranking ran, want 0", dropped)
	}
	c.Publish(*now)
	s := c.Snapshot()
	for _, key := range []string{"shared", "other"} {
		found := false
		for _, row := range s.Contributors {
			if row.Scope == "query_group" && row.Group.QueryGroupKey == key {
				found = true
				if row.Current.RunReturns != 1 {
					t.Fatalf("%s counted %d run returns, want the 1 made while the ranking ran", key, row.Current.RunReturns)
				}
			}
		}
		if !found {
			t.Fatalf("%s has no row: the observation made while the ranking ran was lost", key)
		}
	}
}

// One Publish reads one roster. A reconciliation that replaces it while the
// ranking runs does not reach the publication: its coverage is the roster its
// windows were copied from, so the objects it counts as observed are never
// more than the objects it counts as tracked.
func TestAPublicationReadsOneRoster(t *testing.T) {
	c, now, g := costFixture()
	other := g
	other.QueryGroupKey = "other"
	c.Reconcile([]CostGroup{g, other}, true)
	ctx := context.Background()
	for _, key := range []string{"shared", "other"} {
		o := costObservation(StageSlotCompleted)
		o.Trace.QueryGroupKey = key
		c.Observe(ctx, o)
	}

	release := holdRanking(t, c, *now)
	c.Reconcile([]CostGroup{g}, true)
	release()

	coverage := c.Snapshot().Coverage
	if coverage.TotalGroups != 2 || coverage.TrackedGroups != 2 || coverage.ObservedGroups != 2 {
		t.Fatalf("coverage = %d total, %d tracked, %d observed; want 2, 2, 2 from the one roster the windows were copied from", coverage.TotalGroups, coverage.TrackedGroups, coverage.ObservedGroups)
	}
	c.Publish(*now)
	if coverage := c.Snapshot().Coverage; coverage.TrackedGroups != 1 || coverage.ObservedGroups != 1 {
		t.Fatalf("next publication = %d tracked, %d observed; want the new roster's 1 and 1", coverage.TrackedGroups, coverage.ObservedGroups)
	}
	// The copy kept for the next tick holds windows, not the roster they
	// came from: a replaced roster is not kept alive by it.
	for index, entry := range c.copies {
		if entry.group != nil || entry.plan != nil {
			t.Fatalf("copy %d still refers to its roster after the publication", index)
		}
	}
}

// A reconciliation that keeps a group's revisions and its Plans keeps their
// counts: the group's account and each kept Plan's windows are the same ones,
// so what was counted before it is still there after it, and what is counted
// after it adds to the same numbers.
func TestAReconciliationKeepsTheCountsOfWhatItKeeps(t *testing.T) {
	c, now, g := costFixture()
	ctx := context.Background()
	evaluate := func() {
		o := costObservation(StageEvaluationCompleted)
		o.EvaluationOwner, o.EvaluationRecordsKnown, o.Counts.Records = costA, true, 10
		c.Observe(ctx, o)
	}
	evaluate()
	c.Reconcile([]CostGroup{g}, true)
	evaluate()
	c.Publish(*now)
	s := c.Snapshot()
	group, plan := costRow(t, s, "query_group", CostPlanIdentity{}), costRow(t, s, "strategy_owned", costA)
	if group.Current.Evaluations != 2 || plan.Current.Evaluations != 2 || plan.Current.EvaluationRecords != 20 {
		t.Fatalf("across a reconciliation that kept them: group %d evaluations, Plan %d evaluations and %d records; want 2, 2 and 20",
			group.Current.Evaluations, plan.Current.Evaluations, plan.Current.EvaluationRecords)
	}
}

// An observation is lost only to its own group's account. Another group's
// account being held -- by a Publish copying it, or another observer -- is
// no reason to lose it.
func TestAnObservationIsLostOnlyToItsOwnGroupsAccount(t *testing.T) {
	c, now, g := costFixture()
	other := g
	other.QueryGroupKey = "other"
	c.Reconcile([]CostGroup{g, other}, true)
	ctx := context.Background()
	held := c.scope.Load().groups["shared"].account
	held.mu.Lock()
	o := costObservation(StageSlotCompleted)
	o.Trace.QueryGroupKey = "other"
	c.Observe(ctx, o)
	c.Observe(ctx, costObservation(StageSlotCompleted))
	held.mu.Unlock()

	c.Publish(*now)
	s := c.Snapshot()
	if s.Coverage.ContentionDroppedTotal != 1 || s.Coverage.ContentionDropped != 1 {
		t.Fatalf("contention = %d total, %d in the window; want the one observation of the held group", s.Coverage.ContentionDroppedTotal, s.Coverage.ContentionDropped)
	}
	for _, row := range s.Contributors {
		if row.Scope == "query_group" && row.Group.QueryGroupKey == "other" && row.Current.RunReturns == 1 {
			return
		}
	}
	t.Fatalf("the other group's observation was lost to an account it does not use: %+v", s.Contributors)
}

// A holder found at the first try is waited for, a yield at a time, until
// costAccountRetry has passed. One that lets go within it - a large group's
// copy, another observer of the same group - loses nothing, however many
// yields that takes, and so does one that lets go at the bound itself. One
// still holding when the time is up loses the observation, counted as lost,
// and Observe returns rather than waiting on. The clock here moves one
// microsecond a yield, so the yields are the time.
func TestAnObservationWaitsForAHolderUpToItsBound(t *testing.T) {
	c, now, _ := costFixture()
	account := c.scope.Load().groups["shared"].account
	const step = time.Microsecond
	bound := int(costAccountRetry / step)
	clock, reads := time.Unix(0, 0), 0
	c.retryClock = func() time.Time {
		// A loop that tries without yielding never moves this clock; the
		// reads still stop it.
		if reads++; reads > 10*bound {
			t.Fatalf("the clock was read %d times: the tries are not bounded", reads)
		}
		return clock
	}
	yields := 0
	holdUntil := func(release int) {
		yields, reads = 0, 0
		c.yield = func() {
			yields++
			clock = clock.Add(step)
			if yields > 10*bound {
				t.Fatalf("still trying after %d yields: the bound is not holding", yields)
			}
			if yields == release {
				account.mu.Unlock()
			}
		}
		account.mu.Lock()
		c.Observe(context.Background(), costObservation(StageSlotCompleted))
	}

	holdUntil(5)
	if yields != 5 || c.contentionDropped.Load() != 0 {
		t.Fatalf("a holder that let go after 5 us: %d yields, %d lost; want 5 and none", yields, c.contentionDropped.Load())
	}
	holdUntil(bound)
	if yields != bound || c.contentionDropped.Load() != 0 {
		t.Fatalf("a holder that let go at the bound: %d yields, %d lost; want %d and none", yields, c.contentionDropped.Load(), bound)
	}
	holdUntil(-1)
	account.mu.Unlock()
	if yields != bound || c.contentionDropped.Load() != 1 {
		t.Fatalf("a holder that never let go: %d yields, %d lost; want %d, then one lost", yields, c.contentionDropped.Load(), bound)
	}
	c.Publish(*now)
	if row := costRow(t, c.Snapshot(), "query_group", CostPlanIdentity{}); row.Current.RunReturns != 2 {
		t.Fatalf("run returns = %d, want the two observations whose holders let go within the bound", row.Current.RunReturns)
	}
}

// A loss marks the windows it fell in and none after them. The reading spans
// the current window and the one before it, as every other coverage count
// does, so the loss is in the reading of its own window and of the next one,
// and out of the reading two windows on -- while the process total keeps
// counting it. Every other reason a window can be incomplete is kept out:
// the object and its Plan are observed in every window, with known walls and
// records, after the window the roster began in.
func TestAContentionLossMarksTheWindowsItFellInAndNoneAfter(t *testing.T) {
	now := time.Unix(600, 0)
	c := NewCostSummary(CostSummaryOptions{ProcessID: "process-a", Window: time.Minute, GroupCapacity: 4, PlanCapacity: 8, MetadataBytes: 4096, TopN: 2, Now: func() time.Time { return now }})
	c.Reconcile([]CostGroup{{QueryGroupKey: "shared", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", Members: []CostPlanIdentity{costA}}}, true)
	ctx := context.Background()
	observeWindow := func() {
		c.Observe(ctx, costObservation(StageSlotCompleted))
		o := costObservation(StageEvaluationCompleted)
		o.EvaluationOwner, o.EvaluationRecordsKnown, o.DurationKnown = costA, true, true
		c.Observe(ctx, o)
	}
	read := func() CostCoverage {
		c.Publish(now)
		return c.Snapshot().Coverage
	}

	now = time.Unix(720, 0)
	observeWindow()
	if coverage := read(); coverage.Incomplete {
		t.Fatalf("the window before any loss is incomplete for another reason, so the test reads nothing: %+v", coverage)
	}

	account := c.scope.Load().groups["shared"].account
	account.mu.Lock()
	c.Observe(ctx, costObservation(StageSlotCompleted))
	account.mu.Unlock()
	for _, step := range []struct {
		at         int64
		dropped    uint64
		incomplete bool
		why        string
	}{
		{720, 1, true, "the window the loss fell in"},
		{780, 1, true, "the next window, whose reading still spans the loss"},
		{840, 0, false, "two windows on, the loss out of the reading"},
		{900, 0, false, "and every window after"},
	} {
		now = time.Unix(step.at, 0)
		if step.at != 720 {
			observeWindow()
		}
		coverage := read()
		if coverage.ContentionDropped != step.dropped || coverage.Incomplete != step.incomplete || coverage.ContentionDroppedTotal != 1 {
			t.Fatalf("%s: dropped %d in the window, %d in total, incomplete %v; want %d, 1, %v",
				step.why, coverage.ContentionDropped, coverage.ContentionDroppedTotal, coverage.Incomplete, step.dropped, step.incomplete)
		}
	}
}

// The window counters: counted by the window, read only under that window's
// number, a slot reused four windows on starts from zero, an add for a
// window older than the one the slot holds changes nothing, a full count
// stays full, and concurrent adds all count.
func TestCostEventWindowsCountByWindow(t *testing.T) {
	var w costEventWindows
	for range 3 {
		w.add(40)
	}
	w.add(41)
	if w.count(40) != 3 || w.count(41) != 1 || w.window(41) != 4 || w.window(42) != 1 || w.window(43) != 0 {
		t.Fatalf("counts 40=%d 41=%d, windows 41=%d 42=%d 43=%d; want 3, 1, 4, 1, 0", w.count(40), w.count(41), w.window(41), w.window(42), w.window(43))
	}
	w.add(44)
	if w.count(44) != 1 || w.count(40) != 0 {
		t.Fatalf("slot reused: 44=%d 40=%d; want 1 and 0", w.count(44), w.count(40))
	}
	w.add(40)
	if w.count(44) != 1 || w.count(40) != 0 {
		t.Fatalf("a late add for window 40 changed the slot window 44 holds: 44=%d 40=%d", w.count(44), w.count(40))
	}
	w.slots[45&3].Store(uint64(uint32(45))<<32 | uint64(^uint32(0)))
	w.add(45)
	if w.count(45) != uint64(^uint32(0)) {
		t.Fatalf("a full count wrapped to %d", w.count(45))
	}

	// The slot starts out holding window 96, so the first adds for window
	// 100 race to claim it while the others race to add to it.
	var concurrent costEventWindows
	concurrent.add(96)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				concurrent.add(100)
			}
		}()
	}
	wg.Wait()
	if got := concurrent.count(100); got != 8000 {
		t.Fatalf("concurrent adds counted %d, want 8000", got)
	}
}

// BenchmarkCostSummaryPublishHolds is what a Publish holds an account for:
// copy_ns is the whole copy, every group's account taken and released in
// turn, and account_hold_ns that divided by the groups -- an upper bound on
// one account's hold, since it includes walking the roster. The ranking after
// the copy holds nothing.
func BenchmarkCostSummaryPublishHolds(b *testing.B) {
	for _, shape := range []struct{ groups, plans int }{{700, 1}, {1000, 2}} {
		b.Run(fmt.Sprintf("groups=%d/plans=%d", shape.groups, shape.plans), func(b *testing.B) {
			now := time.Unix(600, 0)
			c := NewCostSummary(CostSummaryOptions{ProcessID: "process", Window: time.Minute, GroupCapacity: shape.groups, PlanCapacity: shape.groups * shape.plans, MetadataBytes: shape.groups * 256, TopN: 20, Now: func() time.Time { return now }})
			groups := make([]CostGroup, shape.groups)
			members := []CostPlanIdentity{costA, costB}[:shape.plans]
			for i := range groups {
				groups[i] = CostGroup{QueryGroupKey: fmt.Sprint(i), Members: members}
			}
			c.Reconcile(groups, true)
			for i := range groups {
				obs := costObservation(StageEvaluationCompleted)
				obs.Trace.QueryGroupKey, obs.EvaluationOwner, obs.Counts.Records = groups[i].QueryGroupKey, costA, int64(i+1)
				c.Observe(context.Background(), obs)
			}
			var started time.Time
			var copying time.Duration
			c.rankingStarted = func() { copying += time.Since(started) }
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				started = time.Now()
				c.Publish(now)
			}
			b.StopTimer()
			b.ReportMetric(float64(copying.Nanoseconds())/float64(b.N), "copy_ns")
			b.ReportMetric(float64(copying.Nanoseconds())/float64(b.N)/float64(shape.groups), "account_hold_ns")
		})
	}
}

// BenchmarkCostSummaryParallelObserve is every executing Slot observing at
// once, each for its own object, with nothing else running: the losses it
// reports are observers losing to one another.
func BenchmarkCostSummaryParallelObserve(b *testing.B) {
	const count = 700
	c := NewCostSummary(CostSummaryOptions{ProcessID: "process", Window: 5 * time.Minute, GroupCapacity: count, PlanCapacity: count * 2, MetadataBytes: count * 128, TopN: 20})
	groups := make([]CostGroup, count)
	for i := range groups {
		groups[i] = CostGroup{QueryGroupKey: fmt.Sprint(i), Members: []CostPlanIdentity{costA}}
	}
	c.Reconcile(groups, true)
	var next sync.Mutex
	assigned := 0
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		next.Lock()
		obs := costObservation(StageEvaluationCompleted)
		obs.Trace.QueryGroupKey, obs.EvaluationOwner, obs.Counts.Records = groups[assigned%count].QueryGroupKey, costA, 100
		assigned++
		next.Unlock()
		for pb.Next() {
			c.Observe(context.Background(), obs)
		}
	})
	b.StopTimer()
	b.ReportMetric(float64(c.contentionDropped.Load())/float64(b.N), "drop/op")
}
