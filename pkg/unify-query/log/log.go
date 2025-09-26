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
	"go.uber.org/zap"
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

type Logger struct {
	logger *zap.Logger
}

func (l *Logger) Printf(format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Info(withTraceID(context.Background(), format, v...))
}

func (l *Logger) Warnf(ctx context.Context, format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Warn(withTraceID(ctx, format, v...))
}

func (l *Logger) Infof(ctx context.Context, format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Info(withTraceID(ctx, format, v...))
}

func (l *Logger) Errorf(ctx context.Context, format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Error(withTraceID(ctx, format, v...))
}

func (l *Logger) Debugf(ctx context.Context, format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Debug(withTraceID(ctx, format, v...))
}

func (l *Logger) Panicf(ctx context.Context, format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Panic(withTraceID(ctx, format, v...))
}

func (l *Logger) Fatalf(ctx context.Context, format string, v ...any) {
	if l == nil || l.logger == nil {
		return
	}
	l.logger.Fatal(withTraceID(ctx, format, v...))
}

func Warnf(ctx context.Context, format string, v ...any) {
	DefaultLogger.Warnf(ctx, format, v...)
}

func Infof(ctx context.Context, format string, v ...any) {
	DefaultLogger.Infof(ctx, format, v...)
}

func Errorf(ctx context.Context, format string, v ...any) {
	DefaultLogger.Errorf(ctx, format, v...)
}

func Debugf(ctx context.Context, format string, v ...any) {
	// DefaultLogger.Debugf(ctx, format, v...)
	fmt.Printf(format+"\n", v...)
}

func Panicf(ctx context.Context, format string, v ...any) {
	DefaultLogger.Panicf(ctx, format, v...)
}

func Fatalf(ctx context.Context, format string, v ...any) {
	DefaultLogger.Fatalf(ctx, format, v...)
}
