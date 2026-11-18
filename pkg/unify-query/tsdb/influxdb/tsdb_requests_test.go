// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	influxrouter "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func influxHTTPRequestCount(t *testing.T, outcome string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != "unify_query_tsdb_requests_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["tsdb_type"] == "influxdb_http" && labels["outcome"] == outcome {
				return m.GetCounter().GetValue()
			}
		}
	}
	return -1
}

// An InfluxDB 1.x request is counted once with its final outcome, after the body is checked: InfluxDB
// answers 200 and states a failure in the top-level error or in a statement's error.
func TestInfluxHTTPRequestsAreCountedAfterTheBodyCheck(t *testing.T) {
	// Its own transport: other tests in this package put httpmock on the default one.
	previousTransport := http.DefaultTransport
	transport := &http.Transport{}
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previousTransport })
	for name, c := range map[string]struct {
		status  int
		body    string
		outcome string
	}{
		"answered":               {http.StatusOK, `{"results":[{"statement_id":0,"series":[{"name":"measurements","columns":["name"],"values":[["cpu"]]}]}]}`, "success"},
		"statement error in 200": {http.StatusOK, `{"results":[{"statement_id":0,"error":"database not found: strict"}]}`, "body_error"},
		"top-level error in 200": {http.StatusOK, `{"error":"error parsing query"}`, "body_error"},
		"unavailable":            {http.StatusServiceUnavailable, `{}`, "error"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer server.Close()
			address, err := url.Parse(server.URL)
			require.NoError(t, err)
			host, portText, err := net.SplitHostPort(address.Host)
			require.NoError(t, err)
			port, err := strconv.Atoi(portText)
			require.NoError(t, err)
			ctx := metadata.InitHashID(context.Background())
			instance, err := NewInstance(ctx, &Options{
				Host: host, Port: port, Timeout: time.Second,
				Protocol: influxrouter.HTTP, ContentType: ContentTypeJson, MaxLimit: 100, MaxSlimit: 100, Curl: &curl.HttpCurl{},
			})
			require.NoError(t, err)
			before := map[string]float64{}
			for _, o := range []string{"success", "error", "rejected", "timeout", "canceled", "body_error"} {
				before[o] = influxHTTPRequestCount(t, o)
			}
			_, _ = instance.metrics(ctx, &metadata.Query{DB: "strict", Measurement: "cpu"})
			for o, value := range before {
				want := value
				if o == c.outcome {
					want++
				}
				require.Equal(t, want, influxHTTPRequestCount(t, o), "outcome %s", o)
			}
		})
	}
}
