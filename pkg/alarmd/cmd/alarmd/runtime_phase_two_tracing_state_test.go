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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Whether a process exports its spans is said in one read, both ways, from
// the assembly that builds the export: the gauge is one-hot on the state, and
// the overview names the state, the collector's host and the sampling. A
// process given no collector says not configured instead of counters at zero
// that read the same as an export with nothing to sample. The token reaches
// neither surface, nor does the endpoint's path; the overview is made of the
// published snapshot checked here (its route is pinned in fleet).
func TestTheSpanExportStateIsSaidInTheGaugeAndTheFleetSnapshot(t *testing.T) {
	const token = "placeholder-span-export-token"
	for name, test := range map[string]struct {
		endpoint string
		state    string
		host     string
	}{
		"no collector given": {state: fleet.TracingOffNotConfigured},
		"a collector given":  {endpoint: "http://collector.example:4318/", state: fleet.TracingOn, host: "collector.example:4318"},
	} {
		t.Run(name, func(t *testing.T) {
			address, client := startPhaseTwoRedis(t)
			ctx := context.Background()
			installTwoPhaseTwoStrategies(t, ctx, client)
			cfg := validGoAccessRuntimeConfig()
			cfg.Redis.Address = address
			withCompatibilityOutput(&cfg, address)
			cfg.Redis.StatePrefix = "alarmd-tracing-state"
			cfg.PhaseTwo.Tracing.OTLPEndpoint = test.endpoint
			if test.endpoint != "" {
				cfg.PhaseTwo.Tracing.Token = token
			}
			recorder := metric.NewRecorder(metric.BuildInfo{})
			bundle, err := openProductionPhaseTwoBundleWithDependencies(
				ctx, cfg, recorder, observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
				func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
					return controlplane.NewLegacyRedisStrategySource(client, prefix)
				},
				phaseTwoProductionExternalDependencies{
					Now: time.Now, HTTPClient: http.DefaultClient,
					PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
						return &recordingPhaseTwoEventSink{}, nil
					}),
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })

			for _, state := range fleet.TracingStates {
				want := 0.0
				if state == test.state {
					want = 1
				}
				if got := counterValue(t, recorder, "alarmd_trace_export", map[string]string{"state": state}); got != want {
					t.Errorf("alarmd_trace_export{state=%q} = %v, want %v", state, got, want)
				}
			}

			store, err := fleet.NewRedisStore(client, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "fleet"), 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			bundle.dependencies.PublishFleet(ctx)
			snapshots, err := store.Load(ctx, []string{cfg.PhaseTwo.Worker.ID})
			if err != nil || len(snapshots) != 1 {
				t.Fatalf("load the published snapshot: %v (%d)", err, len(snapshots))
			}
			facts := snapshots[0].Tracing
			if facts == nil || facts.State != test.state || facts.EndpointHost != test.host {
				t.Fatalf("published tracing = %+v, want %s at %q", facts, test.state, test.host)
			}
			if test.state == fleet.TracingOn && (facts.SampleRatio == nil || *facts.SampleRatio != cfg.TraceSampleRatio() ||
				facts.MaxTracesPerSecond == nil || *facts.MaxTracesPerSecond != cfg.TraceMaxTracesPerSecond()) {
				t.Fatalf("published sampling = %+v, want the configured %v and %d", facts, cfg.TraceSampleRatio(), cfg.TraceMaxTracesPerSecond())
			}
			if test.state == fleet.TracingOffNotConfigured && (facts.Reason != fleet.TracingReasonNotConfigured || facts.SampleRatio != nil) {
				t.Fatalf("published off = %+v, want the reason and no sampling", facts)
			}
			// The snapshot is what the overview and every fleet read are made of.
			body, err := json.Marshal(snapshots[0])
			if err != nil {
				t.Fatal(err)
			}
			metrics := httptest.NewRecorder()
			promhttp.HandlerFor(recorder.Gatherer(), promhttp.HandlerOpts{}).ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
			for surface, text := range map[string]string{"snapshot": string(body), "metrics": metrics.Body.String()} {
				for _, leaked := range []string{token, "/v1/traces", "http://collector.example"} {
					if strings.Contains(text, leaked) {
						t.Fatalf("the %s carries %q", surface, leaked)
					}
				}
			}
		})
	}
}

// Each snapshot carries its own copy of the publisher's facts, pointers
// included, the same rule the aggregate keeps for each row.
func TestTheSnapshotCopiesThePublishersSpanExportFacts(t *testing.T) {
	var cfg config.Config
	cfg.PhaseTwo.Tracing.OTLPEndpoint = "http://collector.example:4318"
	facts := fleetTracingFacts(cfg, true)
	publisher := fleetPublisher{
		tracker: fleet.NewTracker(nil, "replica-1", time.Now), replica: "replica-1", now: time.Now,
		owned:   func() []execution.QueryGroupIdentity { return nil },
		tracing: facts,
	}
	snapshot := publisher.snapshot(context.Background())
	if snapshot.Tracing == nil || snapshot.Tracing == facts || snapshot.Tracing.SampleRatio == facts.SampleRatio ||
		snapshot.Tracing.MaxTracesPerSecond == facts.MaxTracesPerSecond {
		t.Fatalf("snapshot tracing %+v shares the publisher's facts %+v", snapshot.Tracing, facts)
	}
	if *snapshot.Tracing.SampleRatio != *facts.SampleRatio || snapshot.Tracing.EndpointHost != "collector.example:4318" {
		t.Fatalf("snapshot tracing %+v, want the publisher's values", snapshot.Tracing)
	}
	publisher.tracing = nil
	if got := publisher.snapshot(context.Background()).Tracing; got != nil {
		t.Fatalf("a publisher without facts published %+v", got)
	}
}
