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
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
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

// A window is only worth anything if opening it changes what the replica
// actually records. Asserting that the row reached Redis would pass with the
// applier disconnected, which is the shape of the defect this package keeps
// producing: the write lands, nothing observes it, and the operator waits for
// output that cannot come.
func TestOpeningAWindowChangesWhatTheReplicaObserves(t *testing.T) {
	flow, err := observability.NewTargetFlow(observability.New(observability.ComponentRuntime, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	store, err := fleet.NewWindowStore(windowRedis(t), "alarmd-window-wiring")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	watched := strings.Repeat("a", 64)
	var dropped, applied int
	applier := observationWindowApplier{
		store: store, flow: flow, now: func() time.Time { return at },
		observe: func(nowApplied, _, nowDropped int, _ error) { applied, dropped = nowApplied, nowDropped },
	}

	if flow.Selected(watched) {
		t.Fatal("the object is observed before any window was opened")
	}
	if _, err := store.Open(context.Background(), []string{watched}, "operator", time.Minute, at); err != nil {
		t.Fatal(err)
	}
	applier.applyOnce(context.Background())
	if !flow.Selected(watched) {
		t.Fatal("the object is still not observed after a window was opened")
	}
	if applied != 1 || dropped != 0 {
		t.Fatalf("applied = %d dropped = %d, want the single window applied", applied, dropped)
	}

	// And it stops on its own, which is what separates a window from the static
	// selection it replaces.
	at = at.Add(2 * time.Minute)
	applier.applyOnce(context.Background())
	if flow.Selected(watched) {
		t.Fatal("the object is still observed after its window expired")
	}
}

// Opening a window checks the budget, but two windows opened at the same moment
// each see the count before the other's write, so more can be open than the
// diagnostics can carry. Selecting past the budget would be rejected outright
// and leave the replica observing nothing at all, so the excess is trimmed --
// and the trim is reported, because someone is waiting on the window that was
// dropped.
func TestWindowsBeyondTheBudgetAreTrimmedAndReported(t *testing.T) {
	flow, err := observability.NewTargetFlow(observability.New(observability.ComponentRuntime, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	client := windowRedis(t)
	const prefix = "alarmd-window-budget"
	store, err := fleet.NewWindowStore(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now()
	// Written straight to the control plane, which is what the race produces:
	// the store's own cap cannot be exceeded through its API.
	deadline := float64(at.Add(time.Minute).UnixMilli())
	members := make([]*redis.Z, 0, observability.TargetFlowMaxGroups+1)
	for index := 0; index <= observability.TargetFlowMaxGroups; index++ {
		members = append(members, &redis.Z{Score: deadline, Member: fmt.Sprintf("%064x", index)})
	}
	if err := client.ZAdd(context.Background(), prefix+":observation-window", members...).Err(); err != nil {
		t.Fatal(err)
	}

	var applied, dropped int
	var reported error
	applier := observationWindowApplier{
		store: store, flow: flow, now: func() time.Time { return at },
		observe: func(nowApplied, _, nowDropped int, err error) {
			applied, dropped, reported = nowApplied, nowDropped, err
		},
	}
	applier.applyOnce(context.Background())
	if applied != observability.TargetFlowMaxGroups {
		t.Fatalf("applied = %d, want the budget filled rather than the selection refused", applied)
	}
	if dropped != 1 {
		t.Fatalf("dropped = %d, want the one that did not fit counted", dropped)
	}
	if reported != nil {
		t.Fatalf("observed error = %v, want the shortfall reported as a count rather than a failure", reported)
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

// windowRedis gives the window store a real Redis, so the sorted-set and hash
// semantics the store depends on are the ones it will meet in production.
func windowRedis(t *testing.T) *redis.Client {
	t.Helper()
	_, client := startPhaseTwoRedis(t)
	return client
}
