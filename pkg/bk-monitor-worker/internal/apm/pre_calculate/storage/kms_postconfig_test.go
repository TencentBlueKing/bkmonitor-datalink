// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

package storage

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gogo/protobuf/proto"
	"github.com/golang/snappy"
	"github.com/prometheus/prometheus/prompb"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/kmstest"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/remote"
)

func TestKMSPostConfigAPMHeadersAndDynamicTokens(t *testing.T) {
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
				header   http.Header
				data     prompb.WriteRequest
				accepted bool
			}
			received := make(chan request, 8)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/write", r.URL.Path)
				assert.Equal(t, "snappy", r.Header.Get("Content-Encoding"))
				compressed, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				body, err := snappy.Decode(nil, compressed)
				if err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				var data prompb.WriteRequest
				if err := proto.Unmarshal(body, &data); err != nil {
					t.Error(err)
					w.WriteHeader(400)
					return
				}
				accepted := r.Header.Get("X-BK-TOKEN") != "unauthorized-test-token"
				received <- request{r.Header.Clone(), data, accepted}
				if accepted {
					w.WriteHeader(http.StatusNoContent)
				} else {
					w.WriteHeader(http.StatusUnauthorized)
				}
			}))
			defer server.Close()
			payload := kmstest.Payload(t, "../../../../credential/testdata/payload.json")
			delete(payload, "report_tokens")
			payload["prometheus"] = map[string]any{"headers": map[string]any{}}
			dir := t.TempDir()
			promConfig := map[string]any{"url": server.URL + "/write", "headers": map[string]any{}}
			values := map[string]any{"kms": map[string]any{"enabled": false}, "taskConfig": map[string]any{"apmPreCalculate": map[string]any{"metrics": map[string]any{"report": map[string]any{"prometheus": promConfig}}}}}
			if kms {
				values["kms"] = kmstest.Write(t, dir, payload)
			}
			cfg.FilePath = kmstest.ConfigFile(t, values)
			start := func() { viper.Reset(); cfg.InitConfig() }
			start()
			newHandler := func(token string) *MetricDimensionsHandler {
				ctx, cancel := context.WithCancel(context.Background())
				t.Cleanup(cancel)
				return NewMetricDimensionHandler(ctx, "test-data", core.BaseInfo{Token: token, AppName: "test-app"},
					remote.PrometheusWriterOptions{Url: cfg.PromRemoteWriteUrl, Headers: cfg.PromRemoteWriteHeaders},
					MetricConfigOptions{relationMetricMemDuration: time.Hour, flowMetricMemDuration: time.Hour})
			}
			checkRequest := func(token, authorization string) {
				t.Helper()
				require.Len(t, received, 1)
				r := <-received
				require.True(t, r.accepted, "acceptance is based on the receiver, not writer logs")
				require.Equal(t, token, r.header.Get("X-BK-TOKEN"))
				require.Equal(t, authorization, r.header.Get("Authorization"))
				require.NotEmpty(t, r.data.Timeseries)
				for _, series := range r.data.Timeseries {
					require.NotEmpty(t, series.Labels)
					require.NotEmpty(t, series.Samples)
				}
			}
			report := func(h *MetricDimensionsHandler, token, authorization string) {
				t.Helper()
				h.Add(PrometheusStorageData{Kind: PromFlowMetric, Value: map[string]*FlowMetricRecordStats{"__name__=kms_test_flow,service=test-service": {DurationValues: []float64{7}}}})
				// The real APM caller supplies an empty per-batch token.
				h.cleanUpAndReport(h.flowMetricCollector)
				checkRequest(token, authorization)
			}
			before := newHandler("business-dynamic-a")
			report(before, "business-dynamic-a", "")
			headers := map[string]any{"X-BK-TOKEN": "post-fixed-token", "Authorization": "post-authorization"}
			if kms {
				payload["prometheus"] = map[string]any{"headers": headers}
				kmstest.Write(t, dir, payload)
			} else {
				promConfig["headers"] = headers
				kmstest.WriteConfig(t, cfg.FilePath, values)
			}
			report(before, "business-dynamic-a", "")
			start()
			fixed := newHandler("business-dynamic-b")
			report(fixed, "post-fixed-token", "post-authorization")
			batch := prompb.WriteRequest{Timeseries: []prompb.TimeSeries{{Labels: []prompb.Label{{Name: "__name__", Value: "kms_test_explicit"}}, Samples: []prompb.Sample{{Value: 1, Timestamp: 1}}}}}
			// The existing writer can return nil for 4xx; a valid token string
			// rejected by the receiver must still fail credential acceptance.
			_ = fixed.promClient.WriteBatch(context.Background(), "unauthorized-test-token", batch)
			require.Len(t, received, 1)
			rejected := <-received
			require.Equal(t, "unauthorized-test-token", rejected.header.Get("X-BK-TOKEN"))
			require.False(t, rejected.accepted)
			report(before, "business-dynamic-a", "")
			require.NoError(t, fixed.promClient.WriteBatch(context.Background(), "explicit-batch-token", batch))
			checkRequest("explicit-batch-token", "post-authorization")
			report(fixed, "post-fixed-token", "post-authorization")

			// Revert only fixed headers. Distinct per-application tokens still come
			// from BaseInfo and are never copied into the deployment aggregate.
			if kms {
				payload["prometheus"] = map[string]any{"headers": map[string]any{}}
				kmstest.Write(t, dir, payload)
			} else {
				promConfig["headers"] = map[string]any{}
				kmstest.WriteConfig(t, cfg.FilePath, values)
			}
			start()
			for _, token := range []string{"business-dynamic-a", "business-dynamic-b"} {
				report(newHandler(token), token, "")
			}
			report(fixed, "post-fixed-token", "post-authorization")
			data, err := json.Marshal(payload)
			require.NoError(t, err)
			require.NotContains(t, string(data), "business-dynamic-")
		})
	}
}
