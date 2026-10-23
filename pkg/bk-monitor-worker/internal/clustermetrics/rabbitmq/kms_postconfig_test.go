// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package rabbitmq

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/clustermetrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/kmstest"
)

func TestKMSPostConfigRabbitMQPreservesCollectionAndJSONReport(t *testing.T) {
	if kmstest.Isolate(t) {
		return
	}
	old := cfg.FilePath
	t.Cleanup(func() { cfg.FilePath = old; viper.Reset() })
	for _, kms := range []bool{true, false} {
		name := "legacy without KMS files"
		if kms {
			name = "KMS aggregate"
		}
		t.Run(name, func(t *testing.T) {
			reports := make(chan clustermetrics.CustomReportData, 8)
			reportServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v2/push", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
				var report clustermetrics.CustomReportData
				if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				reports <- report
			}))
			defer reportServer.Close()
			management := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				user, password, ok := r.BasicAuth()
				if !ok || user != "user-a" || password != "password-a" {
					t.Error("instance account changed during post-configuration")
					w.WriteHeader(401)
					return
				}
				switch r.URL.Path {
				case "/api/overview":
					_, _ = w.Write([]byte(`{"object_totals":{"queues":1},"queue_totals":{"messages_ready":4}}`))
				case "/api/nodes":
					_, _ = w.Write([]byte(`[{"mem_alarm":false,"disk_free_alarm":false}]`))
				case "/api/queues//":
					_, _ = w.Write([]byte(`[{"name":"important.queue","vhost":"/","state":"running","messages_ready":4}]`))
				default:
					t.Errorf("unexpected management path %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer management.Close()
			inst := newTestInstance(t, management.URL, cfg.RabbitMQClusterMetricInstance{})
			instance := map[string]any{"name": "test-rabbit", "schema": inst.Schema, "domainName": inst.DomainName, "httpPort": inst.HTTPPort, "credentialsRef": "alias-a", "vhosts": []string{"/"}, "queueIncludes": []string{"important.*"}, "bkTenantId": "test-tenant"}
			payload := kmstest.Payload(t, "../../../credential/testdata/payload.json")
			delete(payload, "report_tokens")
			dir := t.TempDir()
			rabbit := map[string]any{"enabled": false, "reportUrl": reportServer.URL + "/v2/push", "reportDataId": 123}
			values := map[string]any{"kms": map[string]any{"enabled": false}, "taskConfig": map[string]any{"rabbitmqMetric": rabbit}}
			if kms {
				values["kms"] = kmstest.Write(t, dir, payload)
			}
			cfg.FilePath = kmstest.ConfigFile(t, values)
			viper.Reset()
			cfg.InitConfig()
			require.NoError(t, ReportRabbitMQClusterMetrics(context.Background(), nil))
			require.Empty(t, reports)
			rabbit["enabled"] = true
			rabbit["instances"] = []any{instance}
			for _, token := range []string{"post-rabbit-a", "post-rabbit-b", "post-rabbit-a"} {
				previous := cfg.RabbitMQClusterMetricReportAccessToken
				if kms {
					payload["report_tokens"] = map[string]any{"rabbitmq": token}
					kmstest.Write(t, dir, payload)
				} else {
					rabbit["reportAccessToken"] = token
					instance["username"], instance["password"] = "user-a", "password-a"
				}
				kmstest.WriteConfig(t, cfg.FilePath, values)
				require.Equal(t, previous, cfg.RabbitMQClusterMetricReportAccessToken)
				viper.Reset()
				cfg.InitConfig()
				// Three complete local collection cycles include management auth,
				// overview/nodes/queues, JSON authentication and preserved labels.
				for cycle := 0; cycle < 3; cycle++ {
					require.NoError(t, CollectAndReportMetrics(context.Background(), cfg.RabbitMQClusterMetricInstances[0]))
					require.Len(t, reports, 1)
					report := <-reports
					require.Equal(t, token, report.AccessToken)
					require.Equal(t, 123, report.DataId)
					require.Len(t, report.Data, 2)
					require.Equal(t, float64(1), report.Data[0].Metrics[metricUp])
					require.Equal(t, "test-rabbit", report.Data[0].Dimension["rabbitmq_name"])
					require.Equal(t, "test-tenant", report.Data[0].Dimension["bk_tenant_id"])
					require.Equal(t, "important.queue", report.Data[1].Dimension["queue"])
				}
			}
		})
	}
}
