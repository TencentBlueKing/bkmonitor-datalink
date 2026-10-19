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
	"fmt"
	"math"
	"strings"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
)

// Type 是当前转换器支持的 BK-RUM 事件类型。
type Type string

const (
	Session   Type = "session"
	View      Type = "view"
	Resource  Type = "resource"
	Action    Type = "action"
	Error     Type = "error"
	Websocket Type = "websocket"
	Vital     Type = "vital"
	Custom    Type = "custom"
)

// 公共字段和已有兼容缺省值。来源 SDK 名称等信息由适配器提供。
const (
	SpanTypeKey        = "span_type"
	OutcomeTypeKey     = "outcome.type"
	ServiceNameKey     = "service.name"
	SessionIDKey       = "session.id"
	SessionTypeKey     = "session.type"
	ResourceTypeKey    = "resource.type"
	ErrorHandledKey    = "error.handled"
	DefaultServiceName = "unknown_service"
)

type spanSchema struct {
	name   string
	kind   ptrace.SpanKind
	prefix string
}

var schemas = map[Type]spanSchema{
	Session:   {name: "browser.session", kind: ptrace.SpanKindInternal},
	View:      {name: "browser.view", kind: ptrace.SpanKindInternal},
	Resource:  {name: "browser.resource", kind: ptrace.SpanKindClient},
	Action:    {kind: ptrace.SpanKindInternal, prefix: "action."},
	Error:     {name: "browser.error", kind: ptrace.SpanKindInternal},
	Websocket: {name: "browser.websocket", kind: ptrace.SpanKindClient},
	Vital:     {name: "browser.web_vital", kind: ptrace.SpanKindInternal},
	Custom:    {kind: ptrace.SpanKindInternal, prefix: "custom."},
}

// ApplyResourceDefaults 只补齐已有服务名缺省值，保留显式值。
func ApplyResourceDefaults(attrs pcommon.Map) {
	if value, ok := attrs.Get(ServiceNameKey); !ok || value.AsString() == "" {
		attrs.PutString(ServiceNameKey, DefaultServiceName)
	}
}

// ApplySpanDefaults 在映射完成后执行，不覆盖显式的零值、false 或其他有效值。
// session.has_replay、采样率、时间点和计数等未知信息均保持缺失。
func ApplySpanDefaults(span ptrace.Span) {
	attrs := span.Attributes()
	putDefaultString(attrs, OutcomeTypeKey, "success")
	if id, ok := attrs.Get(SessionIDKey); ok && id.Str() != "" {
		putDefaultString(attrs, SessionTypeKey, "user")
	}
	typ, ok := attrs.Get(SpanTypeKey)
	if !ok {
		return
	}
	switch Type(typ.Str()) {
	case Resource:
		putDefaultString(attrs, ResourceTypeKey, "other")
	case Error:
		if _, ok := attrs.Get(ErrorHandledKey); !ok {
			attrs.PutBool(ErrorHandledKey, false)
		}
	}
}

func putDefaultString(attrs pcommon.Map, key, fallback string) {
	if value, ok := attrs.Get(key); !ok || (value.Type() == pcommon.ValueTypeStr && value.Str() == "") {
		attrs.PutString(key, fallback)
	}
}

// VitalOutcome 校验通用 Vital 名称和数值，并对五项 Web Vitals 应用评级阈值。
// 其他指标沿用 success 缺省值，表示正常接收，不推断其体验评级。
func VitalOutcome(metric string, value float64) (string, bool) {
	if strings.TrimSpace(metric) == "" || value < 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return "", false
	}
	var good float64
	switch metric {
	case "fcp":
		good = 1800
	case "lcp":
		good = 2500
	case "inp":
		good = 200
	case "ttfb":
		good = 800
	case "cls":
		good = 0.1
	default:
		return "success", true
	}
	if value > good {
		return "warning", true
	}
	return "success", true
}

// ValidateSpan 检查转换器输出的结构约束，不校验来源无法提供的 SDK 必填字段。
// 它不修改数据，也不用于校验旁路输入的任意 OTLP Span。
func ValidateSpan(span ptrace.Span) error {
	attrs := span.Attributes()
	typ, ok := attrs.Get(SpanTypeKey)
	if !ok {
		return fmt.Errorf("%s: missing", SpanTypeKey)
	}
	schema, ok := schemas[Type(typ.AsString())]
	if !ok {
		return fmt.Errorf("%s: unsupported value %q", SpanTypeKey, typ.AsString())
	}
	validName := span.Name() == schema.name
	if schema.prefix != "" {
		validName = strings.HasPrefix(span.Name(), schema.prefix) && len(span.Name()) > len(schema.prefix)
	} else if Type(typ.AsString()) == Error {
		validName = validName || span.Name() == "browser.unhandledrejection" || span.Name() == "browser.resource_error"
	}
	if !validName || span.Kind() != schema.kind {
		return fmt.Errorf("%s: invalid span name %q or kind %s", typ.AsString(), span.Name(), span.Kind())
	}
	if span.TraceID().IsEmpty() || span.SpanID().IsEmpty() {
		return fmt.Errorf("%s: missing trace or span ID", span.Name())
	}
	if span.EndTimestamp() < span.StartTimestamp() {
		return fmt.Errorf("%s: end precedes start", span.Name())
	}
	outcome, ok := attrs.Get(OutcomeTypeKey)
	if !ok {
		return fmt.Errorf("%s: missing", OutcomeTypeKey)
	}
	switch outcome.AsString() {
	case "success", "warning", "error", "timeout", "abort":
	default:
		return fmt.Errorf("%s: invalid value %q", OutcomeTypeKey, outcome.Str())
	}
	if Type(typ.AsString()) == Vital {
		metric, hasMetric := attrs.Get("vital.metric")
		value, hasValue := attrs.Get("vital.value")
		id, hasID := attrs.Get("vital.id")
		if !hasMetric || !hasValue || !hasID {
			return fmt.Errorf("vital: missing metric, value or ID")
		}
		var number float64
		switch value.Type() {
		case pcommon.ValueTypeDouble:
			number = value.Double()
		case pcommon.ValueTypeInt:
			number = float64(value.Int())
		default:
			return fmt.Errorf("vital.value: expected a number")
		}
		if metric.Type() != pcommon.ValueTypeStr || id.Type() != pcommon.ValueTypeStr {
			return fmt.Errorf("vital: expected string metric and ID")
		}
		if _, ok := VitalOutcome(metric.Str(), number); !ok || strings.TrimSpace(id.Str()) == "" {
			return fmt.Errorf("vital: invalid metric, value or ID")
		}
	}
	return nil
}
