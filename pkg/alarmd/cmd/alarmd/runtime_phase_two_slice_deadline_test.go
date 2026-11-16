// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build unix

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A sliced Slot whose next slice's permit wait meets the query's deadline,
// through the production bundle - access.Source over uq.Client, a real
// Redis - fails with access's QUERY_PERMIT_DEADLINE: the read ahead of that
// slice fails so, and so does its read again on the Slot's goroutine, the
// deadline already past (about 0 ms of query). That is the window's, not the
// slice's: the slice is not split for its time or into the Query Group's
// shape, its time out ends not_split, and the next Slot reads the slices the
// Slot before read.
func TestASliceWhosePermitWaitMeetsTheDeadlineIsNoSliceTooLarge(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", largeGroupStrategy(t), 0).Err(); err != nil {
		t.Fatal(err)
	}
	// An hour ahead of the real clock: no deadline runs out in real time,
	// only when the Slots' clock is moved past it.
	base := time.Now().Unix() + 3600
	base -= base % 60
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	// pastAt, once set, is where the first query after moves the clock to:
	// past the Slot's deadline as its first slice is answered.
	var pastAt atomic.Int64
	// Past a slice's series (worker sliceSeries, 87,000): read in two asked
	// slices, a starting tree's five, once a Slot has read it.
	answers := windowMeasureAnswers{series: 90_000}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if at := pastAt.Swap(0); at != 0 {
			clock.Store(at)
		}
		answers.ServeHTTP(writer, request)
	}))
	defer server.Close()

	cfg := largeGroupConfig(t, address, server.URL)
	cfg.Redis.StatePrefix = "alarmd-slice-deadline"
	transport := &countingWindowTransport{next: http.DefaultTransport}
	var mu sync.Mutex
	var ended []observability.QueryFailureFacts
	var usages []observability.SlotBudgetUsageFacts
	// errs is the first few errors any stage raised, for a case that fails
	// to say how.
	var errs []string
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		mu.Lock()
		defer mu.Unlock()
		if observation.Err != nil && len(errs) < 6 {
			errs = append(errs, string(observation.Stage)+": "+observation.Err.Error())
		}
		if facts := observation.QueryFailure; facts != nil && facts.SliceTimeout != "" {
			ended = append(ended, *facts)
		}
		if observation.Stage == observability.StageSlotCompleted && observation.SlotBudgetUsage != nil {
			usages = append(usages, *observation.SlotBudgetUsage)
		}
	})
	sink := &countingWindowEventSink{}
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, StreamClock: time.Now, HTTPClient: &http.Client{Transport: transport}, AdditionalObserver: observer,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return sink, nil
			}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = bundle.Shutdown(ctx) }()
	settleExecutableView(ctx, bundle, 10*time.Second)

	// slicesOf runs the Slot at base+60*slot, and returns the slices its
	// queries read, sorted, and its usage when it completed. A run that
	// returns an error is a Slot that failed, its usage none.
	slicesOf := func(slot int64) ([]string, *observability.SlotBudgetUsageFacts) {
		t.Helper()
		from := transport.answered()
		mu.Lock()
		completedBefore := len(usages)
		mu.Unlock()
		if pastAt.Load() == 0 {
			clock.Store(base + slot*60 + 1)
		}
		if err := bundle.runScheduledOnce(ctx); err != nil {
			t.Logf("Slot %d: %v", slot, err)
		}
		var slices []string
		for _, request := range transport.since(from) {
			if !request.failed {
				slices = append(slices, request.slice)
			}
		}
		sort.Strings(slices)
		mu.Lock()
		defer mu.Unlock()
		if len(usages) > completedBefore {
			usage := usages[len(usages)-1]
			return slices, &usage
		}
		return slices, nil
	}
	// The first Slot reads the Query Group whole; the second starts in
	// slices by its series.
	if _, usage := slicesOf(0); usage == nil {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("the first Slot did not complete: %q", errs)
	}
	before, usage := slicesOf(1)
	if usage == nil || usage.InputSlices < 2 || usage.InputSliceShapeKept != 0 {
		t.Fatalf("the second Slot: usage %+v, want it completed in slices, nothing kept", usage)
	}
	// The third: its clock at its own time, then past its deadline - the
	// minute after it, at which the Slot is due - as its first slice is
	// answered.
	clock.Store(base + 2*60 + 1)
	pastAt.Store(base + 3*60 + 1)
	mu.Lock()
	endedBefore := len(ended)
	mu.Unlock()
	if _, usage := slicesOf(2); usage != nil {
		t.Fatalf("the Slot whose slices' permit waits met its deadline completed: %+v", usage)
	}
	mu.Lock()
	failures := append([]observability.QueryFailureFacts(nil), ended[endedBefore:]...)
	mu.Unlock()
	if len(failures) != 1 || failures[0].Code != "QUERY_PERMIT_DEADLINE" || failures[0].SliceTimeout != observability.InputSliceTimeoutNotSplit {
		t.Fatalf("the slice's time out ended %+v, want QUERY_PERMIT_DEADLINE not_split once", failures)
	}
	// The next Slot: due later, its own deadline ahead.
	after, usage := slicesOf(4)
	if usage == nil || usage.InputSliceShapeStarted != 0 || usage.InputSliceShapeKept != 0 {
		t.Fatalf("the Slot after: usage %+v, want it completed in no shape", usage)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("the Slot after read slices %v, want the ones the Slot before the deadline read %v", after, before)
	}
}
