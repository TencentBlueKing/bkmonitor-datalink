// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var costA = CostPlanIdentity{"tenant", "business", "healthy-large"}
var costB = CostPlanIdentity{"tenant", "business", "small"}

func costFixture() (*CostSummary, *time.Time, CostGroup) {
	now := time.Unix(600, 0)
	o := CostSummaryOptions{ProcessID: "process-a", Window: time.Minute, GroupCapacity: 4, PlanCapacity: 8, MetadataBytes: 4096, TopN: 2, Now: func() time.Time { return now }}
	c := NewCostSummary(o)
	g := CostGroup{QueryGroupKey: "shared", QueryRevision: "query-v1", SnapshotRevision: "snapshot-v1", ScheduleRevision: "schedule-v1", Members: []CostPlanIdentity{costA, costB}}
	c.Reconcile([]CostGroup{g}, true)
	return c, &now, g
}

func costObservation(stage Stage) Observation {
	o := Observation{Stage: stage, Result: ResultSuccess, Duration: time.Millisecond, Trace: TraceFields{QueryGroupKey: "shared", EvaluationTime: 590}}
	if stage == StageProgressCommitted {
		o.ProgressCompletionKind = "FULL_COMPLETED"
	}
	return o
}

func TestCostSummaryCountsCommittedUnavailableButNotStageNameAlone(t *testing.T) {
	c, now, _ := costFixture()
	c.Observe(context.Background(), costObservation(StageSlotCompleted))
	o := costObservation(StageProgressCommitted)
	o.ProgressCompletionKind, o.Result = "COMPLETED_WITH_UNAVAILABLE", ResultDegraded
	c.Observe(context.Background(), o)
	o.ProgressCompletionKind, o.Result = "", ResultSuccess
	c.Observe(context.Background(), o)
	c.Publish(*now)
	if got := costRow(t, c.Snapshot(), "query_group", CostPlanIdentity{}).Current.ProgressCommits; got != 1 {
		t.Fatalf("committed work inferred from result instead of actual receipt: %d", got)
	}
}

func costRow(t *testing.T, s CostSnapshot, scope string, identity CostPlanIdentity) CostContributor {
	t.Helper()
	for _, row := range s.Contributors {
		if row.Scope == scope && row.Plan == identity {
			return row
		}
	}
	t.Fatalf("missing %s %v in %+v", scope, identity, s.Contributors)
	return CostContributor{}
}

func TestCostSummaryHealthyConsumersAndSharedCosts(t *testing.T) {
	c, now, _ := costFixture()
	ctx := context.Background()
	for _, stage := range []Stage{StageSlotStarted, StageQueryCompleted, StageStatePreflight, StageStateApplied, StageSlotCompleted, StageProgressCommitted} {
		o := costObservation(stage)
		o.Counts.Keys = 7
		c.Observe(ctx, o)
	}
	for i, p := range []CostPlanIdentity{costA, costB} {
		o := costObservation(StageEvaluationCompleted)
		o.EvaluationOwner = p
		o.EvaluationRecordsKnown = true
		o.Counts.Records = []int64{1000, 100}[i]
		c.Observe(ctx, o)
	}
	c.Publish(*now)
	s := c.Snapshot()
	shared := costRow(t, s, "query_group", CostPlanIdentity{})
	a, b := costRow(t, s, "strategy_owned", costA), costRow(t, s, "strategy_owned", costB)
	if shared.Current.QueryScopes != 1 || shared.Current.StateCalls != 2 || shared.Current.StateKeys != 14 || shared.Current.EvaluationRecords != 1100 || len(shared.Group.Members) != 2 {
		t.Fatalf("shared costs duplicated/lost: %+v", shared)
	}
	if a.Current.EvaluationRecords != 1000 || b.Current.EvaluationRecords != 100 || a.Current.QueryScopes != 0 || a.Current.StateCalls != 0 || b.Current.RunReturns != 0 {
		t.Fatalf("consumer costs misattributed: a=%+v b=%+v", a, b)
	}
	for _, ranking := range s.Rankings {
		if ranking.Scope == "strategy_owned" && ranking.Dimension == "evaluation_records" {
			if len(ranking.Indexes) != 2 || s.Contributors[ranking.Indexes[0]].Plan != costA {
				t.Fatal("healthy large strategy absent from candidates")
			}
		}
	}
	if s.Scope != "process_observed_candidates" || s.Coverage.ObservedGroups != 1 || s.Coverage.ObservedPlans != 2 || s.Coverage.PartialWindowGroups != 1 || !s.Coverage.Incomplete {
		t.Fatalf("partial process coverage hidden: %+v", s)
	}
}

