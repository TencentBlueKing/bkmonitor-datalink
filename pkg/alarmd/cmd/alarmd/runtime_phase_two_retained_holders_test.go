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
	"maps"
	"net/http"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/worker"
)

// The snapshot names the Slots holding the retained pool by the strategies
// the tracker has seen their objects evaluate, the first few of many with
// all of them counted, and the Slot holding the longest whether or not it
// is among the largest; the pool's figure travels beside what the Slots add
// up to, so a reservation no Slot names is read off the snapshot.
func TestTheSnapshotNamesTheSlotsHoldingTheRetainedPool(t *testing.T) {
	clock := &dueIndexClock{at: time.Unix(20_000, 0)}
	tracker := fleet.NewTracker(nil, "replica-1", clock.now)
	observe := func(queryGroup string, strategies int) {
		ctx := observability.ContextWithTraceFields(context.Background(), observability.TraceFields{QueryGroupKey: queryGroup})
		for index := 0; index < strategies; index++ {
			tracker.Observe(ctx, observability.Observation{ProgressCompletionKind: "FULL_COMPLETED",
				Trace: observability.TraceFields{StrategyID: fmt.Sprintf("9%02d", index), BusinessID: "2", EvaluationTime: 600}})
		}
	}
	observe("qg-large", fleet.RetainedHolderStrategiesListed+2)
	observe("qg-old", 1)
	since := time.Unix(1_800_000_000, 0).UTC()
	large := worker.RetainedHolder{Slot: execution.SlotIdentity{QueryGroup: "qg-large", EvaluationTime: 1_800_000_060},
		Operation: execution.OperationNormal, Since: since.Add(time.Minute), Bytes: 700,
		ByKind: map[string]uint64{worker.RetainedKindInput: 600, worker.RetainedKindFrames: 100}}
	old := worker.RetainedHolder{Slot: execution.SlotIdentity{QueryGroup: "qg-old", EvaluationTime: 1_800_000_000},
		Operation: execution.OperationReplay, Since: since, Bytes: 40, ByKind: map[string]uint64{worker.RetainedKindGap: 40}}
	publisher := fleetPublisher{tracker: tracker, replica: "replica-1", now: clock.now,
		owned: func() []execution.QueryGroupIdentity { return []execution.QueryGroupIdentity{"qg-large", "qg-old"} },
		retainedHolders: func() *fleet.RetainedHolderFacts {
			return retainedHolderFactsOf(worker.RetainedHolderReading{Holders: []worker.RetainedHolder{large}, Oldest: &old,
				Total: 2, Attributed: 740, Held: 1_740}, tracker.StrategiesFor)
		}}
	facts := publisher.snapshot(context.Background()).RetainedHolders
	if facts == nil || len(facts.Holders) != 1 || facts.Oldest == nil {
		t.Fatalf("retained holders = %+v, want the largest listed and the oldest named", facts)
	}
	if facts.Total != 2 || facts.AttributedBytes != 740 || facts.HeldBytes != 1_740 {
		t.Fatalf("total %d, attributed %d, held %d: want 2 Slots naming 740 of a 1740-byte pool",
			facts.Total, facts.AttributedBytes, facts.HeldBytes)
	}
	holder := facts.Holders[0]
	if holder.QueryGroup != "qg-large" || holder.EvaluationTime != 1_800_000_060 || holder.Operation != "normal" ||
		!holder.Since.Equal(since.Add(time.Minute)) || holder.Bytes != 700 || !maps.Equal(holder.ByKind, large.ByKind) {
		t.Fatalf("largest = %+v, want qg-large's normal Slot as read", holder)
	}
	if holder.StrategiesTotal != fleet.RetainedHolderStrategiesListed+2 || len(holder.Strategies) != fleet.RetainedHolderStrategiesListed ||
		holder.Strategies[0].StrategyID != "900" {
		t.Fatalf("largest strategies %+v of %d, want the first %d of all %d counted",
			holder.Strategies, holder.StrategiesTotal, fleet.RetainedHolderStrategiesListed, fleet.RetainedHolderStrategiesListed+2)
	}
	oldest := facts.Oldest
	if oldest.QueryGroup != "qg-old" || oldest.Operation != "replay" || !oldest.Since.Equal(since) ||
		oldest.StrategiesTotal != 1 || len(oldest.Strategies) != 1 || oldest.Strategies[0].StrategyID != "900" {
		t.Fatalf("oldest = %+v, want qg-old's replay named by its one strategy", oldest)
	}
}

// A running replica publishes what holds its retained pool, read back where
// the page and the CLI read it, through the store: an idle replica's pool
// is held by nothing, an empty list rather than an absent fact.
func TestAReplicaPublishesWhatHoldsItsRetainedPool(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-retained-holders-wiring"
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}),
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
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
	store, err := fleet.NewRedisStore(client, productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "fleet"), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	bundle.dependencies.PublishFleet(ctx)
	snapshots, err := store.Load(ctx, []string{cfg.PhaseTwo.Worker.ID})
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("load the published snapshot: %v (%d)", err, len(snapshots))
	}
	got := snapshots[0].RetainedHolders
	if got == nil || got.Holders == nil || len(got.Holders) != 0 || got.Oldest != nil || got.Total != 0 || got.HeldBytes != 0 {
		t.Fatalf("an idle replica published %+v, want an empty pool held by nothing", got)
	}
}
