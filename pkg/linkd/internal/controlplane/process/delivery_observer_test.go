// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplaneprocess

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"linkd/internal/actiondelivery"
	"linkd/internal/config"
	"linkd/internal/controlplane/taskstate"
	"linkd/internal/projection"
)

func deliveryConfigFixture() config.Config {
	return config.Config{Lifecycle: &config.LifecycleConfig{}, Plugins: config.PluginsConfig{KAC: &config.KACPluginConfig{Enabled: true, ActionEndpoint: "https://kac.example/action", JWT: config.JWTConfig{SecretKey: "private-token"}}}}
}

func deliveryTask(t *testing.T, r *taskstate.Registry, id string) taskstate.Task {
	t.Helper()
	for _, task := range r.Snapshot().Tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatal("missing task", id)
	return taskstate.Task{}
}

func TestDeliveryCatalogMatchesActivationAndKeepsSecretsPrivate(t *testing.T) {
	for _, cfg := range []config.Config{{}, {Lifecycle: &config.LifecycleConfig{}}, {Plugins: deliveryConfigFixture().Plugins}, deliveryConfigFixture()} {
		registry := taskCatalog(cfg, 0)
		for _, id := range []string{"projection-producer", "projection-delivery", "action-enqueue", "action-delivery"} {
			row := deliveryTask(t, registry, id)
			if row.Enabled != hasKACDelivery(cfg) || row.Active || !row.Enabled && row.DisabledReason == "" || row.Enabled && row.DisabledReason != "" {
				t.Fatal("catalog/activation mismatch", id)
			}
		}
		raw, _ := json.Marshal(registry.Snapshot())
		if strings.Contains(string(raw), "private-token") || strings.Contains(string(raw), "https://kac.example") {
			t.Fatal("delivery directory exposes resources")
		}
		_, err := newProjectionTaskObserver(registry, nil, nil)
		if (err == nil) != hasKACDelivery(cfg) {
			t.Fatal("observer silently bypassed activation", err)
		}
	}
}

func TestProjectionObservationReportsIdleFailureAndCancellation(t *testing.T) {
	r := taskCatalog(deliveryConfigFixture(), 0)
	o, err := newProjectionTaskObserver(r, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	phase := projection.PhaseProduce
	o.SetRunning(t.Context(), phase, true)
	o.RoundStarted(t.Context(), phase)
	if v := deliveryTask(t, r, string(phase)); !v.Active || !v.Execution.Running {
		t.Fatal("actual start missing", v)
	}
	o.RoundFinished(t.Context(), phase, projection.RoundResult{Duration: time.Millisecond})
	if v := deliveryTask(t, r, string(phase)); v.State != "idle" || v.Execution.Work != 0 {
		t.Fatal("empty page not idle", v)
	}
	o.RoundStarted(t.Context(), phase)
	o.RoundFinished(t.Context(), phase, projection.RoundResult{Scanned: 2, Failed: 1, ErrorCode: "item_failed", Duration: time.Millisecond})
	if v := deliveryTask(t, r, string(phase)); v.State != "failed" || v.Execution.Failed != 1 || v.Execution.Work != 2 {
		t.Fatal("partial failure lost", v)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	o.RoundStarted(ctx, phase)
	o.RoundFinished(ctx, phase, projection.RoundResult{ErrorCode: "cancelled"})
	o.SetRunning(context.WithoutCancel(ctx), phase, false)
	if v := deliveryTask(t, r, string(phase)); v.Active || v.Execution.Running || v.Execution.Canceled != 1 || v.Execution.Failed != 1 {
		t.Fatal("cancel counted as failure or activity leaked", v)
	}
	o.RoundFinished(t.Context(), projection.PhaseDeliver, projection.RoundResult{ErrorCode: "private-token"})
	if v := deliveryTask(t, r, string(projection.PhaseDeliver)); v.Execution.ErrorCode != "invalid_result" {
		t.Fatal("unsafe error code accepted", v)
	}
	o.SetRunning(t.Context(), projection.RunnerPhase("private-token"), true)
	raw, _ := json.Marshal(r.Snapshot())
	if strings.Contains(string(raw), "private-token") {
		t.Fatal("unknown observation leaked")
	}
}

func TestActionObservationKeepsWaitingDistinctFromFailures(t *testing.T) {
	r := taskCatalog(deliveryConfigFixture(), 0)
	o, err := newActionTaskObserver(r, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	phase := actiondelivery.PhaseDeliver
	o.SetRunning(t.Context(), phase, true)
	o.RoundStarted(t.Context(), phase)
	o.RoundFinished(t.Context(), phase, actiondelivery.RoundResult{Scanned: 2, Visited: 2, Outcomes: map[actiondelivery.WorkOutcome]int{actiondelivery.OutcomeWaitingProjection: 1, actiondelivery.OutcomeBlocked: 1}})
	if v := deliveryTask(t, r, string(phase)); v.Execution.Failures != 0 || v.Execution.Outcome != "succeeded" {
		t.Fatal("normal wait counted as failure", v)
	}
	o.RoundStarted(t.Context(), phase)
	o.RoundFinished(t.Context(), phase, actiondelivery.RoundResult{ErrorCode: "scan_failed"})
	if v := deliveryTask(t, r, string(phase)); v.Execution.Failures != 1 || v.Execution.Failed != 1 {
		t.Fatal("scan failure counted as idle", v)
	}
	o.SetRunning(t.Context(), phase, false)
	if deliveryTask(t, r, string(phase)).Active {
		t.Fatal("action loop remained active")
	}
}
