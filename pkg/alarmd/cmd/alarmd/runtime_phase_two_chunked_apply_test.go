// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// One Slot with more series than one Store call carries is applied in two
// chunks. The first attempt loses the second chunk to a storage failure after
// the first chunk was written and Events were acknowledged; the Slot stays
// retryable and Progress keeps the unfinished projection. The second attempt
// re-runs the whole query, evaluation and write path: the first chunk's keys
// read back ALREADY_APPLIED and are neither rewritten nor re-announced, only
// the unapplied chunk is written and its Events re-sent, and Progress commits
// exactly once with FULL.
func TestProductionPhaseTwoBundleReRunsChunkedSlotIdempotently(t *testing.T) {
	const series = 8193
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}

	base := time.Now().Unix()
	base -= base % 300
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	var body bytes.Buffer
	body.WriteString(`{"series":[`)
	for index := 0; index < series; index++ {
		if index > 0 {
			body.WriteByte(',')
		}
		fmt.Fprintf(&body, `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["10.0.%d.%d"],"values":[[%d,95]]}`,
			index/256, index%256, (base-1)*1000)
	}
	body.WriteString(`],"status":null,"trace_id":"c2-chunked-apply","is_partial":false,"result_table_id":["system.cpu"]}`)
	response := body.Bytes()
	var uqCalls atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		uqCalls.Add(1)
		_, _ = writer.Write(response)
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-c2-chunked"
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
	// Every series is abnormal, so the Slot announces one Event per series;
	// the product State budget already admits the Slot through two chunks.
	cfg.PhaseTwo.Coordinator.MaxEvents = 65536
	cfg.PhaseTwo.Coordinator.MaxRetainedBytes = 1 << 30
	if cfg.Limits.Store.MaxKeysPerBatch != 8192 || cfg.PhaseTwo.Coordinator.MaxStateMutations < series {
		t.Fatalf("store call bound %d and State budget %d do not chunk %d series", cfg.Limits.Store.MaxKeysPerBatch, cfg.PhaseTwo.Coordinator.MaxStateMutations, series)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("chunked apply configuration rejected: %v", err)
	}

	events := &recordingPhaseTwoEventSink{}
	var observationsMu sync.Mutex
	var observations []observability.Observation
	var failSecondChunk atomic.Bool
	observer := observability.ObserverFunc(func(ctx context.Context, observation observability.Observation) {
		observationsMu.Lock()
		observations = append(observations, observation)
		observationsMu.Unlock()
		chunk := observation.StateApplyChunk
		if failSecondChunk.Load() && observation.Stage == observability.StageStateApplied && observation.Err == nil &&
			chunk != nil && chunk.Index == 0 && chunk.Count == 2 {
			// Between the first and the second chunk of the first attempt the
			// server refuses every write, so the second chunk's scripts fail.
			if err := redisClient.ConfigSet(ctx, "maxmemory", "1").Err(); err != nil {
				t.Errorf("refuse writes after the first chunk: %v", err)
			}
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
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return events, nil },
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	defer func() {
		if err := bundle.Shutdown(ctx); err != nil {
			t.Errorf("phase-two production Shutdown() error = %v", err)
		}
	}()
	if len(bundle.queryGroups) != 1 {
		t.Fatalf("assigned Query Groups = %v", bundle.queryGroups)
	}
	queryGroup := bundle.queryGroups[0]
	productionOwnership := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	keyPattern := cfg.Redis.StatePrefix + ":runtime:v2:*"

	// Attempt 1: chunk 1 written, chunk 2 refused, Progress not committed.
	failSecondChunk.Store(true)
	clock.Store(base + 1)
	firstStarted := time.Now()
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("runScheduledOnce(first attempt) error = %v", err)
	}
	firstElapsed := time.Since(firstStarted)
	failSecondChunk.Store(false)
	if err := redisClient.ConfigSet(ctx, "maxmemory", "0").Err(); err != nil {
		t.Fatal(err)
	}
	firstEvents := events.snapshot()
	if len(firstEvents) != series {
		t.Fatalf("first attempt announced %d events, want one per series before the first chunk", len(firstEvents))
	}
	keys, err := redisClient.Keys(ctx, keyPattern).Result()
	if err != nil || len(keys) != cfg.Limits.Store.MaxKeysPerBatch {
		t.Fatalf("Runtime State keys after the failed attempt = %d error=%v, want exactly the first chunk", len(keys), err)
	}
	progress := loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroup)
	if progress.LastFullSlot != 0 || progress.NextSlot != execution.EvaluationTime(base) || progress.UnfinishedSlot == nil {
		t.Fatalf("Progress after the failed attempt = %+v, want the unfinished projection without a commit", progress)
	}
	observationsMu.Lock()
	firstApplied := stateApplyChunkObservations(observations)
	firstCommits := countObservations(observations, observability.StageProgressCommitted, observability.ResultSuccess)
	observationsMu.Unlock()
	if len(firstApplied) != 2 || firstApplied[0].Result != observability.ResultSuccess || firstApplied[0].Counts.Keys != 8192 ||
		firstApplied[1].Err == nil || firstApplied[1].StateApplyChunk.Index != 1 ||
		firstApplied[1].ReasonCode != execution.ReasonCode(contract.ReasonStateWriteRetryable) || firstCommits != 0 {
		t.Fatalf("first attempt chunk observations = %+v commits=%d", firstApplied, firstCommits)
	}

	// Attempt 2: the retry of the same Slot through the real path.
	secondStarted := time.Now()
	var completed bool
	for tick := 1; tick <= 6 && !completed; tick++ {
		clock.Store(base + 1 + int64(tick)*5)
		if err := bundle.runScheduledOnce(ctx); err != nil {
			t.Fatalf("runScheduledOnce(retry %d) error = %v", tick, err)
		}
		progress = loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroup)
		completed = progress.LastFullSlot == execution.EvaluationTime(base)
	}
	secondElapsed := time.Since(secondStarted)
	if !completed || progress.LastCompletionKind != execution.CompletionFull || progress.NextSlot != execution.EvaluationTime(base+300) {
		t.Fatalf("Progress after the re-run = %+v, want FULL at %d", progress, base)
	}
	if uqCalls.Load() != 2 {
		t.Fatalf("real UQ wire calls = %d, want one per attempt", uqCalls.Load())
	}
	keys, err = redisClient.Keys(ctx, keyPattern).Result()
	if err != nil || len(keys) != series {
		t.Fatalf("Runtime State keys after the re-run = %d error=%v, want every series", len(keys), err)
	}
	allEvents := events.snapshot()
	replayed := allEvents[len(firstEvents):]
	if len(replayed) != series-cfg.Limits.Store.MaxKeysPerBatch {
		t.Fatalf("re-run announced %d events, want only the unapplied chunk's %d", len(replayed), series-cfg.Limits.Store.MaxKeysPerBatch)
	}
	first := make(map[string]contract.TriggerEventV1, len(firstEvents))
	for _, event := range firstEvents {
		first[event.RecordRef.DimensionIdentityDigest] = event
	}
	for _, event := range replayed {
		original, found := first[event.RecordRef.DimensionIdentityDigest]
		if !found || original.EvaluationTime != event.EvaluationTime || original.PlanRef != event.PlanRef || original.EventKind != event.EventKind {
			t.Fatalf("re-sent event %+v has no stable first-attempt identity", event)
		}
	}

	// Every key holds its first revision: the re-run rewrote nothing.
	backend, err := state.NewRedisBackend(cfg.RedisBackendOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	router, err := state.NewFixedRouter("phase-two-primary", backend)
	if err != nil {
		t.Fatal(err)
	}
	stateStore, err := state.NewExecutionStore(state.ExecutionStoreOptions{
		Prefix: cfg.Redis.StatePrefix, Router: router, MaxValueBytes: cfg.Limits.Codec.MaxEncodedBytes,
		MaxItemsPerCall: cfg.Limits.Store.MaxKeysPerBatch, RuntimeTTL: cfg.Redis.MaxTTL.Duration(),
	})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := productionOwnership.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := productionOwnership.dependencies.Catalog.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: queryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: execution.EvaluationTime(base),
		DuePlans: schedule.DuePlanRefs(execution.EvaluationTime(base)),
	})
	if err != nil {
		t.Fatal(err)
	}
	applyVersion, err := execution.BuildApplyVersion(frozen.Contract, 1)
	if err != nil {
		t.Fatal(err)
	}
	items := make([]execution.StatePreflightItem, 0, len(firstEvents))
	for _, event := range firstEvents {
		items = append(items, execution.StatePreflightItem{Identity: execution.StateKeyIdentity{
			Plan:                 execution.PlanIdentity{TenantID: event.TenantID, BusinessID: event.BusinessID, StrategyID: event.PlanRef.StrategyID},
			StateGeneration:      execution.StateGeneration(event.PlanRef.StateCompatibilityHash),
			SeriesIdentityDigest: execution.SeriesIdentityDigest(event.RecordRef.DimensionIdentityDigest),
		}, ApplyVersion: applyVersion})
	}
	for start := 0; start < len(items); start += cfg.Limits.Store.MaxKeysPerBatch {
		end := min(start+cfg.Limits.Store.MaxKeysPerBatch, len(items))
		loaded, err := stateStore.LoadRuntime(ctx, execution.StatePreflightRequest{Contract: frozen.Contract, Items: items[start:end]})
		if err != nil {
			t.Fatal(err)
		}
		for _, view := range loaded.Items {
			if view.BlobRevision != 1 || (view.Status != execution.StateFoundReady && view.Status != execution.StateFoundWarming) ||
				view.PersistedApplyVersion != applyVersion {
				t.Fatalf("Runtime State %s = revision %d status %s version %+v, want one write at the Slot version",
					view.Identity.SeriesIdentityDigest, view.BlobRevision, view.Status, view.PersistedApplyVersion)
			}
		}
	}

	observationsMu.Lock()
	applied := stateApplyChunkObservations(observations)
	commits := countObservations(observations, observability.StageProgressCommitted, observability.ResultSuccess)
	stages := observedStages(observations[len(observations)-min(len(observations), 4096):])
	observationsMu.Unlock()
	if len(applied) != 3 || applied[2].Counts.Keys != int64(series-cfg.Limits.Store.MaxKeysPerBatch) ||
		applied[2].StateApplyChunk.Count != 1 || applied[2].Result != observability.ResultSuccess || commits != 1 {
		t.Fatalf("re-run applied %+v with %d Progress commits, want one chunk of the unapplied keys and one commit", applied, commits)
	}
	assertObservedOrder(t, stages, []observability.Stage{
		observability.StageEventACKed, observability.StageStateApplied, observability.StageProgressCommitted,
	})
	t.Logf("chunked apply capacity evidence: series=%d first attempt=%s (chunk 1 written, chunk 2 refused) re-run=%s",
		series, firstElapsed, secondElapsed)
}

func stateApplyChunkObservations(observations []observability.Observation) []observability.Observation {
	var matched []observability.Observation
	for _, observation := range observations {
		if observation.Component == observability.ComponentState && observation.Stage == observability.StageStateApplied &&
			observation.StateApplyChunk != nil {
			matched = append(matched, observation)
		}
	}
	return matched
}

func countObservations(observations []observability.Observation, stage observability.Stage, result observability.Result) int {
	count := 0
	for _, observation := range observations {
		if observation.Stage == stage && observation.Result == result {
			count++
		}
	}
	return count
}
