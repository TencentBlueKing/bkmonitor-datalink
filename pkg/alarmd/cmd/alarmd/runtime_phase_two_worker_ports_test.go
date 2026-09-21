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
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The production runtime fills in every port the coordinator has.
//
// A port whose type allows nil is a port a production runtime can be missing
// while every test double has it, and nothing fails: the capability simply does
// not happen. That is not hypothetical here -- a deadline port shipped twice
// implemented by every fake and by nothing in production, and the only symptom
// was a dispatch policy that never took effect.
//
// So this walks the whole struct rather than naming the port that prompted it.
// Naming one would leave the next optional port uncovered, and the next one is
// the one nobody will remember to add a line for. Anything genuinely meant to
// be absent in production belongs in the exception list below, with the reason
// written down at the moment somebody decides it.
func TestTheProductionRuntimeFillsInEveryWorkerPort(t *testing.T) {
	address, redisClient := startPhaseTwoRedis(t)
	ctx := context.Background()
	strategyDocument, err := os.ReadFile("testdata/g1_full_threshold_strategy.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_ids", `[1001]`, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if err := redisClient.Set(ctx, "alarm-config.strategy_1001", strategyDocument, 0).Err(); err != nil {
		t.Fatal(err)
	}
	uqServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`{"series":[],"status":null,"trace_id":"unused","is_partial":false,"result_table_id":[]}`))
	}))
	defer uqServer.Close()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd:phase-two:worker-ports"
	cfg.PhaseTwo.Access.UQEndpoint = uqServer.URL
	now := func() time.Time { return time.Now() }

	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
		func(client redis.Cmdable, prefix string) (controlplane.StrategySource, error) {
			return controlplane.NewLegacyRedisStrategySource(client, prefix)
		},
		phaseTwoProductionExternalDependencies{
			Now: now, HTTPClient: uqServer.Client(),
			OpenEvents: func(enginekafka.DecisionSinkConfig) (productionPhaseTwoEventSink, error) {
				return &recordingPhaseTwoEventSink{}, nil
			},
		},
	)
	if err != nil {
		t.Fatalf("open production bundle: %v", err)
	}
	defer func() { _ = bundle.Shutdown(ctx) }()

	// Ports that are deliberately absent in production, each with the reason.
	// Empty today: every port this runtime has is one it needs.
	deliberatelyAbsent := map[string]string{}

	ports := reflect.ValueOf(bundle.workerPorts)
	portsType := ports.Type()
	if portsType.NumField() == 0 {
		t.Fatal("the coordinator has no ports; this test is checking nothing")
	}
	checked := 0
	for index := 0; index < portsType.NumField(); index++ {
		field := portsType.Field(index)
		if !field.IsExported() {
			continue
		}
		checked++
		if reason, allowed := deliberatelyAbsent[field.Name]; allowed {
			if !ports.Field(index).IsZero() {
				t.Errorf("port %s is listed as deliberately absent (%s) and the runtime wired it anyway; "+
					"remove it from the list", field.Name, reason)
			}
			continue
		}
		if ports.Field(index).IsZero() {
			t.Errorf("the production runtime left port %s unset. A port that may be nil is one production "+
				"can be missing while every test double has it: nothing fails, the capability just does "+
				"not happen. Wire it, or add it to deliberatelyAbsent with the reason.", field.Name)
		}
	}
	if checked == 0 {
		t.Fatal("no exported ports were checked; the scan is not reaching the struct")
	}
}
