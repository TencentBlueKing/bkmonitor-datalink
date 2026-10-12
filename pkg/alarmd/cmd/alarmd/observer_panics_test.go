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
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A panic in an observer handed the observation directly, outside any
// fan-out, is still recovered, and counted under the entry rather than
// swallowed.
func TestAPanicOutsideTheFanOutIsCountedUnderTheEntry(t *testing.T) {
	before := observability.ObserverPanicCounts()[observability.ObserverEntry]
	observeRuntime(context.Background(), observability.ObserverFunc(func(context.Context, observability.Observation) {
		panic("observer defect")
	}), observability.Observation{})
	if after := observability.ObserverPanicCounts()[observability.ObserverEntry]; after != before+1 {
		t.Fatalf("entry panics %d -> %d, want one more", before, after)
	}
}

// The production assembly names its observers: one that panics on every
// observation is counted under its own name, and the recorder before it in
// the fan-out still records what happened.
func TestTheProductionFanOutCountsAPanickingObserverByName(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-observer-panics"
	recorder := metric.NewRecorder(metric.BuildInfo{Version: "0.0.1", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: http.DefaultClient,
			AdditionalObserver: observability.ObserverFunc(func(context.Context, observability.Observation) {
				panic("observer defect")
			}),
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := bundle.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	before := observability.ObserverPanicCounts()
	failed, cancel := context.WithCancel(ctx)
	cancel()
	bundle.dependencies.PublishFleet(failed)
	after := observability.ObserverPanicCounts()
	if after[observability.ObserverAdditional] <= before[observability.ObserverAdditional] {
		t.Fatalf("the panicking observer was not counted under its name: %v -> %v", before, after)
	}
	if after[observability.ObserverEntry] != before[observability.ObserverEntry] || after[observability.ObserverUnnamed] != before[observability.ObserverUnnamed] {
		t.Fatalf("the panic escaped its member: %v -> %v", before, after)
	}
	if got := fleetPublishSeries(t, recorder, string(observability.ResultFailed)); got != 1 {
		t.Fatalf("failed publish metric = %v, want the recorder to have recorded it", got)
	}
}
