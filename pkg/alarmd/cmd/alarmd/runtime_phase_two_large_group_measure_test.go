// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// largeGroupStrategy is the largest Query Group seen on a 4 GiB replica as a
// legacy strategy: one time-series clause aggregated by the maximum over 60
// seconds by three dimensions, detected every 60 seconds, five anomalies of
// five points to trigger and three windows to recover.
func largeGroupStrategy(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	item := document["items"].([]any)[0].(map[string]any)
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["agg_interval"], query["agg_method"] = 60, "MAX"
	query["agg_dimension"] = []string{"bk_target_cloud_id", "bk_target_ip", "device_name"}
	detect := document["detects"].([]any)[0].(map[string]any)
	detect["trigger_config"] = map[string]any{"count": 5, "check_window": 5}
	detect["recovery_config"] = map[string]any{"check_window": 3}
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

// largeGroupAnswer is its answer for one Slot: series series of one point
// each, the three dimensions at the lengths the group's carry at most - a
// cloud id of three bytes, an address of fourteen, a device name of 32.
func largeGroupAnswer(series int, pointMillis int64) []byte {
	var body bytes.Buffer
	body.WriteString(`{"series":[`)
	for index := 0; index < series; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],`+
			`"group_keys":["bk_target_cloud_id","bk_target_ip","device_name"],"group_values":["%03d","198.51.100.%03d","device-%025d"],`+
			`"values":[[%d,12.5]]}`, index%1000, index%256, index, pointMillis)
	}
	body.WriteString(`],"status":null,"trace_id":"large-group-measure","is_partial":false,"result_table_id":["system.cpu"]}`)
	return body.Bytes()
}

// largeGroupLiveHeap is the live heap in bytes, as the runtime last marked it.
func largeGroupLiveHeap() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	return sample[0].Value.Uint64()
}

// TestMeasureALargeQueryGroupsSlotAgainstTheHeap runs Slots of the largest
// Query Group seen on a 4 GiB replica - 95,760 series of one point and three
// dimensions - through the production bundle on a real Redis, three a
// minute apart, and holds what the third reserved, input, output and State
// apart, to the live heap it took: the peak of the live heap over the Slot, the collector set to run at
// every few percent of growth, less what was live before it. It prints both
// and asserts nothing; run on demand with ALARMD_MEASURE_LARGE_GROUP=1. It
// builds against the code before batch 7 as well, which is how the two are
// compared.
func TestMeasureALargeQueryGroupsSlotAgainstTheHeap(t *testing.T) {
	if os.Getenv("ALARMD_MEASURE_LARGE_GROUP") == "" {
		t.Skip("a measurement: set ALARMD_MEASURE_LARGE_GROUP=1")
	}
	const series = 95_760
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", largeGroupStrategy(t), 0).Err(); err != nil {
		t.Fatal(err)
	}
	// A minute boundary two minutes ahead, so the frozen query deadline the
	// UQ client puts on the request stays ahead of the wall clock.
	base := time.Now().Unix()
	base = base - base%60 + 120
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	// Three Slots a minute apart, the third measured: by then the State holds
	// the history a running group's does, as far as its trigger window needs.
	const slots = 3
	var responses [slots][]byte
	for slot := range responses {
		responses[slot] = largeGroupAnswer(series, (base+int64(slot)*60-1)*1000)
	}
	var answering atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(responses[answering.Load()])
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-large-group"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)
	// The Slot is measured, not refused: every budget holds it, the counts
	// at the most the chunked State apply takes (max_keys_per_batch x 64).
	cfg.PhaseTwo.Coordinator.MaxEvents = 1 << 19
	cfg.PhaseTwo.Coordinator.MaxStateMutations = 1 << 19
	cfg.PhaseTwo.Coordinator.MaxGapMutations = 1 << 19
	cfg.PhaseTwo.Coordinator.MaxRetainedBytes = 8 << 30
	if err := cfg.Validate(); err != nil {
		t.Fatalf("configuration rejected: %v", err)
	}

	events := &recordingPhaseTwoEventSink{}
	var mu sync.Mutex
	var usage *observability.SlotBudgetUsageFacts
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		if observation.Stage == observability.StageSlotCompleted && observation.SlotBudgetUsage != nil {
			mu.Lock()
			copied := *observation.SlotBudgetUsage
			usage = &copied
			mu.Unlock()
		}
	})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(), AdditionalObserver: observer,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil }),
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

	for slot := int64(0); slot < slots-1; slot++ {
		answering.Store(slot)
		clock.Store(base + slot*60 + 1)
		if err := bundle.runScheduledOnce(ctx); err != nil {
			t.Fatalf("Slot %d: %v", slot, err)
		}
	}
	mu.Lock()
	usage = nil
	mu.Unlock()
	answering.Store(slots - 1)
	clock.Store(base + (slots-1)*60 + 1)
	previous := debug.SetGCPercent(5)
	runtime.GC()
	before := largeGroupLiveHeap()
	var peak atomic.Uint64
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(time.Millisecond) // fixed-wait: the sampling interval of a measurement; it waits on no condition
		defer ticker.Stop()
		for {
			if live := largeGroupLiveHeap(); live > peak.Load() {
				peak.Store(live)
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	started := time.Now()
	runErr := bundle.runScheduledOnce(ctx)
	took := time.Since(started)
	close(done)
	<-sampled
	debug.SetGCPercent(previous)
	if runErr != nil {
		t.Fatal(runErr)
	}
	mu.Lock()
	defer mu.Unlock()
	if usage == nil {
		t.Fatal("no Slot completed with its budget usage")
	}
	live := peak.Load() - before
	reserved := usage.RetainedInputBytes + usage.RetainedOutputBytes + usage.RetainedStateBytes + usage.RetainedGapBytes
	t.Logf("series=%d took=%s reserved=%d (input %d output %d state %d gap %d; %d a series) live_peak=%d (%d a series) ratio=%.3f "+
		"retained_bytes=%d resident_before=%d", usage.Series, took.Round(time.Millisecond), reserved, usage.RetainedInputBytes,
		usage.RetainedOutputBytes, usage.RetainedStateBytes, usage.RetainedGapBytes, reserved/max(usage.Series, 1),
		live, live/series, float64(reserved)/float64(live), usage.RetainedBytes, before)
}

// largeGroupConfig is the configuration the large group's measurements run
// under, the UQ answer at uqURL.
func largeGroupConfig(t *testing.T, address, uqURL string) config.Config {
	t.Helper()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-large-group"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqURL
	cfg.PhaseTwo.Access.MinReadyDelay = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.DownstreamExecutionReserve = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Worker.RegistrationRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.ControlLeaderRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(10 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)
	cfg.PhaseTwo.Coordinator.MaxEvents = 1 << 19
	cfg.PhaseTwo.Coordinator.MaxStateMutations = 1 << 19
	cfg.PhaseTwo.Coordinator.MaxGapMutations = 1 << 19
	cfg.PhaseTwo.Coordinator.MaxRetainedBytes = 8 << 30
	if err := cfg.Validate(); err != nil {
		t.Fatalf("configuration rejected: %v", err)
	}
	return cfg
}

// retainedReservedOf reads what the recorder says the retained pool holds,
// and whether it says it at all.
func retainedReservedOf(recorder *metric.Recorder) (uint64, bool) {
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		return 0, false
	}
	for _, family := range families {
		if family.GetName() != "alarmd_capacity_reserved" {
			continue
		}
		for _, series := range family.GetMetric() {
			for _, label := range series.GetLabel() {
				if label.GetName() == "budget" && label.GetValue() == "retained_bytes" {
					return uint64(series.GetGauge().GetValue()), true
				}
			}
		}
	}
	return 0, false
}

// TestMeasureALargeQueryGroupsTakeoverAgainstTheHeap takes the largest Query
// Group over as a restarted replica does: one bundle runs a Slot of it and
// commits, shuts down, and a second bundle on the same Redis starts three
// and a half minutes later and catches the group up - the Slots behind as
// replays, the current one as it comes. Through the second bundle's rounds
// it samples, every 10 ms, the live heap less what the retained pool holds,
// the collector set to run at every few percent of growth, and prints the
// most it read beside the live heap before the takeover. A takeover that
// holds a Slot's structures with nothing reserved reads far above what is
// live outside any Slot. Asserts nothing; run on demand with
// ALARMD_MEASURE_LARGE_GROUP=1. It builds against the code before batch 7.
func TestMeasureALargeQueryGroupsTakeoverAgainstTheHeap(t *testing.T) {
	if os.Getenv("ALARMD_MEASURE_LARGE_GROUP") == "" {
		t.Skip("a measurement: set ALARMD_MEASURE_LARGE_GROUP=1")
	}
	const series = 95_760
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", largeGroupStrategy(t), 0).Err(); err != nil {
		t.Fatal(err)
	}
	base := time.Now().Unix()
	base = base - base%60 + 120
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	// Each request is answered for its own range: one point a series at its
	// start, answers kept by start once built.
	var answersMu sync.Mutex
	answers := map[int64][]byte{}
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var query struct {
			StartTime string `json:"start_time"`
		}
		if err := json.NewDecoder(request.Body).Decode(&query); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		var start int64
		if _, err := fmt.Sscan(query.StartTime, &start); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		answersMu.Lock()
		answer, ok := answers[start]
		if !ok {
			answer = largeGroupAnswer(series, start*1000)
			answers[start] = answer
		}
		answersMu.Unlock()
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write(answer)
	}))
	defer uqServer.Close()
	cfg := largeGroupConfig(t, address, uqServer.URL)
	// What the second owner's rounds did, stage by stage, so a round that
	// ran nothing reads as that rather than as a takeover that held nothing.
	var stagesMu sync.Mutex
	stages := map[observability.Stage]int{}
	observer := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		stagesMu.Lock()
		stages[observation.Stage]++
		stagesMu.Unlock()
	})
	open := func(recorder *metric.Recorder) *phaseTwoWorkerBundle {
		bundle, err := openProductionPhaseTwoBundleWithDependencies(
			ctx, cfg, recorder, observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
			func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
				return controlplane.NewLegacyRedisStrategySource(client, prefix)
			},
			phaseTwoProductionExternalDependencies{
				Now: now, HTTPClient: uqServer.Client(), AdditionalObserver: observer,
				PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
					return &recordingPhaseTwoEventSink{}, nil
				}),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := bundle.Start(ctx); err != nil {
			t.Fatal(err)
		}
		settleExecutableView(ctx, bundle, 10*time.Second)
		return bundle
	}

	first := open(metric.NewRecorder(metric.BuildInfo{}))
	clock.Store(base + 1)
	if err := first.runScheduledOnce(ctx); err != nil {
		t.Fatalf("the first owner's Slot: %v", err)
	}
	if err := first.Shutdown(ctx); err != nil {
		t.Fatalf("the first owner's shutdown: %v", err)
	}

	previous := debug.SetGCPercent(5)
	defer debug.SetGCPercent(previous)
	runtime.GC()
	before := largeGroupLiveHeap()
	clock.Store(base + 3*60 + 30)
	recorder := metric.NewRecorder(metric.BuildInfo{})
	stagesMu.Lock()
	clear(stages)
	stagesMu.Unlock()
	second := open(recorder)
	t.Logf("the second owner holds %d Query Groups", len(second.queryGroups))
	defer func() { _ = second.Shutdown(ctx) }()
	var worst, worstLive, worstReserved, missed atomic.Uint64
	var worstAt atomic.Int64
	done := make(chan struct{})
	sampled := make(chan struct{})
	started := time.Now()
	go func() {
		defer close(sampled)
		ticker := time.NewTicker(10 * time.Millisecond) // fixed-wait: the sampling interval of a measurement; it waits on no condition
		defer ticker.Stop()
		for {
			live := largeGroupLiveHeap()
			reserved, known := retainedReservedOf(recorder)
			if !known {
				missed.Add(1)
			} else if live > reserved && live-reserved > worst.Load() {
				worst.Store(live - reserved)
				worstLive.Store(live)
				worstReserved.Store(reserved)
				worstAt.Store(int64(time.Since(started) / time.Millisecond))
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	// The rounds a takeover runs: the Slots behind, then the current one and
	// the next, a minute apart.
	// After each round returns, collected and read again: what is live
	// then with nothing reserved is what a finished Slot left reachable.
	for round := int64(0); round < 4; round++ {
		clock.Store(base + 3*60 + 30 + round*60)
		if err := second.runScheduledOnce(ctx); err != nil {
			t.Logf("round %d: %v", round, err)
		}
		runtime.GC()
		live := largeGroupLiveHeap()
		reserved, _ := retainedReservedOf(recorder)
		t.Logf("round %d done at %d ms: live %d reserved %d live_less_reserved_over_before %d", round,
			time.Since(started).Milliseconds(), live, reserved, int64(live)-int64(reserved)-int64(before))
	}
	close(done)
	<-sampled
	runtime.GC()
	after := largeGroupLiveHeap()
	stagesMu.Lock()
	t.Logf("the second owner's stages: slot_completed %d replay_takeover %d slot_diverted %d slot_skipped %d query_completed %d takeover_completed %d",
		stages[observability.StageSlotCompleted], stages[observability.StageReplayTakeover], stages[observability.StageSlotDiverted],
		stages[observability.StageSlotSkipped], stages[observability.StageQueryCompleted], stages[observability.StageTakeoverCompleted])
	stagesMu.Unlock()
	if missed.Load() > 0 {
		t.Logf("%d samples found no reservation reading", missed.Load())
	}
	t.Logf("takeover series=%d live_before=%d worst_live_less_reserved=%d (live %d reserved %d at %d ms) live_after=%d "+
		"worst_over_before=%d", series, before, worst.Load(), worstLive.Load(), worstReserved.Load(), worstAt.Load(), after,
		int64(worst.Load())-int64(before))
}
