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
	"strconv"
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

	const answered = `{"result":true,"code":"00","message":"ok"}`
	cases := []struct {
		name    string
		status  int
		body    string
		cut     bool
		delay   time.Duration
		cancel  bool
		outcome string
		// failure is the status vmQuery's error answers with (0: vmQuery returns no error).
		failure int
	}{
		{name: "answered", status: http.StatusOK, body: answered, outcome: "success"},
		{name: "failure in a 200 body", status: http.StatusOK, body: `{"result":false,"code":"1500005","message":"query failed"}`, outcome: "body_error"},
		{name: "code not OK in a 200 body", status: http.StatusOK, body: `{"result":true,"code":"1500404","message":"no route"}`, outcome: "body_error"},
		{name: "body cut short", status: http.StatusOK, body: answered, cut: true, outcome: "error", failure: http.StatusBadGateway},
		{name: "unavailable", status: http.StatusServiceUnavailable, body: answered, outcome: "error", failure: http.StatusBadGateway},
		{name: "throttled", status: http.StatusTooManyRequests, body: answered, outcome: "error", failure: http.StatusBadGateway},
		{name: "query refused", status: http.StatusUnprocessableEntity, body: answered, outcome: "rejected", failure: http.StatusBadRequest},
		{name: "too slow", status: http.StatusOK, body: answered, delay: 2 * time.Second, outcome: "timeout", failure: http.StatusGatewayTimeout},
		{name: "canceled", status: http.StatusOK, body: answered, cancel: true, outcome: "canceled", failure: http.StatusBadRequest},
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
				if c.cut {
					// Promise more than is sent, then drop the connection mid-body.
					w.Header().Set("Content-Length", strconv.Itoa(len(c.body)+64))
					w.WriteHeader(c.status)
					_, _ = w.Write([]byte(c.body[:len(c.body)/2]))
					if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
						_ = conn.Close()
					}
					return
				}
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer server.Close()
			before := map[string]float64{}
			for _, outcome := range []string{"success", "error", "rejected", "timeout", "canceled", "body_error"} {
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
			// A failure stated in a 200 body is reported to the caller by the format step, not by vmQuery.
			if c.failure == 0 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				require.Equal(t, c.failure, metadata.FailureStatus(err), err.Error())
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
