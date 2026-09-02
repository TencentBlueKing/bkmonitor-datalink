// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
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

func TestProductionPhaseTwoBundleUsesCanonicalSourceAndRuntimeRedisOverride(t *testing.T) {
	sourceAddress, sourceClient := startPhaseTwoRedis(t)
	runtimeAddress, runtimeClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := sourceClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := sourceClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = sourceAddress
	cfg.Redis.StatePrefix = "alarmd:phase-two:g1:v1"
	runtimeRedis := cfg.Redis.Connection()
	runtimeRedis.Address = runtimeAddress
	cfg.PhaseTwo.RuntimeRedis = &runtimeRedis
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL

	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: uqServer.Client(),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	if len(bundle.queryGroups) != 1 {
		t.Fatalf("selected strategies compiled to Query Groups = %v, want exactly one", bundle.queryGroups)
	}
	sourceKeys, err := sourceClient.Keys(ctx, cfg.Redis.StatePrefix+"*").Result()
	if err != nil || len(sourceKeys) != 0 {
		t.Fatalf("top-level StrategySource Redis contains runtime keys %v, error=%v", sourceKeys, err)
	}
	runtimeKeys, err := runtimeClient.Keys(ctx, cfg.Redis.StatePrefix+"*").Result()
	if err != nil || len(runtimeKeys) == 0 {
		t.Fatalf("runtime Redis keys = %v, error=%v, want Go-owned facts", runtimeKeys, err)
	}
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatalf("phase-two production Shutdown() error = %v", err)
	}
}

func TestProductionPhaseTwoBundleStartsIdleWithEmptyCatalogThenActivatesQueryGroup(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}

	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd:phase-two:empty-catalog"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL

	var observationsMu sync.Mutex
	var observations []observability.Observation
	health := newPhaseTwoApplicationHealth()
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime), health,
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observationsMu.Lock()
				defer observationsMu.Unlock()
				observations = append(observations, observation)
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production empty Catalog Start() error = %v", err)
	}
	if len(bundle.queryGroups) != 0 || len(bundle.runners) != 0 {
		t.Fatalf("empty Catalog runtime Query Groups/runners = %v/%d, want healthy idle", bundle.queryGroups, len(bundle.runners))
	}
	productionControl := bundle.dependencies.Control.(*productionPhaseTwoControl)
	activation, err := productionControl.dependencies.Repository.LoadActivation(ctx)
	if err != nil || activation.RecordRevision != 1 || len(activation.Plans) != 0 {
		t.Fatalf("empty Catalog activation = %+v, %v", activation, err)
	}
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("empty Catalog health = %+v, want ready idle Worker", snapshot)
	}

	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("first non-empty refresh error = %v", err)
	}
	if err := bundle.refreshAndReconcile(ctx, true); err != nil {
		t.Fatalf("confirmed non-empty refresh error = %v", err)
	}
	if len(bundle.queryGroups) != 1 || len(bundle.runners) != 1 {
		t.Fatalf("activated Query Groups/runners = %v/%d, want one", bundle.queryGroups, len(bundle.runners))
	}
	activation, err = productionControl.dependencies.Repository.LoadActivation(ctx)
	if err != nil || activation.RecordRevision != 2 || len(activation.Plans) != 1 {
		t.Fatalf("non-empty Catalog activation = %+v, %v", activation, err)
	}
	if snapshot := health.HealthSnapshot(); snapshot.State != observability.HealthReady || !snapshot.Ready {
		t.Fatalf("post-activation health = %+v, want ready", snapshot)
	}
	observationsMu.Lock()
	for _, observation := range observations {
		if observation.Stage == observability.Stage(observability.StageFatal) {
			observationsMu.Unlock()
			t.Fatalf("legal empty Catalog became process fatal: %+v", observation)
		}
	}
	observationsMu.Unlock()
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatalf("phase-two production Shutdown() error = %v", err)
	}
}

