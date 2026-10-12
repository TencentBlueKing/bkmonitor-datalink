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
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// A replica publishes the Query Groups it declines - an execution of each
// hung here and has not returned - in its own fleet snapshot, from the same
// set its registration names, with the execution slots its dispatcher runs:
// each decline holds one of them until its execution returns. Read back
// where the page and the CLI read it, through the store. None declined, the
// fact is absent; a decline lifted leaves it.
func TestAReplicaPublishesItsDeclinesWithTheExecutionSlotsItHas(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-declines-wiring"
	cfg.PhaseTwo.Scheduler.ActiveExecutionLimit, cfg.PhaseTwo.Scheduler.ReadyQueueCapacity = 3, 64
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
	published := func() *fleet.DeclineFacts {
		t.Helper()
		bundle.dependencies.PublishFleet(ctx)
		snapshots, err := store.Load(ctx, []string{cfg.PhaseTwo.Worker.ID})
		if err != nil || len(snapshots) != 1 {
			t.Fatalf("load the published snapshot: %v (%d)", err, len(snapshots))
		}
		return snapshots[0].Declines
	}
	if got := published(); got != nil {
		t.Fatalf("a replica declining nothing published %+v", got)
	}
	since := time.Unix(1_800_000_000, 0).UTC()
	bundle.mu.Lock()
	bundle.declined = map[execution.QueryGroupIdentity]*declinedQueryGroup{
		"qg-b": {stage: "evaluate", since: since.Add(time.Minute)},
		"qg-a": {stage: "query", since: since},
	}
	bundle.mu.Unlock()
	want := fleet.DeclineFacts{Fanout: 3, Total: 2, QueryGroups: []fleet.DeclinedQueryGroup{
		{QueryGroup: "qg-a", Stage: "query", Since: since}, {QueryGroup: "qg-b", Stage: "evaluate", Since: since.Add(time.Minute)}}}
	if got := published(); got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("published %+v, want %+v", got, want)
	}
	// More than the snapshot names: all counted, the first by name listed.
	many := map[execution.QueryGroupIdentity]*declinedQueryGroup{}
	for index := 0; index < fleet.MaxDeclineSamples+2; index++ {
		many[execution.QueryGroupIdentity(fmt.Sprintf("qg-%02d", index))] = &declinedQueryGroup{stage: "query", since: since}
	}
	bundle.mu.Lock()
	bundle.declined = many
	bundle.mu.Unlock()
	if got := published(); got == nil || got.Total != fleet.MaxDeclineSamples+2 || len(got.QueryGroups) != fleet.MaxDeclineSamples ||
		got.QueryGroups[0].QueryGroup != "qg-00" {
		t.Fatalf("published %+v, want all %d counted and the first %d by name", got, fleet.MaxDeclineSamples+2, fleet.MaxDeclineSamples)
	}
	bundle.mu.Lock()
	bundle.declined = nil
	bundle.mu.Unlock()
	if got := published(); got != nil {
		t.Fatalf("the declines lifted, published %+v", got)
	}
}
