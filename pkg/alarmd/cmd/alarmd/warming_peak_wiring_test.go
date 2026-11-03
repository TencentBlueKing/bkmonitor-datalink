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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The production assembly carries the hourly warming line: warming writes
// observed through the runtime's observer in one hour come out, at the first
// apply of the next, as a state_warming_peak line to the same observers the
// rest of the runtime's lines go to.
func TestTheProductionObserverWritesTheHourlyWarmingLine(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-warming-peak"
	var mu sync.Mutex
	offset := time.Duration(0)
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return time.Now().Add(offset)
	}
	var lines []observability.Observation
	recorder := metric.NewRecorder(metric.BuildInfo{Version: "0.0.1", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: http.DefaultClient,
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				if observation.Stage == observability.StageWarmingPeak {
					mu.Lock()
					lines = append(lines, observation)
					mu.Unlock()
				}
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
	apply := func(warming int) {
		facts := &observability.StateWriteReuseFacts{}
		for i := 0; i < warming; i++ {
			facts.Record(observability.StateWriteReuseChanged, observability.StateWriteReuseStoredWarming)
		}
		facts.Record(observability.StateWriteReuseDecisionStable, observability.StateWriteReuseStoredReady)
		traced := observability.ContextWithTraceFields(ctx, observability.TraceFields{QueryGroupKey: "qg-wiring", StrategyID: "7"})
		bundle.dependencies.Observer.Observe(traced, observability.Observation{
			Component: observability.ComponentState, Stage: observability.StageMutationCompared,
			Result: observability.ResultSuccess, StateWriteReuse: facts,
		})
	}
	apply(42)
	mu.Lock()
	offset = time.Hour
	mu.Unlock()
	apply(0)

	mu.Lock()
	defer mu.Unlock()
	if len(lines) != 1 || lines[0].WarmingPeak == nil {
		t.Fatalf("warming lines = %+v, want one carrying the hour", lines)
	}
	peak := lines[0].WarmingPeak
	if peak.Writes != 42 || len(peak.PeakGroups) != 1 || peak.PeakGroups[0].QueryGroup != "qg-wiring" {
		t.Fatalf("peak = %+v, want 42 writes from qg-wiring", peak)
	}
}
