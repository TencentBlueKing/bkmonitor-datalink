// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bksql

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

func bkSQLRequestCount(t *testing.T, outcome string) float64 {
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
			if labels["tsdb_type"] == "bk_sql" && labels["outcome"] == outcome {
				return m.GetCounter().GetValue()
			}
		}
	}
	return -1
}

// A bk_sql request is counted once with its final outcome, after the body is checked: the gateway
// answers 200 and states a failure in the body.
func TestBkSQLRequestsAreCountedAfterTheBodyCheck(t *testing.T) {
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
		"answered":              {http.StatusOK, `{"result":true,"code":"00","message":"ok","data":{"totalRecords":0,"list":[]}}`, "success"},
		"failure in a 200 body": {http.StatusOK, `{"result":false,"code":"1532618","message":"query failed","errors":{"error":"sql error"}}`, "body_error"},
		"code not OK":           {http.StatusOK, `{"result":true,"code":"1500004","message":"no permission"}`, "body_error"},
		"unavailable":           {http.StatusServiceUnavailable, `{}`, "error"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(c.status)
				_, _ = w.Write([]byte(c.body))
			}))
			defer server.Close()
			ctx := metadata.InitHashID(context.Background())
			instance, err := NewInstance(ctx, &Options{Address: server.URL, Timeout: time.Second, MaxLimit: 100, Curl: &curl.HttpCurl{}})
			require.NoError(t, err)
			before := map[string]float64{}
			for _, o := range []string{"success", "error", "rejected", "timeout", "canceled", "body_error"} {
				before[o] = bkSQLRequestCount(t, o)
			}
			ctx, span := trace.NewSpan(ctx, "bksql-request-outcome")
			instance.client.QuerySync(ctx, QuerySyncRequest{SQL: "SELECT 1"}, span)
			var spanErr error
			span.End(&spanErr)
			for o, value := range before {
				want := value
				if o == c.outcome {
					want++
				}
				require.Equal(t, want, bkSQLRequestCount(t, o), "outcome %s", o)
			}
		})
	}
}
