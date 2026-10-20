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

// appendSessionEvent 按 session_type 分流 Session 插件事件，旧版事件仍按 Session 处理。
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

// appendSessionAction 用开始/结束消息的时间差补写 Action 耗时，缺少开始消息时保持瞬时。
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
	// 重复或乱序的结束记录取最大耗时；起点保持 SDK 的 action.timestamp。
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
