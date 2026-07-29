// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package podterminatingreporter

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"
)

func TestReporterRegistryExposesExactMetricsWithoutGlobalRegistration(t *testing.T) {
	require.False(t, gathererContainsMetric(prometheus.DefaultGatherer, "pod_terminating_seconds"))

	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1000, 0)
	var snapshots []Snapshot
	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{
		{Namespace: "default", Pod: "example", Node: "node-1", Seconds: 30},
	}, now, persistSnapshots(&snapshots)))
	registry := NewRegistry(state, func() time.Time { return now.Add(time.Second) })

	expected := `# HELP pod_terminating_reporter_active_entries Number of currently deleting Pod dimensions in the latest persisted state.
# TYPE pod_terminating_reporter_active_entries gauge
pod_terminating_reporter_active_entries 1
# HELP pod_terminating_reporter_kubernetes_api_errors_total Total Kubernetes API request errors observed by the reporter.
# TYPE pod_terminating_reporter_kubernetes_api_errors_total counter
pod_terminating_reporter_kubernetes_api_errors_total{operation="get_state"} 0
pod_terminating_reporter_kubernetes_api_errors_total{operation="list_pods"} 0
pod_terminating_reporter_kubernetes_api_errors_total{operation="patch_state"} 0
# HELP pod_terminating_reporter_last_success_timestamp_seconds Unix timestamp of the latest successful refresh.
# TYPE pod_terminating_reporter_last_success_timestamp_seconds gauge
pod_terminating_reporter_last_success_timestamp_seconds 1000
# HELP pod_terminating_reporter_recovery_entries Number of recovery tombstones in the latest persisted state.
# TYPE pod_terminating_reporter_recovery_entries gauge
pod_terminating_reporter_recovery_entries 0
# HELP pod_terminating_reporter_refresh_duration_seconds Duration of the latest complete refresh attempt, including Pod listing and state persistence.
# TYPE pod_terminating_reporter_refresh_duration_seconds gauge
pod_terminating_reporter_refresh_duration_seconds 0
# HELP pod_terminating_reporter_refresh_success Whether the latest complete refresh succeeded and remains fresh.
# TYPE pod_terminating_reporter_refresh_success gauge
pod_terminating_reporter_refresh_success 1
# HELP pod_terminating_reporter_state_bytes UTF-8 bytes of compact state.json in the latest persisted state.
# TYPE pod_terminating_reporter_state_bytes gauge
pod_terminating_reporter_state_bytes ` + fmt.Sprint(state.MetricsSnapshot(now.Add(time.Second)).StateBytes) + `
# HELP pod_terminating_seconds Seconds since deletion was requested for a Pod.
# TYPE pod_terminating_seconds gauge
pod_terminating_seconds{namespace="default",node="node-1",pod="example"} 30
`
	require.NoError(t, testutil.GatherAndCompare(registry, strings.NewReader(expected),
		"pod_terminating_seconds",
		"pod_terminating_reporter_refresh_success",
		"pod_terminating_reporter_last_success_timestamp_seconds",
		"pod_terminating_reporter_active_entries",
		"pod_terminating_reporter_recovery_entries",
		"pod_terminating_reporter_state_bytes",
		"pod_terminating_reporter_refresh_duration_seconds",
		"pod_terminating_reporter_kubernetes_api_errors_total",
	))
	require.False(t, gathererContainsMetric(prometheus.DefaultGatherer, "pod_terminating_seconds"))
}

func TestReporterRegistrySuppressesStaleRowsButKeepsCapacity(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1000, 0)
	var snapshots []Snapshot
	require.NoError(t, state.ApplySuccess(context.Background(), []MetricRow{testRow(7200)}, now, persistSnapshots(&snapshots)))
	registry := NewRegistry(state, func() time.Time { return now.Add(3*time.Minute + time.Second) })

	families, err := registry.Gather()
	require.NoError(t, err)
	values := make(map[string]float64)
	for _, family := range families {
		if len(family.Metric) > 0 && family.Metric[0].Gauge != nil {
			values[family.GetName()] = family.Metric[0].Gauge.GetValue()
		}
	}
	require.Equal(t, float64(0), values["pod_terminating_reporter_refresh_success"])
	require.Equal(t, float64(1), values["pod_terminating_reporter_active_entries"])
	require.False(t, gathererContainsMetric(registry, "pod_terminating_seconds"))
}

