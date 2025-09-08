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
	"go.uber.org/atomic"

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
	assert.NotPanics(t, func() {
		Ready()
	})
}

func newSvc(code define.StatusCode, msg string, err error) (HttpService, *atomic.Int64) {
	n := atomic.NewInt64(0)
	svc := HttpService{
		receiver.Publisher{Func: func(record *define.Record) { n.Inc() }},
		pipeline.Validator{Func: func(record *define.Record) (define.StatusCode, string, error) {
			return code, msg, err
		}},
	}
	return svc, n
}

func TestHttpRequest(t *testing.T) {
	t.Run("traces pb/content", func(t *testing.T) {
		g := generator.NewTracesGenerator(define.TracesOptions{
			SpanCount: 10,
			SpanKind:  1,
		})
		b, _ := ptrace.NewProtoMarshaler().MarshalTraces(g.Generate())
		buf := bytes.NewBuffer(b)

		req := httptest.NewRequest(http.MethodPut, localV1TracesURL, buf)
		req.Header.Set("Content-Type", define.ContentTypeProtobuf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.ExportTraces(rw, req)
		assert.Equal(t, rw.Code, http.StatusOK)
		assert.Equal(t, int64(1), n.Load())
	})

	t.Run("invalid body", func(t *testing.T) {
		buf := bytes.NewBufferString("{-}")
		req := httptest.NewRequest(http.MethodPut, localV1TracesURL, buf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.ExportTraces(rw, req)
		assert.Equal(t, rw.Code, http.StatusBadRequest)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("read failed", func(t *testing.T) {
		buf := testkits.NewBrokenReader()
		req := httptest.NewRequest(http.MethodPut, localV1TracesURL, buf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.ExportTraces(rw, req)
		assert.Equal(t, rw.Code, http.StatusInternalServerError)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("traces precheck failed", func(t *testing.T) {
		g := generator.NewTracesGenerator(define.TracesOptions{
			SpanCount: 10,
			SpanKind:  1,
		})
		b, _ := ptrace.NewJSONMarshaler().MarshalTraces(g.Generate())
		buf := bytes.NewBuffer(b)
		req := httptest.NewRequest(http.MethodPut, localV1TracesURL, buf)

		svc, n := newSvc(define.StatusCodeUnauthorized, "", errors.New("MUST ERROR"))
		rw := httptest.NewRecorder()
		svc.ExportTraces(rw, req)
		assert.Equal(t, rw.Code, http.StatusUnauthorized)
		assert.Equal(t, int64(0), n.Load())
	})

	t.Run("traces success", func(t *testing.T) {
		g := generator.NewTracesGenerator(define.TracesOptions{
			SpanCount: 10,
			SpanKind:  1,
		})
		b, _ := ptrace.NewJSONMarshaler().MarshalTraces(g.Generate())
		buf := bytes.NewBuffer(b)
		req := httptest.NewRequest(http.MethodPut, localV1TracesURL, buf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.ExportTraces(rw, req)
		assert.Equal(t, rw.Code, http.StatusOK)
		assert.Equal(t, int64(1), n.Load())
	})

	t.Run("metrics success", func(t *testing.T) {
		g := generator.NewMetricsGenerator(define.MetricsOptions{
			GaugeCount: 10,
		})
		b, _ := pmetric.NewJSONMarshaler().MarshalMetrics(g.Generate())
		buf := bytes.NewBuffer(b)
		req := httptest.NewRequest(http.MethodPut, localV1MetricsURL, buf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.ExportMetrics(rw, req)
		assert.Equal(t, rw.Code, http.StatusOK)
		assert.Equal(t, int64(1), n.Load())
	})

	t.Run("logs success", func(t *testing.T) {
		g := generator.NewLogsGenerator(define.LogsOptions{
			LogCount:  10,
			LogLength: 10,
		})
		b, _ := plog.NewJSONMarshaler().MarshalLogs(g.Generate())
		buf := bytes.NewBuffer(b)
		req := httptest.NewRequest(http.MethodPut, localV1LogsURL, buf)

		svc, n := newSvc(define.StatusCodeOK, "", nil)
		rw := httptest.NewRecorder()
		svc.ExportLogs(rw, req)
		assert.Equal(t, rw.Code, http.StatusOK)
		assert.Equal(t, int64(1), n.Load())
	})
}
