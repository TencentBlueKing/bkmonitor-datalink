// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package converter

import (
	"github.com/elastic/beats/libbeat/common"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
)

type tracesEvent struct {
	define.CommonEvent
}

func (e tracesEvent) RecordType() define.RecordType {
	return define.RecordTraces
}

type tracesConverter struct{}

func (c tracesConverter) Clean() {}

func (c tracesConverter) ToEvent(token define.Token, dataId int32, data common.MapStr) define.Event {
	return tracesEvent{define.NewCommonEvent(token, dataId, data)}
}

func (c tracesConverter) ToDataID(record *define.Record) int32 {
	return record.Token.TracesDataId
}

func (c tracesConverter) Convert(record *define.Record, f define.GatherFunc) {
	pdTraces := record.Data.(ptrace.Traces)
	resourceSpansSlice := pdTraces.ResourceSpans()
	if resourceSpansSlice.Len() == 0 {
		return
	}
	dataId := c.ToDataID(record)

	for i := 0; i < resourceSpansSlice.Len(); i++ {
		resourceSpans := resourceSpansSlice.At(i)
		rsAttrs := resourceSpans.Resource().Attributes().AsRaw()
		scopeSpansSlice := resourceSpans.ScopeSpans()
		events := make([]define.Event, 0)
		for j := 0; j < scopeSpansSlice.Len(); j++ {
			spans := scopeSpansSlice.At(j).Spans()
			for k := 0; k < spans.Len(); k++ {
				content, kind := c.Extract(record.RequestClient.IP, spans.At(k), rsAttrs)
				DefaultMetricMonitor.IncConverterSpanKindCounter(dataId, kind)
				events = append(events, c.ToEvent(record.Token, dataId, content))
			}
		}
		if len(events) > 0 {
			f(events...)
		}
	}
}

func (c tracesConverter) Extract(ip string, span ptrace.Span, resources common.MapStr) (common.MapStr, string) {
	ms := common.MapStr{
		"span_name":      span.Name(),
		"span_id":        span.SpanID().HexString(),
		"trace_id":       span.TraceID().HexString(),
		"parent_span_id": span.ParentSpanID().HexString(),
		"kind":           span.Kind(),
		"start_time":     span.StartTimestamp() / 1000,
		"end_time":       span.EndTimestamp() / 1000,
		"trace_state":    span.TraceState(),
		"elapsed_time":   c.spanElapsedTime(span.EndTimestamp(), span.StartTimestamp()),
		"links":          c.spanLinks(span.Links()),
		"events":         c.spanEvents(span.Events()),
		"status":         c.spanStatus(span.Status()),
		"attributes":     CleanAttributesMap(span.Attributes().AsRaw()),
		"resource":       resources,
		"client_ip":      ip,
	}
	return ms, span.Kind().String()
}

func (c tracesConverter) spanElapsedTime(endTs, startTs pcommon.Timestamp) pcommon.Timestamp {
	return (endTs - startTs) / 1000
}

func (c tracesConverter) spanLinks(links ptrace.SpanLinkSlice) []common.MapStr {
	result := make([]common.MapStr, 0, links.Len())
	for i := 0; i < links.Len(); i++ {
		link := links.At(i)
		result = append(result, common.MapStr{
			"trace_id":    link.TraceID().HexString(),
			"span_id":     link.SpanID().HexString(),
			"trace_state": link.TraceState(),
			"attributes":  CleanAttributesMap(link.Attributes().AsRaw()),
		})
	}
	return result
}

func (c tracesConverter) spanStatus(status ptrace.SpanStatus) common.MapStr {
	return common.MapStr{
		"code":    status.Code(),
		"message": status.Message(),
	}
}

func (c tracesConverter) spanEvents(events ptrace.SpanEventSlice) []common.MapStr {
	result := make([]common.MapStr, 0, events.Len())
	for i := 0; i < events.Len(); i++ {
		event := events.At(i)
		result = append(result, common.MapStr{
			"name":       event.Name(),
			"timestamp":  event.Timestamp() / 1000,
			"attributes": CleanAttributesMap(event.Attributes().AsRaw()),
		})
	}
	return result
}
