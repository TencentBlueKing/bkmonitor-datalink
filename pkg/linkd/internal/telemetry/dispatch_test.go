// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/taskdispatch/observation"
)

func TestDispatchMetricsResetAndConcurrentObservation(t *testing.T) {
	ctx := t.Context()
	runtime, err := Start(ctx, Config{Metrics: MetricsConfig{Exporter: ExporterPrometheus, Prometheus: PrometheusConfig{ListenAddress: "127.0.0.1:0"}}}, RoleAllInOne, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	observer := runtime.DispatchObserver()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			observer.Operation(ctx, "heartbeat_client", false, time.Millisecond)
			observer.Transition(ctx, "worker", "cleaner", "running", "stopping", "disconnected", 0)
		})
	}
	wg.Wait()
	observer.Transition(ctx, "controller", "cleaner", "stopping", "stopped", "reported", 3*time.Second)
	observer.ControllerSnapshot(ctx, observation.ControllerObservation{Tasks: map[string]map[string]int64{"cleaner": {"running": 2}}, Replicas: map[string]map[string]int64{"cleaner": {"target": 3, "running": 2, "shortage": 1}}})
	observer.WorkerSnapshot(ctx, observation.WorkerObservation{Tasks: map[string]map[string]int64{"cleaner": {"running": 1}}, Failures: 3, Paused: 1, AuthorizationRemaining: 10 * time.Second})
	scrape := func() string {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+runtime.PrometheusListenAddress()+"/metrics", nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := (&http.Client{Timeout: time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		b, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	before := scrape()
	for _, want := range []string{"linkd_dispatch_operations_total", "linkd_dispatch_handoff_duration_seconds_count", "linkd_dispatch_worker_heartbeat_failures", "linkd_dispatch_controller_replicas"} {
		if !strings.Contains(before, want) {
			t.Fatalf("missing metric %s", want)
		}
	}
	observer.ControllerSnapshot(ctx, observation.ControllerObservation{})
	observer.WorkerSnapshot(ctx, observation.WorkerObservation{})
	after := scrape()
	found := 0
	for _, line := range strings.Split(after, "\n") {
		if strings.HasPrefix(line, "linkd_dispatch_controller_tasks{") || strings.HasPrefix(line, "linkd_dispatch_worker_tasks{") {
			if !strings.HasSuffix(line, " 0") {
				t.Fatalf("stale task gauge: %s", line)
			}
			found++
		}
		if strings.HasPrefix(line, "linkd_dispatch_") && (strings.Contains(line, "worker_id=") || strings.Contains(line, "event_source_id=") || strings.Contains(line, "epoch=") || strings.Contains(line, "topic=")) {
			t.Fatalf("unbounded labels: %s", line)
		}
		if strings.HasPrefix(line, "linkd_dispatch_operations_total{") && strings.Contains(line, `linkd_operation="heartbeat_client"`) && !strings.HasSuffix(line, " 8") {
			t.Fatalf("lost concurrent observation: %s", line)
		}
	}
	if found != 24 {
		t.Fatalf("zero gauges=%d want24", found)
	}
}
