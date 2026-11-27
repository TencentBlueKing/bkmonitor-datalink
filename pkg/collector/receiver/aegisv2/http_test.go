// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegisv2

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

const collectWebVitalsPayload = `{
	"topic": "SDK-test", "scheme": "v2",
	"bean": {"version": "1.0.0", "referer": "https://example.com/ref"},
	"d2": [{
		"fields": "{\"type\":\"web_vitals\",\"session\":{\"id\":\"session-1\"}}",
		"message": ["{\"FCP\":0,\"LCP\":452,\"FID\":-1,\"CLS\":0.1,\"timestamp\":1782214911075}"]
	}]
}`

func TestExportCollect_WebVitalsPublishesOnlyTraces(t *testing.T) {
	var published []*define.Record
	var validated []define.RecordType
	svc := httpService{
		Publisher: receiver.Publisher{Func: func(r *define.Record) { published = append(published, r) }},
		Validator: pipeline.Validator{Func: func(r *define.Record) (define.StatusCode, string, error) {
			validated = append(validated, r.RecordType)
			return define.StatusCodeOK, "", nil
		}},
	}
	req := httptest.NewRequest(http.MethodPost, routeV2Collect, bytes.NewBufferString(collectWebVitalsPayload))
	req.Header.Set(define.ContentType, define.ContentTypeJson)
	resp := httptest.NewRecorder()
	svc.ExportCollect(resp, req)
	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, []define.RecordType{define.RecordRumTraces}, validated)
	require.Len(t, published, 1)
	require.Equal(t, define.RecordRumTraces, published[0].RecordType)
	traces := published[0].Data.(ptrace.Traces)
	require.Equal(t, 3, traces.SpanCount()) // FCP=0, LCP=452, CLS=0.1; FID is unsupported.
	spans := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans()
	for i := 0; i < spans.Len(); i++ {
		assert.Equal(t, "browser.web_vital", spans.At(i).Name())
	}
}
func TestExportCollect_Token(t *testing.T) {
	for _, tt := range []struct{ name, token, topicKey, want string }{
		{"X-BK-TOKEN", "token-1", "", "token-1"},
		{"X-Topic-Key fallback", "", "topic-1", "topic-1"},
		{"X-BK-TOKEN wins over X-Topic-Key", "token-1", "topic-1", "token-1"},
		{"no token", "", "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var published []*define.Record
			svc := httpService{
				Publisher: receiver.Publisher{Func: func(r *define.Record) { published = append(published, r) }},
				Validator: pipeline.Validator{Func: func(*define.Record) (define.StatusCode, string, error) {
					return define.StatusCodeOK, "", nil
				}},
			}
			req := httptest.NewRequest(http.MethodPost, routeV2Collect, bytes.NewBufferString(collectWebVitalsPayload))
			req.Header.Set(define.ContentType, define.ContentTypeJson)
			if tt.token != "" {
				req.Header.Set(define.KeyToken, tt.token)
			}
			if tt.topicKey != "" {
				req.Header.Set(headerTopicKey, tt.topicKey)
			}
			resp := httptest.NewRecorder()
			svc.ExportCollect(resp, req)

			require.Equal(t, http.StatusOK, resp.Code)
			require.Len(t, published, 1)
			assert.Equal(t, tt.want, published[0].Token.Original)
		})
	}
}