func TestCostSummaryRetriesRetainActualCostAndSnapshotReadsDoNotAdd(t *testing.T) {
	c, now, _ := costFixture()
	ctx := context.Background()
	for i := range 2 {
		started := costObservation(StageSlotStarted)
		started.Trace.OwnerEpoch = uint64(i + 1)
		c.Observe(ctx, started)
		completed := costObservation(StageSlotCompleted)
		completed.Trace.OwnerEpoch = uint64(i + 1)
		if i == 0 {
			completed.Result = ResultFailed
		}
		c.Observe(ctx, completed)
	}
	c.Observe(ctx, costObservation(StageProgressCommitted))
	c.Publish(*now)
	first, second := c.Snapshot(), c.Snapshot()
	row := costRow(t, second, "query_group", CostPlanIdentity{})
	if row.Current.Attempts != 2 || row.Current.RunReturns != 2 || row.Current.FailedRunReturns != 1 || row.Current.ProgressCommits != 1 || row.Current.RunWall.ObservedNS != int64(2*time.Millisecond) || !reflect.DeepEqual(first, second) {
		t.Fatalf("retry cost or snapshot purity: %+v", row)
	}
}

func TestCostSummaryUnknownIdentityDurationAndNoBorrowedTraceIdentity(t *testing.T) {
	c, now, _ := costFixture()
	ctx := ContextWithTraceFields(context.Background(), TraceFields{QueryGroupKey: "shared", StrategyID: costA.StrategyID, BusinessID: costA.BusinessID})
	o := Observation{Stage: StageEvaluationCompleted, Result: ResultFailed, Counts: Counts{Records: 10}}
	c.Observe(ctx, o)
	o.EvaluationOwner = costA
	o.Counts.Records = 0
	c.Observe(ctx, o)
	o.DurationKnown, o.EvaluationRecordsKnown = true, true
	c.Observe(ctx, o)
	c.Publish(*now)
	s := c.Snapshot()
	row := costRow(t, s, "query_group", CostPlanIdentity{})
	if row.Current.EvaluationWall.Unknown != 2 || row.Current.EvaluationWall.Measured != 1 || row.Current.EvaluationRecordsUnknown != 1 || row.Current.UnattributedEvaluations != 1 || row.Current.FailedEvaluations != 3 {
		t.Fatalf("unknown values became zero/borrowed identity: %+v", row)
	}
	if !s.Coverage.Incomplete || s.Coverage.UnattributedEvaluations != 1 || s.Coverage.UnknownWallObservations != 2 {
		t.Fatalf("unknown coverage hidden: %+v", s.Coverage)
	}
}

func TestCostSummaryRotationRevisionsEvictionAndMetadataBounds(t *testing.T) {
	c, now, g := costFixture()
	c.Observe(context.Background(), costObservation(StageSlotCompleted))
	*now = now.Add(time.Minute)
	c.Publish(*now)
	row := costRow(t, c.Snapshot(), "query_group", CostPlanIdentity{})
	if row.Current.RunReturns != 0 || row.Previous.RunReturns != 1 {
		t.Fatal("previous window lost")
	}
	*now = now.Add(time.Minute)
	c.Publish(*now)
	if len(c.Snapshot().Contributors) != 0 {
		t.Fatal("expired cost retained")
	}
	c.Observe(context.Background(), costObservation(StageSlotCompleted))
	g.QueryRevision = "query-v2"
	c.Reconcile([]CostGroup{g}, true)
	stale := costObservation(StageSlotCompleted)
	stale.Trace.QueryRevision = "query-v1"
	c.Observe(context.Background(), stale)
	c.Publish(*now)
	if len(c.Snapshot().Contributors) != 0 || c.Snapshot().Coverage.UntrackedObservations != 1 {
		t.Fatal("revision reused old cost or silently dropped stale event")
	}
	c.Reconcile(nil, true)
	if len(c.groups) != 0 {
		t.Fatal("retired objects retained")
	}
	c.options.MetadataBytes = 1
	c.Reconcile([]CostGroup{g}, true)
	c.Publish(*now)
	if !c.Snapshot().Coverage.Incomplete || c.Snapshot().Coverage.TrackedGroups != 0 {
		t.Fatal("metadata budget exceeded")
	}
}

