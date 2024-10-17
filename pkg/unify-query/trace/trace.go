// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trace

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	TracerName = "bk-monitorv3/unify-query"
)

type Span struct {
	name string
	span oteltrace.Span
}

// NewSpan 新建一个 span
func NewSpan(ctx context.Context, name string) (context.Context, *Span) {
	var span oteltrace.Span
	// 向trace context中添加trace
	tracer := otel.Tracer(TracerName)
	ctx, span = tracer.Start(ctx, name)

	return ctx, &Span{
		name: name,
		span: span,
	}
}

// TraceID 获取 traceid
func (s *Span) TraceID() string {
	if s.span == nil {
		return ""
	}

	return s.span.SpanContext().TraceID().String()
}

// Set attribute 打点
func (s *Span) Set(key string, value any) {
	if s.span == nil {
		return
	}
	var attr attribute.KeyValue
	switch value.(type) {
	case bool:
		attr = attribute.Bool(key, value.(bool))
	case int:
		attr = attribute.Int(key, value.(int))
	case int64:
		attr = attribute.Int64(key, value.(int64))
	case []int64:
		attr = attribute.Int64Slice(key, value.([]int64))
	case float64:
		attr = attribute.Float64(key, value.(float64))
	case []float64:
		attr = attribute.Float64Slice(key, value.([]float64))
	case string:
		attr = attribute.String(key, value.(string))
	case []string:
		attr = attribute.StringSlice(key, value.([]string))
	case time.Time:
		location, err := time.LoadLocation("Asia/Shanghai")
		if err != nil {
			return
		}
		t := value.(time.Time)
		attr = attribute.String(key, t.In(location).Format("2006-01-02 15:04:05"))
	case time.Duration:
		attr = attribute.String(key, value.(time.Duration).String())
	default:
		attr = attribute.String(key, fmt.Sprintf("%+v", value))
	}

	s.span.SetAttributes(attr)
}

// End span end 增加错误异常判断
func (s *Span) End(errPoint *error) {
	if *errPoint != nil {
		s.span.SetStatus(codes.Error, fmt.Sprintf("%v", *errPoint))
		s.span.RecordError(*errPoint)
	}
	s.span.End()
}
