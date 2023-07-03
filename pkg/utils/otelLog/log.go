// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package otelLog

import (
	"context"
	"fmt"
	"os"

	"github.com/uptrace/opentelemetry-go-extra/otelzap"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type _ interface {
	Debugf(ctx context.Context, format string, v ...any)
	Infof(ctx context.Context, format string, v ...any)
	Warnf(ctx context.Context, format string, v ...any)
	Errorf(ctx context.Context, format string, v ...any)
	Panicf(ctx context.Context, format string, v ...any)
	Fatalf(ctx context.Context, format string, v ...any)
}

type OtelOption struct {
	Level string
	Path  string
}

type OtelLogger struct {
	zapLogger  *zap.Logger
	otelLogger *otelzap.Logger
}

func getLevel(level string) zapcore.Level {
	switch level {
	case "debug":
		return zap.DebugLevel
	case "info":
		return zap.InfoLevel
	case "warning":
		return zap.WarnLevel
	case "error":
		return zap.ErrorLevel
	case "fatal":
		return zap.FatalLevel
	default:
		return zap.InfoLevel
	}
}

func NewLogger(opt *OtelOption) *OtelLogger {
	var (
		encoder zapcore.Encoder
		err     error
	)

	var writeSyncer zapcore.WriteSyncer
	if writeSyncer, err = NewReopenableWriteSyncer(opt.Path); err != nil {
		fmt.Printf("failed to create syncer for->[%s]", err)
	} else {
		writeSyncer = zapcore.Lock(os.Stdout)
	}

	level := getLevel(opt.Level)

	encoder = zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
		MessageKey:     "message",
		LevelKey:       "level",
		TimeKey:        "timestamp",
		NameKey:        "logger",
		CallerKey:      "caller",
		FunctionKey:    "function",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	})
	zapLogger := zap.New(
		zapcore.NewCore(encoder, writeSyncer, zap.NewAtomicLevelAt(level)),
		zap.AddCaller(), zap.AddCallerSkip(1),
	)
	otelLogger := otelzap.New(zapLogger,
		otelzap.WithTraceIDField(true),
		otelzap.WithCaller(true),
		otelzap.WithStackTrace(true),
		otelzap.WithMinLevel(zapcore.InfoLevel),
		otelzap.WithErrorStatusLevel(zapcore.WarnLevel),
	)

	return &OtelLogger{
		zapLogger:  zapLogger,
		otelLogger: otelLogger,
	}
}

func (o *OtelLogger) OtelLogger() *otelzap.Logger {
	return o.otelLogger
}

func (o *OtelLogger) ZapLogger() *zap.Logger {
	return o.zapLogger
}

func (o *OtelLogger) Warnf(ctx context.Context, format string, v ...any) {
	o.otelLogger.Ctx(ctx).Warn(fmt.Sprintf(format, v...))
}

func (o *OtelLogger) Infof(ctx context.Context, format string, v ...any) {
	o.otelLogger.Ctx(ctx).Info(fmt.Sprintf(format, v...))

}

func (o *OtelLogger) Errorf(ctx context.Context, format string, v ...any) {
	o.otelLogger.Ctx(ctx).Error(fmt.Sprintf(format, v...))

}

func (o *OtelLogger) Debugf(ctx context.Context, format string, v ...any) {
	o.otelLogger.Ctx(ctx).Debug(fmt.Sprintf(format, v...))

}

func (o *OtelLogger) Panicf(ctx context.Context, format string, v ...any) {
	o.otelLogger.Ctx(ctx).Panic(fmt.Sprintf(format, v...))

}

func (o *OtelLogger) Fatalf(ctx context.Context, format string, v ...any) {
	o.otelLogger.Ctx(ctx).Fatal(fmt.Sprintf(format, v...))

}
