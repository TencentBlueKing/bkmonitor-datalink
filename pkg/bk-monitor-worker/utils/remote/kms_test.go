// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License. See the License at http://opensource.org/licenses/MIT.

package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/credential"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/prometheus/prometheus/prompb"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestKMSPrometheusHeadersAndTokensKeepProtocolWithoutLogOutput(t *testing.T) {
	dir, err := filepath.Abs("../../credential/testdata")
	require.NoError(t, err)
	v, raw := viper.New(), viper.New()
	raw.Set("kms.enabled", true)
	raw.Set("kms.envelope_file", filepath.Join(dir, "envelope"))
	raw.Set("kms.private_key_file", filepath.Join(dir, "private-key"))
	snapshot, err := credential.Prepare(v, raw, "bmw")
	require.NoError(t, err)
	snapshot.Apply(v)
	var count int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "test-prometheus-secret", r.Header.Get("Authorization"))
		require.Equal(t, "test-wire-token", r.Header.Get("X-BK-TOKEN"))
		require.Equal(t, "snappy", r.Header.Get("Content-Encoding"))
		count++
	}))
	defer server.Close()
	writer := NewPrometheusWriterClient("test-wire-token", server.URL, v.GetStringMapString("taskConfig.apmPreCalculate.metrics.report.prometheus.headers"))
	logFile := filepath.Join(t.TempDir(), "remote.log")
	writer.logger = logger.New(logger.Options{Filename: logFile})
	err = writer.WriteBatch(context.Background(), "", prompb.WriteRequest{Timeseries: []prompb.TimeSeries{{Labels: []prompb.Label{{Name: "__name__", Value: "test_metric"}}, Samples: []prompb.Sample{{Value: 1, Timestamp: 1}}}}})
	require.NoError(t, err)
	require.Equal(t, 1, count)
	data, err := os.ReadFile(logFile)
	require.NoError(t, err)
	require.NotContains(t, string(data), "test-prometheus-secret")
	require.NotContains(t, string(data), "test-wire-token")
	require.Contains(t, string(data), "pushed 1 series")
}
