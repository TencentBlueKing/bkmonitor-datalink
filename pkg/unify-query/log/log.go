// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package log

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/trace"
)

func withTraceID(ctx context.Context, format string, v ...any) string {
	str := fmt.Sprintf(format, v...)
	span := trace.SpanFromContext(ctx)
	traceID := span.SpanContext().TraceID()

	if traceID != [16]byte{0} {
		return fmt.Sprintf("[%s] %s", traceID, str)
	}
	return str
}

func Warnf(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Warn(withTraceID(ctx, format, v...))
}

func Infof(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Info(withTraceID(ctx, format, v...))
}

func Errorf(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Error(withTraceID(ctx, format, v...))
}

func Debugf(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Debug(withTraceID(ctx, format, v...))
}

func Panicf(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Panic(withTraceID(ctx, format, v...))
}

func Fatalf(ctx context.Context, format string, v ...any) {
	OtLogger.Ctx(ctx).Fatal(withTraceID(ctx, format, v...))
}
