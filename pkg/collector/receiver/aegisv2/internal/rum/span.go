// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.
package rum

import (
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// AppendSpan 使用协议定义的名称和 Kind 创建瞬时 Span。
// name 为空时采用该类型的固定名称；Action、Custom 和错误子类可提供名称。
// 来源上下文、持续时长和异常信息由调用方映射；完成后应用缺省规则并校验。
func AppendSpan(spans ptrace.SpanSlice, typ Type, name string, traceID pcommon.TraceID, spanID pcommon.SpanID, timestamp pcommon.Timestamp) ptrace.Span {
	schema := schemas[typ]
	if name == "" {
		name = schema.name
	}
	span := spans.AppendEmpty()
	span.SetTraceID(traceID)
	span.SetSpanID(spanID)
	span.SetName(name)
	span.SetKind(schema.kind)
	span.SetStartTimestamp(timestamp)
	span.SetEndTimestamp(timestamp)
	span.Attributes().PutString(SpanTypeKey, string(typ))
	return span
}
