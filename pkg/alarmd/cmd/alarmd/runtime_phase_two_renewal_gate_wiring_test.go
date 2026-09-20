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

// The production bundle binds the renewal gate's reset counter to its store.
//
// It has to be asserted against the production assembly, because the defect it
// guards is a missing call and nothing else fails without it. A worker that
// never binds it keeps renewing keys, keeps passing Slots and keeps reporting a
// healthy everything; the only other symptom is the EVAL rate returning to
// where it was before the gate existed, which is the very thing the counter was
// added to make visible. Deleting the one line in the bundle left every unit
// test in this repository green.
//
// The collector reports nothing at all when unbound, so the presence of the
// series is the assertion. A collector that answered zero when unbound could
// not be told apart from a healthy worker here or on a real scrape.
func TestProductionBundleReportsRenewalGateResets(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-renewal-gate-wiring"

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
	// The bundle holds Redis clients and a maintenance group even though this
	// test never starts it. Leaving them alive perturbs the timing-sensitive
	// assertions elsewhere in this package.
	t.Cleanup(func() {
		if err := bundle.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})

	value, reported := renewalGateResetSeries(t, recorder)
	if !reported {
		t.Fatal("the production bundle reports no renewal gate reset series; the counter has no reader, " +
			"so a worker that has forgotten every key life it remembered looks exactly like one that " +
			"has not")
	}
	if value != 0 {
		t.Fatalf("resets = %v on a bundle that has loaded nothing, want a computed zero", value)
	}
}

// A recorder nobody bound reports no series at all, which is what makes the
// assertion above mean something.
func TestAnUnboundRenewalGateReportsNoSeries(t *testing.T) {
	recorder := metric.NewRecorder(metric.BuildInfo{Version: "0.2.9999", Commit: "0123456789abcdef", SchemaVersion: "v3"})
	if _, reported := renewalGateResetSeries(t, recorder); reported {
		t.Fatal("an unbound collector reported a series; a zero from a collector nobody wired up reads " +
			"exactly like the healthy zero, and the wiring test above would pass without the wiring")
	}
}

func renewalGateResetSeries(t *testing.T, recorder *metric.Recorder) (float64, bool) {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_state_renewal_gate_resets_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			return series.GetCounter().GetValue(), true
		}
	}
	return 0, false
}
