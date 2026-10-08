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
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/projection"
)

func TestProjectionObserverExportsBoundedResultsAndPreservesPageOnFailure(t *testing.T) {
	runtime, err := Start(t.Context(), config.TelemetryConfig{Metrics: config.TelemetryMetricsConfig{Exporter: config.TelemetryExporterPrometheus, Prometheus: config.TelemetryPrometheusConfig{ListenAddress: "127.0.0.1:0"}}}, RoleControlPlane, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	scrape := func() string {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+runtime.PrometheusListenAddress()+metricsPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		res, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		data, err := io.ReadAll(res.Body)
		if err != nil || res.StatusCode != 200 {
			t.Fatal("scrape failed", err)
		}
		body := string(data)
		assertScrapeMatchesCatalog(t, body)
		if strings.Contains(body, "private-error") || strings.Contains(body, "bk_tenant_id=") {
			t.Fatal("unbounded label")
		}
		return body
	}
	sample := func(body, name, labels, value string) {
		t.Helper()
		for line := range strings.SplitSeq(body, "\n") {
			if strings.HasPrefix(line, name+"{") && strings.Contains(line, labels) && strings.HasSuffix(line, " "+value) {
				return
			}
		}
		t.Fatalf("missing %s %s=%s", name, labels, value)
	}
	var logs bytes.Buffer
	one, two := runtime.ProjectionRunnerObserver(slog.New(slog.NewJSONHandler(&logs, nil))), runtime.ProjectionRunnerObserver(nil)
	ctx, phase := t.Context(), projection.PhaseProduce
	for _, observer := range []*ProjectionRunnerObserver{one, two} {
		observer.SetRunning(ctx, phase, true)
		observer.RoundStarted(ctx, phase)
	}
	one.SetRunning(ctx, phase, true)
	labels := `linkd_task="projection-producer"`
	sample(scrape(), "linkd_projection_runner_active", labels, "2")
	at := time.Unix(1791200000, 0)
	one.RoundFinished(ctx, phase, projection.RoundResult{Scanned: 4, Advanced: 1, Deferred: 2, CapacityDeferred: 1, Failed: 1, ErrorCode: "item_failed", ObservedAt: at, OldestObservedAge: time.Minute, Duration: time.Second})
	one.RoundStarted(ctx, phase)
	one.RoundFinished(ctx, phase, projection.RoundResult{Failed: 1, ErrorCode: "scan_failed"})
	one.SetRunning(ctx, phase, false)
	body := scrape()
	sample(body, "linkd_projection_runner_active", labels, "1")
	sample(body, "linkd_projection_runner_inflight", labels, "1")
	sample(body, "linkd_projection_last_page_items", labels, "4")
	sample(body, "linkd_projection_last_page_oldest_age_seconds", labels, "60")
	sample(body, "linkd_projection_last_page_observed_at_seconds", labels, "1.7912e+09")
	for _, state := range []string{"advanced", "deferred", "capacity", "failed"} {
		sample(body, "linkd_projection_work_observations_total", `linkd_outcome="`+state+`"`, "1")
	}
	// 失败扫描的 Failed=1 不再虚构一条 failed 业务项；非法结果也不得污染指标或日志。
	one.RoundFinished(ctx, phase, projection.RoundResult{Scanned: 16, Advanced: 17, ErrorCode: "private-error"})
	two.RoundFinished(ctx, phase, projection.RoundResult{ErrorCode: "cancelled"})
	two.SetRunning(ctx, phase, false)
	two.SetRunning(ctx, phase, false)
	body = scrape()
	sample(body, "linkd_projection_runner_active", labels, "0")
	sample(body, "linkd_projection_runner_inflight", labels, "0")
	sample(body, "linkd_projection_runner_rounds_total", `linkd_outcome="cancelled"`, "1")
	if strings.Count(logs.String(), "\n") != 2 || strings.Contains(logs.String(), "private-error") {
		t.Fatal("unsafe/extra logs", logs.String())
	}
	one.RoundFinished(ctx, phase, projection.RoundResult{ObservedAt: at.Add(time.Second)})
	body = scrape()
	sample(body, "linkd_projection_last_page_items", labels, "0")
	sample(body, "linkd_projection_last_page_oldest_age_seconds", labels, "0")
}

func TestProjectionObserverValidationAndConcurrentShutdown(t *testing.T) {
	runtime, err := Start(t.Context(), config.TelemetryConfig{}, RoleControlPlane, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Shutdown(context.Background()) }()
	observer := runtime.ProjectionRunnerObserver(nil)
	var wg sync.WaitGroup
	for _, phase := range []projection.RunnerPhase{projection.PhaseProduce, projection.PhaseDeliver} {
		wg.Go(func() {
			for range 20 {
				observer.SetRunning(t.Context(), phase, true)
				observer.RoundStarted(t.Context(), phase)
				observer.RoundFinished(t.Context(), phase, projection.RoundResult{Scanned: 1, Failed: 1, ErrorCode: "cancelled"})
				observer.SetRunning(t.Context(), phase, false)
			}
		})
	}
	wg.Wait()
	for _, r := range []projection.RoundResult{
		{Scanned: 17}, {Scanned: 1, Advanced: 2}, {Deferred: 1, CapacityDeferred: 2}, {Advanced: -1}, {Failed: 17},
		{Duration: -time.Second}, {OldestObservedAge: -time.Second}, {Scanned: 1, Advanced: 1, ErrorCode: "scan_failed"},
		{Scanned: 1, Advanced: 1, ErrorCode: "private-error"}, {Failed: 2, ErrorCode: "scan_failed"},
	} {
		if validProjectionRound(r) {
			t.Fatal("invalid projection result accepted", r)
		}
	}
	var logs bytes.Buffer
	var empty *Runtime
	withoutMetrics := empty.ProjectionRunnerObserver(slog.New(slog.NewJSONHandler(&logs, nil)))
	withoutMetrics.RoundFinished(t.Context(), "tenant-derived-phase", projection.RoundResult{ErrorCode: "scan_failed"})
	withoutMetrics.RoundFinished(t.Context(), projection.PhaseProduce, projection.RoundResult{ErrorCode: "scan_failed", Failed: 1})
	if strings.Contains(logs.String(), "tenant-derived-phase") || strings.Count(logs.String(), "\n") != 1 {
		t.Fatal("invalid phase accepted")
	}
}
