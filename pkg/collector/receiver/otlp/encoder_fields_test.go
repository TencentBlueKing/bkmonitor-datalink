// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package otlp

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	logsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func otlpJSONToProtoJSON(body []byte) []byte {
	return []byte(strings.NewReplacer(
		`"traceId":"0102030405060708090a0b0c0d0e0f10"`, `"traceId":"AQIDBAUGBwgJCgsMDQ4PEA=="`,
		`"traceId":"101112131415161718191a1b1c1d1e1f"`, `"traceId":"EBESExQVFhcYGRobHB0eHw=="`,
		`"spanId":"0102030405060708"`, `"spanId":"AQIDBAUGBwg="`,
		`"spanId":"1011121314151617"`, `"spanId":"EBESExQVFhc="`,
		`"parentSpanId":"0807060504030201"`, `"parentSpanId":"CAcGBQQDAgE="`,
	).Replace(string(body)))
}

func protoJSONToOTLPJSON(body []byte) []byte {
	return []byte(strings.NewReplacer(
		`"traceId":"AQIDBAUGBwgJCgsMDQ4PEA=="`, `"traceId":"0102030405060708090a0b0c0d0e0f10"`,
		`"traceId":"EBESExQVFhcYGRobHB0eHw=="`, `"traceId":"101112131415161718191a1b1c1d1e1f"`,
		`"spanId":"AQIDBAUGBwg="`, `"spanId":"0102030405060708"`,
		`"spanId":"EBESExQVFhc="`, `"spanId":"1011121314151617"`,
		`"parentSpanId":"CAcGBQQDAgE="`, `"parentSpanId":"0807060504030201"`,
		`"kind":"SPAN_KIND_SERVER"`, `"kind":2`,
		`"code":"STATUS_CODE_ERROR"`, `"code":2`,
		`"severityNumber":"SEVERITY_NUMBER_WARN"`, `"severityNumber":13`,
		`"aggregationTemporality":"AGGREGATION_TEMPORALITY_DELTA"`, `"aggregationTemporality":1`,
		`"aggregationTemporality":"AGGREGATION_TEMPORALITY_CUMULATIVE"`, `"aggregationTemporality":2`,
	).Replace(string(body)))
}

// pdata v0.61 does not model every OTLP field, so these tests compare the
// represented fields and require both encoders to accept newer unknown fields.
func assertOTLPWireRoundTrip(
	t *testing.T,
	body string,
	request proto.Message,
	decode func(Encoder, []byte) (any, error),
	reencode func(any, bool) ([]byte, error),
	clearAdditions func(proto.Message),
) {
	t.Helper()
	require.NoError(t, protojson.Unmarshal([]byte(body), request))

	protobufPayload, err := proto.Marshal(request)
	require.NoError(t, err)

	for _, tc := range []struct {
		name    string
		encoder Encoder
		payload []byte
		json    bool
	}{
		{name: "protobuf", encoder: PbEncoder(), payload: protobufPayload},
		{name: "json", encoder: JsonEncoder(), payload: protoJSONToOTLPJSON([]byte(body)), json: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := decode(tc.encoder, tc.payload)
			require.NoError(t, err)

			encoded, err := reencode(data, tc.json)
			require.NoError(t, err)

			got := proto.Clone(request)
			if tc.json {
				err = protojson.Unmarshal(otlpJSONToProtoJSON(encoded), got)
			} else {
				err = proto.Unmarshal(encoded, got)
			}
			require.NoError(t, err)

			want := proto.Clone(request)
			clearAdditions(want)
			clearAdditions(got)
			require.True(t, proto.Equal(want, got), "OTLP fields changed during decode and encode")
		})
	}
}

