// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package fleet

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

type costProjectionRedis struct {
	redis.Cmdable
	mu       sync.Mutex
	values   map[string]string
	readKeys []string
	readEnds []int64
	setErr   error
}

func (r *costProjectionRedis) Set(_ context.Context, key string, value any, _ time.Duration) *redis.StatusCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.setErr != nil {
		return redis.NewStatusResult("", r.setErr)
	}
	r.values[key] = string(value.([]byte))
	return redis.NewStatusResult("OK", nil)
}

func (r *costProjectionRedis) GetRange(ctx context.Context, key string, start, end int64) *redis.StringCmd {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readKeys, r.readEnds = append(r.readKeys, key), append(r.readEnds, end)
	if err := ctx.Err(); err != nil {
		return redis.NewStringResult("", err)
	}
	v := r.values[key]
	return redis.NewStringResult(v[start:min(int64(len(v)), end+1)], nil)
}

func projectionFixture(t *testing.T, commands int) (*CostProjectionStore, *costProjectionRedis, time.Time) {
	t.Helper()
	r := &costProjectionRedis{values: make(map[string]string)}
	s, err := NewCostProjectionStore(r, "test:diagnostics", CostProjectionLimits{PublishBytes: 16 << 10, ReadBytes: 64 << 10, ReadCommands: commands, Timeout: time.Second, TTL: time.Minute, FreshFor: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return s, r, time.Unix(600, 0)
}

func projectionSnapshot(at time.Time) observability.CostSnapshot {
	return observability.CostSnapshot{Enabled: true, ProcessID: "process", Scope: "process_observed_candidates", GeneratedAt: at}
}

func TestCostProjectionRoundTripAndIndependentReplicaRegistryCoverage(t *testing.T) {
	s, r, at := projectionFixture(t, 4)
	for _, replica := range []string{"a", "b"} {
		if result, err := s.Publish(context.Background(), replica, at, projectionSnapshot(at)); err != nil || result.MarkerWritten || result.WrittenBytes == 0 {
			t.Fatalf("publish: %+v %v", result, err)
		}
	}
	view := s.Load(context.Background(), []string{"a", "b"}, true, at)
	if !view.Complete || len(view.Snapshots) != 2 || view.ReadCommands != 2 || view.ReadBytes > s.limits.ReadBytes {
		t.Fatalf("view: %+v", view)
	}
	for _, key := range r.readKeys {
		if !strings.Contains(key, ":cost-projection:v1:") || strings.Contains(key, "fleet-snapshot") {
			t.Fatalf("read full fleet or wrong namespace: %s", key)
		}
	}
	partial := s.Load(context.Background(), []string{"a", "b"}, false, at)
	if partial.Complete || len(partial.Gaps) != 1 || partial.Gaps[0].Reason != "REGISTRY_INCOMPLETE" {
		t.Fatalf("finite list disguised as complete registry: %+v", partial)
	}
}

func TestCostProjectionRefreshBudgetRotatesAndOversizedReplicaCannotStarve(t *testing.T) {
	s, r, at := projectionFixture(t, 1)
	s.limits.ReadBytes = s.limits.PublishBytes + 1
	r.values[s.key(costReplicaHash("oversized"))] = strings.Repeat("x", s.limits.ReadBytes+100)
	if _, err := s.Publish(context.Background(), "healthy", at, projectionSnapshot(at)); err != nil {
		t.Fatal(err)
	}
	first := s.Load(context.Background(), []string{"oversized", "healthy"}, true, at)
	second := s.Load(context.Background(), []string{"oversized", "healthy"}, true, at)
	if first.Complete || first.Attempted != 1 || first.ReadBytes != s.limits.ReadBytes || first.Gaps[0].Reason != "READ_BUDGET" || first.Deferred != 1 {
		t.Fatalf("oversized value bypassed budget: %+v", first)
	}
	if second.Complete || len(second.Snapshots) != 1 || second.Snapshots[0].Replica != "healthy" || second.Deferred != 1 {
		t.Fatalf("fixed-prefix starvation: %+v", second)
	}
}

func TestCostProjectionRejectsBeforeEncodingAndOverwritesPreviousFreshValue(t *testing.T) {
	s, r, at := projectionFixture(t, 4)
	if _, err := s.Publish(context.Background(), "a", at, projectionSnapshot(at)); err != nil {
		t.Fatal(err)
	}
	large := projectionSnapshot(at)
	large.ProcessID = strings.Repeat("secret", s.limits.PublishBytes)
	result, err := s.Publish(context.Background(), "a", at.Add(time.Second), large)
	if !errors.Is(err, ErrCostProjectionBudget) || !result.MarkerWritten || result.WrittenBytes > s.limits.PublishBytes {
		t.Fatalf("budget rejection not explicit: %+v %v", result, err)
	}
	if strings.Contains(r.values[s.key(costReplicaHash("a"))], "secret") {
		t.Fatal("oversized data encoded into marker")
	}
	view := s.Load(context.Background(), []string{"a"}, true, at.Add(time.Second))
	if view.Complete || len(view.Snapshots) != 0 || view.Gaps[0].Reason != "PUBLISH_BUDGET" {
		t.Fatalf("old fresh snapshot survived rejection: %+v", view)
	}
	r.setErr = errors.New("store unavailable")
	if _, err := s.Publish(context.Background(), "a", at, projectionSnapshot(at)); !errors.Is(err, r.setErr) {
		t.Fatal("write failure hidden from local cache")
	}
}

func TestCostProjectionMissingStaleInvalidAndCancelledRemainGaps(t *testing.T) {
	s, r, at := projectionFixture(t, 5)
	_, _ = s.Publish(context.Background(), "stale", at, projectionSnapshot(at))
	r.values[s.key(costReplicaHash("invalid"))] = `{}`
	view := s.Load(context.Background(), []string{"missing", "stale", "invalid"}, true, at.Add(time.Minute))
	if view.Complete || len(view.Snapshots) != 0 || len(view.Gaps) != 3 || view.Gaps[0].Reason != "MISSING" || view.Gaps[1].Reason != "STALE" || view.Gaps[2].Reason != "INVALID_IDENTITY" {
		t.Fatalf("invalid facts accepted: %+v", view)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	view = s.Load(ctx, []string{"missing", "stale"}, true, at)
	if view.Complete || view.ReadCommands != 0 || view.Deferred != 2 {
		t.Fatalf("canceled refresh continued: %+v", view)
	}
}

func TestCostProjectionAdmissionBoundIncludesEscapingAndAllScalarFields(t *testing.T) {
	_, _, at := projectionFixture(t, 1)
	snapshot := projectionSnapshot(at)
	snapshot.Contributors = []observability.CostContributor{{Scope: "strategy_owned", Group: observability.CostGroup{QueryGroupKey: "\"<&\n中", Members: []observability.CostPlanIdentity{{TenantID: "t", BusinessID: "b", StrategyID: "s"}}}, Current: observability.CostScalars{EvaluationRecords: ^uint64(0)}}}
	snapshot.Rankings = []observability.CostRanking{{Scope: "strategy_owned", Dimension: "evaluation_records", Indexes: []int{0}}}
	// The one float the schema carries, at the length the encoder gives a
	// share that does not round: the bound has to hold for it too.
	snapshot.Retained = observability.CostRetainedReading{PeakSumBytes: 1_003_487_232, GroupsWithPeak: 3, LimitBytes: 1 << 30, LimitKnown: true,
		PeakShare: float64(1_003_487_232) / float64(1<<30), HardStops: 13, ShareStops: 1}
	wire := costProjectionWire{Version: 1, ReplicaHash: costReplicaHash("a"), ObservedAt: at, Status: "AVAILABLE", Cost: &snapshot}
	encoded, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	for _, budget := range []int{1, len(encoded) - 1, len(encoded), 1 << 20} {
		if costJSONFits(reflect.ValueOf(wire), budget) && len(encoded) > budget {
			t.Fatalf("preflight undercounted JSON: %d > %d", len(encoded), budget)
		}
	}
	if !costJSONFits(reflect.ValueOf(wire), 1<<20) {
		t.Fatal("known bounded schema rejected")
	}
}

func TestCostProjectionTotalBytesAreSharedAndBusyRefreshDoesNotWait(t *testing.T) {
	s, r, at := projectionFixture(t, 4)
	for _, replica := range []string{"a", "b"} {
		if _, err := s.Publish(context.Background(), replica, at, projectionSnapshot(at)); err != nil {
			t.Fatal(err)
		}
	}
	firstBytes := len(r.values[s.key(costReplicaHash("a"))])
	s.limits.ReadBytes = firstBytes + 10
	view := s.Load(context.Background(), []string{"a", "b"}, true, at)
	if view.Complete || len(view.Snapshots) != 1 || view.ReadBytes != s.limits.ReadBytes || view.Gaps[0].Reason != "READ_BUDGET" || r.readEnds[1] != 9 {
		t.Fatalf("per-replica allowance multiplied total budget: %+v", view)
	}
	s.readMu.Lock()
	defer s.readMu.Unlock()
	done := make(chan CostProjectionView, 1)
	go func() { done <- s.Load(context.Background(), []string{"a"}, true, at) }()
	select {
	case view = <-done:
		if view.Complete || view.ReadCommands != 0 || view.Deferred != 1 || view.Gaps[0].Reason != "PROJECTION_UNAVAILABLE" {
			t.Fatalf("busy refresh hidden: %+v", view)
		}
	case <-time.After(time.Second):
		t.Fatal("busy refresh waited for in-flight Redis read")
	}
}

func TestCostProjectionConcurrentPublicationAndRead(t *testing.T) {
	s, _, at := projectionFixture(t, 4)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 10; n++ {
				_, _ = s.Publish(context.Background(), "a", at, projectionSnapshot(at))
				view := s.Load(context.Background(), []string{"a", "missing"}, true, at)
				if view.ReadBytes > s.limits.ReadBytes || view.ReadCommands > s.limits.ReadCommands || view.Complete {
					t.Errorf("concurrent read exceeded allowance or hid missing replica: %+v", view)
				}
			}
		}()
	}
	wg.Wait()
}

// A snapshot the cost summary built, its coverage filled in - the per-result
// counts of due Plans not evaluated and a named miss - is admitted, written
// and read back whole. The tests above publish bare snapshots, which the
// size admission walked by type and let through; this one walks a filled
// coverage, which is what a replica publishes every refresh.
func TestCostProjectionCarriesAFilledCoverage(t *testing.T) {
	now := time.Unix(800, 0)
	summary := observability.NewCostSummary(observability.CostSummaryOptions{ProcessID: "process", Window: 5 * time.Minute, GroupCapacity: 8,
		PlanCapacity: 8, MetadataBytes: 4096, TopN: 2, Now: func() time.Time { return now }})
	plan := func(id string) observability.CostPlanIdentity {
		return observability.CostPlanIdentity{TenantID: "t", BusinessID: "b", StrategyID: id}
	}
	group := func(key, id string) observability.CostGroup {
		return observability.CostGroup{QueryGroupKey: key, QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r",
			Members: []observability.CostPlanIdentity{plan(id)}, Schedules: []observability.CostSchedule{{Plan: plan(id), IntervalSeconds: 60, CompletionOffsetSeconds: 60}}}
	}
	summary.Reconcile([]observability.CostGroup{group("refused", "1"), group("silent", "2")}, true)
	now = time.Unix(1400, 0)
	summary.Reconcile([]observability.CostGroup{group("refused", "1"), group("silent", "2"), group("late", "3")}, true)
	now = time.Unix(1440, 0)
	summary.Observe(context.Background(), observability.Observation{Stage: observability.StageProgressCommitted, Result: observability.ResultSuccess,
		ProgressCompletionKind: "COMPLETED_WITH_UNAVAILABLE",
		Trace:                  observability.TraceFields{QueryGroupKey: "refused", QueryRevision: "q", SnapshotRevision: "s", ScheduleRevision: "r", EvaluationTime: 1320}})
	summary.Publish(now)
	snapshot := summary.Snapshot()
	if len(snapshot.Coverage.UnevaluatedDuePlans) == 0 || len(snapshot.Coverage.UnobservedDueSample) == 0 || len(snapshot.Coverage.PartialWindowSample) == 0 {
		t.Fatalf("setup: coverage = %+v, want per-result counts, a named miss and a group tracked mid-window", snapshot.Coverage)
	}

	s, _, _ := projectionFixture(t, 4)
	at := snapshot.GeneratedAt
	if result, err := s.Publish(context.Background(), "a", at, snapshot); err != nil || result.MarkerWritten || result.WrittenBytes == 0 {
		t.Fatalf("publish of a filled coverage = %+v %v, want it written", result, err)
	}
	view := s.Load(context.Background(), []string{"a"}, true, at)
	if !view.Complete || len(view.Snapshots) != 1 {
		t.Fatalf("view = %+v, want the one replica read", view)
	}
	var read observability.CostSnapshot
	if err := json.Unmarshal(view.Snapshots[0].Cost, &read); err != nil {
		t.Fatal(err)
	}
	partial, readPartial := snapshot.Coverage.PartialWindowSample, read.Coverage.PartialWindowSample
	if len(readPartial) != len(partial) || readPartial[0].QueryGroupKey != partial[0].QueryGroupKey || !readPartial[0].TrackedSince.Equal(partial[0].TrackedSince) {
		t.Fatalf("partial-window sample read back %+v, want %+v", readPartial, partial)
	}
	if !reflect.DeepEqual(read.Coverage.UnevaluatedDuePlans, snapshot.Coverage.UnevaluatedDuePlans) ||
		!reflect.DeepEqual(read.Coverage.UnobservedDueSample, snapshot.Coverage.UnobservedDueSample) {
		t.Fatalf("read back %+v / %+v, want %+v / %+v", read.Coverage.UnevaluatedDuePlans, read.Coverage.UnobservedDueSample,
			snapshot.Coverage.UnevaluatedDuePlans, snapshot.Coverage.UnobservedDueSample)
	}
}

// Sized by the replicas it reads (zero read bounds), a refresh reads each
// replica's projection once, within its publish bound, after asking the
// memory line for all of them; refused, it reads nothing and defers every
// replica under MEMORY_REFUSED.
func TestAProjectionReadIsSizedByItsReplicasAndAdmittedFirst(t *testing.T) {
	r := &costProjectionRedis{values: make(map[string]string)}
	s, err := NewCostProjectionStore(r, "test:diagnostics", CostProjectionLimits{PublishBytes: 16 << 10, Timeout: time.Second, TTL: time.Minute, FreshFor: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(600, 0)
	replicas := []string{"a", "b", "c"}
	for _, replica := range replicas {
		if _, err := s.Publish(context.Background(), replica, at, projectionSnapshot(at)); err != nil {
			t.Fatal(err)
		}
	}
	var asks []uint64
	admit := true
	s.AdmitReads(func(bytes uint64) bool { asks = append(asks, bytes); return admit })
	view := s.Load(context.Background(), replicas, true, at)
	if !view.Complete || len(view.Snapshots) != 3 || view.ReadCommands != 3 || len(asks) != 1 || asks[0] != uint64(3*(16<<10+1)) {
		t.Fatalf("view %+v asks %v, want the three read after one ask for three projections", view, asks)
	}
	admit = false
	reads := len(r.readKeys)
	refused := s.Load(context.Background(), replicas, true, at)
	if refused.Complete || refused.Deferred != 3 || len(refused.Snapshots) != 0 || len(r.readKeys) != reads ||
		len(refused.Gaps) != 1 || refused.Gaps[0].Reason != "MEMORY_REFUSED" || refused.Gaps[0].Count != 3 {
		t.Fatalf("refused view %+v after %d reads, want nothing read and every replica deferred as MEMORY_REFUSED", refused, len(r.readKeys)-reads)
	}
}
