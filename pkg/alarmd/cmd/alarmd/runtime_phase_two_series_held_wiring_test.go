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
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The production bundle binds the series-held reading to the Query Groups it
// runs and the rounds its fleet tracker keeps: a Query Group it runs whose
// Plan bound four series reads four, and reads nothing once that Plan's last
// round is older than the longest interval alarmd evaluates at.
func TestTheProductionBundleReportsTheSeriesItsQueryGroupsBind(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-series-held-wiring"
	recorder := metric.NewRecorder(metric.BuildInfo{})
	var ahead atomic.Int64
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now:        func() time.Time { return time.Now().Add(time.Duration(ahead.Load())) },
			HTTPClient: http.DefaultClient,
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			}),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ahead.Store(0)
		if err := bundle.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	matched := 4
	traced := observability.ContextWithTraceFields(ctx, observability.TraceFields{QueryGroupKey: "qg-wired"})
	bundle.dependencies.Observer.Observe(traced, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageEvaluationCompleted,
		Result:            observability.ResultSuccess,
		Trace:             observability.TraceFields{StrategyID: "1", BusinessID: "2", EvaluationTime: time.Now().Unix()},
		PlanSeriesMatched: &matched,
	})
	held := func() float64 {
		t.Helper()
		families, err := recorder.Gatherer().Gather()
		if err != nil {
			t.Fatal(err)
		}
		for _, family := range families {
			if family.GetName() == "alarmd_state_series_held" && len(family.GetMetric()) == 1 {
				return family.GetMetric()[0].GetGauge().GetValue()
			}
		}
		t.Fatal("no alarmd_state_series_held on the scrape")
		return 0
	}
	if got := held(); got != 0 {
		t.Fatalf("series held with no Query Group run = %v, want 0", got)
	}
	bundle.mu.Lock()
	bundle.runners["qg-wired"] = &phaseTwoQueryGroupLifecycle{runner: newFakePhaseTwoQueryGroup(), done: make(chan struct{})}
	bundle.mu.Unlock()
	t.Cleanup(func() {
		bundle.mu.Lock()
		delete(bundle.runners, "qg-wired")
		bundle.mu.Unlock()
	})
	if got := held(); got != 4 {
		t.Fatalf("series held = %v, want the four its Plan bound", got)
	}
	ahead.Store(int64(phaseTwoMaxSupportedEvaluationInterval + time.Second))
	if got := held(); got != 0 {
		t.Fatalf("series held a day and a second after the Plan's last round = %v, want 0", got)
	}
}
