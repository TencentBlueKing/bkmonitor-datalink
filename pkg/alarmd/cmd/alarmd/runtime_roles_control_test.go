// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/publicsurface"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/roles"
)

func TestProductionControlRoleBundleCompilesWithoutWorkerDependencies(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	document, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Set(ctx, "alarm-config.strategy_1001", document, 0).Err(); err != nil {
		t.Fatal(err)
	}
	cfg := validGoAccessRuntimeConfig()
	cfg.Roles = roles.Set{roles.Control}
	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "control-only-test"
	cfg.PhaseTwo.Worker.ID = "control-1"
	withCompatibilityOutput(&cfg, address)
	// These coordinates must never be opened by the control factory. Its
	// execution budgets are absent too; only remote worker formats matter.
	cfg.PlatformCache.CMDB = &config.RedisConnectionConfig{Address: "not-a-redis-address"}
	cfg.PhaseTwo.Access.UQEndpoint = ""
	cfg.PhaseTwo.Coordinator = config.PhaseTwoCoordinatorConfig{}
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Hour)
	sink := &recordingPhaseTwoEventSink{}
	var sourceCalls int
	bundle, err := openProductionControlRoleBundle(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}),
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			sourceCalls++
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		}, phaseTwoProductionExternalDependencies{Now: time.Now,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) { return sink, nil })})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bundle.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	owner := bundle.dependencies.Ownership.(*productionPhaseTwoOwnership)
	if owner.dependencies.Executor != nil || owner.flights != nil || bundle.workerPorts.Query != nil || bundle.dependencies.ViewClient != nil {
		t.Fatal("control factory constructed local worker execution")
	}
	if sourceCalls != 1 {
		t.Fatalf("strategy source constructions = %d, want 1", sourceCalls)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if len(bundle.queryGroups) != 1 || len(bundle.runners) != 0 {
		t.Fatalf("compiled groups/runners = %v/%d, want 1/0", bundle.queryGroups, len(bundle.runners))
	}
	if state := bundle.dependencies.ViewStreamStats(); !state.Leading || state.ControlEpoch == 0 {
		t.Fatalf("control lease stream state = %+v", state)
	}
	keys, err := client.Keys(ctx, cfg.Redis.StatePrefix+"*").Result()
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range keys {
		if strings.Contains(key, ":worker:") || strings.Contains(key, ":qg_lease:") {
			t.Fatalf("control-only process created local execution key %q", key)
		}
	}
	request := httptest.NewRequest("GET", "/api/objects?scope=strategies", nil)
	response := httptest.NewRecorder()
	bundle.dependencies.FleetAPI.ServeHTTP(response, request)
	// Business-role processes expose directory evidence through authenticated
	// channel routing. An anonymous request must not bypass that boundary.
	if response.Code != 403 || !strings.Contains(response.Body.String(), publicsurface.RestrictedCode) {
		t.Fatalf("control exposed its private directory = %d %s", response.Code, response.Body.String())
	}
	if err := bundle.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	registry, err := ownership.NewRedisStoreWithClient(client, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership"))
	if err != nil {
		t.Fatal(err)
	}
	if leader, exists, err := registry.ReadControlLeader(ctx); err != nil || exists {
		t.Fatalf("leader after shutdown = %+v, %v, %v", leader, exists, err)
	}
	sink.mu.Lock()
	closed := sink.closed
	sink.mu.Unlock()
	if !closed {
		t.Fatal("control shutdown left its output sink open")
	}
}
