// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package rabbitmq

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/credential"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics"
)

func TestKMSMultiInstanceCollectionAndIndependentReport(t *testing.T) {
	var reportMu sync.Mutex
	reports := map[string]clustermetrics.CustomReportData{}
	var failReport int32
	reportServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var report clustermetrics.CustomReportData
		if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if report.AccessToken != "test-rabbit-report-token" {
			t.Errorf("unexpected reporting token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if atomic.LoadInt32(&failReport) != 0 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		reportMu.Lock()
		reports[report.Data[0].Dimension["rabbitmq_name"].(string)] = report
		reportMu.Unlock()
	}))
	defer reportServer.Close()
	var failA int32
	var requestsA, requestsB int32
	management := func(user, password string, fail, counter *int32) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			u, p, ok := r.BasicAuth()
			if !ok || u != user || p != password {
				t.Errorf("unexpected instance authentication")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			atomic.AddInt32(counter, 1)
			if atomic.LoadInt32(fail) != 0 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			switch {
			case r.URL.Path == "/api/overview":
				_, _ = w.Write([]byte(`{"object_totals":{"queues":2},"queue_totals":{"messages_ready":5}}`))
			case r.URL.Path == "/api/nodes":
				_, _ = w.Write([]byte(`[{"mem_alarm":false,"disk_free_alarm":false}]`))
			case r.URL.EscapedPath() == "/api/queues/%2F":
				_, _ = w.Write([]byte(`[{"name":"important.queue","vhost":"/","state":"running","messages_ready":4},{"name":"skip.queue","vhost":"/","state":"running","messages_ready":1}]`))
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
	}
	var failB int32
	a := management("user-a", "password-a", &failA, &requestsA)
	defer a.Close()
	b := management("user-b", "password-b", &failB, &requestsB)
	defer b.Close()
	instances := []cfg.RabbitMQClusterMetricInstance{
		newTestInstance(t, a.URL, cfg.RabbitMQClusterMetricInstance{Name: "kms-a", CredentialsRef: "alias-a", Vhosts: []string{"/"}, QueueIncludes: []string{"important.*"}, QueueExcludes: []string{"skip.*"}, BkBizID: 2, BkTenantID: "tenant-a", TimeoutSeconds: 3}),
		newTestInstance(t, b.URL, cfg.RabbitMQClusterMetricInstance{Name: "kms-b", CredentialsRef: "alias-b", Vhosts: []string{"/"}, QueueIncludeRegexes: []string{`^important\.`}, BkBizID: 3, BkTenantID: "tenant-b", TimeoutSeconds: 3}),
	}
	dir, err := filepath.Abs("../../../credential/testdata")
	require.NoError(t, err)
	old := cfg.FilePath
	t.Cleanup(func() { cfg.FilePath = old; viper.Reset() })
	setReportConfig(t, reportServer.URL)
	writeConfig := func(instances []cfg.RabbitMQClusterMetricInstance) {
		rawInstances := make([]map[string]any, 0, len(instances))
		for _, inst := range instances {
			rawInstances = append(rawInstances, map[string]any{"name": inst.Name, "credentialsRef": inst.CredentialsRef, "schema": inst.Schema, "domainName": inst.DomainName, "httpPort": inst.HTTPPort, "amqpPort": 5672, "vhosts": inst.Vhosts, "queueIncludes": inst.QueueIncludes, "queueExcludes": inst.QueueExcludes, "queueIncludeRegexes": inst.QueueIncludeRegexes, "bkBizId": inst.BkBizID, "bkTenantId": inst.BkTenantID, "timeoutSeconds": inst.TimeoutSeconds})
		}
		data, err := json.Marshal(map[string]any{"kms": map[string]any{"enabled": true, "envelope_file": filepath.Join(dir, "envelope"), "private_key_file": filepath.Join(dir, "private-key")}, "taskConfig": map[string]any{"rabbitmqMetric": map[string]any{"enabled": true, "reportUrl": reportServer.URL, "reportDataId": 123, "instances": rawInstances}}})
		require.NoError(t, err)
		cfg.FilePath = filepath.Join(t.TempDir(), "bmw.json")
		require.NoError(t, os.WriteFile(cfg.FilePath, data, 0600))
		viper.Reset()
		cfg.InitConfig()
	}
	for _, order := range [][]cfg.RabbitMQClusterMetricInstance{instances, {instances[1], instances[0]}} {
		writeConfig(order)
		for _, inst := range cfg.RabbitMQClusterMetricInstances {
			require.NoError(t, CollectAndReportMetrics(context.Background(), inst))
		}
	}
	reportMu.Lock()
	for _, name := range []string{"kms-a", "kms-b"} {
		report := reports[name]
		require.Equal(t, 123, report.DataId)
		require.Len(t, report.Data, 2)
		require.Equal(t, float64(1), report.Data[0].Metrics[metricUp])
		require.Equal(t, "important.queue", report.Data[1].Dimension["queue"])
		require.Equal(t, "/", report.Data[1].Dimension["vhost"])
	}
	reportMu.Unlock()
	require.GreaterOrEqual(t, atomic.LoadInt32(&requestsA), int32(6))
	require.GreaterOrEqual(t, atomic.LoadInt32(&requestsB), int32(6))
	atomic.StoreInt32(&failA, 1)
	for _, inst := range cfg.RabbitMQClusterMetricInstances {
		err := CollectAndReportMetrics(context.Background(), inst)
		if inst.Name == "kms-a" {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
		}
	}
	reportMu.Lock()
	require.Equal(t, float64(0), reports["kms-a"].Data[0].Metrics[metricUp])
	require.Equal(t, float64(1), reports["kms-b"].Data[0].Metrics[metricUp])
	reportMu.Unlock()
	atomic.StoreInt32(&failReport, 1)
	require.Error(t, CollectAndReportMetrics(context.Background(), cfg.RabbitMQClusterMetricInstances[0]))
	// Unknown aliases stop configuration binding before collection is possible.
	v, raw := viper.New(), viper.New()
	data, err := os.ReadFile(cfg.FilePath)
	require.NoError(t, err)
	for _, c := range []*viper.Viper{v, raw} {
		c.SetConfigType("json")
		require.NoError(t, c.ReadConfig(bytes.NewReader(data)))
	}
	v.Set("taskConfig.rabbitmqMetric.instances", []map[string]any{{"credentialsref": "missing"}})
	_, err = credential.Prepare(v, raw, "bmw")
	require.Error(t, err)
}

