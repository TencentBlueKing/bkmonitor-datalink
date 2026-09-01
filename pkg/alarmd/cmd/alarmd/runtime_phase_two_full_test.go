// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
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

type recordingPhaseTwoEventSink struct {
	mu     sync.Mutex
	events []contract.TriggerEventV1
	closed bool
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
