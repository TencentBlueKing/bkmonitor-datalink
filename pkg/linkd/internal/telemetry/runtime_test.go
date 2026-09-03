// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"context"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"linkd/internal/consume"
	controlplaneredisstream "linkd/internal/controlplane/redisstream"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
)

func TestPrometheusScrapeUsesOTelNamesAndLowCardinalityAttributes(t *testing.T) {
	t.Parallel()

	config := Config{Metrics: MetricsConfig{
		Exporter:   ExporterPrometheus,
		Prometheus: PrometheusConfig{ListenAddress: "127.0.0.1:0"},
	}}
	runtime, err := Start(context.Background(), config, RoleCleaner, "test-version")
	if err != nil && strings.Contains(err.Error(), "operation not permitted") {
		t.Skipf("sandbox does not allow local listeners: %v", err)
	}
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if runtime.PrometheusListenAddress() == "" {
		t.Fatal("PrometheusListenAddress() is empty")
	}
	t.Cleanup(func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Fatalf("Shutdown() error = %v", err)
		}
	})
	labels := consume.RuntimeLabels{
		Stage: "clean", Transport: "kafka", EventSourceID: "source-a", RecordPipelineAttempts: true,
	}
	observer := runtime.ConsumeObserver(labels)
	ctx := context.Background()
	message := consume.Message{EnqueuedAt: time.Now().Add(-time.Second)}
	observer.FlowTransition(ctx, "start")
	observer.DeliveryReceived(ctx, consume.DeliveryObservation{Lane: "raw-events/2", Bytes: 64, Redelivered: true})
	observer.HandlerStarted(ctx, message)
	observer.HandlerFinished(ctx, consume.OutcomeComplete, 15*time.Millisecond)
	observer.StepFinished(ctx, consume.StepObservation{Step: "transform", Outcome: "succeeded", Items: 1, Duration: time.Millisecond})
	observer.SettlementFinished(ctx, consume.SettlementObservation{
		Mode: consume.SettlementCumulative, Lane: "raw-events/2", Messages: 3,
		Succeeded: true, Duration: 2 * time.Millisecond,
	})
	observer.Snapshot(ctx, consume.RuntimeSnapshot{InflightMessages: 2, InflightBytes: 128, Lanes: []consume.LaneSnapshot{{
		Lane: "raw-events/2", InflightMessages: 2, InflightBytes: 128, Paused: true, Owned: true,
	}}})
	runtime.ObserveCleanerBackpressureCheck(ctx, "sampled", 42, true)
	runtime.ObserveCleanerBackpressureTransition(ctx, "pause")
	panicRecovered := false
	func() {
		defer func() { panicRecovered = recover() != nil }()
		_, _ = runtime.ObserveFinalHook(panickingFinalHook{}).Execute(ctx, lifecycle.FinalHookInput{
			Alert: domain.Alert{EventSourceID: "source-a"},
		})
	}()
	if !panicRecovered {
		t.Fatal("observed FinalHook did not preserve panic semantics")
	}
	streamObserver := runtime.RedisStreamObserver()
	streamObserver.ObserveSnapshot(ctx, controlplaneredisstream.Snapshot{
		Exists: true, ExpectedGroupPresent: true, Length: 100, EntriesAdded: 120,
		MemoryBytes: 4096, Groups: 1, Consumers: 2, Pending: 3, MaxLag: 4,
		OldestEntryAgeSeconds: 60, OldestPendingAgeSeconds: 30,
		MaxEntries: 90, EntriesAboveConfiguredMax: 10, TrimRequired: true, TrimSafe: true,
	})
	streamObserver.ReconcileFinished(ctx, "succeeded", 5*time.Millisecond, 10)
	taskObserver := runtime.ControlPlaneTaskObserver(ControlPlaneTaskElasticsearchAlertArchiver)
	taskObserver.SetActive(ctx, true)
	taskObserver.RunFinished(ctx, 20*time.Millisecond, true)
	runtime.ObserveElasticsearchArchiveBatch(ctx, 9, 7, 2)
	runtime.ObserveElasticsearchArchiveBatch(ctx, 0, 0, 0)

	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"http://"+runtime.PrometheusListenAddress()+metricsPath,
		nil,
	)
	if err != nil {
		t.Fatalf("build GET /metrics request: %v", err)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET /metrics error = %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read metrics error = %v", err)
	}
	text := string(body)
	for _, expected := range []string{
		"linkd_pipeline_attempts_total",
		"linkd_pipeline_attempt_duration_seconds_bucket",
		`linkd_pipeline_attempt_duration_seconds_bucket{linkd_event_source_id="source-a",linkd_outcome="succeeded",linkd_stage="clean",linkd_trigger="queue",messaging_system="kafka",otel_scope_name="linkd",otel_scope_schema_url="",otel_scope_version="",le="0.9"}`,
		`linkd_pipeline_attempt_duration_seconds_bucket{linkd_event_source_id="source-a",linkd_outcome="succeeded",linkd_stage="clean",linkd_trigger="queue",messaging_system="kafka",otel_scope_name="linkd",otel_scope_schema_url="",otel_scope_version="",le="1.1"}`,
		"linkd_messaging_inflight",
		"linkd_messaging_received_messages_total",
		"linkd_messaging_settled_messages_total",
		"linkd_cleaner_step_items_total",
		"linkd_cleaner_flow_active",
		"linkd_cleaner_backpressure_checks_total",
		"linkd_cleaner_backpressure_unresolved",
		"linkd_cleaner_backpressure_paused_ratio",
		"linkd_cleaner_backpressure_transitions_total",
		"linkd_final_hook_operations_total",
		"linkd_final_hook_duration_seconds_bucket",
		"linkd_redis_stream_entries",
		"linkd_redis_stream_memory_bytes",
		"linkd_redis_stream_pending",
		"linkd_redis_stream_consumer_group_max_lag",
		"linkd_redis_stream_oldest_pending_age_seconds",
		"linkd_redis_stream_reconcile_operations_total",
		"linkd_redis_stream_trimmed_entries_total",
		"linkd_redis_stream_trim_required_ratio",
		"linkd_redis_stream_trim_safe_ratio",
		"linkd_redis_stream_trim_last_entries",
		"linkd_control_plane_task_active_ratio",
		"linkd_control_plane_task_runs_total",
		"linkd_control_plane_task_run_duration_seconds_bucket",
		"linkd_control_plane_task_last_success_seconds",
		"linkd_elasticsearch_alert_archiver_archived_alerts_total",
		"linkd_elasticsearch_alert_archiver_scanned_alerts_total",
		"linkd_elasticsearch_alert_archiver_failed_alerts_total",
		"linkd_elasticsearch_alert_archiver_last_batch_scanned",
		"linkd_elasticsearch_alert_archiver_last_batch_items",
		"linkd_elasticsearch_alert_archiver_last_batch_failed",
		`linkd_task="elasticsearch-alert-archiver"`,
		"target_info",
		`linkd_stage="clean"`,
		`linkd_event_source_id="source-a"`,
		`messaging_kafka_partition="2"`,
		`messaging_system="kafka"`,
	} {
		if !strings.Contains(text, expected) {
			t.Fatalf("metrics missing %q:\n%s", expected, text)
		}
	}
	for _, forbidden := range []string{"bk_tenant_id", "event_id", "alert_id", "raw-events", `consumer_group="`} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("metrics contain forbidden attribute %q", forbidden)
		}
	}
}