func TestProductionPhaseTwoBundleExecutesFullNonEmptySlotEndToEnd(t *testing.T) {
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
	var uqCalls atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		uqCalls.Add(1)
		if request.URL.Path != "/query/ts" || request.Header.Get("Bk-Query-Source") != "alarmd" ||
			request.Header.Get("X-Bk-Tenant-Id") != "tenant-a" || request.Header.Get("X-Bk-Scope-Space-Uid") != "bkcc__2" {
			t.Errorf("unexpected UQ wire coordinates: path=%s headers=%v", request.URL.Path, request.Header)
		}
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode UQ request: %v", err)
		}
		if len(payload["query_list"].([]any)) != 1 {
			t.Errorf("UQ request query_list = %#v", payload["query_list"])
		}
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` +
			strconv.FormatInt((base-1)*1000, 10) + `,95]]}],"status":null,"trace_id":"g1-full-uq","is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g1-full"
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

	events := &recordingPhaseTwoEventSink{}
	var observationsMu sync.Mutex
	var observations []observability.Observation
	additionalObserver := observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
		observationsMu.Lock()
		defer observationsMu.Unlock()
		observations = append(observations, observation)
	})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(), AdditionalObserver: additionalObserver,
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return events, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	clock.Store(base + 1)
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("phase-two production Slot error = %v", err)
	}
	if uqCalls.Load() != 1 {
		t.Fatalf("real UQ wire calls = %d, want 1", uqCalls.Load())
	}
	written := events.snapshot()
	if len(written) != 1 || written[0].EventKind != "ABNORMAL" || written[0].PlanRef.StrategyID != "1001" {
		t.Fatalf("TriggerEvent ACK fixture = %+v", written)
	}

	if len(bundle.queryGroups) != 1 {
		t.Fatalf("assigned Query Groups = %v", bundle.queryGroups)
	}
	queryGroup := bundle.queryGroups[0]
	productionOwnership, ok := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	if !ok {
		t.Fatalf("production ownership type = %T", bundle.dependencies.Ownership)
	}
	progress, err := productionOwnership.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: queryGroup})
	if err != nil || progress.Status != execution.ProgressFound || progress.Progress == nil ||
		progress.Progress.LastFullSlot != execution.EvaluationTime(base) ||
		progress.Progress.NextSlot != execution.EvaluationTime(base+300) {
		t.Fatalf("Redis Progress = %+v, error=%v", progress, err)
	}

	schedule, err := productionOwnership.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
	if err != nil {
		t.Fatal(err)
	}
	firstSlot, ok := schedule.FirstSlot()
	if !ok || firstSlot != execution.EvaluationTime(base) {
		t.Fatalf("initial frozen Slot = %d, %v", firstSlot, ok)
	}
	frozen, err := productionOwnership.dependencies.Catalog.FreezeSlotContract(ctx, execution.FreezeSlotContractRequest{
		QueryGroup: queryGroup, ScheduleRevision: schedule.Segment.ScheduleRevision,
		ScheduleSegmentStart: schedule.Segment.Start, EvaluationTime: firstSlot,
		DuePlans: schedule.DuePlanRefs(firstSlot),
	})
	if err != nil {
		t.Fatal(err)
	}
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
	applyVersion, err := execution.BuildApplyVersion(frozen.Contract, 1)
	if err != nil {
		t.Fatal(err)
	}
	stateIdentity := execution.StateKeyIdentity{
		Plan: execution.PlanIdentity{
			TenantID: written[0].TenantID, BusinessID: written[0].BusinessID, StrategyID: written[0].PlanRef.StrategyID,
		},
		StateGeneration:      execution.StateGeneration(written[0].PlanRef.StateCompatibilityHash),
		SeriesIdentityDigest: execution.SeriesIdentityDigest(written[0].RecordRef.DimensionIdentityDigest),
	}
	stateResult, err := stateStore.LoadRuntime(ctx, execution.StatePreflightRequest{
		Contract: frozen.Contract, Items: []execution.StatePreflightItem{{Identity: stateIdentity, ApplyVersion: applyVersion}},
	})
	if err != nil || len(stateResult.Items) != 1 || stateResult.Items[0].BlobRevision == 0 ||
		(stateResult.Items[0].Status != execution.StateFoundReady && stateResult.Items[0].Status != execution.StateFoundWarming) {
		t.Fatalf("Redis Runtime State = %+v, error=%v", stateResult, err)
	}

	observationsMu.Lock()
	stages := observedStages(observations)
	observationsMu.Unlock()
	assertObservedOrder(t, stages, []observability.Stage{
		observability.StageSnapshotRefreshed, observability.StageAssignmentAcquired,
		observability.StageScheduleDue, observability.StageSlotStarted, observability.StageEvaluationCompleted,
		observability.StageQueryCompleted, observability.StageSideEffectAdmission,
		observability.StageEventACKed, observability.StageStateApplied,
		observability.StageProgressCommitted, observability.StageSlotCompleted,
	})
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatalf("phase-two production Shutdown() error = %v", err)
	}
	if !events.isClosed() {
		t.Fatal("production shutdown did not close TriggerEvent sink")
	}
}

