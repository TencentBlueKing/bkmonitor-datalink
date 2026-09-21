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
