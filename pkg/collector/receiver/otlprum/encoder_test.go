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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

func newTestTraces() ptrace.Traces {
	traces := ptrace.NewTraces()
	resource := traces.ResourceSpans().AppendEmpty()
	resource.Resource().Attributes().PutString("service.name", "rum-browser")
	scope := resource.ScopeSpans().AppendEmpty()
	scope.Scope().SetName("rum-sdk")
	span := scope.Spans().AppendEmpty()
	span.SetName("page.load")
	span.SetTraceID(pcommon.TraceID{1, 2, 3})
	span.SetSpanID(pcommon.SpanID{4, 5, 6})
	span.SetKind(ptrace.SpanKindClient)
	span.SetStartTimestamp(1000000)
	span.SetEndTimestamp(2000000)
	span.Attributes().PutString("url.path", "/home")
	return traces
}

type otlpRequest interface {
	MarshalJSON() ([]byte, error)
	MarshalProto() ([]byte, error)
}

func marshalRequest(t *testing.T, req otlpRequest, contentType string) []byte {
	t.Helper()
	var buf []byte
	var err error
	if contentType == define.ContentTypeProtobuf {
		buf, err = req.MarshalProto()
	} else {
		buf, err = req.MarshalJSON()
	}
	require.NoError(t, err)
	return buf
}

func TestEncoders(t *testing.T) {
	traces := newTestTraces()
	metrics := pmetric.NewMetrics()
	metric := metrics.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	metric.SetName("page.load.duration")
	metric.SetEmptyGauge()
	metric.Gauge().DataPoints().AppendEmpty().SetDoubleVal(123.5)
	logs := plog.NewLogs()
	logs.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty().Body().SetStringVal("page loaded")

	for _, format := range []struct {
		name        string
		contentType string
		encoder     Encoder
	}{
		{"json", define.ContentTypeJson, JsonEncoder()},
		{"pb", define.ContentTypeProtobuf, PbEncoder()},
	} {
		t.Run(format.name, func(t *testing.T) {
			assert.Equal(t, format.name, format.encoder.Type())
			for _, tc := range []struct {
				rtype define.RecordType
				data  any
				req   otlpRequest
			}{
				{define.RecordRumTraces, traces, ptraceotlp.NewRequestFromTraces(traces)},
				{define.RecordMetrics, metrics, pmetricotlp.NewRequestFromMetrics(metrics)},
				{define.RecordLogs, logs, plogotlp.NewRequestFromLogs(logs)},
			} {
				t.Run(tc.rtype.S(), func(t *testing.T) {
					buf := marshalRequest(t, tc.req, format.contentType)
					data, err := unmarshalRecordData(format.encoder, tc.rtype, buf)
					require.NoError(t, err)
					assert.Equal(t, tc.data, data)

					_, err = unmarshalRecordData(format.encoder, tc.rtype, []byte{0xff})
					assert.Error(t, err)
				})
			}

			for _, rtype := range []define.RecordType{define.RecordUndefined, define.RecordTraces} {
				t.Run("unsupported/"+rtype.S(), func(t *testing.T) {
					data, err := unmarshalRecordData(format.encoder, rtype, nil)
					assert.ErrorIs(t, err, define.ErrUnknownRecordType)
					assert.Nil(t, data)
				})
			}
		})
	}
}
