// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/strategy"
)

// A ring-ratio strategy reads a history window that ends before its primary
// window, so its history query is readable before the primary one and its
// Slot is not deferred as a whole: the execution sends the history query and
// waits in place, holding its slot, for the primary. Through the production
// bundle, a round run 200 ms before the primary is readable makes that wait:
// its slot_completed line carries it and the counter counts it, in the
// shortest bucket. A round run once both are readable makes none.
func TestAnInPlaceReadinessWaitIsOnTheSlotLineAndCounted(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	document := controlledG4StrategyDocument(t, 7402, strategy.DetectorKindSimpleRingRatio, "usage", "system.cpu_summary",
		[]string{"host"}, map[string]any{"floor": 50, "ceil": nil})
	if err := client.Set(ctx, "alarm-config.strategy_ids", "[7402]", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "alarm-config.strategy_7402", []byte(document), 0).Err(); err != nil {
		t.Fatal(err)
	}
	backend := &poolBackend{}
	base := controlledG4Base(t) + 60
	backend.clock.Store(base * 1000)
	var mu sync.Mutex
	var waits []observability.InPlaceWaitFacts
	completed := 0
	cfg := controlledG4RuntimeConfig(address, "http://controlled-uq", "alarmd-inplace-wait")
	// At the settling floor, so no read hold is seeded: a held Slot is
	// deferred until its last query is ready and never waits in place.
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(execution.MinimumSettlingWait)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(poolBudgetReserve)
	recorder := metric.NewRecorder(metric.BuildInfo{})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{
			Now: backend.now, HTTPClient: &http.Client{Transport: controlledRoundTripper(backend.roundTrip)},
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, o observability.Observation) {
				if o.Stage != observability.StageSlotCompleted {
					return
				}
				mu.Lock()
				defer mu.Unlock()
				completed++
				if o.InPlaceWait != nil {
					waits = append(waits, *o.InPlaceWait)
				}
			}),
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	round := func(minute int64, beforePrimaryReady time.Duration) ([]observability.InPlaceWaitFacts, int) {
		t.Helper()
		mu.Lock()
		waits, completed = nil, 0
		mu.Unlock()
		if len(bundle.queryGroups) != 1 {
			t.Fatalf("Query Groups = %v, want one", bundle.queryGroups)
		}
		backend.clock.Store((base+minute*60)*1000 + (firstReadWaitOf(cfg, bundle.queryGroups[0], 60) - beforePrimaryReady).Milliseconds())
		if err := runScheduledOnceSettled(ctx, bundle); err != nil {
			t.Fatalf("minute %d: %v", minute, err)
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]observability.InPlaceWaitFacts(nil), waits...), completed
	}
	if got, done := round(0, -500*time.Millisecond); done == 0 || len(got) != 0 {
		t.Fatalf("minute 0, run once both queries are readable: %d Slots completed with waits %+v, want a Slot and no wait", done, got)
	}
	got, done := round(1, 200*time.Millisecond)
	if done == 0 || len(got) != 1 {
		t.Fatalf("minute 1, run 200 ms before the primary is readable: %d Slots completed with waits %+v, want one wait", done, got)
	}
	wait := got[0]
	if wait.Waits != 1 || wait.TotalMillis < 150 || wait.TotalMillis > 250 || wait.SentBeforeFirstWait != 1 ||
		wait.BeforeAnySentMillis != 0 || wait.HeldPreparedBytes == 0 || wait.HeldInputBytes == 0 {
		t.Fatalf("the wait on the line %+v, want one of about 200 ms after the history query was sent, holding the history "+
			"answer that came back during it, with a prepared estimate", wait)
	}
	if got := counterValue(t, recorder, "bkmonitor_alarmd_access_inplace_readiness_waits_total", map[string]string{"wait": "le5s"}); got != 1 {
		t.Fatalf("access_inplace_readiness_waits_total{wait=le5s} = %v, want 1", got)
	}
}
