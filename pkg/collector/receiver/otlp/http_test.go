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
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/generator"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/testkits"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/pipeline"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

const (
	localV1TracesURL  = "http://localhost/v1/traces"
	localV1MetricsURL = "http://localhost/v1/metrics"
	localV1LogsURL    = "http://localhost/v1/logs"
)

func TestReady(t *testing.T) {
	assert.NotPanics(t, Ready)
}

func TestHttpTracesPbContent(t *testing.T) {
	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
		SpanKind:  1,
	})
	b, err := ptrace.NewProtoMarshaler().MarshalTraces(g.Generate())
	assert.NoError(t, err)

	buf := &bytes.Buffer{}
	buf.Write(b)

	req := httptest.NewRequest(http.MethodPut, localV1TracesURL, buf)
	req.Header.Set("Content-Type", define.ContentTypeProtobuf)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	svc.ExportTraces(rw, req)
	assert.Equal(t, rw.Code, http.StatusOK)
	assert.Equal(t, 1, n)
}

func TestHttpInvalidBody(t *testing.T) {
	buf := &bytes.Buffer{}
	buf.WriteString("{-}")
	req := httptest.NewRequest(http.MethodPut, localV1TracesURL, buf)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	svc.ExportTraces(rw, req)
	assert.Equal(t, rw.Code, http.StatusBadRequest)
	assert.Equal(t, 0, n)
}

func TestHttpReadFailed(t *testing.T) {
	buf := testkits.NewBrokenReader()
	req := httptest.NewRequest(http.MethodPut, localV1TracesURL, buf)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	svc.ExportTraces(rw, req)
	assert.Equal(t, rw.Code, http.StatusInternalServerError)
	assert.Equal(t, 0, n)
}

func TestHttpTracesPreCheckFailed(t *testing.T) {
	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
		SpanKind:  1,
	})
	b, err := ptrace.NewJSONMarshaler().MarshalTraces(g.Generate())
	assert.NoError(t, err)

	buf := &bytes.Buffer{}
	buf.Write(b)
	req := httptest.NewRequest(http.MethodPut, localV1TracesURL, buf)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeUnauthorized, "", errors.New("MUST ERROR")
		}},
	}
	rw := httptest.NewRecorder()
	svc.ExportTraces(rw, req)
	assert.Equal(t, rw.Code, http.StatusUnauthorized)
	assert.Equal(t, 0, n)
}

func TestHttpTracesTokenAfterPreCheck(t *testing.T) {
	g := generator.NewTracesGenerator(define.TracesOptions{
		SpanCount: 10,
		SpanKind:  1,
	})
	b, err := ptrace.NewJSONMarshaler().MarshalTraces(g.Generate())
	assert.NoError(t, err)

	buf := &bytes.Buffer{}
	buf.Write(b)
	req := httptest.NewRequest(http.MethodPut, localV1TracesURL, buf)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	svc.ExportTraces(rw, req)
	assert.Equal(t, rw.Code, http.StatusOK)
	assert.Equal(t, 1, n)
}

func TestHttpMetricsTokenAfterPreCheck(t *testing.T) {
	g := generator.NewMetricsGenerator(define.MetricsOptions{
		GaugeCount: 10,
	})
	b, err := pmetric.NewJSONMarshaler().MarshalMetrics(g.Generate())
	assert.NoError(t, err)

	buf := &bytes.Buffer{}
	buf.Write(b)
	req := httptest.NewRequest(http.MethodPut, localV1MetricsURL, buf)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	svc.ExportMetrics(rw, req)
	assert.Equal(t, rw.Code, http.StatusOK)
	assert.Equal(t, 1, n)
}

func TestHttpLogsTokenAfterPreCheck(t *testing.T) {
	g := generator.NewLogsGenerator(define.LogsOptions{
		LogCount:  10,
		LogLength: 10,
	})
	b, err := plog.NewJSONMarshaler().MarshalLogs(g.Generate())
	assert.NoError(t, err)

	buf := &bytes.Buffer{}
	buf.Write(b)
	req := httptest.NewRequest(http.MethodPut, localV1LogsURL, buf)

	var n int
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n++ }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return define.StatusCodeOK, "", nil
		}},
	}
	rw := httptest.NewRecorder()
	svc.ExportLogs(rw, req)
	assert.Equal(t, rw.Code, http.StatusOK)
	assert.Equal(t, 1, n)
}