func TestExportCollect_Protocols(t *testing.T) {
	traces := ptrace.NewTraces()
	span := traces.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetName("otlp-span")
	span.SetTraceID(pcommon.TraceID([16]byte{1, 2, 3}))
	request := ptraceotlp.NewRequestFromTraces(traces)
	jsonBody, err := request.MarshalJSON()
	require.NoError(t, err)
	protoBody, err := request.MarshalProto()
	require.NoError(t, err)
	// The first protobuf field tag is 0x0a, which must not be trimmed as a newline.
	require.Equal(t, byte('\n'), protoBody[0])

	for _, tt := range []struct {
		name        string
		contentType string
		body        []byte
		spanName    string
	}{
		{"aegis", define.ContentTypeJson, sampleAegisPayload, "browser.resource"},
		{"aegis page performance", define.ContentTypeJson, loadTestPayload("testdata/page_performance_initial_load.json"), "browser.view"},
		{"aegis with prefix", "", append([]byte(" ,\n"), sampleAegisPayload...), "browser.resource"},
		{"otlp json", define.ContentTypeJson, jsonBody, "otlp-span"},
		{"otlp json with prefix", "application/json; charset=utf-8", append([]byte(" ,\n"), jsonBody...), "otlp-span"},
		{"otlp protobuf", define.ContentTypeProtobuf, protoBody, "otlp-span"},
		{"otlp protobuf with parameters", define.ContentTypeProtobuf + "; charset=binary", protoBody, "otlp-span"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var published []*define.Record
			svc := httpService{
				Publisher: receiver.Publisher{Func: func(r *define.Record) { published = append(published, r) }},
				Validator: pipeline.Validator{Func: func(*define.Record) (define.StatusCode, string, error) {
					return define.StatusCodeOK, "", nil
				}},
			}
			req := httptest.NewRequest(http.MethodPost, routeV2Collect, bytes.NewReader(tt.body))
			req.Header.Set(define.ContentType, tt.contentType)
			resp := httptest.NewRecorder()
			svc.ExportCollect(resp, req)

			require.Equal(t, http.StatusOK, resp.Code)
			assert.Equal(t, define.ContentTypeJson, resp.Header().Get(define.ContentType))
			assert.JSONEq(t, `{}`, resp.Body.String())
			require.Len(t, published, 1)
			require.Equal(t, define.RecordRumTraces, published[0].RecordType)
			got := published[0].Data.(ptrace.Traces)
			require.Equal(t, 1, got.SpanCount())
			assert.Equal(t, tt.spanName, got.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Name())
			if tt.spanName == "otlp-span" {
				assert.Equal(t, span.TraceID(), got.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).TraceID())
			}
			if tt.spanName == "browser.view" {
				attrs := got.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0).Attributes().AsRaw()
				assert.Equal(t, "update", attrs["view.phase"])
				assert.Equal(t, "e6ec88756f2be63c", attrs["view.id"])
				assert.NotContains(t, attrs, "vital.metric")
			}
		})
	}
}

func TestExportCollect_RejectsInvalidPayload(t *testing.T) {
	for _, body := range []string{
		`{"topic":`,
		`{"topic":"SDK-test","bean":42}`,
		`{"topic":"SDK-test","d2":{}}`,
		`{"topic":"SDK-test","d2":[{"fields":"invalid","message":[]}]}`,
		`{"topic":"SDK-test","d2":[{"fields":"{}","message":["invalid"]}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			svc := httpService{
				Publisher: receiver.Publisher{Func: func(*define.Record) { t.Fatal("invalid payload published") }},
				Validator: pipeline.Validator{Func: func(*define.Record) (define.StatusCode, string, error) {
					t.Fatal("invalid payload reached validation")
					return define.StatusCodeOK, "", nil
				}},
			}
			req := httptest.NewRequest(http.MethodPost, routeV2Collect, bytes.NewBufferString(body))
			resp := httptest.NewRecorder()
			svc.ExportCollect(resp, req)
			assert.Equal(t, http.StatusBadRequest, resp.Code)
		})
	}
}

func TestExportCollect_RejectsTracesBeforePublish(t *testing.T) {
	var validated []define.RecordType
	svc := httpService{
		Publisher: receiver.Publisher{Func: func(*define.Record) { t.Fatal("rejected request published") }},
		Validator: pipeline.Validator{Func: func(r *define.Record) (define.StatusCode, string, error) {
			validated = append(validated, r.RecordType)
			return define.StatusCodeTooManyRequests, define.ProcessorRateLimiter, errors.New("rate limited")
		}},
	}
	req := httptest.NewRequest(http.MethodPost, routeV2Collect, bytes.NewBufferString(collectWebVitalsPayload))
	resp := httptest.NewRecorder()
	svc.ExportCollect(resp, req)

	assert.Equal(t, http.StatusTooManyRequests, resp.Code)
	assert.Equal(t, []define.RecordType{define.RecordRumTraces}, validated)
}
