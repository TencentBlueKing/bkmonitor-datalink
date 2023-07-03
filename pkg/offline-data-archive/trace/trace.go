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

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	// TracerName trace name
	TracerName = "bk-monitorv3/offline-data-archive"
)

// InsertIntIntoSpan 添加int类型的属性
func InsertIntIntoSpan(key string, value int, span oteltrace.Span) {
	if span == nil {
		return
	}
	span.SetAttributes(
		attribute.Int(key, value),
	)
}

// InsertStringIntoSpan 添加string类型的属性
func InsertStringIntoSpan(key, value string, span oteltrace.Span) {
	if span == nil {
		return
	}
	span.SetAttributes(
		attribute.String(key, value),
	)
}

// IntoContext 填充trace，并返回处理后的context和span
// span为nil时，说明没有开启trace
func IntoContext(globalCtx context.Context, tracerName, spanName string) (context.Context, oteltrace.Span) {
	var (
		span     oteltrace.Span
		traceCtx context.Context
	)

	// 向trace context中添加trace
	tracer := otel.Tracer(tracerName)
	traceCtx, span = tracer.Start(globalCtx, spanName)

	return traceCtx, span

}
