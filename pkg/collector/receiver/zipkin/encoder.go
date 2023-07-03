// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package zipkin

import (
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/zipkin/zipkinv1"
	"github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/zipkin/zipkinv2"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver"
)

// ThriftV1 Encoder

func ThriftV1Encoder() receiver.Encoder {
	return thriftV1Encoder{tracesEncoder: zipkinv1.NewThriftTracesUnmarshaler()}
}

type thriftV1Encoder struct {
	receiver.NoopEncoder
	tracesEncoder ptrace.Unmarshaler
}

func (e thriftV1Encoder) Type() string { return "thrift.v1" }

func (e thriftV1Encoder) UnmarshalTraces(buf []byte) (ptrace.Traces, error) {
	return e.tracesEncoder.UnmarshalTraces(buf)
}

// JsonV1 Encoder

func JsonV1Encoder() receiver.Encoder {
	return jsonV1Encoder{tracesEncoder: zipkinv1.NewJSONTracesUnmarshaler(true)}
}

type jsonV1Encoder struct {
	receiver.NoopEncoder
	tracesEncoder ptrace.Unmarshaler
}

func (e jsonV1Encoder) Type() string { return "json.v1" }

func (e jsonV1Encoder) UnmarshalTraces(buf []byte) (ptrace.Traces, error) {
	return e.tracesEncoder.UnmarshalTraces(buf)
}

// PbV2 Encoder

func PbV2Encoder() receiver.Encoder {
	return pbV2Encoder{tracesEncoder: zipkinv2.NewProtobufTracesUnmarshaler(false, true)}
}

type pbV2Encoder struct {
	receiver.NoopEncoder
	tracesEncoder ptrace.Unmarshaler
}

func (e pbV2Encoder) Type() string { return "pb.v2" }

func (e pbV2Encoder) UnmarshalTraces(buf []byte) (ptrace.Traces, error) {
	return e.tracesEncoder.UnmarshalTraces(buf)
}

// JsonV2 Encoder

func JsonV2Encoder() receiver.Encoder {
	return jsonV2Encoder{tracesEncoder: zipkinv2.NewJSONTracesUnmarshaler(true)}
}

type jsonV2Encoder struct {
	receiver.NoopEncoder
	tracesEncoder ptrace.Unmarshaler
}

func (e jsonV2Encoder) Type() string { return "json.v2" }

func (e jsonV2Encoder) UnmarshalTraces(buf []byte) (ptrace.Traces, error) {
	return e.tracesEncoder.UnmarshalTraces(buf)
}
