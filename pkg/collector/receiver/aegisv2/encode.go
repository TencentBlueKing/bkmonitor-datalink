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

	"github.com/gogo/protobuf/jsonpb"
	"github.com/gogo/protobuf/proto"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	spb "google.golang.org/genproto/googleapis/rpc/status"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

// Encoder 负责解析 Traces/Metrics/Logs 数据至 OT 标准数据模型
type Encoder interface {
	Type() string
	UnmarshalTraces(b []byte) (ptrace.Traces, error)
	UnmarshalMetrics(b []byte) (pmetric.Metrics, error)
	UnmarshalLogs(b []byte) (plog.Logs, error)
}

func unmarshalRecordData(encoder Encoder, rtype define.RecordType, b []byte) (any, error) {
	switch rtype {
	case define.RecordTraces:
		return encoder.UnmarshalTraces(b)
	case define.RecordMetrics:
		return encoder.UnmarshalMetrics(b)
	case define.RecordLogs:
		return encoder.UnmarshalLogs(b)
	}
	return nil, define.ErrUnknownRecordType
}

// JsonEncoder Json 编码器实现
func JsonEncoder() Encoder {
	return jsonEncoder{}
}

func JsonEncoderWithTraceID(traceID pcommon.TraceID) Encoder {
	return jsonEncoder{requestTraceID: traceID}
}

type jsonEncoder struct {
	requestTraceID pcommon.TraceID
}

func (e jsonEncoder) Type() string {
	return "json"
}

func (e jsonEncoder) UnmarshalTraces(buf []byte) (ptrace.Traces, error) {
	traces, err := decodeTracesWithTraceID(buf, e.requestTraceID)
	if !errors.Is(err, ErrNotAegisV2) {
		return traces, err
	}

	req := ptraceotlp.NewRequest()
	if err := req.UnmarshalJSON(buf); err != nil {
		return ptrace.Traces{}, err
	}
	return req.Traces(), nil
}

func (e jsonEncoder) UnmarshalMetrics(buf []byte) (pmetric.Metrics, error) {
	metrics, err := decodeMetrics(buf)
	if !errors.Is(err, ErrNotAegisV2) {
		return metrics, err
	}

	req := pmetricotlp.NewRequest()
	if err := req.UnmarshalJSON(buf); err != nil {
		return pmetric.Metrics{}, err
	}
	return req.Metrics(), nil
}

func (e jsonEncoder) UnmarshalLogs(buf []byte) (plog.Logs, error) {
	logs, err := decodeLogs(buf)
	if !errors.Is(err, ErrNotAegisV2) {
		return logs, err
	}

	req := plogotlp.NewRequest()
	if err := req.UnmarshalJSON(buf); err != nil {
		return plog.Logs{}, err
	}
	return req.Logs(), nil
}

// PbEncoder Pb 编码器实现
func PbEncoder() Encoder {
	return pbEncoder{}
}

type pbEncoder struct{}

func (pbEncoder) Type() string {
	return "pb"
}

func (pbEncoder) UnmarshalTraces(buf []byte) (ptrace.Traces, error) {
	req := ptraceotlp.NewRequest()
	if err := req.UnmarshalProto(buf); err != nil {
		return ptrace.Traces{}, err
	}
	return req.Traces(), nil
}

func (pbEncoder) UnmarshalMetrics(buf []byte) (pmetric.Metrics, error) {
	req := pmetricotlp.NewRequest()
	if err := req.UnmarshalProto(buf); err != nil {
		return pmetric.Metrics{}, err
	}
	return req.Metrics(), nil
}

func (pbEncoder) UnmarshalLogs(buf []byte) (plog.Logs, error) {
	req := plogotlp.NewRequest()
	if err := req.UnmarshalProto(buf); err != nil {
		return plog.Logs{}, err
	}
	return req.Logs(), nil
}

func newResponseHandler(contentType string, traceID pcommon.TraceID) receiver.ResponseHandler {
	switch contentType {
	case define.ContentTypeProtobuf:
		return httpPbResponseHandler{encoder: PbEncoder()}
	}
	return httpJsonResponseHandler{
		marshaler: &jsonpb.Marshaler{},
		encoder:   JsonEncoderWithTraceID(traceID),
	}
}

type httpPbResponseHandler struct {
	encoder Encoder
}

func (h httpPbResponseHandler) ContentType() string {
	return define.ContentTypeProtobuf
}

func (h httpPbResponseHandler) Response(rtype define.RecordType) ([]byte, error) {
	switch rtype {
	case define.RecordTraces:
		return ptraceotlp.NewResponse().MarshalProto()
	case define.RecordMetrics:
		return pmetricotlp.NewResponse().MarshalProto()
	case define.RecordLogs:
		return plogotlp.NewResponse().MarshalProto()
	}
	return nil, define.ErrUnknownRecordType
}

func (h httpPbResponseHandler) Unmarshal(rtype define.RecordType, b []byte) (any, error) {
	return unmarshalRecordData(h.encoder, rtype, b)
}

func (h httpPbResponseHandler) ErrorStatus(status any) ([]byte, error) {
	buf := new(bytes.Buffer)
	s, ok := status.(*spb.Status)
	if !ok {
		return buf.Bytes(), nil
	}
	return proto.Marshal(s)
}

type httpJsonResponseHandler struct {
	marshaler *jsonpb.Marshaler
	encoder   Encoder
}

func (h httpJsonResponseHandler) ContentType() string {
	return define.ContentTypeJson
}

func (h httpJsonResponseHandler) Response(rtype define.RecordType) ([]byte, error) {
	switch rtype {
	case define.RecordTraces:
		return ptraceotlp.NewResponse().MarshalJSON()
	case define.RecordMetrics:
		return pmetricotlp.NewResponse().MarshalJSON()
	case define.RecordLogs:
		return plogotlp.NewResponse().MarshalJSON()
	}
	return nil, define.ErrUnknownRecordType
}

func (h httpJsonResponseHandler) Unmarshal(rtype define.RecordType, b []byte) (any, error) {
	return unmarshalRecordData(h.encoder, rtype, b)
}

func (h httpJsonResponseHandler) ErrorStatus(status any) ([]byte, error) {
	buf := new(bytes.Buffer)
	s, ok := status.(*spb.Status)
	if !ok {
		return buf.Bytes(), nil
	}
	err := h.marshaler.Marshal(buf, s)
	return buf.Bytes(), err
}