func TestOTLPTraceFieldsRoundTrip(t *testing.T) {
	const body = `{
		"resourceSpans": [{
			"resource": {"attributes": [{"key":"service.name","value":{"stringValue":"collector"}}],"droppedAttributesCount":1},
			"schemaUrl":"https://example.test/resource/1",
			"scopeSpans": [{
				"scope":{"name":"instrumentation","version":"1.2.3","attributes":[{"key":"scope.key","value":{"boolValue":true}}]},
				"schemaUrl":"https://example.test/scope/1",
				"spans": [{
					"traceId":"AQIDBAUGBwgJCgsMDQ4PEA==","spanId":"AQIDBAUGBwg=","parentSpanId":"CAcGBQQDAgE=",
					"traceState":"vendor=value","name":"GET /items","kind":"SPAN_KIND_SERVER",
					"startTimeUnixNano":"100","endTimeUnixNano":"200",
					"attributes":[{"key":"http.method","value":{"stringValue":"GET"}}],"droppedAttributesCount":2,
					"events":[{"timeUnixNano":"150","name":"exception","attributes":[{"key":"exception.type","value":{"stringValue":"error"}}],"droppedAttributesCount":3}],"droppedEventsCount":4,
					"links":[{"traceId":"EBESExQVFhcYGRobHB0eHw==","spanId":"EBESExQVFhc=","traceState":"vendor=link","attributes":[{"key":"link.key","value":{"intValue":"7"}}],"droppedAttributesCount":5,"flags":512}],"droppedLinksCount":6,
					"status":{"message":"failed","code":"STATUS_CODE_ERROR"},"flags":257
				}]
			}]
		}]
	}`

	request := &tracev1.ExportTraceServiceRequest{}
	assertOTLPWireRoundTrip(
		t,
		body,
		request,
		func(encoder Encoder, payload []byte) (any, error) { return encoder.UnmarshalTraces(payload) },
		func(data any, json bool) ([]byte, error) {
			req := ptraceotlp.NewRequestFromTraces(data.(ptrace.Traces))
			if json {
				return req.MarshalJSON()
			}
			return req.MarshalProto()
		},
		func(message proto.Message) {
			request := message.(*tracev1.ExportTraceServiceRequest)
			for _, resourceSpans := range request.ResourceSpans {
				for _, scopeSpans := range resourceSpans.ScopeSpans {
					for _, span := range scopeSpans.Spans {
						span.Flags = 0
						for _, link := range span.Links {
							link.Flags = 0
						}
					}
				}
			}
		},
	)
}