func TestHTTPHandlerLivenessReadinessAndMetrics(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1000, 0)
	current := now
	handler := NewHTTPHandler(state, func() time.Time { return current }, time.Second)

	requireHTTPStatus(t, handler, "/livez", http.StatusOK, "ok")
	requireHTTPStatus(t, handler, "/healthz", http.StatusOK, "ok")
	requireHTTPStatus(t, handler, "/readyz", http.StatusServiceUnavailable, "stale")

	require.NoError(t, state.ApplySuccess(context.Background(), nil, now, nil))
	requireHTTPStatus(t, handler, "/readyz", http.StatusOK, "ok")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	require.Equal(t, http.StatusOK, response.Code)
	require.Contains(t, response.Body.String(), "pod_terminating_reporter_refresh_success 1")
	requireHTTPStatus(t, handler, "/unknown", http.StatusNotFound, "")
}

type blockingGatherer struct {
	started chan struct{}
	release <-chan struct{}
}

func (g *blockingGatherer) Gather() ([]*dto.MetricFamily, error) {
	g.started <- struct{}{}
	<-g.release
	return nil, nil
}

func TestHTTPHandlerBoundsConcurrentScrapesWithoutBlockingProbes(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	now := time.Unix(1000, 0)
	require.NoError(t, state.ApplySuccess(context.Background(), nil, now, nil))
	started := make(chan struct{}, maxMetricsRequestsInFlight+1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	handler := newHTTPHandlerWithGatherer(
		state,
		func() time.Time { return now },
		5*time.Second,
		&blockingGatherer{started: started, release: release},
	)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := &http.Client{Timeout: time.Second}

	var waitGroup sync.WaitGroup
	statuses := make(chan int, maxMetricsRequestsInFlight)
	for index := 0; index < maxMetricsRequestsInFlight; index++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			response, err := client.Get(server.URL + "/metrics")
			if err != nil {
				statuses <- 0
				return
			}
			defer response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	for index := 0; index < maxMetricsRequestsInFlight; index++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("metrics gather did not start")
		}
	}

	response, err := client.Get(server.URL + "/metrics")
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.Contains(t, string(body), "Limit of concurrent requests reached")
	require.Empty(t, started)

	requireHTTPURLStatus(t, server.URL+"/livez", http.StatusOK)
	requireHTTPURLStatus(t, server.URL+"/readyz", http.StatusOK)

	releaseOnce.Do(func() { close(release) })
	waitGroup.Wait()
	close(statuses)
	for status := range statuses {
		require.Equal(t, http.StatusOK, status)
	}
}

func TestHTTPHandlerTimesOutSlowScrape(t *testing.T) {
	state := NewState(10*time.Minute, 3*time.Minute)
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	handler := newHTTPHandlerWithGatherer(
		state,
		time.Now,
		20*time.Millisecond,
		&blockingGatherer{started: started, release: release},
	)
	server := httptest.NewServer(handler)
	defer server.Close()

	response, err := (&http.Client{Timeout: time.Second}).Get(server.URL + "/metrics")
	require.NoError(t, err)
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, response.StatusCode)
	require.Contains(t, string(body), "Exceeded configured timeout")
	releaseOnce.Do(func() { close(release) })
}

func gathererContainsMetric(gatherer prometheus.Gatherer, name string) bool {
	families, err := gatherer.Gather()
	if err != nil {
		return false
	}
	for _, family := range families {
		if family.GetName() == name {
			return true
		}
	}
	return false
}

func requireHTTPURLStatus(t *testing.T, url string, status int) {
	t.Helper()
	response, err := http.Get(url)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, status, response.StatusCode)
}

func requireHTTPStatus(t *testing.T, handler http.Handler, path string, status int, body string) {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
	require.Equal(t, status, response.Code)
	actual, err := io.ReadAll(response.Result().Body)
	require.NoError(t, err)
	require.Equal(t, body, string(actual))
}