func TestCostSummaryCapacityCoverageAndSnapshotIsolation(t *testing.T) {
	c, now, g := costFixture()
	c.options.GroupCapacity, c.options.PlanCapacity = 1, 1
	other := g
	other.QueryGroupKey = "other"
	c.Reconcile([]CostGroup{g, other}, false)
	c.Observe(context.Background(), costObservation(StageSlotCompleted))
	dropped := costObservation(StageSlotCompleted)
	dropped.Trace.QueryGroupKey = "other"
	c.Observe(context.Background(), dropped)
	c.Publish(*now)
	s := c.Snapshot()
	if s.Coverage.TotalGroups != 2 || s.Coverage.TrackedGroups != 1 || s.Coverage.TotalPlans != 4 || s.Coverage.TrackedPlans != 1 || !s.Coverage.Incomplete || s.Coverage.UntrackedObservations != 1 || s.Coverage.ObservedPlans != 0 {
		t.Fatalf("saturation hidden: %+v", s.Coverage)
	}
	before := c.Snapshot()
	s.Contributors[0].Group.Members[0].StrategyID = "tampered"
	s.Rankings[4].Indexes[0] = 123
	if !reflect.DeepEqual(before, c.Snapshot()) {
		t.Fatal("API caller mutated cached evidence")
	}
	for _, o := range []CostSummaryOptions{{}, {ProcessID: "p", Window: time.Minute, GroupCapacity: 1, PlanCapacity: 1, TopN: 1}} {
		disabled := NewCostSummary(o)
		disabled.Observe(context.Background(), dropped)
		if disabled.Snapshot().Enabled || !disabled.Snapshot().Coverage.Incomplete || CostSummaryCapacityBytes(o) != 0 {
			t.Fatal("missing budget enabled collection")
		}
	}
}

func TestCostSummaryConcurrentObserveReconcilePublish(t *testing.T) {
	c, now, g := costFixture()
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if worker == 0 {
					c.Reconcile([]CostGroup{g}, true)
					c.Publish(*now)
					_ = c.Snapshot()
				} else {
					c.Observe(context.Background(), costObservation(StageSlotCompleted))
				}
			}
		}()
	}
	wg.Wait()
	c.Publish(*now)
	snapshot := c.Snapshot()
	var recorded uint64
	for _, row := range snapshot.Contributors {
		if row.Scope == "query_group" {
			recorded += row.Current.RunReturns
		}
	}
	if recorded+snapshot.Coverage.ContentionDroppedTotal != 300 {
		t.Fatal("concurrent reconciliation lost observations without coverage")
	}
}

func TestCostSummaryObserveNeverWaitsForPublisher(t *testing.T) {
	c, now, _ := costFixture()
	c.mu.Lock()
	done := make(chan struct{})
	go func() {
		c.Observe(context.Background(), costObservation(StageSlotCompleted))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		c.mu.Unlock()
		t.Fatal("execution waited for summary publication")
	}
	c.mu.Unlock()
	c.Publish(*now)
	if c.Snapshot().Coverage.ContentionDroppedTotal != 1 || !c.Snapshot().Coverage.Incomplete {
		t.Fatal("contention drop hidden")
	}
}