func TestOTLPMetricsFieldsRoundTrip(t *testing.T) {
	const body = `{
		"resourceMetrics": [{
			"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"collector"}}],"droppedAttributesCount":1},
			"schemaUrl":"https://example.test/resource/1",
			"scopeMetrics":[{
				"scope":{"name":"instrumentation","version":"1.2.3"},"schemaUrl":"https://example.test/scope/1",
				"metrics":[
					{"name":"requests","description":"request count","unit":"{request}","metadata":[{"key":"category","value":{"stringValue":"http"}}],"sum":{"dataPoints":[{"startTimeUnixNano":"100","timeUnixNano":"200","attributes":[{"key":"route","value":{"stringValue":"/items"}}],"asInt":"7","exemplars":[{"filteredAttributes":[{"key":"sampled","value":{"boolValue":true}}],"timeUnixNano":"150","asDouble":1.5,"spanId":"AQIDBAUGBwg=","traceId":"AQIDBAUGBwgJCgsMDQ4PEA=="}],"flags":1}],"aggregationTemporality":"AGGREGATION_TEMPORALITY_DELTA","isMonotonic":true}},
					{"name":"active","unit":"1","gauge":{"dataPoints":[{"startTimeUnixNano":"100","timeUnixNano":"200","asDouble":2.5}]}},
					{"name":"latency","unit":"ms","histogram":{"dataPoints":[{"startTimeUnixNano":"100","timeUnixNano":"200","count":"2","sum":3.5,"bucketCounts":["1","1"],"explicitBounds":[1],"min":0.5,"max":2}],"aggregationTemporality":"AGGREGATION_TEMPORALITY_CUMULATIVE"}},
					{"name":"latency.exp","unit":"ms","exponentialHistogram":{"dataPoints":[{"startTimeUnixNano":"100","timeUnixNano":"200","count":"2","sum":3.5,"scale":1,"zeroCount":"0","positive":{"offset":0,"bucketCounts":["2"]},"negative":{"offset":0,"bucketCounts":["0"]},"zeroThreshold":0.01}],"aggregationTemporality":"AGGREGATION_TEMPORALITY_DELTA"}},
					{"name":"quantiles","unit":"1","summary":{"dataPoints":[{"startTimeUnixNano":"100","timeUnixNano":"200","count":"2","sum":3.5,"quantileValues":[{"quantile":0.5,"value":1.5}]}]}}
				]
			}]
		}]
	}`

	request := &metricsv1.ExportMetricsServiceRequest{}
	assertOTLPWireRoundTrip(
		t,
		body,
		request,
		func(encoder Encoder, payload []byte) (any, error) { return encoder.UnmarshalMetrics(payload) },
		func(data any, json bool) ([]byte, error) {
			req := pmetricotlp.NewRequestFromMetrics(data.(pmetric.Metrics))
			if json {
				return req.MarshalJSON()
			}
			return req.MarshalProto()
		},
		func(message proto.Message) {
			request := message.(*metricsv1.ExportMetricsServiceRequest)
			for _, resourceMetrics := range request.ResourceMetrics {
				for _, scopeMetrics := range resourceMetrics.ScopeMetrics {
					for _, metric := range scopeMetrics.Metrics {
						metric.Metadata = nil
						if histogram := metric.GetExponentialHistogram(); histogram != nil {
							for _, point := range histogram.DataPoints {
								// pdata v0.61 does not model zero_threshold, so it is dropped on both encoders.
								point.ZeroThreshold = 0
							}
						}
					}
				}
			}
		},
	)
}

func TestOTLPLogFieldsRoundTrip(t *testing.T) {
	const body = `{
		"resourceLogs": [{
			"resource":{"attributes":[{"key":"service.name","value":{"stringValue":"collector"}}],"droppedAttributesCount":1},
			"schemaUrl":"https://example.test/resource/1",
			"scopeLogs":[{
				"scope":{"name":"instrumentation","version":"1.2.3"},"schemaUrl":"https://example.test/scope/1",
				"logRecords":[{
					"timeUnixNano":"100","observedTimeUnixNano":"110","severityNumber":"SEVERITY_NUMBER_WARN","severityText":"WARN",
					"body":{"kvlistValue":{"values":[{"key":"message","value":{"stringValue":"request failed"}}]}},
					"attributes":[{"key":"http.status_code","value":{"intValue":"500"}}],"droppedAttributesCount":2,
					"flags":1,"traceId":"AQIDBAUGBwgJCgsMDQ4PEA==","spanId":"AQIDBAUGBwg=","eventName":"http.request.failed"
				}]
			}]
		}]
	}`

	request := &logsv1.ExportLogsServiceRequest{}
	assertOTLPWireRoundTrip(
		t,
		body,
		request,
		func(encoder Encoder, payload []byte) (any, error) { return encoder.UnmarshalLogs(payload) },
		func(data any, json bool) ([]byte, error) {
			req := plogotlp.NewRequestFromLogs(data.(plog.Logs))
			if json {
				return req.MarshalJSON()
			}
			return req.MarshalProto()
		},
		func(message proto.Message) {
			request := message.(*logsv1.ExportLogsServiceRequest)
			for _, resourceLogs := range request.ResourceLogs {
				for _, scopeLogs := range resourceLogs.ScopeLogs {
					for _, logRecord := range scopeLogs.LogRecords {
						logRecord.EventName = ""
					}
				}
			}
		},
	)
}