func TestCollectAndReportMetrics(t *testing.T) {
	var report clustermetrics.CustomReportData
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/overview":
			assert.Equal(t, "Basic dXNlcjpwYXNz", r.Header.Get("Authorization"))
			_, _ = w.Write([]byte(`{
				"object_totals": {"connections": 2, "channels": 3, "queues": 2, "consumers": 4},
				"queue_totals": {"messages": 8, "messages_ready": 5, "messages_unacknowledged": 3},
				"message_stats": {
					"publish": 10, "publish_details": {"rate": 1.5},
					"deliver_get": 9, "deliver_get_details": {"rate": 1.2},
					"ack": 7, "ack_details": {"rate": 1.1},
					"redeliver": 1, "redeliver_details": {"rate": 0.1}
				}
			}`))
		case "/api/nodes":
			_, _ = w.Write([]byte(`[{"mem_alarm": false, "disk_free_alarm": true}]`))
		case "/api/queues":
			_, _ = w.Write([]byte(`[
				{
					"name": "important.queue", "vhost": "/", "state": "running",
					"messages": 6, "messages_ready": 4, "messages_unacknowledged": 2,
					"consumers": 2, "consumer_utilisation": 0.75, "memory": 1024,
					"message_stats": {
						"publish": 6, "publish_details": {"rate": 0.6},
						"deliver_get": 5, "deliver_get_details": {"rate": 0.5},
						"ack": 4, "ack_details": {"rate": 0.4},
						"redeliver": 1, "redeliver_details": {"rate": 0.1}
					}
				},
				{"name": "skip.queue", "vhost": "/", "state": "running", "messages": 1}
			]`))
		case "/report":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&report))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	setReportConfig(t, server.URL+"/report")
	instance := newTestInstance(t, server.URL, cfg.RabbitMQClusterMetricInstance{
		Name:          "main-rabbitmq",
		Username:      "user",
		Password:      "pass",
		QueueIncludes: []string{"important.*"},
		QueueExcludes: []string{"skip.*"},
		BkBizID:       2,
		BkTenantID:    "system",
	})

	require.NoError(t, CollectAndReportMetrics(context.Background(), instance))
	require.Len(t, report.Data, 2)

	overview := report.Data[0]
	assert.Equal(t, 123, report.DataId)
	assert.Equal(t, "token", report.AccessToken)
	assert.Equal(t, "bk_rabbitmq", overview.Target)
	assert.Equal(t, "main-rabbitmq", overview.Dimension["rabbitmq_name"])
	assert.Equal(t, float64(5), overview.Metrics[metricMessagesReady])
	assert.Equal(t, float64(1), overview.Metrics[metricDiskFreeAlarm])
	assert.Equal(t, float64(0), overview.Metrics[metricMemoryAlarm])

	queue := report.Data[1]
	assert.Equal(t, "/", queue.Dimension["vhost"])
	assert.Equal(t, "important.queue", queue.Dimension["queue"])
	assert.Equal(t, "running", queue.Dimension["state"])
	assert.Equal(t, float64(4), queue.Metrics[metricQueueMessagesReady])
	assert.Equal(t, float64(0.75), queue.Metrics[metricQueueConsumerUtilisation])
	assert.Equal(t, float64(1), queue.Metrics[metricQueueState])
}

