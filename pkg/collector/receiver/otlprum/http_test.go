// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package otlprum

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	spb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

const rumURL = "http://localhost/v1/rum"

func TestReady(t *testing.T) {
	Ready()
	assert.Contains(t, receiver.RecvHttpRoutes(), define.RouteInfo{
		Source:     define.SourceRum,
		HttpMethod: http.MethodPost,
		Path:       "/v1/rum",
	})

	var match mux.RouteMatch
	require.True(t, receiver.RecvHttpRouter().Match(httptest.NewRequest(http.MethodPost, rumURL, nil), &match))
	assert.NotNil(t, match.Handler)
	assert.False(t, receiver.RecvHttpRouter().Match(httptest.NewRequest(http.MethodGet, rumURL, nil), &mux.RouteMatch{}))
}

func TestExportRum(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		tokenSource string
		forwardedIP string
	}{
		{"json", define.ContentTypeJson, "query", "198.51.100.1"},
		{"protobuf", define.ContentTypeProtobuf, "header", ""},
		{"default json", "", "bearer", ""},
		{"json charset", "application/json; charset=utf-8", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			traces := newTestTraces()
			buf := marshalRequest(t, ptraceotlp.NewRequestFromTraces(traces), tc.contentType)
			req := httptest.NewRequest(http.MethodPost, rumURL, bytes.NewReader(buf))
			req.RemoteAddr = "192.0.2.1:1234"
			req.Header.Set(define.ContentType, tc.contentType)
			req.Header.Set(define.KeyUserMetadata, "env=prod, region=shanghai")
			req.Header.Set("X-Forwarded-For", tc.forwardedIP)
			wantToken := "rum-token"
			switch tc.tokenSource {
			case "query":
				query := req.URL.Query()
				query.Set(define.KeyToken, wantToken)
				req.URL.RawQuery = query.Encode()
				req.Header.Set(define.KeyToken, "lower-priority-token")
			case "header":
				req.Header.Set(define.KeyToken, wantToken)
			case "bearer":
				req.Header.Set("Authorization", "Bearer "+wantToken)
			default:
				wantToken = ""
			}

			var validated []*define.Record
			var published []*define.Record
			svc := HttpService{
				Publisher: receiver.Publisher{Func: func(r *define.Record) {
					assert.Len(t, validated, 1, "validate before publishing")
					published = append(published, r)
				}},
				Validator: pipeline.Validator{Func: func(r *define.Record) (define.StatusCode, string, error) {
					validated = append(validated, r)
					r.Token.TracesDataId = 1001
					return define.StatusCodeOK, "", nil
				}},
			}
			rw := httptest.NewRecorder()
			svc.ExportRum(rw, req)

			require.Equal(t, http.StatusOK, rw.Code)
			require.Len(t, validated, 1)
			require.Len(t, published, 1)
			r := published[0]
			assert.Same(t, validated[0], r)
			assert.Equal(t, define.RecordRumTraces, r.RecordType)
			assert.Equal(t, define.RequestHttp, r.RequestType)
			assert.Equal(t, define.Token{Original: wantToken, TracesDataId: 1001}, r.Token)
			assert.Equal(t, map[string]string{"env": "prod", "region": "shanghai"}, r.Metadata)
			assert.Equal(t, traces, r.Data)
			wantIP := "192.0.2.1"
			if tc.forwardedIP != "" {
				wantIP = tc.forwardedIP
			}
			assert.Equal(t, wantIP, r.RequestClient.IP)

			response := ptraceotlp.NewResponse()
			if tc.contentType == define.ContentTypeProtobuf {
				assert.Equal(t, define.ContentTypeProtobuf, rw.Header().Get(define.ContentType))
				require.NoError(t, response.UnmarshalProto(rw.Body.Bytes()))
			} else {
				assert.Equal(t, define.ContentTypeJson, rw.Header().Get(define.ContentType))
				require.NoError(t, response.UnmarshalJSON(rw.Body.Bytes()))
			}
			assert.Equal(t, ptraceotlp.NewResponse(), response)
		})
	}
}