func TestProductionPhaseTwoBundleKeepsHealthyQueryGroupWhenSiblingEventACKIsRetryable(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	failedDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var healthyStrategy map[string]any
	if err := json.Unmarshal(failedDocument, &healthyStrategy); err != nil {
		t.Fatal(err)
	}
	healthyStrategy["id"] = 1002
	item := healthyStrategy["items"].([]any)[0].(map[string]any)
	item["id"] = 12
	item["query_md5"] = "g3b-healthy-query-md5"
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["result_table_id"] = "system.mem"
	healthyDocument, err := json.Marshal(healthyStrategy)
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001,1002]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", failedDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1002", healthyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}

	base := time.Now().Unix()
	base -= base % 300
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	var uqCalls atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		uqCalls.Add(1)
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` +
			strconv.FormatInt((base-1)*1000, 10) + `,95]]}],"status":null,"trace_id":"g3b-query","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g3b-qg-isolation"
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

	events := &selectiveRetryablePhaseTwoEventSink{failedStrategyID: "1001"}
	var observationsMu sync.Mutex
	var observations []observability.Observation
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				observationsMu.Lock()
				defer observationsMu.Unlock()
				observations = append(observations, observation)
			}),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return events, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("openProductionPhaseTwoBundleWithDependencies() error = %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("phase-two production Start() error = %v", err)
	}
	if len(bundle.queryGroups) != 2 || len(bundle.runners) != 2 {
		t.Fatalf("initial Query Groups/runners = %v/%d, want two", bundle.queryGroups, len(bundle.runners))
	}
	runResults := make(map[execution.QueryGroupIdentity]*recordingPhaseTwoQueryGroupRuntime, len(bundle.runners))
	bundle.mu.Lock()
	for queryGroup, lifecycle := range bundle.runners {
		recording := &recordingPhaseTwoQueryGroupRuntime{next: lifecycle.runner}
		lifecycle.runner = recording
		runResults[queryGroup] = recording
	}
	bundle.mu.Unlock()
	clock.Store(base + 1)
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("runScheduledOnce(retryable sibling) error = %v", err)
	}
	if uqCalls.Load() != 2 {
		t.Fatalf("real UQ wire calls = %d, want two independent Query Groups", uqCalls.Load())
	}
	if len(bundle.runners) != 2 {
		t.Fatalf("retryable Event ACK migrated a Query Group: runners=%d", len(bundle.runners))
	}

	attempted, acknowledged := events.snapshot()
	if attempted["1001"] != 1 || attempted["1002"] != 1 || acknowledged["1001"] != 0 || acknowledged["1002"] != 1 {
		t.Fatalf("Event attempts/ACKs = %v/%v, want failed retryable sibling and healthy ACK", attempted, acknowledged)
	}
	productionOwnership := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	queryGroupsByStrategy := make(map[string]execution.QueryGroupIdentity, 2)
	for _, queryGroup := range bundle.queryGroups {
		schedule, scheduleErr := productionOwnership.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
		if scheduleErr != nil || len(schedule.Plans) != 1 {
			t.Fatalf("Query Group %s schedule=%+v error=%v", queryGroup, schedule, scheduleErr)
		}
		queryGroupsByStrategy[schedule.Plans[0].Identity.StrategyID] = queryGroup
	}
	failedProgress, err := productionOwnership.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{
		QueryGroup: queryGroupsByStrategy["1001"],
	})
	if err != nil || failedProgress.Status != execution.ProgressMissing {
		t.Fatalf("failed Query Group Progress=%+v error=%v, want unchanged", failedProgress, err)
	}
	healthyProgress, err := productionOwnership.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{
		QueryGroup: queryGroupsByStrategy["1002"],
	})
	if err != nil || healthyProgress.Status != execution.ProgressFound || healthyProgress.Progress == nil ||
		healthyProgress.Progress.LastFullSlot != execution.EvaluationTime(base) {
		t.Fatalf("healthy Query Group Progress=%+v error=%v", healthyProgress, err)
	}
	failedResult, failedAttempted, failedErr := runResults[queryGroupsByStrategy["1001"]].snapshot()
	if failedErr != nil || !failedAttempted || failedResult.Completed ||
		failedResult.Result != observability.ResultRetrying ||
		failedResult.ReasonCode != execution.ReasonCode(contract.ReasonOutputACKUnknown) {
		t.Fatalf("failed scheduler Runner result=%+v attempted=%t error=%v", failedResult, failedAttempted, failedErr)
	}
	healthyResult, healthyAttempted, healthyErr := runResults[queryGroupsByStrategy["1002"]].snapshot()
	if healthyErr != nil || !healthyAttempted || !healthyResult.Completed {
		t.Fatalf("healthy scheduler Runner result=%+v attempted=%t error=%v", healthyResult, healthyAttempted, healthyErr)
	}
	bundle.mu.RLock()
	failedRunnerStillOwned := bundle.runners[queryGroupsByStrategy["1001"]].runner == runResults[queryGroupsByStrategy["1001"]]
	bundle.mu.RUnlock()
	if !failedRunnerStillOwned {
		t.Fatal("retryable Event ACK migrated the failed Query Group")
	}

	observationsMu.Lock()
	var retryingQG execution.QueryGroupIdentity
	for _, observation := range observations {
		if observation.Stage == observability.Stage(observability.StageFatal) {
			observationsMu.Unlock()
			t.Fatalf("retryable Query Group failure became Worker fatal: %+v", observation)
		}
		if observation.Stage == observability.StageSlotCompleted &&
			observation.Result == observability.ResultRetrying &&
			observation.ReasonCode == execution.ReasonCode(contract.ReasonOutputACKUnknown) {
			retryingQG = execution.QueryGroupIdentity(observation.Trace.QueryGroupKey)
		}
	}
	observationsMu.Unlock()
	if retryingQG != queryGroupsByStrategy["1001"] {
		t.Fatalf("retrying Slot observation Query Group=%s, want failed Query Group %s", retryingQG, queryGroupsByStrategy["1001"])
	}
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatalf("phase-two production Shutdown() error = %v", err)
	}
}