func TestCollectAndReportMetricsReportsDownWhenOverviewFails(t *testing.T) {
	var report clustermetrics.CustomReportData
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/overview":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`boom`))
		case "/report":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&report))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	setReportConfig(t, server.URL+"/report")
	instance := newTestInstance(t, server.URL, cfg.RabbitMQClusterMetricInstance{Name: "main-rabbitmq"})

	err := CollectAndReportMetrics(context.Background(), instance)
	require.Error(t, err)
	require.Len(t, report.Data, 1)
	assert.Equal(t, float64(0), report.Data[0].Metrics[metricUp])
}

func TestCollectAndReportMetricsKeepsQueuesWhenOverviewAndNodesForbidden(t *testing.T) {
	var report clustermetrics.CustomReportData
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/overview":
			w.WriteHeader(http.StatusForbidden)
		case r.URL.Path == "/api/nodes":
			w.WriteHeader(http.StatusForbidden)
		case r.URL.EscapedPath() == "/api/queues/%2F":
			_, _ = w.Write([]byte(`[
				{
					"name": "important.queue", "vhost": "/", "state": "running",
					"messages": 6, "messages_ready": 4, "messages_unacknowledged": 2,
					"consumers": 2
				}
			]`))
		case r.URL.Path == "/report":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&report))
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	setReportConfig(t, server.URL+"/report")
	instance := newTestInstance(t, server.URL, cfg.RabbitMQClusterMetricInstance{
		Name:   "main-rabbitmq",
		Vhosts: []string{"/"},
	})

	require.NoError(t, CollectAndReportMetrics(context.Background(), instance))
	require.Len(t, report.Data, 2)
	assert.Equal(t, float64(1), report.Data[0].Metrics[metricUp])
	_, ok := report.Data[0].Metrics[metricMemoryAlarm]
	assert.False(t, ok)
	assert.Equal(t, "important.queue", report.Data[1].Dimension["queue"])
	assert.Equal(t, float64(4), report.Data[1].Metrics[metricQueueMessagesReady])
}

func TestQueueFilter(t *testing.T) {
	filter, err := newQueueFilter(cfg.RabbitMQClusterMetricInstance{
		Vhosts:              []string{"/"},
		QueueIncludes:       []string{"*.queue", "critical?"},
		QueueExcludes:       []string{"tmp.*"},
		QueueIncludeRegexes: []string{`^biz\.(alpha|beta)$`},
		QueueExcludeRegexes: []string{`^celery(ev)?\.`},
	})
	require.NoError(t, err)

	assert.True(t, filter.match(queueResponse{Name: "important.queue", Vhost: "/"}))
	assert.True(t, filter.match(queueResponse{Name: "critical1", Vhost: "/"}))
	assert.True(t, filter.match(queueResponse{Name: "biz.alpha", Vhost: "/"}))
	assert.False(t, filter.match(queueResponse{Name: "tmp.queue", Vhost: "/"}))
	assert.False(t, filter.match(queueResponse{Name: "celeryev.worker", Vhost: "/"}))
	assert.False(t, filter.match(queueResponse{Name: "important.queue", Vhost: "other"}))
	assert.False(t, filter.match(queueResponse{Name: "important.topic", Vhost: "/"}))
}

func TestQueueFilterInvalidRegex(t *testing.T) {
	_, err := newQueueFilter(cfg.RabbitMQClusterMetricInstance{
		QueueExcludeRegexes: []string{"["},
	})
	require.Error(t, err)
}

func setReportConfig(t *testing.T, reportURL string) {
	t.Helper()

	oldReportURL := cfg.RabbitMQClusterMetricReportUrl
	oldDataID := cfg.RabbitMQClusterMetricReportDataId
	oldToken := cfg.RabbitMQClusterMetricReportAccessToken
	oldTarget := cfg.RabbitMQClusterMetricTarget
	t.Cleanup(func() {
		cfg.RabbitMQClusterMetricReportUrl = oldReportURL
		cfg.RabbitMQClusterMetricReportDataId = oldDataID
		cfg.RabbitMQClusterMetricReportAccessToken = oldToken
		cfg.RabbitMQClusterMetricTarget = oldTarget
	})

	cfg.RabbitMQClusterMetricReportUrl = reportURL
	cfg.RabbitMQClusterMetricReportDataId = 123
	cfg.RabbitMQClusterMetricReportAccessToken = "token"
	cfg.RabbitMQClusterMetricTarget = "bk_rabbitmq"
}

func newTestInstance(t *testing.T, serverURL string, instance cfg.RabbitMQClusterMetricInstance) cfg.RabbitMQClusterMetricInstance {
	t.Helper()

	parsedURL, err := url.Parse(serverURL)
	require.NoError(t, err)

	host, portText, err := net.SplitHostPort(parsedURL.Host)
	require.NoError(t, err)

	port, err := strconv.Atoi(portText)
	require.NoError(t, err)

	instance.Schema = parsedURL.Scheme
	instance.DomainName = host
	instance.HTTPPort = port
	return instance
}
