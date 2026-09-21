// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// TestProductionPhaseTwoWorkerAcknowledgesTheActivationItExecutesBy: the
// production bundle writes into its heartbeat the Activation record revision
// it executes by and its own occupancy, and publishes the same revision on
// its fleet snapshot, so the fleet page can say the replica is acked against
// what the control plane published. Nothing reads the heartbeat fields yet;
// this pins that they are written, from the same sources as the page.
func TestProductionPhaseTwoWorkerAcknowledgesTheActivationItExecutesBy(t *testing.T) {
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
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd:phase-two:acknowledgement"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	var nowUnix atomic.Int64
	nowUnix.Store(time.Now().Unix())
	now := func() time.Time { return time.Unix(nowUnix.Load(), 0) }
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("open production bundle: %v", err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = bundle.Shutdown(ctx) }()
	control := bundle.dependencies.Control.(*productionPhaseTwoControl)
	activation, err := control.dependencies.Repository.LoadActivation(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if activation.RecordRevision == 0 {
		t.Fatal("the bundle started without an activation record")
	}

	// The heartbeat carries the acknowledgement and the load.
	if err := bundle.register(ctx, ownership.WorkerReady); err != nil {
		t.Fatal(err)
	}
	store, err := ownership.NewRedisStoreWithClient(redisClient, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership"))
	if err != nil {
		t.Fatal(err)
	}
	workers, _, err := store.ListReadyWorkers(ctx, now())
	if err != nil || len(workers) != 1 {
		t.Fatalf("ListReadyWorkers() = %+v, %v; want this worker", workers, err)
	}
	registration := workers[0]
	if registration.Applied == nil || registration.Applied.ActivationRecordRevision != activation.RecordRevision {
		t.Fatalf("heartbeat applied = %+v, want activation record revision %d", registration.Applied, activation.RecordRevision)
	}
	if registration.Load == nil || registration.Load.OwnedQueryGroups != len(bundle.ownedQueryGroups()) ||
		registration.Load.PermitBudget <= 0 {
		t.Fatalf("heartbeat load = %+v, want the owned count %d and the permit budget", registration.Load, len(bundle.ownedQueryGroups()))
	}

	// The fleet page reads the same revision back from the snapshot and
	// counts the replica as acked against the published one.
	bundle.dependencies.PublishFleet(ctx)
	recorder := httptest.NewRecorder()
	bundle.dependencies.FleetAPI.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("fleet health status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var health fleet.HealthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if health.PublishedVersion != activation.RecordRevision || health.Workers != (fleet.WorkerAcknowledgement{Ready: 1, Acked: 1}) {
		t.Fatalf("fleet health = published %d workers %+v, want published %d and one acked replica", health.PublishedVersion, health.Workers, activation.RecordRevision)
	}
	if len(health.PerReplica) != 1 || health.PerReplica[0].AckedVersion == nil || *health.PerReplica[0].AckedVersion != activation.RecordRevision {
		t.Fatalf("per-replica acked version = %+v, want %d", health.PerReplica, activation.RecordRevision)
	}
}
