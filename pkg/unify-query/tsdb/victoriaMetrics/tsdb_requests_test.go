// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package victoriaMetrics

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/curl"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func vmRequestCount(t *testing.T, outcome string) float64 {
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
			if labels["tsdb_type"] == "victoria_metrics" && labels["outcome"] == outcome {
				return m.GetCounter().GetValue()
			}
		}
	}
	return -1
}

// Every VM request is counted with its outcome, failures included, on a real HTTP server.
func TestVMRequestsAreCountedByOutcome(t *testing.T) {
	previousTransport := http.DefaultTransport
	transport := &http.Transport{}
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previousTransport })

	cases := []struct {
		name    string
		status  int
		delay   time.Duration
		cancel  bool
		outcome string
	}{
		{name: "answered", status: http.StatusOK, outcome: "success"},
		{name: "unavailable", status: http.StatusServiceUnavailable, outcome: "error"},
		{name: "throttled", status: http.StatusTooManyRequests, outcome: "error"},
		{name: "query refused", status: http.StatusUnprocessableEntity, outcome: "rejected"},
		{name: "too slow", status: http.StatusOK, delay: 2 * time.Second, outcome: "timeout"},
		{name: "canceled", status: http.StatusOK, cancel: true, outcome: "canceled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if c.delay > 0 {
					select {
					case <-time.After(c.delay):
					case <-r.Context().Done():
						return
					}
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			before := map[string]float64{}
			for _, outcome := range []string{"success", "error", "rejected", "timeout", "canceled"} {
				before[outcome] = vmRequestCount(t, outcome)
			}
			instance := &Instance{url: server.URL, timeout: 200 * time.Millisecond, curl: &curl.HttpCurl{}}
			ctx := metadata.InitHashID(context.Background())
			if c.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			ctx, span := trace.NewSpan(ctx, "vm-request-outcome")
			err := instance.vmQuery(ctx, "{}", &VmResponse{}, span)
			span.End(&err)
			if c.outcome == "success" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
			for outcome, value := range before {
				want := value
				if outcome == c.outcome {
					want++
				}
				require.Equal(t, want, vmRequestCount(t, outcome), "outcome %s", outcome)
			}
		})
	}
}
