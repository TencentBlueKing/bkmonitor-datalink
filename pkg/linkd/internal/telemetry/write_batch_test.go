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
	"strconv"
	"strings"
	"testing"
	"time"

	"linkd/internal/consume"
)

func TestBatchMetricsAndSignalDurationIsolation(t *testing.T) {
	ctx := t.Context()
	r, err := Start(ctx, Config{Metrics: MetricsConfig{Exporter: ExporterPrometheus, Prometheus: PrometheusConfig{ListenAddress: "127.0.0.1:0"}}}, RoleLifecycle, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if e := r.Shutdown(context.Background()); e != nil {
			t.Error(e)
		}
	}()
	b, err := r.NewWriteBatchObserver()
	if err != nil {
		t.Fatal(err)
	}
	b.BatchFinished(ctx, "write", 100, 4096, 20*time.Millisecond, 3*time.Millisecond, 1)
	b.BatchFinished(ctx, "write", 2, 100, 0, time.Millisecond, 2)
	b.BatchFinished(ctx, "write", 4, 200, time.Millisecond, 2*time.Millisecond, 0)
	b.BatchPhase(ctx, "write", "connection", 3*time.Millisecond)
	b.BatchTriggered(ctx, "write", "operations")
	o := r.ConsumeObserver(consume.RuntimeLabels{Stage: "lifecycle", Transport: "redis_streams"})
	o.HandlerStarted(ctx, consume.Message{})
	o.HandlerFinished(ctx, consume.OutcomeComplete, 2*time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+r.PrometheusListenAddress()+metricsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	for _, tc := range []struct {
		name, label string
		want        float64
	}{
		{"linkd_elasticsearch_write_batch_batches_total", `linkd_outcome="partial_failed"`, 1},
		{"linkd_elasticsearch_write_batch_batches_total", `linkd_outcome="failed"`, 1},
		{"linkd_elasticsearch_write_batch_batches_total", `linkd_outcome="succeeded"`, 1},
		{"linkd_elasticsearch_write_batch_items_total", `linkd_outcome="succeeded"`, 103},
		{"linkd_elasticsearch_write_batch_items_total", `linkd_outcome="failed"`, 3},
		{"linkd_elasticsearch_write_batch_operations_count", "", 3},
		{"linkd_elasticsearch_write_batch_operations_sum", "", 106},
		{"linkd_elasticsearch_write_batch_phase_duration_seconds_count", `linkd_batch_phase="connection"`, 1},
		{"linkd_elasticsearch_write_batch_triggers_total", `linkd_batch_trigger="operations"`, 1},
		{"linkd_elasticsearch_write_batch_duration_seconds_bucket", `le="0.005"`, 3},
		{"linkd_elasticsearch_write_batch_queue_duration_seconds_bucket", `le="0.025"`, 3},
	} {
		found := false
		for _, line := range strings.Split(text, "\n") {
			if !strings.HasPrefix(line, tc.name+"{") || !strings.Contains(line, `linkd_batch_kind="write"`) || !strings.Contains(line, tc.label) {
				continue
			}
			fields := strings.Fields(line)
			got, e := strconv.ParseFloat(fields[len(fields)-1], 64)
			if e != nil || got != tc.want {
				t.Fatalf("%s got=%v want=%v", line, got, tc.want)
			}
			found = true
		}
		if !found {
			t.Errorf("missing %s %s", tc.name, tc.label)
		}
	}
	if !strings.Contains(text, "linkd_messaging_handler_duration_seconds_count") {
		t.Fatal("missing Signal duration")
	}
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "linkd_pipeline_attempt_duration_seconds_") && strings.Contains(line, `linkd_stage="lifecycle"`) {
			t.Fatalf("Signal polluted Event duration: %s", line)
		}
	}
}