func TestExportRumErrors(t *testing.T) {
	for _, contentType := range []string{define.ContentTypeJson, define.ContentTypeProtobuf} {
		t.Run(contentType, func(t *testing.T) {
			for _, tc := range []struct {
				name          string
				httpCode      int
				grpcCode      codes.Code
				validationErr error
				processor     string
			}{
				{name: "invalid body", httpCode: http.StatusBadRequest, grpcCode: codes.InvalidArgument},
				{name: "read failed", httpCode: http.StatusInternalServerError},
				{
					name: "unauthorized", httpCode: http.StatusUnauthorized, grpcCode: codes.Unknown,
					validationErr: errors.New("invalid token"), processor: define.ProcessorTokenChecker,
				},
				{
					name: "rate limited", httpCode: http.StatusTooManyRequests, grpcCode: codes.ResourceExhausted,
					validationErr: status.Error(codes.ResourceExhausted, "rate limit exceeded"), processor: define.ProcessorRateLimiter,
				},
				{
					name: "bad request", httpCode: http.StatusBadRequest, grpcCode: codes.InvalidArgument,
					validationErr: errors.New("invalid record"), processor: define.ProcessorProxyValidator,
				},
			} {
				t.Run(tc.name, func(t *testing.T) {
					buf := marshalRequest(t, ptraceotlp.NewRequestFromTraces(newTestTraces()), contentType)
					var body io.Reader = bytes.NewReader(buf)
					switch tc.name {
					case "invalid body":
						body = bytes.NewReader([]byte{0xff})
					case "read failed":
						body = testkits.NewBrokenReader()
					}
					req := httptest.NewRequest(http.MethodPost, rumURL, body)
					req.Header.Set(define.ContentType, contentType)
					validated, published := 0, 0
					svc := HttpService{
						Publisher: receiver.Publisher{Func: func(*define.Record) { published++ }},
						Validator: pipeline.Validator{Func: func(*define.Record) (define.StatusCode, string, error) {
							validated++
							return define.StatusCode(tc.httpCode), tc.processor, tc.validationErr
						}},
					}
					rw := httptest.NewRecorder()
					svc.ExportRum(rw, req)

					assert.Equal(t, tc.httpCode, rw.Code)
					assert.Zero(t, published)
					if tc.validationErr != nil {
						assert.Equal(t, 1, validated)
					} else {
						assert.Zero(t, validated)
					}
					if tc.name == "read failed" {
						assert.Equal(t, define.ContentTypeJson, rw.Header().Get(define.ContentType))
						assert.Empty(t, rw.Body.Bytes())
						return
					}
					assert.Equal(t, contentType, rw.Header().Get(define.ContentType))
					var got spb.Status
					if contentType == define.ContentTypeProtobuf {
						require.NoError(t, proto.Unmarshal(rw.Body.Bytes(), &got))
					} else {
						require.NoError(t, protojson.Unmarshal(rw.Body.Bytes(), &got))
					}
					assert.Equal(t, int32(tc.grpcCode), got.Code)
					assert.NotEmpty(t, got.Message)
					if tc.validationErr != nil {
						wantMessage := tc.validationErr.Error()
						if s, ok := status.FromError(tc.validationErr); ok {
							wantMessage = s.Message()
						}
						assert.Equal(t, wantMessage, got.Message)
					}
				})
			}
		})
	}
}

type otlpResponse interface {
	UnmarshalJSON([]byte) error
	UnmarshalProto([]byte) error
}

func TestHttpResponseHandlers(t *testing.T) {
	for _, handler := range []receiver.ResponseHandler{HttpJsonResponseHandler(), HttpPbResponseHandler()} {
		t.Run(handler.ContentType(), func(t *testing.T) {
			for _, tc := range []struct {
				rtype    define.RecordType
				response otlpResponse
			}{
				{define.RecordRumTraces, ptraceotlp.NewResponse()},
				{define.RecordMetrics, pmetricotlp.NewResponse()},
				{define.RecordLogs, plogotlp.NewResponse()},
			} {
				t.Run(tc.rtype.S(), func(t *testing.T) {
					buf, err := handler.Response(tc.rtype)
					require.NoError(t, err)
					if handler.ContentType() == define.ContentTypeProtobuf {
						assert.NoError(t, tc.response.UnmarshalProto(buf))
					} else {
						assert.NoError(t, tc.response.UnmarshalJSON(buf))
					}
				})
			}

			buf, err := handler.Response(define.RecordUndefined)
			assert.ErrorIs(t, err, define.ErrUnknownRecordType)
			assert.Nil(t, buf)
			data, err := handler.Unmarshal(define.RecordUndefined, nil)
			assert.ErrorIs(t, err, define.ErrUnknownRecordType)
			assert.Nil(t, data)
			buf, err = handler.ErrorStatus("invalid status type")
			assert.NoError(t, err)
			assert.Empty(t, buf)
		})
	}
}

type failingErrorResponseHandler struct {
	receiver.ResponseHandler
}

func (failingErrorResponseHandler) ErrorStatus(any) ([]byte, error) {
	return nil, errors.New("marshal failed")
}

func TestWriteErrorMarshalFailure(t *testing.T) {
	rw := httptest.NewRecorder()
	handler := failingErrorResponseHandler{ResponseHandler: HttpJsonResponseHandler()}
	writeError(rw, handler, errors.New("invalid request"), http.StatusBadRequest)
	assert.Equal(t, http.StatusInternalServerError, rw.Code)
	assert.Equal(t, define.ContentTypeJson, rw.Header().Get(define.ContentType))
	assert.JSONEq(t, `{"code":13,"message":"failed to marshal error message"}`, rw.Body.String())
}
