// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package aegis

import (
	"fmt"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/random"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/rum"
)

// ConvertJSON 将 Aegis V2 JSON 转换为 BK-RUM Traces，不读取 HTTP 或发布 Record。
// 非 Aegis JSON 返回 ErrNotAegis；其他错误均表示输入或转换失败。
func ConvertJSON(data []byte) (ptrace.Traces, error) {
	payload, records, err := decodePayload(data)
	if err != nil {
		return ptrace.Traces{}, err
	}
	return convertTraces(payload, records, pcommon.TraceID{})
}

// spanConverter 只保存一次转换所需的状态，不跨请求共享。
// 事件映射分布在同名文件；Span 构造和协议约束由 bkrum 包维护。
type spanConverter struct {
	payload collectPayload
	spans   ptrace.SpanSlice
	traceID pcommon.TraceID
	now     pcommon.Timestamp
	// actionSpans 按非空 Action ID 关联已生成的 Span，供 Session 插件的
	// Action 结束记录补写结束时间，同时承担同批去重。
	actionSpans map[string]ptrace.Span
	// actionStarts 记录 Session 插件 Action 开始消息的时间戳，用于计算耗时。
	actionStarts map[string]int64
}

func convertTraces(payload collectPayload, records []d2Record, traceID pcommon.TraceID) (ptrace.Traces, error) {
	traces := ptrace.NewTraces()
	if len(records) == 0 {
		return traces, nil
	}
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	buildResourceAttrs(resourceSpans.Resource().Attributes(), payload)
	scopeSpans := resourceSpans.ScopeSpans().AppendEmpty()
	putCollectorScope(scopeSpans.Scope(), payload.Bean.Version)
	if traceID.IsEmpty() {
		traceID = random.TraceID()
	}
	c := spanConverter{
		payload: payload, spans: scopeSpans.Spans(), traceID: traceID,
		now:          pcommon.NewTimestampFromTime(time.Now()),
		actionSpans:  make(map[string]ptrace.Span),
		actionStarts: make(map[string]int64),
	}

	// 多条消息可以引用同一 Action；同批按非空 ID 去重，无 ID 的 Action 不参与去重。
	for _, record := range records {
		action := record.Fields.Action
		if hasAction(action) {
			if action.ID == "" {
				c.appendAction(record)
			} else if _, exists := c.actionSpans[action.ID]; !exists {
				c.actionSpans[action.ID] = c.appendAction(record)
			}
		}
		for _, msg := range record.Message {
			c.appendMessage(aegisEvent{record: record, msg: msg})
		}
	}
	for i := 0; i < c.spans.Len(); i++ {
		span := c.spans.At(i)
		rum.ApplySpanDefaults(span)
		if err := rum.ValidateSpan(span); err != nil {
			return ptrace.Traces{}, fmt.Errorf("BK-RUM span %d: %w", i, err)
		}
	}
	return traces, nil
}

// appendMessage 是唯一事件分发点，按已确定的 Aegis 事件类型选择映射。
// 新增类型时，在这里选定处理函数，并在对应事件文件实现完整映射。
func (c *spanConverter) appendMessage(e aegisEvent) {
	switch e.eventType() {
	case eventTypeWebVitals:
		c.appendVitals(e)
	case eventTypeAssetsSpeed, eventTypeAPI:
		c.appendResource(e)
	case eventTypeCustomEvent:
		c.appendCustom(e)
	case eventTypePagePerformance:
		c.appendView(e, spanViewUpdate)
	case eventTypePV:
		c.appendView(e, spanViewStart)
	case eventTypeWebsocket:
		c.appendWebsocket(e)
	case eventTypeSession:
		c.appendSessionEvent(e)
	case eventTypeError:
		c.appendError(e)
	default:
		c.appendCustom(e)
	}
}

// newSpan 设置所有 Span 共用的标识、瞬时时间和上下文；持续时长由领域映射设置。
func (c *spanConverter) newSpan(record d2Record, timestamp int64, typ rum.Type, name string) ptrace.Span {
	ts := millisToTimestamp(timestamp, c.now)
	span := rum.AppendSpan(c.spans, typ, name, c.traceID, random.SpanID(), ts)
	putCommonSpanAttrs(span.Attributes(), c.payload, record)
	return span
}

// newEventSpan 使用消息时间创建 Span；仅输出各事件显式映射的 BK-RUM 属性。
func (c *spanConverter) newEventSpan(e aegisEvent, typ rum.Type, name string) ptrace.Span {
	return c.newSpan(e.record, e.msg.Timestamp, typ, name)
}

type aegisEvent struct {
	record d2Record
	msg    d2Message
}

// eventType 优先使用明确类型及插件，避免把错误正文等业务文本当作消息别名。
// 仅空类型和 normal 保留旧 SDK 的别名兼容，且不能覆盖错误标记。
func (e aegisEvent) eventType() string {
	typ := e.record.Fields.Type
	switch typ {
	case eventTypeWebVitals, eventTypeAssetsSpeed, eventTypeAPI, eventTypeCustomEvent,
		eventTypePagePerformance, eventTypePV, eventTypeWebsocket, eventTypeSession, eventTypeError:
		return typ
	}
	if e.isWebsocket() {
		return eventTypeWebsocket
	}
	if e.isError() {
		return eventTypeError
	}
	if typ == "" || typ == eventTypeNormal {
		switch e.msg.Msg {
		case eventTypeWebVitals, eventTypeCustomEvent, eventTypePagePerformance, eventTypePV, eventTypeSession:
			return e.msg.Msg
		case eventTypeAssetSpeed:
			return eventTypeAssetsSpeed
		case eventTypeSPA:
			if e.record.Fields.Plugin == pluginSPA {
				return eventTypePV
			}
		}
	}
	return typ
}
func (e aegisEvent) isWebsocket() bool {
	return e.record.Fields.Type == eventTypeWebsocket || e.record.Fields.Plugin == pluginWebsocket
}

func millisToTimestamp(ms int64, fallback pcommon.Timestamp) pcommon.Timestamp {
	if ms <= 0 {
		return fallback
	}
	return pcommon.NewTimestampFromTime(time.UnixMilli(ms))
}
