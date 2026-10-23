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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	enginekafka "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/kafka"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/scheduler"
)

// The bound past which a wait for the view is listed is the deployment's own
// timings, handed to the tracker by the production assembly: the Leader's
// lease (the old Leader's must run out), the refresh interval (a candidate
// takes the lead, and publishes, only on a refresh tick), the renewal
// interval (a refused round is asked again within it once the view
// arrives) and the reconcile interval (a moved Query Group opens on the
// next one). Read where the reader reads it, the object detail's live row,
// for a Query Group whose rounds the view refused for not carrying it.
func TestTheProductionTrackerBoundsTheViewsWaitByTheDeploymentsTimings(t *testing.T) {
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	installTwoPhaseTwoStrategies(t, ctx, client)
	cfg := validGoAccessRuntimeConfig()
	cfg.Redis.Address = address
	withCompatibilityOutput(&cfg, address)
	cfg.Redis.StatePrefix = "alarmd-awaiting-view-wiring"
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

	want := cfg.PhaseTwo.Ownership.ControlLeaderTTL.Duration() + cfg.PhaseTwo.Control.RefreshInterval.Duration() +
		cfg.PhaseTwo.Ownership.LeaseRenewInterval.Duration() + cfg.PhaseTwo.Control.ReconcileInterval.Duration()
	const queryGroup = "qg-awaiting-view"
	waiting := observability.ContextWithTraceFields(ctx, observability.TraceFields{QueryGroupKey: queryGroup})
	for round := 0; round < 2; round++ {
		bundle.dependencies.Observer.Observe(waiting, observability.Observation{
			Component: observability.ComponentScheduler, Stage: observability.StageRunnerReturned,
			Result: observability.ResultTerminal, RunOutcome: "view_not_executable", AwaitingView: true,
			Err: &scheduler.ViewNotExecutableError{Reason: "not_in_view", AwaitingView: true},
		})
	}
	recorder := httptest.NewRecorder()
	bundle.dependencies.FleetAPI.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/objects/"+queryGroup, nil))
	var body struct {
		Tracked *struct {
			AwaitingView *fleet.AwaitingView `json:"awaiting_view"`
		} `json:"tracked"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode the object detail: %v (%s)", err, recorder.Body.String())
	}
	if body.Tracked == nil || body.Tracked.AwaitingView == nil {
		t.Fatalf("the object detail carries no wait: %s", recorder.Body.String())
	}
	if got := body.Tracked.AwaitingView.BoundSeconds; got != int64(want/time.Second) {
		t.Fatalf("the wait's bound = %d s, want the deployment's timings summed, %d s", got, int64(want/time.Second))
	}
}
