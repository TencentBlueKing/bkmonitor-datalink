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
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics"
)

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
