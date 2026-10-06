// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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

// TestProductionPhaseTwoRefreshReusesItsObservationWhileTheSourceSignalsNoChange:
// the production bundle hands the Legacy source's change signal to the
// reconciler and puts how each round read onto the refresh line. A steady
// round that finds the signal where it was reads no document and says so,
// with the signal's age by the bundle's clock; the publisher moving the
// signal brings the next round's read back.
func TestProductionPhaseTwoRefreshReusesItsObservationWhileTheSourceSignalsNoChange(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var nowUnix atomic.Int64
	nowUnix.Store(time.Now().Unix())
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.last_updated", strconv.FormatInt(nowUnix.Load()-45, 10), 0).Err(); err != nil {
		t.Fatal(err)
	}
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd:phase-two:change-gate"
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	var refreshMu sync.Mutex
	var refreshes []observability.SourceRefreshFacts
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: func() time.Time { return time.Unix(nowUnix.Load(), 0) }, HTTPClient: uqServer.Client(),
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if observation.SourceRefresh == nil {
					return
				}
				refreshMu.Lock()
				defer refreshMu.Unlock()
				refreshes = append(refreshes, *observation.SourceRefresh)
			}),
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
	last := func() observability.SourceRefreshFacts {
		refreshMu.Lock()
		defer refreshMu.Unlock()
		return refreshes[len(refreshes)-1]
	}
	// The round after Start reads, as the round that published at Start did
	// not end unchanged; the round after that finds nothing moved and reuses.
	for round := 0; round < 2; round++ {
		result, err := control.Refresh(ctx)
		if err != nil || result.Status != phaseTwoControlHealthy {
			t.Fatalf("Refresh() round %d = (%+v, %v), want healthy", round, result, err)
		}
	}
	facts := last()
	if facts.Status != observability.SourceRefreshUnchanged || facts.ReadMode != observability.SourceReadSkipped ||
		facts.ReadReason != observability.SourceReadUnchanged || facts.StrategiesRead != 0 ||
		!facts.ChangeSignalPresent || facts.ChangeSignalAgeSeconds != 45 {
		t.Fatalf("steady refresh line = %+v, want a skipped round reporting the 45 s old signal", facts)
	}

	// The publisher ran again: the signal moved and the next round reads.
	nowUnix.Add(10)
	if err := redisClient.Set(ctx, "alarm-config.last_updated", strconv.FormatInt(nowUnix.Load(), 10), 0).Err(); err != nil {
		t.Fatal(err)
	}
	if result, err := control.Refresh(ctx); err != nil || result.Status != phaseTwoControlHealthy {
		t.Fatalf("Refresh() after the signal moved = (%+v, %v), want healthy", result, err)
	}
	facts = last()
	if facts.Status != observability.SourceRefreshUnchanged || facts.ReadMode != observability.SourceReadFull ||
		facts.ReadReason != observability.SourceReadChanged || facts.StrategiesRead != 1 ||
		!facts.ChangeSignalPresent || facts.ChangeSignalAgeSeconds != 0 {
		t.Fatalf("refresh line after the signal moved = %+v, want a full read for a changed signal", facts)
	}
}
