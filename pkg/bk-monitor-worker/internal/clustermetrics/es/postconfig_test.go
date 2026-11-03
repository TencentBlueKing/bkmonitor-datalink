// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/storage"
)

func TestReportESMetricsWithoutDestinationSkipsDatabase(t *testing.T) {
	oldURL, oldHost, oldPort := cfg.ESClusterMetricReportUrl, cfg.StorageMysqlHost, cfg.StorageMysqlPort
	t.Cleanup(func() {
		cfg.ESClusterMetricReportUrl, cfg.StorageMysqlHost, cfg.StorageMysqlPort = oldURL, oldHost, oldPort
	})
	// A local listener catches any accidental database access without requiring MySQL.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	var connections atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			connections.Add(1)
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	cfg.StorageMysqlHost = "127.0.0.1"
	cfg.StorageMysqlPort = listener.Addr().(*net.TCPAddr).Port
	cfg.ESClusterMetricReportUrl = ""
	require.NotPanics(t, func() {
		require.NoError(t, ReportESClusterMetrics(context.Background(), nil))
	})
	require.Zero(t, connections.Load())
}

func TestCollectESMetricsWithoutDestinationSkipsQueuedTask(t *testing.T) {
	oldURL := cfg.ESClusterMetricReportUrl
	t.Cleanup(func() { cfg.ESClusterMetricReportUrl = oldURL })
	cfg.ESClusterMetricReportUrl = ""
	// A task already queued before reporting was disabled must not even parse
	// cluster configuration or decrypt its credentials.
	require.NoError(t, CollectAndReportMetrics(storage.ClusterInfo{
		CustomOption: "invalid-json",
		Password:     "aes_str:::invalid-ciphertext",
	}))
}

func TestCollectESMetricsAfterDestinationAdded(t *testing.T) {
	oldURL, oldToken := cfg.ESClusterMetricReportUrl, cfg.ESClusterMetricReportAccessToken
	oldDataID, oldTarget, oldClient := cfg.ESClusterMetricReportDataId, cfg.ESClusterMetricTarget, httpClient
	t.Cleanup(func() {
		cfg.ESClusterMetricReportUrl, cfg.ESClusterMetricReportAccessToken = oldURL, oldToken
		cfg.ESClusterMetricReportDataId, cfg.ESClusterMetricTarget, httpClient = oldDataID, oldTarget, oldClient
	})
	httpClient = &http.Client{Timeout: 3 * time.Second}
	var requests atomic.Int32
	esServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		user, password, ok := r.BasicAuth()
		if !ok || user != "test-user" || password != "test-password" {
			t.Error("unexpected cluster authentication")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/_cluster/health":
			_, _ = w.Write([]byte(`{"cluster_name":"test-cluster","status":"green","number_of_nodes":2}`))
		case "/_cat/shards":
			_, _ = w.Write([]byte(`[]`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer esServer.Close()
	u, err := url.Parse(esServer.URL)
	require.NoError(t, err)
	port, err := strconv.Atoi(u.Port())
	require.NoError(t, err)
	cluster := storage.ClusterInfo{
		ClusterID: 123, ClusterName: "test-cluster", DisplayName: "test-cluster-display",
		DomainName: u.Hostname(), Port: uint(port), Username: "test-user", Password: "test-password",
		CustomOption: `{"bk_biz_id":123}`, BkTenantID: "test-tenant",
	}
	var mu sync.Mutex
	var reports []clustermetrics.CustomReportData
	reportServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("unexpected reporting method: %s", r.Method)
		}
		var report clustermetrics.CustomReportData
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		mu.Lock()
		reports = append(reports, report)
		mu.Unlock()
	}))
	defer reportServer.Close()
	cfg.ESClusterMetricReportDataId, cfg.ESClusterMetricTarget = 123, "test-target"

	cfg.ESClusterMetricReportUrl = ""
	require.NoError(t, CollectAndReportMetrics(cluster))
	require.Zero(t, requests.Load(), "unopened reporting must not query the cluster")

	// Adding a destination retains both authenticated and legacy empty-token
	// reporting. The destination, not token emptiness, controls collection.
	for _, token := range []string{"test-report-token", ""} {
		t.Run("token="+token, func(t *testing.T) {
			mu.Lock()
			reports = nil
			mu.Unlock()
			cfg.ESClusterMetricReportUrl = reportServer.URL
			cfg.ESClusterMetricReportAccessToken = token
			require.NoError(t, CollectAndReportMetrics(cluster))
			mu.Lock()
			defer mu.Unlock()
			require.NotEmpty(t, reports)
			var foundClusterHealth bool
			for _, report := range reports {
				require.Equal(t, 123, report.DataId)
				require.Equal(t, token, report.AccessToken)
				for _, metric := range report.Data {
					require.Equal(t, "test-target", metric.Target)
					require.Equal(t, "123", metric.Dimension["cluster_id"])
					require.Equal(t, "test-tenant", metric.Dimension["bk_tenant_id"])
					require.Equal(t, float64(123), metric.Dimension["bk_biz_id"])
					if value, ok := metric.Metrics["elasticsearch_cluster_health_number_of_nodes"]; ok {
						require.Equal(t, float64(2), value)
						foundClusterHealth = true
					}
				}
			}
			require.True(t, foundClusterHealth, "the real collector must deliver cluster health metrics")
		})
	}
	beforeDisable := requests.Load()
	cfg.ESClusterMetricReportUrl = ""
	require.NoError(t, CollectAndReportMetrics(cluster))
	require.Equal(t, beforeDisable, requests.Load(), "withdrawing the destination must stop collection again")
}