func TestCostSummaryObserveAllocations(t *testing.T) {
	for _, mode := range []string{"disabled", "enabled", "saturated", "irrelevant"} {
		t.Run(mode, func(t *testing.T) {
			c, _, _ := costFixture()
			o := costObservation(StageEvaluationCompleted)
			o.EvaluationOwner, o.Counts.Records = costA, 100
			switch mode {
			case "disabled":
				c = NewCostSummary(CostSummaryOptions{})
			case "saturated":
				o.Trace.QueryGroupKey = "not-admitted"
			case "irrelevant":
				o.Stage = StageOther
			}
			if got := testing.AllocsPerRun(1000, func() { c.Observe(context.Background(), o) }); got != 0 {
				t.Fatalf("hot observation allocated: %v", got)
			}
		})
	}
}

func BenchmarkCostSummaryObserve(b *testing.B) {
	for _, mode := range []string{"disabled", "enabled", "saturated"} {
		b.Run(mode, func(b *testing.B) {
			c, _, _ := costFixture()
			c.options.Now = time.Now
			o := costObservation(StageEvaluationCompleted)
			o.EvaluationOwner, o.Counts.Records = costA, 100
			if mode == "disabled" {
				c = NewCostSummary(CostSummaryOptions{})
			} else if mode == "saturated" {
				o.Trace.QueryGroupKey = "not-admitted"
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				c.Observe(context.Background(), o)
			}
		})
	}
}

func BenchmarkCostSummaryPublish(b *testing.B) {
	for _, count := range []int{100, 1000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			now := time.Unix(600, 0)
			o := CostSummaryOptions{ProcessID: "process", Window: time.Minute, GroupCapacity: count, PlanCapacity: count * 2, MetadataBytes: count * 128, TopN: 10, Now: func() time.Time { return now }}
			c := NewCostSummary(o)
			groups := make([]CostGroup, count)
			for i := range groups {
				groups[i] = CostGroup{QueryGroupKey: fmt.Sprint(i), Members: []CostPlanIdentity{costA, costB}}
			}
			c.Reconcile(groups, true)
			for i := range groups {
				obs := costObservation(StageEvaluationCompleted)
				obs.Trace.QueryGroupKey, obs.EvaluationOwner, obs.Counts.Records = groups[i].QueryGroupKey, costA, int64(i+1)
				c.Observe(context.Background(), obs)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				c.Publish(now)
			}
		})
	}
}

func BenchmarkCostSummaryReconcile(b *testing.B) {
	now := time.Unix(600, 0)
	o := CostSummaryOptions{ProcessID: "process", Window: time.Minute, GroupCapacity: 1000, PlanCapacity: 2000, MetadataBytes: 128000, TopN: 10, Now: func() time.Time { return now }}
	c := NewCostSummary(o)
	groups := make([]CostGroup, o.GroupCapacity)
	for i := range groups {
		groups[i] = CostGroup{QueryGroupKey: fmt.Sprint(i), Members: []CostPlanIdentity{costA, costB}}
	}
	c.Reconcile(groups, true)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		c.Reconcile(groups, true)
	}
	b.ReportMetric(float64(CostSummaryCapacityBytes(o)), "capacity_B")
}