func TestProductionPhaseTwoBundleCompletesUnavailableWithoutStoppingHealthyQueryGroupAndRecovers(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, redisClient)

	base := time.Now().Add(2 * time.Second).Unix()
	var clock atomic.Int64
	clock.Store(base)
	now := func() time.Time { return time.Unix(clock.Load(), 0) }
	var failedOnce atomic.Bool
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var payload struct {
			QueryList []struct {
				TableID string `json:"table_id"`
			} `json:"query_list"`
			EndTime string `json:"end_time"`
		}
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || len(payload.QueryList) != 1 {
			t.Errorf("decode UQ request: payload=%+v error=%v", payload, err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if payload.QueryList[0].TableID == "system.cpu" && failedOnce.CompareAndSwap(false, true) {
			writer.WriteHeader(http.StatusBadGateway)
			return
		}
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil {
			t.Errorf("parse end_time %q: %v", payload.EndTime, err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["host"],"group_values":["127.0.0.1"],"values":[[` +
			strconv.FormatInt((end-1)*1000, 10) + `,95]]}],"status":null,"trace_id":"g3b-recovered","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-g3b-access-recovery"
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

	events := &recordingPhaseTwoEventSink{}
	var fatal atomic.Bool
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if observation.Stage == observability.Stage(observability.StageFatal) {
					fatal.Store(true)
				}
			}),
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

	productionOwnership := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	queryGroupsByStrategy := make(map[string]execution.QueryGroupIdentity, 2)
	for _, queryGroup := range bundle.queryGroups {
		schedule, scheduleErr := productionOwnership.dependencies.Catalog.ReadInitialFrozenSchedule(ctx, queryGroup)
		if scheduleErr != nil || len(schedule.Plans) != 1 {
			t.Fatalf("Query Group %s schedule=%+v error=%v", queryGroup, schedule, scheduleErr)
		}
		queryGroupsByStrategy[schedule.Plans[0].Identity.StrategyID] = queryGroup
	}

	clock.Store(base)
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("runScheduledOnce(unavailable sibling) error = %v", err)
	}
	failed := loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroupsByStrategy["1001"])
	healthy := loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroupsByStrategy["1002"])
	if failed.LastCompletionKind != execution.CompletionUnavailable || failed.LastFullSlot != 0 ||
		failed.CurrentOrRecentGap == nil || failed.CurrentOrRecentGap.ReasonCode != execution.ReasonCode(contract.ReasonQueryUnavailable) {
		t.Fatalf("unavailable Query Group Progress=%+v gap=%+v", failed, failed.CurrentOrRecentGap)
	}
	if healthy.LastFullSlot != execution.EvaluationTime(base) || healthy.LastCompletionKind != execution.CompletionFull {
		t.Fatalf("healthy Query Group Progress=%+v", healthy)
	}
	for _, event := range events.snapshot() {
		if event.PlanRef.StrategyID == "1001" {
			t.Fatalf("UNAVAILABLE emitted an unproven event: %+v", event)
		}
	}

	clock.Store(base + 1)
	if err := bundle.runScheduledOnce(ctx); err != nil {
		t.Fatalf("runScheduledOnce(recovered sibling) error = %v", err)
	}
	recovered := loadPhaseTwoProgress(t, ctx, productionOwnership, queryGroupsByStrategy["1001"])
	if recovered.LastFullSlot != execution.EvaluationTime(base+1) ||
		recovered.LastCompletionKind != execution.CompletionFull || recovered.NextSlot != execution.EvaluationTime(base+2) {
		t.Fatalf("recovered Query Group Progress=%+v", recovered)
	}
	foundRecoveredEvent := false
	for _, event := range events.snapshot() {
		foundRecoveredEvent = foundRecoveredEvent || event.PlanRef.StrategyID == "1001"
	}
	if !foundRecoveredEvent || fatal.Load() || len(bundle.runners) != 2 {
		t.Fatalf("recovery event=%t fatal=%t runners=%d", foundRecoveredEvent, fatal.Load(), len(bundle.runners))
	}
}

