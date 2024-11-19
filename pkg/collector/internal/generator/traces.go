// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package generator

import (
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
)

/*
	// SpanKindUnspecified represents that the SpanKind is unspecified, it MUST NOT be used.
	SpanKindUnspecified = 0

	// SpanKindInternal indicates that the span represents an internal operation within an application,
	// as opposed to an operation happening at the boundaries. Default value.
	SpanKindInternal = 1

	// SpanKindServer indicates that the span covers server-side handling of an RPC or other
	// remote network request.
	SpanKindServer = 2

	// SpanKindProducer indicates that the span describes a producer sending a message to a broker.
	// Unlike CLIENT and SERVER, there is often no direct critical path latency relationship
	// between producer and consumer spans.
	// A PRODUCER span ends when the message was accepted by the broker while the logical processing of
	// the message might span a much longer time.
	SpanKindClient = 3

	// SpanKindProducer indicates that the span describes a producer sending a message to a broker.
	// Unlike CLIENT and SERVER, there is often no direct critical path latency relationship
	// between producer and consumer spans.
	// A PRODUCER span ends when the message was accepted by the broker while the logical processing of
	// the message might span a much longer time.
	SpanKindProducer = 4

	// SpanKindConsumer indicates that the span describes consumer receiving a message from a broker.
	// Like the PRODUCER kind, there is often no direct critical path latency relationship between
	// producer and consumer spans.
	SpanKindConsumer = 5
*/

type TracesGenerator struct {
	opts define.TracesOptions

	attributes pcommon.Map
	resources  pcommon.Map
}

func NewTracesGenerator(opts define.TracesOptions) *TracesGenerator {
	attributes := random.AttributeMap(opts.RandomAttributeKeys, opts.DimensionsValueType)
	resources := random.AttributeMap(opts.RandomResourceKeys, opts.DimensionsValueType)
	return &TracesGenerator{
		attributes: attributes,
		resources:  resources,
		opts:       opts,
	}
}

func (g *TracesGenerator) Generate() ptrace.Traces {
	pdTraces := ptrace.NewTraces()
	rs := pdTraces.ResourceSpans().AppendEmpty()
	rs.Resource().Attributes().UpsertString("service.name", "generator.service")
	g.resources.CopyTo(rs.Resource().Attributes())
	for k, v := range g.opts.Resources {
		rs.Resource().Attributes().UpsertString(k, v)
	}

	now := time.Now()
	for i := 0; i < g.opts.SpanCount; i++ {
		span := rs.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
		span.SetName(random.String(12))
		span.SetSpanID(random.SpanID())
		span.SetTraceID(random.TraceID())
		span.SetStartTimestamp(pcommon.NewTimestampFromTime(now))
		span.SetEndTimestamp(pcommon.NewTimestampFromTime(now.Add(time.Second)))
		span.SetKind(ptrace.SpanKind(g.opts.SpanKind))
		g.attributes.CopyTo(span.Attributes())
		for k, v := range g.opts.Attributes {
			span.Attributes().UpsertString(k, v)
		}

		for j := 0; j < g.opts.EventCount; j++ {
			event := span.Events().AppendEmpty()
			event.SetName(random.String(8))
		}
		for j := 0; j < g.opts.LinkCount; j++ {
			link := span.Links().AppendEmpty()
			link.SetTraceID(random.TraceID())
		}
	}
	return pdTraces
}

func FromJsonToTraces(b []byte) (ptrace.Traces, error) {
	return ptrace.NewJSONUnmarshaler().UnmarshalTraces(b)
}
