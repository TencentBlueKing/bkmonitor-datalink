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

// A replica whose snapshot publish keeps failing vanishes from the aggregated
// view, which can then report only that the replica is missing. Missing and
// failing to publish call for different actions, and the process cannot tell
// them apart from the outside, because the evidence that would distinguish them
// is exactly the write that failed.
//
// This has to be asserted against the production assembly rather than against
// the observer in isolation. The defect it guards is an unset field: the
// publisher accepted a nil callback and dropped every error, so the transition
// logic could be perfectly correct and perfectly unreachable while every unit
// test passed.
func TestProductionBundleReportsFleetSnapshotPublishOutcome(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)

	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-fleet-publish-wiring"

	var mu sync.Mutex
	var observations []observability.Observation
	recorder := metric.NewRecorder(metric.BuildInfo{})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, recorder,
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: time.Now, HTTPClient: http.DefaultClient,
			AdditionalObserver: observability.ObserverFunc(func(_ context.Context, observation observability.Observation) {
				mu.Lock()
				defer mu.Unlock()
				observations = append(observations, observation)
			}),
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
	if bundle.dependencies.PublishFleet == nil {
		t.Fatal("production bundle exposes no fleet publish")
	}

	// A cancelled context fails the Redis write the same way an unreachable
	// control plane would, without disturbing the shared server.
	failed, cancel := context.WithCancel(ctx)
	cancel()
	bundle.dependencies.PublishFleet(failed)
	assertFleetPublishResult(t, &mu, &observations, observability.ResultFailed)
	// The metric is the load-bearing channel, not the log: log collection has
	// to be configured per environment before it answers anything, and the
	// generic observation catalog is a whitelist, so a stage that is emitted
	// but not registered produces a log line and no series at all.
	if got := fleetPublishSeries(t, recorder, string(observability.ResultFailed)); got != 1 {
		t.Fatalf("failed publish metric = %v, want 1", got)
	}

	bundle.dependencies.PublishFleet(ctx)
	assertFleetPublishResult(t, &mu, &observations, observability.ResultResumed)
	if got := fleetPublishSeries(t, recorder, string(observability.ResultResumed)); got != 1 {
		t.Fatalf("resumed publish metric = %v, want 1", got)
	}

	// A steady state reports nothing: an outage lasting an hour is one fact,
	// not one per reconcile tick.
	before := fleetPublishObservations(&mu, &observations)
	bundle.dependencies.PublishFleet(ctx)
	if after := fleetPublishObservations(&mu, &observations); len(after) != len(before) {
		t.Fatalf("a second healthy publish reported %d observations, want none", len(after)-len(before))
	}
}

func assertFleetPublishResult(t *testing.T, mu *sync.Mutex, observations *[]observability.Observation, want observability.Result) {
	t.Helper()
	reported := fleetPublishObservations(mu, observations)
	if len(reported) == 0 {
		t.Fatalf("no fleet publish observation was reported, want one with result %s", want)
	}
	if last := reported[len(reported)-1]; last.Result != want {
		t.Fatalf("last fleet publish result = %s, want %s", last.Result, want)
	}
}

// fleetPublishSeries reads the published counter rather than the in-process
// collector, so it fails the same way a scrape would if the stage were missing
// from the metric catalog.
func fleetPublishSeries(t *testing.T, recorder *metric.Recorder, result string) float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_observation_total" {
			continue
		}
		for _, series := range family.GetMetric() {
			stage, seen := "", ""
			for _, label := range series.GetLabel() {
				switch label.GetName() {
				case "stage":
					stage = label.GetValue()
				case "result":
					seen = label.GetValue()
				}
			}
			if stage == observability.StageFleetSnapshotPublish && seen == result {
				return series.GetCounter().GetValue()
			}
		}
	}
	return 0
}

func fleetPublishObservations(mu *sync.Mutex, observations *[]observability.Observation) []observability.Observation {
	mu.Lock()
	defer mu.Unlock()
	var reported []observability.Observation
	for _, observation := range *observations {
		if observation.Stage == observability.StageFleetSnapshotPublish {
			reported = append(reported, observation)
		}
	}
	return reported
}