func BenchmarkCostSummaryConcurrentReporting(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		for _, reportEvery := range []time.Duration{0, 100 * time.Millisecond, time.Millisecond} {
			b.Run(fmt.Sprintf("groups=%d/report_every=%s", count, reportEvery), func(b *testing.B) {
				o := CostSummaryOptions{ProcessID: "process", Window: time.Minute, GroupCapacity: count, PlanCapacity: count * 2, MetadataBytes: count * 128, TopN: 10}
				c := NewCostSummary(o)
				groups := make([]CostGroup, count)
				for i := range groups {
					groups[i] = CostGroup{QueryGroupKey: fmt.Sprint(i), Members: []CostPlanIdentity{costA, costB}}
				}
				c.Reconcile(groups, true)
				obs := costObservation(StageEvaluationCompleted)
				obs.Trace.QueryGroupKey, obs.EvaluationOwner, obs.Counts.Records = "0", costA, 100
				var maxReport atomic.Int64
				stop, finished := make(chan struct{}), make(chan struct{})
				if reportEvery > 0 {
					go func() {
						defer close(finished)
						ticker := time.NewTicker(reportEvery)
						defer ticker.Stop()
						for {
							select {
							case <-stop:
								return
							case <-ticker.C:
								started := time.Now()
								c.Reconcile(groups, true)
								c.Publish(time.Now())
								maxReport.Store(max(maxReport.Load(), time.Since(started).Nanoseconds()))
							}
						}
					}()
				}
				b.ResetTimer()
				for range b.N {
					c.Observe(context.Background(), obs)
				}
				b.StopTimer()
				if reportEvery > 0 {
					close(stop)
					<-finished
				}
				b.ReportMetric(float64(maxReport.Load()), "max_report_ns")
				b.ReportMetric(float64(c.contentionDropped.Load())/float64(b.N), "drop/op")
			})
		}
	}
}

// Run with -benchtime=35s or longer to include at least one 30s reconciliation
// and six 5s publications. The producer is deliberately busy; no Redis or
// application evaluation is included. Latency is sampled every 4096 calls.
func BenchmarkCostSummaryNormalReporting(b *testing.B) {
	for _, count := range []int{1000, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			c := NewCostSummary(CostSummaryOptions{ProcessID: "process", Window: 5 * time.Minute, GroupCapacity: count, PlanCapacity: count * 2, MetadataBytes: count * 128, TopN: 20})
			groups := make([]CostGroup, count)
			for i := range groups {
				groups[i] = CostGroup{QueryGroupKey: fmt.Sprint(i), Members: []CostPlanIdentity{costA, costB}}
			}
			c.Reconcile(groups, true)
			obs := costObservation(StageEvaluationCompleted)
			obs.EvaluationOwner, obs.Counts.Records = costA, 100
			var publications, reconciliations, maxPublish, maxReconcile atomic.Int64
			stop, finished := make(chan struct{}), make(chan struct{})
			go func() {
				defer close(finished)
				lastReconcile := time.Now()
				ticker := time.NewTicker(5 * time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-stop:
						return
					case at := <-ticker.C:
						if at.Sub(lastReconcile) >= 30*time.Second {
							started := time.Now()
							c.Reconcile(groups, true)
							maxReconcile.Store(max(maxReconcile.Load(), time.Since(started).Nanoseconds()))
							reconciliations.Add(1)
							lastReconcile = at
						}
						started := time.Now()
						c.Publish(at)
						maxPublish.Store(max(maxPublish.Load(), time.Since(started).Nanoseconds()))
						publications.Add(1)
					}
				}
			}()
			samples := make([]int64, 0, 65536)
			b.ResetTimer()
			for i := range b.N {
				obs.Trace.QueryGroupKey = groups[i%count].QueryGroupKey
				if i%4096 == 0 && len(samples) < cap(samples) {
					started := time.Now()
					c.Observe(context.Background(), obs)
					samples = append(samples, time.Since(started).Nanoseconds())
				} else {
					c.Observe(context.Background(), obs)
				}
			}
			b.StopTimer()
			close(stop)
			<-finished
			sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
			b.ReportMetric(float64(c.contentionDropped.Load())/float64(b.N), "drop/op")
			b.ReportMetric(float64(publications.Load()), "publications")
			b.ReportMetric(float64(reconciliations.Load()), "reconciliations")
			b.ReportMetric(float64(maxPublish.Load()), "max_publish_ns")
			b.ReportMetric(float64(maxReconcile.Load()), "max_reconcile_ns")
			if len(samples) > 0 {
				b.ReportMetric(float64(samples[(len(samples)-1)*50/100]), "sample_p50_ns")
				b.ReportMetric(float64(samples[(len(samples)-1)*95/100]), "sample_p95_ns")
				b.ReportMetric(float64(samples[(len(samples)-1)*99/100]), "sample_p99_ns")
				b.ReportMetric(float64(samples[len(samples)-1]), "sample_max_ns")
			}
		})
	}
}
