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
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
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

type jsonEncoder struct{}

func (jsonEncoder) Type() string {
	return "json"
}

func (jsonEncoder) UnmarshalTraces(buf []byte) (ptrace.Traces, error) {
	req := ptraceotlp.NewRequest()
	if err := req.UnmarshalJSON(buf); err != nil {
		return ptrace.Traces{}, err
	}
	return req.Traces(), nil
}

func (jsonEncoder) UnmarshalMetrics(buf []byte) (pmetric.Metrics, error) {
	req := pmetricotlp.NewRequest()
	if err := req.UnmarshalJSON(buf); err != nil {
		return pmetric.Metrics{}, err
	}
	return req.Metrics(), nil
}

func (jsonEncoder) UnmarshalLogs(buf []byte) (plog.Logs, error) {
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
