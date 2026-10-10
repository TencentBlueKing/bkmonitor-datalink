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

// The production bundle binds the retained-byte pool's reservation to its
// coordinator. The collector reports nothing when unbound, so the series
// being there is the assertion that the one line in the bundle exists; a
// bundle that has run nothing holds nothing, so it reads a computed zero.
func TestProductionBundleReportsTheRetainedReservation(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-retained-reservation-wiring"

	recorder := metric.NewRecorder(metric.BuildInfo{
		Version: "0.2.9999", Commit: "0123456789abcdef", SchemaVersion: "v3",
	})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: http.DefaultClient,
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
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

	value, reported := retainedReservationSeries(t, recorder)
	if !reported {
		t.Fatal("the production bundle reports no capacity_reserved series: the pool's usage has no reader, " +
			"and the only reading of the 1 GiB wall left is the sum of peaks, an upper bound")
	}
	if value != 0 {
		t.Fatalf("reserved = %v on a bundle that has run nothing, want a computed zero", value)
	}
}

// A recorder nobody bound reports no series, which is what makes the
// assertion above mean something.
func TestAnUnboundRetainedReservationReportsNoSeries(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{Version: "0.2.9999", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	if _, reported := retainedReservationSeries(t, recorder); reported {
		t.Fatal("an unbound collector reported a series")
	}
}

func retainedReservationSeries(t *testing.T, recorder *metric.Recorder) (float64, bool) {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_capacity_reserved" {
			continue
		}
		for _, series := range family.GetMetric() {
			return series.GetGauge().GetValue(), true
		}
	}
	return 0, false
}
