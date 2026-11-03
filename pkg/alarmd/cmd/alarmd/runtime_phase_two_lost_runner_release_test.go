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
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// Through the production bundle, in the order the lost-lease paths take: the
// Runner is detached first and released after, and in between the next
// reconcile - its lease expired - opens the Query Group again here. The lost
// Runner's release must leave its successor bound: the successor's next
// Slot is queried and completes, rather than every Slot being refused as a
// stale fence while the successor's lease keeps renewing.
func TestALostRunnersLateReleaseLeavesItsSuccessorRunning(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	cfg := validGoAccessRuntimeConfig()
	followsSeedHosts(t, ctx, client, cfg.PlatformKeyPrefix())
	raw, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["update_time"], document["strategy_revision"], document["scenario"] = 1725000007, 7, "os"
	item := document["items"].([]any)[0].(map[string]any)
	delete(item, "target")
	item["query_configs"].([]any)[0].(map[string]any)["agg_interval"] = 60
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]any{
		"alarm-config.strategy_ids":                  `[` + followsStrategyID + `]`,
		"alarm-config.strategy_" + followsStrategyID: encoded,
		"alarm-config.last_updated":                  "1725000007",
	} {
		if err := client.Set(ctx, key, value, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}

	const interval = int64(60)
	base := time.Now().Unix()
	base += interval - base%interval
	// Read by the bundle's own goroutines after Start: atomic.
	clock := &atomic.Int64{}
	clock.Store(base * 1000)
	now := func() time.Time { return time.UnixMilli(clock.Load()) }
	var queried atomic.Int64
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		queried.Add(1)
		var payload struct {
			EndTime string `json:"end_time"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil || end <= 0 {
			end = clock.Load() / 1000
		}
		point := strconv.FormatInt((end-1)*1000, 10)
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
			`"group_keys":["bk_target_cloud_id","bk_target_ip"],"group_values":["0","` + followsHostA + `"],"values":[[` + point + `,5]]}],` +
			`"status":null,"trace_id":"lost-runner","is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(uqServer.Close)

	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-lost-runner"
	withCompatibilityOutput(&cfg, address)
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
	// Short enough for the test's clock to let it lapse.
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(2 * time.Minute)
	cfg.PhaseTwo.Ownership.LeaseRenewInterval = config.Duration(time.Minute)
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, metric.NewRecorder(metric.BuildInfo{}),
		observability.Discard(observability.ComponentRuntime), newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			PrepareEvents: preparedEvents(func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &followsSink{mode: followsOK}, nil
			}),
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = bundle.Shutdown(context.Background()) })
	if len(bundle.queryGroups) != 1 {
		t.Fatalf("Query Groups = %v, want one", bundle.queryGroups)
	}
	queryGroup := bundle.queryGroups[0]
	clock.Store((base+30)*1000 + 60*1000)
	if _, _, err := settledRunner(bundle, queryGroup).RunOne(ctx); err != nil || queried.Load() == 0 {
		t.Fatalf("first round: %v, %d queries", err, queried.Load())
	}

	// The lost path: detached first.
	bundle.mu.RLock()
	lost := bundle.runners[queryGroup]
	bundle.mu.RUnlock()
	if !bundle.detachQueryGroup(queryGroup, lost) {
		t.Fatal("the Runner was not detached")
	}
	// Its lease lapses - the deadline is kept on Redis's own clock, so it is
	// moved there - and the next reconcile opens the Query Group again here.
	clock.Store((base+30)*1000 + 4*60*1000)
	ownershipKey := productionPhaseTwoPrefix(cfg.Redis.StatePrefix, "ownership") + ":{" + ownership.ControlHashTag(queryGroup) + "}:ownership"
	if err := client.HSet(ctx, ownershipKey, "deadline_ms", 1).Err(); err != nil {
		t.Fatal(err)
	}
	if err := bundle.applyAssignment(ctx, []execution.QueryGroupIdentity{queryGroup}); err != nil {
		t.Fatal(err)
	}
	if settledRunner(bundle, queryGroup) == nil {
		t.Fatal("the reconcile did not open a successor")
	}
	// The lost Runner's release, late.
	if err := lost.runner.Release(ctx); err != nil {
		t.Fatalf("the lost Runner's release: %v", err)
	}

	before := queried.Load()
	clock.Store((base+30)*1000 + 5*60*1000)
	if _, _, err := settledRunner(bundle, queryGroup).RunOne(ctx); err != nil {
		t.Fatalf("the successor's round: %v", err)
	}
	if queried.Load() == before {
		t.Fatal("the successor's Slot was not queried: the lost Runner's release unbound it")
	}
}
