// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package task

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/kmstest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
)

func TestKMSPostConfigSLOActivationAndWireToken(t *testing.T) {
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
			type request struct {
				token, method, path string
				body                []byte
			}
			received := make(chan request, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				received <- request{r.Header.Get("X-BK-TOKEN"), r.Method, r.URL.Path, body}
				w.WriteHeader(http.StatusAccepted)
			}))
			defer server.Close()
			payload := kmstest.Payload(t, "../../../credential/testdata/payload.json")
			tokens := payload["report_tokens"].(map[string]any)
			tokens["slo"] = ""
			dir := t.TempDir()
			slo := map[string]any{"sloPushGatewayEndpoint": server.URL}
			values := map[string]any{"kms": map[string]any{"enabled": false}, "taskConfig": map[string]any{"metadata": map[string]any{"slo": slo}}}
			if kms {
				values["kms"] = kmstest.Write(t, dir, payload)
			}
			cfg.FilePath = kmstest.ConfigFile(t, values)
			viper.Reset()
			cfg.InitConfig()
			require.False(t, confirmSloConfig(), "an endpoint with an explicitly empty token keeps SLO disabled")
			require.NoError(t, SloPush(context.Background(), nil), "disabled SLO must return before database access")
			require.Empty(t, received)

			if kms {
				tokens["slo"] = "post-slo-token"
				kmstest.Write(t, dir, payload)
			} else {
				slo["sloPushGatewayToken"] = "post-slo-token"
				kmstest.WriteConfig(t, cfg.FilePath, values)
			}
			require.False(t, confirmSloConfig(), "updating mounted material alone does not enable the running task")
			viper.Reset()
			cfg.InitConfig()
			require.True(t, confirmSloConfig(), "the next startup enables SLO with the existing endpoint")
			registry := prometheus.NewRegistry()
			gauge := prometheus.NewGauge(prometheus.GaugeOpts{Name: "kms_test_slo"})
			registry.MustRegister(gauge)
			gauge.Set(99)
			// Exercise the same push consumer as SloPush without its database calculations.
			metrics.PushRes(registry)
			require.Len(t, received, 1)
			r := <-received
			require.Equal(t, "post-slo-token", r.token)
			require.Equal(t, http.MethodPut, r.method)
			require.Equal(t, "/metrics/job/slo", r.path)
			require.Contains(t, string(r.body), "kms_test_slo")
			cfg.SloPushGatewayEndpoint = ""
			require.False(t, confirmSloConfig(), "a token alone does not enable SLO")
		})
	}
}
