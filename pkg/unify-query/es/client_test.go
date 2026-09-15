// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package es

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	elasticsearch "github.com/elastic/go-elasticsearch/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type trackedBody struct {
	io.Reader
	closed atomic.Bool
}

func (b *trackedBody) Close() error {
	b.closed.Store(true)
	return nil
}

type readErrorBody struct {
	err    error
	closed atomic.Bool
}

func (b *readErrorBody) Read([]byte) (int, error) {
	return 0, b.err
}

func (b *readErrorBody) Close() error {
	b.closed.Store(true)
	return nil
}

func newTestESClient(t *testing.T, maxConcurrency int, transport http.RoundTripper) *ESClient {
	t.Helper()
	client, err := elasticsearch.NewClient(elasticsearch.Config{
		Addresses:    []string{"http://example.test"},
		Transport:    transport,
		DisableRetry: true,
	})
	require.NoError(t, err)
	return &ESClient{
		tokenChan: make(chan int, maxConcurrency),
		client:    client,
	}
}

func newResponse(statusCode int, body io.ReadCloser) *http.Response {
	return &http.Response{
		StatusCode: statusCode,
		Header: http.Header{
			"Content-Type":      []string{"application/json"},
			"X-Elastic-Product": []string{"Elasticsearch"},
		},
		Body: body,
	}
}

func TestSearchAllowsMissingTargets(t *testing.T) {
	log.InitTestLogger()
	requestBody := `{"from":20,"size":10,"query":{"range":{"time":{"gte":1725148800000,"lt":1725235200000}}},"aggs":{"levels":{"terms":{"field":"level"}}}}`
	responseBody := "{\n  \"hits\": {\"total\": {\"value\": 0}, \"hits\": []},\n  \"aggregations\": {\"levels\": {\"buckets\": []}}\n}\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/missing,existing/_search", r.URL.Path)
		assert.Equal(t, "true", r.URL.Query().Get("ignore_unavailable"))
		assert.Equal(t, "true", r.URL.Query().Get("allow_no_indices"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, requestBody, string(body))
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		_, _ = io.WriteString(w, responseBody)
	}))
	defer server.Close()

	client, err := NewClient(&ESInfo{Host: server.URL, MaxConcurrency: 1})
	require.NoError(t, err)
	result, err := client.Search(context.Background(), requestBody, "missing", "existing")
	require.NoError(t, err)
	assert.Equal(t, responseBody, result)
}

