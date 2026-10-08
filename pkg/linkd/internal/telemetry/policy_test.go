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
	"testing"
	"time"

	"linkd/internal/config"
)

func TestPolicyDetailedMetricsHaveBoundedLabels(t *testing.T) {
	r, err := Start(t.Context(), config.TelemetryConfig{Metrics: config.TelemetryMetricsConfig{Exporter: config.TelemetryExporterPrometheus, Prometheus: config.TelemetryPrometheusConfig{ListenAddress: "127.0.0.1:0"}}}, RoleLifecycle, "test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Shutdown(context.Background()) })
	r.ObservePolicyMatch(t.Context(), "suppression", "matched", time.Millisecond)
	r.ObservePolicyMatch(t.Context(), "private-policy-id", "private-payload", time.Second)
	r.ObservePolicyDelay(t.Context(), "merge", time.Second)
	r.ObservePolicySample(t.Context(), "failed")
	r.ObservePolicyStateInit(t.Context(), "clip", "repaired")
	req, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+r.PrometheusListenAddress()+metricsPath, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := (&http.Client{Timeout: time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	assertScrapeMatchesCatalog(t, text)
	for _, metric := range []string{"linkd_policy_match_duration_seconds_count", "linkd_policy_check_delay_seconds_count", "linkd_policy_statistics_samples_total", "linkd_policy_state_initializations_total"} {
		if !strings.Contains(text, metric) {
			t.Fatal("missing metric", metric)
		}
	}
	if strings.Contains(text, "private-policy-id") || strings.Contains(text, "private-payload") || strings.Contains(text, "bk_tenant_id=") {
		t.Fatal("unbounded or sensitive labels")
	}
}
