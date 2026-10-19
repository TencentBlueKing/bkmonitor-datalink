// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegis

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

func TestBKRUMResourceMapping(t *testing.T) {
	span := testSpans(t, testRecord("assets_speed", map[string]any{
		"msg": "asset_speed", "url": "https://cdn.example.com:8443/assets/123/style.css?v=1", "assetType": "css",
		"method": "get", "status": 200, "duration": 47.3, "transferSize": 0, "decodedBodySize": 1200, "encodedBodySize": 500,
		"nextHopProtocol": "h2", "domainLookup": 0, "connectTime": 4.5, "tlsTime": 2.5, "requestTime": 20, "responseTime": 3.7,
	})).At(0)
	assert.Equal(t, "browser.resource", span.Name())
	assert.Equal(t, ptrace.SpanKindClient, span.Kind())
	assert.Equal(t, int64(47_300_000), int64(span.EndTimestamp()-span.StartTimestamp()))
	assert.Equal(t, testTimestamp, span.StartTimestamp().AsTime().UnixMilli())
	assert.Equal(t, 0, span.Events().Len())
	assertAttrs(t, span.Attributes(), map[string]any{
		"span_type": "resource", "resource.type": "link", "outcome.type": "success",
		"url.full": "https://cdn.example.com:8443/assets/123/style.css?v=1", "url.template": "/assets/:id/style.css",
		"url.scheme": "https", "server.address": "cdn.example.com", "server.port": int64(8443),
		"http.request.method": "GET", "http.response.status_code": int64(200), "resource.size": int64(1200),
		"resource.decoded_body_size": int64(1200), "resource.encoded_body_size": int64(500), "resource.transfer_size": int64(0),
		"resource.cache.hit": true, "resource.protocol": "h2", "resource.dns.duration": float64(0),
		"resource.connect.duration": 4.5, "resource.ssl.duration": 2.5, "resource.first_byte.duration": float64(20), "resource.download.duration": 3.7,
	})
	assertAbsent(t, span.Attributes(), "resource.dns.start", "transfer_size", "cache_hit", "next_hop_protocol", "target_domain", "duration_bucket", "network.protocol.name")
}

func TestBKRUMResourceOutcome(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     map[string]any
		outcome string
		status  ptrace.StatusCode
		events  int
	}{
		{"success", map[string]any{"status": 200}, "success", ptrace.StatusCodeUnset, 0},
		{"http failure", map[string]any{"status": 500}, "error", ptrace.StatusCodeError, 0},
		{"request timeout", map[string]any{"status": 408}, "timeout", ptrace.StatusCodeError, 0},
		{"gateway timeout", map[string]any{"status": 504}, "timeout", ptrace.StatusCodeError, 0},
		{"abort", map[string]any{"isErr": true, "errorName": "AbortError"}, "abort", ptrace.StatusCodeUnset, 0},
		{"timeout exception", map[string]any{"isErr": true, "errorName": "TimeoutError", "errorMsg": "timed out"}, "timeout", ptrace.StatusCodeError, 1},
		{"network error", map[string]any{"status": 0, "isErr": true, "errorName": "TypeError", "errorMsg": "Failed to fetch"}, "error", ptrace.StatusCodeError, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.raw["requestType"] = "xhr"
			span := testSpans(t, testRecord("api", tc.raw)).At(0)
			assert.Equal(t, "browser.resource", span.Name())
			assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
			assertAttrs(t, span.Attributes(), map[string]any{"outcome.type": tc.outcome, "resource.type": "xhr"})
			assert.Equal(t, tc.status, span.Status().Code())
			assert.Equal(t, tc.events, span.Events().Len())
		})
	}
}

func TestBKRUMResourceUnavailableData(t *testing.T) {
	span := testSpans(t, testRecord("assets_speed", map[string]any{
		"transferSize": 0, "decodedBodySize": 0, "domainLookup": -1, "requestTime": "NaN", "duration": -1,
	})).At(0)
	assertAbsent(t, span.Attributes(), "resource.cache.hit", "resource.dns.duration", "resource.first_byte.duration")
	assert.Equal(t, span.StartTimestamp(), span.EndTimestamp())
	for _, raw := range []map[string]any{{"transferSize": 0}, {"transferSize": -1}, {"decodedBodySize": 20}} {
		assertAbsent(t, testSpans(t, testRecord("assets_speed", raw)).At(0).Attributes(), "resource.cache.hit")
	}
	for _, raw := range []map[string]any{
		{"isHttps": false, "connectTime": 10, "tlsTime": 5},
		{"url": "http://example.com/style.css", "connectTime": 10, "tlsTime": 5},
		{"connectTime": 0, "tlsTime": 139.4},
	} {
		assertAbsent(t, testSpans(t, testRecord("assets_speed", raw)).At(0).Attributes(), "resource.ssl.duration")
	}
}

func TestBKRUMTraceLink(t *testing.T) {
	parent := "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	record := testRecord("api", map[string]any{"requestType": "fetch", "traceparent": parent})
	span := testSpans(t, record).At(0)
	require.Equal(t, 1, span.Links().Len())
	assert.Equal(t, "0123456789abcdef0123456789abcdef", span.Links().At(0).TraceID().HexString())
	assert.Equal(t, "0123456789abcdef", span.Links().At(0).SpanID().HexString())
	for _, value := range []string{"", "https://referrer.test/", "00-00000000000000000000000000000000-0123456789abcdef-01", "00-0123456789abcdef0123456789abcdef-0000000000000000-01"} {
		record.Message[0].raw["traceparent"] = value
		assert.Zero(t, testSpans(t, record).At(0).Links().Len())
	}
}

func TestResourceEndTimestamp_InvalidDuration(t *testing.T) {
	start := pcommon.NewTimestampFromTime(time.Now())
	for _, value := range []any{-1, "NaN", "+Inf", 1e20} {
		assert.Equal(t, start, resourceEndTimestamp(start, d2Message{raw: map[string]any{"duration": value}}))
	}
}