type panickingFinalHook struct{}

func (panickingFinalHook) Execute(
	context.Context,
	lifecycle.FinalHookInput,
) (lifecycle.FinalHookResult, error) {
	panic("final hook failed")
}

func TestStartFailsBeforeBusinessRunsWhenPortIsOccupied(t *testing.T) {
	t.Parallel()

	listenConfig := net.ListenConfig{}
	listener, err := listenConfig.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil && strings.Contains(err.Error(), "operation not permitted") {
		t.Skipf("sandbox does not allow local listeners: %v", err)
	}
	if err != nil {
		t.Fatalf("listen test port: %v", err)
	}
	defer func() { _ = listener.Close() }()
	config := Config{Metrics: MetricsConfig{
		Exporter:   ExporterPrometheus,
		Prometheus: PrometheusConfig{ListenAddress: listener.Addr().String()},
	}}
	if _, err := Start(context.Background(), config, RoleCleaner, "test"); err == nil || !strings.Contains(err.Error(), "listen prometheus") {
		t.Fatalf("Start() error = %v", err)
	}
}

func TestDisabledMetricsUsesNoopProvider(t *testing.T) {
	t.Parallel()

	runtime, err := Start(context.Background(), Config{}, RoleCleaner, "test")
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if runtime.PrometheusListenAddress() != "" || runtime.MeterProvider() == nil {
		t.Fatalf("disabled runtime = %#v", runtime)
	}
	if err := runtime.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}
