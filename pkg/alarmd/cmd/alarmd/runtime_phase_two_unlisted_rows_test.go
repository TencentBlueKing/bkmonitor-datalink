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
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// The production bundle serves a healthy object's row from its own tracker:
// an observation the replica emits for a Query Group reaches the tracker at
// the head of the observer chain, and the bundle's own fleet route answers
// with that live row, listed false, carrying the Plan's no-data tracking.
// Wired as production wires it, so a service never handed the tracker fails
// here even when every route test passes.
func TestTheProductionFleetRouteServesAHealthyObjectsLiveRow(t *testing.T) {
	address, _ := startPhaseTwoRedis(t)
	ctx := context.Background()
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd:phase-two:unlisted-rows"
	bundle, err := openProductionPhaseTwoBundleWithDependencies(
		ctx, cfg, metric.NewRecorder(metric.BuildInfo{}), observability.Discard(observability.ComponentRuntime),
		newPhaseTwoApplicationHealth(),
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
		t.Fatalf("open production bundle: %v", err)
	}
	defer func() { _ = bundle.Shutdown(ctx) }()
	traced := observability.ContextWithTraceFields(ctx, observability.TraceFields{QueryGroupKey: "qg-healthy"})
	bundle.dependencies.Observer.Observe(traced, observability.Observation{
		Component: observability.ComponentEvaluation, Stage: observability.StageNoDataDecided,
		Direction: observability.DirectionInternal, Result: observability.ResultSuccess,
		Trace:         observability.TraceFields{StrategyID: "4101", BusinessID: "2", EvaluationTime: 600},
		NoDataAbsence: &observability.NoDataAbsenceFacts{Outcome: "EVALUATED", RosterSource: "HISTORY", Expected: 2, Present: 1, Absent: 1},
	})
	recorder := httptest.NewRecorder()
	bundle.dependencies.FleetAPI.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/objects/qg-healthy", nil))
	var body struct {
		Tracked *struct {
			Listed         *bool `json:"listed"`
			NoDataTracking []struct {
				Expected uint64 `json:"expected"`
				Absent   uint64 `json:"absent"`
			} `json:"no_data_tracking"`
		} `json:"tracked"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v (%s)", err, recorder.Body.String())
	}
	if body.Tracked == nil || body.Tracked.Listed == nil || *body.Tracked.Listed || len(body.Tracked.NoDataTracking) != 1 ||
		body.Tracked.NoDataTracking[0].Expected != 2 || body.Tracked.NoDataTracking[0].Absent != 1 {
		t.Fatalf("object route body = %s, want this replica's live row with the Plan's no-data tracking", recorder.Body.String())
	}
}
