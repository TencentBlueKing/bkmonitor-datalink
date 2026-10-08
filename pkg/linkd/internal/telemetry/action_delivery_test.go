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

	"linkd/internal/actiondelivery"
	"linkd/internal/config"
)

func TestActionRunnerMetricsAggregationScopeAndShutdown(t *testing.T) {
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
		req, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+runtime.PrometheusListenAddress()+metricsPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		raw, err := io.ReadAll(resp.Body)
		if err != nil || resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode, err)
		}
		result := string(raw)
		assertScrapeMatchesCatalog(t, result)
		for _, forbidden := range []string{"private-tenant", "private-alert", "private-task", "secret-error", "request_hash"} {
			if strings.Contains(result, forbidden) {
				t.Fatal("business identity in metrics", forbidden)
			}
		}
		return result
	}
	assertSample := func(body, name, labels, value string) {
		t.Helper()
		for line := range strings.SplitSeq(body, "\n") {
			if strings.HasPrefix(line, name+"{") && strings.Contains(line, labels) && strings.HasSuffix(line, " "+value) {
				return
			}
		}
		t.Fatalf("missing sample %s %s = %s", name, labels, value)
	}
	var log bytes.Buffer
	one, two := runtime.ActionRunnerObserver(slog.New(slog.NewJSONHandler(&log, nil))), runtime.ActionRunnerObserver(nil)
	phase := actiondelivery.PhaseDeliver
	ctx := t.Context()
	one.SetRunning(ctx, phase, true)
	one.SetRunning(ctx, phase, true)
	two.SetRunning(ctx, phase, true)
	one.RoundStarted(ctx, phase)
	two.RoundStarted(ctx, phase)
	labels := `linkd_task="action-delivery"`
	body := scrape()
	assertSample(body, "linkd_action_runner_active", labels, "2")
	assertSample(body, "linkd_action_runner_inflight", labels, "2")
	at := time.Unix(1791200000, 0)
	one.RoundFinished(ctx, phase, actiondelivery.RoundResult{Scanned: 2, Visited: 2, Outcomes: map[actiondelivery.WorkOutcome]int{actiondelivery.OutcomeQueued: 1, actiondelivery.OutcomeFailed: 1}, ObservedAt: at, OldestObservedAge: time.Minute, Unconfirmed: 1, ErrorCode: "item_failed", Duration: time.Second, Failures: []actiondelivery.WorkFailure{{TenantID: "private-tenant", AlertID: "private-alert", TaskID: "private-task", Code: "secret-error"}}})
	// 失败扫描不覆盖最后成功页的观察时间与数量。
	one.RoundStarted(ctx, phase)
	one.RoundFinished(ctx, phase, actiondelivery.RoundResult{ErrorCode: "scan_failed"})
	one.SetRunning(ctx, phase, false)
	one.SetRunning(ctx, phase, false)
	body = scrape()
	assertSample(body, "linkd_action_runner_active", labels, "1")
	assertSample(body, "linkd_action_runner_inflight", labels, "1")
	assertSample(body, "linkd_action_last_page_items", labels, "2")
	assertSample(body, "linkd_action_last_page_oldest_age_seconds", labels, "60")
	assertSample(body, "linkd_action_last_page_observed_at_seconds", labels, "1.7912e+09")
	assertSample(body, "linkd_action_work_unconfirmed_total", labels, "1")
	assertSample(body, "linkd_action_work_observations_total", `linkd_outcome="queued"`, "1")
	// 非法观察值不污染标签，也不能把已经结束的页留在执行中。
	two.RoundFinished(ctx, phase, actiondelivery.RoundResult{ErrorCode: "secret-error"})
	two.SetRunning(ctx, phase, false)
	body = scrape()
	assertSample(body, "linkd_action_runner_active", labels, "0")
	assertSample(body, "linkd_action_runner_inflight", labels, "0")
	if strings.Contains(log.String(), "secret-error") || !strings.Contains(log.String(), "execution_failed") || !strings.Contains(log.String(), "private-alert") {
		t.Fatal("unsafe/missing diagnostic", log.String())
	}
	if strings.Count(log.String(), "\n") != 3 {
		t.Fatal("unexpected log count", log.String())
	}
}

func TestActionRunnerObserverConcurrentCancellationAndValidation(t *testing.T) {
	runtime, err := Start(t.Context(), config.TelemetryConfig{}, RoleControlPlane, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = runtime.Shutdown(context.Background()) }()
	observer := runtime.ActionRunnerObserver(nil)
	var wg sync.WaitGroup
	for _, phase := range []actiondelivery.RunnerPhase{actiondelivery.PhaseEnqueue, actiondelivery.PhaseDeliver} {
		wg.Go(func() {
			for range 10 {
				observer.SetRunning(t.Context(), phase, true)
				observer.RoundStarted(t.Context(), phase)
				observer.RoundFinished(t.Context(), phase, actiondelivery.RoundResult{Scanned: 1, Outcomes: map[actiondelivery.WorkOutcome]int{actiondelivery.OutcomeUnstarted: 1}, ErrorCode: "cancelled"})
				observer.SetRunning(t.Context(), phase, false)
			}
		})
	}
	wg.Wait()
	for _, invalid := range []actiondelivery.RoundResult{
		{Scanned: 17}, {Visited: 1}, {Unconfirmed: 1}, {Duration: -time.Second},
		{Scanned: 1, Outcomes: map[actiondelivery.WorkOutcome]int{"secret-error": 1}},
		{Scanned: 1, Outcomes: map[actiondelivery.WorkOutcome]int{actiondelivery.OutcomeEnqueued: 2}},
		{Failures: make([]actiondelivery.WorkFailure, 5)},
	} {
		if validActionRound(invalid) {
			t.Fatal("invalid result accepted", invalid)
		}
	}
	var logs bytes.Buffer
	var absent *Runtime
	absent.ActionRunnerObserver(slog.New(slog.NewTextHandler(&logs, nil))).RoundFinished(t.Context(), actiondelivery.PhaseEnqueue, actiondelivery.RoundResult{ErrorCode: "scan_failed"})
	if !strings.Contains(logs.String(), "scan_failed") {
		t.Fatal("metrics-disabled observation lost logs")
	}
	observer.RoundStarted(t.Context(), "unknown")
	observer.RoundFinished(t.Context(), "unknown", actiondelivery.RoundResult{})
}