func TestSearchReturnsElasticsearchError(t *testing.T) {
	log.InitTestLogger()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"bad query"}`)
	}))
	defer server.Close()

	client, err := NewClient(&ESInfo{Host: server.URL, MaxConcurrency: 1})
	require.NoError(t, err)
	_, err = client.Search(context.Background(), `{}`, "existing")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "400 Bad Request")
	assert.Contains(t, err.Error(), `{"error":"bad query"}`)
}

func TestClientMethodsReturnRawResponseAndCloseBody(t *testing.T) {
	tests := []struct {
		name string
		path string
		body string
		call func(*ESClient) (string, error)
	}{
		{
			name: "search",
			path: "/logs/_search",
			body: "{\n\"hits\":{\"total\":1,\"hits\":[{\"_id\":\"1\"}]}}\n",
			call: func(client *ESClient) (string, error) {
				return client.Search(context.Background(), `{}`, "logs")
			},
		},
		{
			name: "cat aliases",
			path: "/_cat/aliases",
			body: "logs logs-000001 - - -\n",
			call: func(client *ESClient) (string, error) {
				return client.Aliases()
			},
		},
		{
			name: "alias with index",
			path: "/logs-000001/_alias",
			body: `{"logs-000001":{"aliases":{"logs":{}}}}`,
			call: func(client *ESClient) (string, error) {
				return client.AliasWithIndex("logs-000001")
			},
		},
		{
			name: "cat indices",
			path: "/_cat/indices",
			body: "green open logs-000001 id 1 0 1 0 1kb 1kb\n",
			call: func(client *ESClient) (string, error) {
				return client.Indices()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader(tt.body)}
			client := newTestESClient(t, 1, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, tt.path, req.URL.Path)
				assert.Empty(t, req.URL.Query().Get("format"))
				return newResponse(http.StatusOK, body), nil
			}))

			result, err := tt.call(client)
			require.NoError(t, err)
			assert.Equal(t, tt.body, result)
			assert.True(t, body.closed.Load())
		})
	}
}

func TestClientMethodsReturnTransportError(t *testing.T) {
	transportErr := errors.New("transport failed")
	tests := []struct {
		name string
		call func(*ESClient) (string, error)
	}{
		{
			name: "search",
			call: func(client *ESClient) (string, error) {
				return client.Search(context.Background(), `{}`, "logs")
			},
		},
		{name: "cat aliases", call: func(client *ESClient) (string, error) { return client.Aliases() }},
		{
			name: "alias with index",
			call: func(client *ESClient) (string, error) { return client.AliasWithIndex("logs-000001") },
		},
		{name: "cat indices", call: func(client *ESClient) (string, error) { return client.Indices() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newTestESClient(t, 1, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return nil, transportErr
			}))
			_, err := tt.call(client)
			require.Error(t, err)
			assert.Contains(t, err.Error(), transportErr.Error())
		})
	}
}

func TestClientMethodsReturnHTTPErrorAndCloseBody(t *testing.T) {
	tests := []struct {
		name string
		call func(*ESClient) (string, error)
	}{
		{
			name: "search",
			call: func(client *ESClient) (string, error) {
				return client.Search(context.Background(), `{}`, "logs")
			},
		},
		{name: "cat aliases", call: func(client *ESClient) (string, error) { return client.Aliases() }},
		{
			name: "alias with index",
			call: func(client *ESClient) (string, error) { return client.AliasWithIndex("logs-000001") },
		},
		{name: "cat indices", call: func(client *ESClient) (string, error) { return client.Indices() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &trackedBody{Reader: strings.NewReader(`{"error":"bad request"}`)}
			client := newTestESClient(t, 1, roundTripFunc(func(*http.Request) (*http.Response, error) {
				return newResponse(http.StatusBadRequest, body), nil
			}))
			_, err := tt.call(client)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "400 Bad Request")
			assert.Contains(t, err.Error(), `{"error":"bad request"}`)
			assert.True(t, body.closed.Load())
		})
	}
}

func TestSearchReturnsReadErrorAndClosesBody(t *testing.T) {
	readErr := errors.New("read failed")
	body := &readErrorBody{err: readErr}
	client := newTestESClient(t, 1, roundTripFunc(func(*http.Request) (*http.Response, error) {
		return newResponse(http.StatusOK, body), nil
	}))

	_, err := client.Search(context.Background(), `{}`, "logs")
	require.ErrorIs(t, err, readErr)
	assert.True(t, body.closed.Load())
}

func TestSearchRespectsMaxConcurrency(t *testing.T) {
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var inFlight atomic.Int32
	var maxInFlight atomic.Int32

	client := newTestESClient(t, 1, roundTripFunc(func(*http.Request) (*http.Response, error) {
		current := inFlight.Add(1)
		for {
			maximum := maxInFlight.Load()
			if current <= maximum || maxInFlight.CompareAndSwap(maximum, current) {
				break
			}
		}
		started <- struct{}{}
		<-release
		inFlight.Add(-1)
		return newResponse(http.StatusOK, io.NopCloser(strings.NewReader(`{"hits":{"hits":[]}}`))), nil
	}))

	errs := make(chan error, 2)
	go func() {
		_, err := client.Search(context.Background(), `{}`, "logs-1")
		errs <- err
	}()
	<-started
	go func() {
		_, err := client.Search(context.Background(), `{}`, "logs-2")
		errs <- err
	}()

	select {
	case <-started:
		t.Fatal("second request reached the transport before the first released its concurrency token")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	assert.Equal(t, int32(1), maxInFlight.Load())
}

func TestSearchCanCancelWhileWaitingForConcurrencyToken(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	var calls atomic.Int32
	client := newTestESClient(t, 1, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		started <- struct{}{}
		<-release
		return newResponse(http.StatusOK, io.NopCloser(strings.NewReader(`{"hits":{"hits":[]}}`))), nil
	}))

	firstDone := make(chan error, 1)
	go func() {
		_, err := client.Search(context.Background(), `{}`, "logs-1")
		firstDone <- err
	}()
	<-started

	ctx, cancel := context.WithCancel(context.Background())
	secondDone := make(chan error, 1)
	go func() {
		_, err := client.Search(ctx, `{}`, "logs-2")
		secondDone <- err
	}()
	cancel()

	select {
	case err := <-secondDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(time.Second):
		t.Fatal("request did not return after its context was canceled while waiting for a token")
	}
	assert.Equal(t, int32(1), calls.Load())
	close(release)
	require.NoError(t, <-firstDone)
}

func TestSearchPassesContextCancellationToTransport(t *testing.T) {
	started := make(chan struct{}, 1)
	client := newTestESClient(t, 1, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		started <- struct{}{}
		<-req.Context().Done()
		return nil, req.Context().Err()
	}))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.Search(ctx, `{}`, "logs")
		done <- err
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		require.Error(t, err)
		assert.Contains(t, err.Error(), context.Canceled.Error())
	case <-time.After(time.Second):
		t.Fatal("in-flight request did not return after its context was canceled")
	}
}
