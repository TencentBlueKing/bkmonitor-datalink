// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BK-Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package remote

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/prometheus/prompb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPrometheusWriteClient struct {
	tokens   []string
	requests []prompb.WriteRequest
}

func (c *mockPrometheusWriteClient) Close(context.Context) error {
	return nil
}

func (c *mockPrometheusWriteClient) WriteBatch(_ context.Context, token string, request prompb.WriteRequest) error {
	c.tokens = append(c.tokens, token)
	c.requests = append(c.requests, request)
	return nil
}

func TestRelationDataIDReporterUsesConfiguredDataIDToken(t *testing.T) {
	writer := &mockPrometheusWriteClient{}
	fetchCount := 0
	reporter := newRelationDataIDReporter(123, writer, func(dataID uint) (string, error) {
		fetchCount++
		assert.Equal(t, uint(123), dataID)
		return "relation-token", nil
	})

	series := prompb.TimeSeries{Labels: []prompb.Label{{Name: "__name__", Value: "node_with_system"}}}
	require.NoError(t, reporter.Write(context.Background(), series))
	require.NoError(t, reporter.Write(context.Background(), series))

	assert.Equal(t, 1, fetchCount)
	assert.Equal(t, []string{"relation-token", "relation-token"}, writer.tokens)
	assert.Len(t, writer.requests, 2)
}

func TestRelationDataIDReporterDoesNotWriteWhenTokenLookupFails(t *testing.T) {
	writer := &mockPrometheusWriteClient{}
	reporter := newRelationDataIDReporter(123, writer, func(uint) (string, error) {
		return "", errors.New("time series group not found")
	})

	err := reporter.Write(context.Background(), prompb.TimeSeries{})
	require.Error(t, err)
	assert.Empty(t, writer.requests)
}

func TestNewRelationDataIDReporterRequiresDataID(t *testing.T) {
	reporter, err := NewRelationDataIDReporter(0, "http://remote-write", nil)
	assert.Nil(t, reporter)
	require.Error(t, err)
}

func TestRelationDataIDReporterRefreshesTokenAndDoesNotUseStaleToken(t *testing.T) {
	writer := &mockPrometheusWriteClient{}
	token := "first"
	var lookupErr error
	calls := 0
	r := newRelationDataIDReporter(123, writer, func(uint) (string, error) { calls++; return token, lookupErr })
	require.NoError(t, r.Write(context.Background()))
	require.Zero(t, calls)
	require.NoError(t, r.Write(context.Background(), prompb.TimeSeries{}))
	r.tokenExpires = time.Now().Add(-time.Second)
	lookupErr = errors.New("unavailable")
	require.Error(t, r.Write(context.Background(), prompb.TimeSeries{}))
	require.Len(t, writer.requests, 1)
	lookupErr = nil
	token = ""
	require.Error(t, r.Write(context.Background(), prompb.TimeSeries{}))
	require.Len(t, writer.requests, 1)
	token = "rotated"
	require.NoError(t, r.Write(context.Background(), prompb.TimeSeries{}))
	require.Equal(t, []string{"first", "rotated"}, writer.tokens)
}

func TestRelationDataIDReporterHTTPStatusAndToken(t *testing.T) {
	for _, status := range []int{200, 204, 400, 401, 403, 429, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				require.Equal(t, "relation-token", req.Header.Get("X-BK-TOKEN"))
				w.WriteHeader(status)
			}))
			defer server.Close()
			writer := NewPrometheusWriterClient("app-token", server.URL, nil)
			r := newRelationDataIDReporter(123, writer, func(uint) (string, error) { return "relation-token", nil })
			defer r.Close(context.Background())
			err := r.Write(context.Background(), prompb.TimeSeries{Samples: []prompb.Sample{{Value: 1, Timestamp: 1}}})
			if status >= 200 && status < 300 {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}