func installTwoPhaseTwoStrategies(t *testing.T, ctx context.Context, redisClient *redis.Client) {
	t.Helper()
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var firstStrategy map[string]any
	if err := json.Unmarshal(raw, &firstStrategy); err != nil {
		t.Fatal(err)
	}
	firstItem := firstStrategy["items"].([]any)[0].(map[string]any)
	firstItem["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 1
	first, err := json.Marshal(firstStrategy)
	if err != nil {
		t.Fatal(err)
	}
	var second map[string]any
	if err := json.Unmarshal(first, &second); err != nil {
		t.Fatal(err)
	}
	second["id"] = 1002
	item := second["items"].([]any)[0].(map[string]any)
	item["id"], item["query_md5"] = 12, "g3b-access-healthy-query-md5"
	item["query_configs"].([]any)[0].(map[string]any)["result_table_id"] = "system.mem"
	encoded, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":  `[1001,1002]`,
		"alarm-config.strategy_1001": first,
		"alarm-config.strategy_1002": encoded,
	} {
		if err := redisClient.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
}

func loadPhaseTwoProgress(
	t *testing.T,
	ctx context.Context,
	production *productionPhaseTwoOwnership,
	queryGroup execution.QueryGroupIdentity,
) execution.ScheduleProgress {
	t.Helper()
	loaded, err := production.dependencies.Progress.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: queryGroup})
	if err != nil || loaded.Status != execution.ProgressFound || loaded.Progress == nil {
		t.Fatalf("Query Group %s Progress=%+v error=%v", queryGroup, loaded, err)
	}
	return *loaded.Progress
}

type recordingPhaseTwoEventSink struct {
	mu     sync.Mutex
	events []contract.TriggerEventV1
	closed bool
}

type selectiveRetryablePhaseTwoEventSink struct {
	mu               sync.Mutex
	failedStrategyID string
	attempted        map[string]int
	acknowledged     map[string]int
	closed           bool
}

func (sink *selectiveRetryablePhaseTwoEventSink) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if sink.attempted == nil {
		sink.attempted = make(map[string]int)
		sink.acknowledged = make(map[string]int)
	}
	for index := range events {
		if _, err := contract.EncodeTriggerEventV1(&events[index]); err != nil {
			return err
		}
		strategyID := events[index].PlanRef.StrategyID
		sink.attempted[strategyID]++
		if strategyID == sink.failedStrategyID {
			return &retryablePhaseTwoEventError{err: errors.New("broker ACK unavailable")}
		}
	}
	for index := range events {
		sink.acknowledged[events[index].PlanRef.StrategyID]++
	}
	return nil
}

