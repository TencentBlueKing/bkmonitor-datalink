// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jaeger

import (
	"context"

	apachethrift "github.com/apache/thrift/lib/go/thrift"
	"github.com/jaegertracing/jaeger/thrift-gen/jaeger"
	jaegertranslator "github.com/open-telemetry/opentelemetry-collector-contrib/pkg/translator/jaeger"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

type Encoder interface {
	Type() string
	UnmarshalTraces(buf []byte) (ptrace.Traces, error)
}

func newThriftV1Encoder() thriftEncoder {
	return thriftEncoder{tdSerializer: apachethrift.NewTDeserializer()}
}

// thriftEncoder ThriftV1 编码器实现
type thriftEncoder struct {
	tdSerializer *apachethrift.TDeserializer
}

func (e thriftEncoder) Type() string {
	return "thrift"
}

func (e thriftEncoder) UnmarshalTraces(buf []byte) (ptrace.Traces, error) {
	batch := &jaeger.Batch{}
	if err := e.tdSerializer.Read(context.Background(), batch, buf); err != nil {
		return ptrace.NewTraces(), err
	}
	return jaegertranslator.ThriftToTraces(batch)
}
