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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Through the production bundle: every series whose host CMDB places by its
// bk_host_id is counted as placed under its Plan's grouping - here grouped
// by bk_target_ip, the grouping Python's rewrite of that dimension reaches -
// and the one at another address than the series reports is counted as
// differing too. The series reporting its host's own address is placed and
// not differing: the count a zero of the differing ones is read against.
func TestADifferingAddressIsCountedThroughTheProductionBundle(t *testing.T) {
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
	query := item["query_configs"].([]any)[0].(map[string]any)
	query["agg_interval"] = 60
	query["agg_dimension"] = []any{"bk_target_ip", "bk_target_cloud_id", "bk_host_id"}
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
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		var payload struct {
			EndTime string `json:"end_time"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil || end <= 0 {
			end = clock.Load() / 1000
		}
		point := strconv.FormatInt((end-1)*1000, 10)
		// Host 1 reports an address CMDB does not give it; host 2 its own.
		series := []string{}
		for _, host := range [][2]string{{"1", "198.51.100.7"}, {"2", followsHostB}} {
			series = append(series, `{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],`+
				`"group_keys":["bk_host_id","bk_target_cloud_id","bk_target_ip"],"group_values":["`+host[0]+`","0","`+host[1]+`"],`+
				`"values":[[`+point+`,95]]}`)
		}
		_, _ = writer.Write([]byte(`{"series":[` + strings.Join(series, ",") + `],"status":null,"trace_id":"address-differs",` +
			`"is_partial":false,"result_table_id":["system.cpu"]}`))
	}))
	t.Cleanup(uqServer.Close)

	cfg.Redis.Address = address
	cfg.Redis.StatePrefix = "alarmd-address-differs"
	withCompatibilityOutput(&cfg, address)
	cfg.PhaseTwo.Control.RefreshInterval = config.Duration(time.Millisecond)
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	cfg.PhaseTwo.Worker.RegistrationTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.ControlLeaderTTL = config.Duration(6 * time.Hour)
	cfg.PhaseTwo.Ownership.LeaseTTL = config.Duration(6 * time.Hour)
	recorder := metric.NewRecorder(metric.BuildInfo{})
	bundle, err := openProductionPhaseTwoBundleWithDependencies(ctx, cfg, recorder, observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
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
	runner := settledRunner(bundle, bundle.queryGroups[0])
	clock.Store((base+30)*1000 + 60*1000)
	if _, _, err := runner.RunOne(ctx); err != nil {
		t.Fatalf("RunOne: %v", err)
	}

	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	counted := func(suffix string) map[string]float64 {
		counts := map[string]float64{}
		for _, family := range families {
			if !strings.HasSuffix(family.GetName(), suffix) {
				continue
			}
			for _, series := range family.GetMetric() {
				for _, label := range series.GetLabel() {
					counts[label.GetValue()] += series.GetCounter().GetValue()
				}
			}
		}
		return counts
	}
	if counts := counted("admission_cmdb_address_differs_total"); counts["true"] != 1 || counts["false"] != 0 {
		t.Fatalf("differing: counted %v, want the one series whose address is not its host's, under grouped_by_target_ip=true", counts)
	}
	if counts := counted("admission_cmdb_placed_by_host_id_total"); counts["true"] != 2 || counts["false"] != 0 {
		t.Fatalf("placed: counted %v, want both series CMDB placed by bk_host_id, under grouped_by_target_ip=true", counts)
	}
}