func (sink *selectiveRetryablePhaseTwoEventSink) Shutdown(context.Context) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.closed = true
	return nil
}

func (sink *selectiveRetryablePhaseTwoEventSink) Close() error {
	return sink.Shutdown(context.Background())
}

func (sink *selectiveRetryablePhaseTwoEventSink) snapshot() (map[string]int, map[string]int) {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	attempted := make(map[string]int, len(sink.attempted))
	acknowledged := make(map[string]int, len(sink.acknowledged))
	for strategyID, count := range sink.attempted {
		attempted[strategyID] = count
	}
	for strategyID, count := range sink.acknowledged {
		acknowledged[strategyID] = count
	}
	return attempted, acknowledged
}

type retryablePhaseTwoEventError struct{ err error }

func (err *retryablePhaseTwoEventError) Error() string              { return err.err.Error() }
func (err *retryablePhaseTwoEventError) Unwrap() error              { return err.err }
func (err *retryablePhaseTwoEventError) RetryableOutputDependency() {}

type recordingPhaseTwoQueryGroupRuntime struct {
	next phaseTwoQueryGroupRuntime
	mu   sync.Mutex

	result    execution.SlotExecutionResult
	attempted bool
	err       error
}

func (runtime *recordingPhaseTwoQueryGroupRuntime) RunOne(ctx context.Context) (execution.SlotExecutionResult, bool, error) {
	result, attempted, err := runtime.next.RunOne(ctx)
	runtime.mu.Lock()
	runtime.result = result
	runtime.attempted = attempted
	runtime.err = err
	runtime.mu.Unlock()
	return result, attempted, err
}

func (runtime *recordingPhaseTwoQueryGroupRuntime) MaintainLease(ctx context.Context, interval, ttl time.Duration) error {
	return runtime.next.MaintainLease(ctx, interval, ttl)
}

func (runtime *recordingPhaseTwoQueryGroupRuntime) Release(ctx context.Context) error {
	return runtime.next.Release(ctx)
}

func (runtime *recordingPhaseTwoQueryGroupRuntime) snapshot() (execution.SlotExecutionResult, bool, error) {
	runtime.mu.Lock()
	defer runtime.mu.Unlock()
	return runtime.result, runtime.attempted, runtime.err
}

func (sink *recordingPhaseTwoEventSink) WriteBatch(_ context.Context, events []contract.TriggerEventV1) error {
	for index := range events {
		if _, err := contract.EncodeTriggerEventV1(&events[index]); err != nil {
			return err
		}
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.events = append(sink.events, events...)
	return nil
}

func (sink *recordingPhaseTwoEventSink) Shutdown(context.Context) error {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	sink.closed = true
	return nil
}

func (sink *recordingPhaseTwoEventSink) Close() error { return sink.Shutdown(context.Background()) }

func (sink *recordingPhaseTwoEventSink) snapshot() []contract.TriggerEventV1 {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return append([]contract.TriggerEventV1(nil), sink.events...)
}

func (sink *recordingPhaseTwoEventSink) isClosed() bool {
	sink.mu.Lock()
	defer sink.mu.Unlock()
	return sink.closed
}

func assertObservedOrder(t *testing.T, got, want []observability.Stage) {
	t.Helper()
	index := 0
	for _, stage := range got {
		if index < len(want) && stage == want[index] {
			index++
		}
	}
	if index != len(want) {
		t.Fatalf("observation order = %v, missing ordered suffix %v", got, want[index:])
	}
}

func startPhaseTwoRedis(t *testing.T) (string, *redis.Client) {
	t.Helper()
	executable, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server is not installed")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(executable, "--bind", "127.0.0.1", "--port", port,
		"--save", "", "--appendonly", "no", "--dir", t.TempDir(), "--daemonize", "no", "--loglevel", "warning")
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{
		Addr: address, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second,
	})
	t.Cleanup(func() {
		_ = client.Close()
		if command.ProcessState == nil || !command.ProcessState.Exited() {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if client.Ping(context.Background()).Err() == nil {
			return address, client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("redis-server did not become ready: %s", output.String())
	return "", nil
}
