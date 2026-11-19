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
	"bytes"
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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb/decoder"
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

// endingBody hands over the whole answer on its first read, after end has ended the request's context:
// the decoder then meets the end between two chunks it already holds, as when a query's deadline passes
// while a large answer is decoded.
type endingBody struct {
	req   *http.Request
	data  *bytes.Reader
	end   func(*http.Request)
	ended bool
}

func (b *endingBody) Read(p []byte) (int, error) {
	if !b.ended {
		b.ended = true
		b.end(b.req)
	}
	return b.data.Read(p)
}

func (b *endingBody) Close() error { return nil }

type endingTransport struct {
	body []byte
	end  func(*http.Request)
}

func (t *endingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK, Status: "200 OK", Header: http.Header{}, Request: req,
		Body: &endingBody{req: req, data: bytes.NewReader(t.body), end: t.end},
	}, nil
}

// A request whose context ends while its answer is decoded is counted as a timeout or a cancel, with
// either decoder: neither is a failure of InfluxDB.
func TestInfluxHTTPRequestEndedWhileDecodingIsCountedByTheContext(t *testing.T) {
	chunk, err := (&decoder.Response{Results: []decoder.Result{{StatementID: 0}}}).MarshalMsg(nil)
	require.NoError(t, err)
	bodies := map[string][]byte{
		ContentTypeJson:    []byte(`{"results":[{"statement_id":0}]}` + "\n" + `{"results":[{"statement_id":0}]}` + "\n"),
		ContentTypeMsgpack: append(append([]byte{}, chunk...), chunk...),
	}
	previousTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	for contentType, body := range bodies {
		for _, c := range []struct {
			name    string
			timeout time.Duration
			outcome string
		}{
			{"deadline", 100 * time.Millisecond, "timeout"},
			{"canceled", 5 * time.Second, "canceled"},
		} {
			t.Run(contentType+"/"+c.name, func(t *testing.T) {
				ctx, cancel := context.WithCancel(metadata.InitHashID(context.Background()))
				defer cancel()
				end := func(req *http.Request) { <-req.Context().Done() }
				if c.name == "canceled" {
					end = func(*http.Request) { cancel() }
				}
				http.DefaultTransport = &endingTransport{body: body, end: end}
				instance, err := NewInstance(ctx, &Options{
					Host: "192.0.2.10", Port: 8086, Timeout: c.timeout,
					Protocol: influxrouter.HTTP, ContentType: contentType, MaxLimit: 100, MaxSlimit: 100, Curl: &curl.HttpCurl{},
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
}

// A request InfluxDB answers with a failure is returned as an error by every metadata reader, not read as an
// empty result; an answer without a failure still reads as before. metrics already returned the request's
// error at its end and is kept here as a guard.
func TestInfluxHTTPReadersReturnAFailedRequest(t *testing.T) {
	previousTransport := http.DefaultTransport
	transport := &http.Transport{}
	http.DefaultTransport = transport
	t.Cleanup(func() { transport.CloseIdleConnections(); http.DefaultTransport = previousTransport })
	for name, c := range map[string]struct {
		status int
		body   string
		failed bool
	}{
		"unavailable": {http.StatusServiceUnavailable, `{}`, true},
		"answered":    {http.StatusOK, `{"results":[{"statement_id":0}]}`, false},
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
			query := &metadata.Query{DB: "strict", Measurement: "cpu", Field: "value"}
			end := time.Now()
			start := end.Add(-time.Hour)

			_, exemplarErr := instance.QueryExemplar(ctx, []string{"trace_id"}, query, start, end)
			_, namesErr := instance.QueryLabelNames(ctx, query, start, end)
			_, metricsErr := instance.metrics(ctx, query)
			_, valuesErr := instance.QueryLabelValues(ctx, query, "host", start, end)
			for reader, err := range map[string]error{
				"QueryExemplar": exemplarErr, "QueryLabelNames": namesErr, "metrics": metricsErr, "QueryLabelValues": valuesErr,
			} {
				if c.failed {
					require.Error(t, err, reader)
				} else {
					require.NoError(t, err, reader)
				}
			}
		})
	}
}
