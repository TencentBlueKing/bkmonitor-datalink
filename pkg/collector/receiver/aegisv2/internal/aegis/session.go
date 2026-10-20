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
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/aegisv2/internal/rum"
)

// appendSessionEvent 是 Session 插件事件的分流点。aegis-web-sdk-v2 2.5.38/2.6.12
// 起统一上报 fields.type=session，实际事件种类在 message.session_type
// （session/view/action），阶段在 is_active；旧版 session_type=start/rotate/end
// 及其他取值仍按 Session 处理。
func (c *spanConverter) appendSessionEvent(e aegisEvent) {
	switch stringField(e.msg.raw, "session_type") {
	case sessionKindView:
		c.appendSessionView(e)
	case sessionKindAction:
		c.appendSessionAction(e)
	default:
		c.appendSession(e)
	}
}

func (c *spanConverter) appendSession(e aegisEvent) {
	span := c.newEventSpan(e, rum.Session, "")
	attrs, msg := span.Attributes(), e.msg
	phase := firstNonEmptyString(msg.raw, "session_phase", "session_type")
	switch phase {
	case sessionPhaseStart, sessionPhaseRotate, sessionPhaseEnd:
		attrs.PutString(attrSessionPhase, phase)
	default:
		if active, ok := extractBool(msg.raw, "is_active"); ok {
			attrs.PutString(attrSessionPhase, activePhase(active))
		} else if firstNonEmptyString(msg.raw, "session_from") == sessionFromLocalGenerate {
			attrs.PutString(attrSessionPhase, sessionPhaseStart)
		}
	}
}

// appendSessionView 处理 View 生命周期记录；is_active 缺失时不推断阶段。
func (c *spanConverter) appendSessionView(e aegisEvent) {
	active, ok := extractBool(e.msg.raw, "is_active")
	if !ok {
		c.appendView(e, "")
		return
	}
	c.appendView(e, activePhase(active))
}

// appendSessionAction 处理 Action 生命周期记录。开始记录由 appendAction 生成
// 瞬时 Span，这里不重复生成；结束记录用同批开始与结束消息的时间差补写结束时间。
// fields.action.timestamp 是客户端时钟，message.timestamp 经服务端校时，两者
// 直接相减会叠加时钟偏差，因此只用消息间的时间差表达耗时。缺少开始消息时
// 无法换算，保持瞬时 Span。
func (c *spanConverter) appendSessionAction(e aegisEvent) {
	active, ok := extractBool(e.msg.raw, "is_active")
	if !ok {
		return
	}
	id := e.record.Fields.Action.ID
	if id == "" {
		return
	}
	if active {
		if _, exists := c.actionStarts[id]; !exists {
			c.actionStarts[id] = e.msg.Timestamp
		}
		return
	}
	span, ok := c.actionSpans[id]
	if !ok {
		return
	}
	start := c.actionStarts[id]
	if start <= 0 || e.msg.Timestamp <= start {
		return
	}
	// 重复或乱序的结束记录取最大耗时；Span 起点保持 SDK 给出的 action.timestamp。
	duration := pcommon.Timestamp(uint64(e.msg.Timestamp-start) * uint64(time.Millisecond))
	if span.StartTimestamp()+duration > span.EndTimestamp() {
		span.SetEndTimestamp(span.StartTimestamp() + duration)
	}
}

// activePhase 将 is_active 映射为生命周期阶段，Session 与 View 记录共用。
func activePhase(active bool) string {
	if active {
		return sessionPhaseStart
	}
	return sessionPhaseEnd
}
