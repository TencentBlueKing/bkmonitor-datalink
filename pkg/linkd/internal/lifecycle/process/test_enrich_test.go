// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lifecycleprocess

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"linkd/internal/config"
	"linkd/internal/domain"
	"linkd/internal/lifecycle"
	"linkd/internal/lifecycle/enrich"
	"linkd/internal/store/storetest"
	"linkd/internal/telemetry"
)

func TestSimulatedEnrichUsesObservedDataSource(t *testing.T) {
	runtime, err := telemetry.Start(t.Context(), telemetry.Config{Metrics: telemetry.MetricsConfig{Exporter: telemetry.ExporterPrometheus, Prometheus: telemetry.PrometheusConfig{ListenAddress: "127.0.0.1:0"}}}, telemetry.RoleLifecycle, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, tc := range []struct {
		name    string
		options map[string]any
		status  domain.EnrichStatus
	}{
		{name: "success", options: map[string]any{"calls": 3}, status: domain.EnrichStatusSucceeded},
		{name: "injected failure", options: map[string]any{"calls": 3, "error_rate": 1}, status: domain.EnrichStatusFailed},
		{name: "call timeout", options: map[string]any{"calls": 3, "sleep_mean_milliseconds": 100, "timeout_milliseconds": 1}, status: domain.EnrichStatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := config.EventSource{EventSourceID: "source", Enrich: config.EnrichConfig{Processors: []config.EnrichProcessorConfig{{Type: "test", Config: map[string]any{"fields": map[string]any{"sample": true}, "datasource": tc.options}}}}}
			if err := validateEnricherConfig(source); err != nil {
				t.Fatal(err)
			}
			opened, err := openEnrichRuntime(t.Context(), source, 8, time.Second, runtime)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := opened.Close(); err != nil {
					t.Error(err)
				}
			}()
			router, err := opened.router(source, runtime)
			if err != nil {
				t.Fatal(err)
			}
			alert := storetest.Alert("private-tenant", "private-alert", "event", "fp", "warning")
			result, err := router.Enrich(t.Context(), lifecycle.EnrichInput{Alert: alert})
			if err != nil || result.Status != tc.status {
				t.Fatalf("status=%v error=%v", result.Status, err)
			}
			payload, err := enrich.DecodePayload(result.Data)
			if err != nil {
				t.Fatal(err)
			}
			value := payload.Processors[0]["test"]
			if tc.status == domain.EnrichStatusSucceeded && string(value.Value["sample"]) != "true" {
				t.Fatal("configured fields missing")
			}
			if tc.status == domain.EnrichStatusFailed && (len(value.Value) != 0 || len(value.Diagnostics) != 1 || value.Diagnostics[0].Dependency != "test") {
				t.Fatalf("failure envelope=%#v", value)
			}
		})
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+runtime.PrometheusListenAddress()+"/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	body := string(data)
	if !strings.Contains(body, `linkd_datasource="test"`) {
		t.Fatal("test datasource was not observed")
	}
	for outcome, count := range map[string]string{"found": "3", "failed": "1", "canceled": "1"} {
		found := false
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "linkd_enrich_datasource_operations_total{") && strings.Contains(line, `linkd_datasource="test"`) && strings.Contains(line, `linkd_operation="call"`) && strings.Contains(line, `linkd_outcome="`+outcome+`"`) && strings.HasSuffix(line, " "+count) {
				found = true
			}
		}
		if !found {
			t.Errorf("datasource %s count=%s not exported", outcome, count)
		}
	}
	if !strings.Contains(body, "linkd_enrich_datasource_duration_seconds_bucket") || strings.Contains(body, "private-") {
		t.Fatal("datasource histogram missing or identity leaked")
	}
}
