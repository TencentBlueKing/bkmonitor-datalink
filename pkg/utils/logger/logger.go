// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package logger

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Level int8

var loggerLevelMap = map[string]Level{
	DebugLevelDesc:  DebugLevel,
	InfoLevelDesc:   InfoLevel,
	WarnLevelDesc:   WarnLevel,
	ErrorLevelDesc:  ErrorLevel,
	DPanicLevelDesc: DPanicLevel,
	PanicLevelDesc:  PanicLevel,
	FatalLevelDesc:  FatalLevel,
}

const (
	DebugLevelDesc  = "debug"
	InfoLevelDesc   = "info"
	WarnLevelDesc   = "warn"
	ErrorLevelDesc  = "error"
	DPanicLevelDesc = "dpanic"
	PanicLevelDesc  = "panic"
	FatalLevelDesc  = "fatal"
)

const (
	// DebugLevel logs are typically voluminous, and are usually disabled in
	// production.
	DebugLevel Level = iota - 1

	// InfoLevel is the default logging priority.
	InfoLevel

	// WarnLevel logs are more important than Info, but don't need individual
	// human review.
	WarnLevel

	// ErrorLevel logs are high-priority. If an application is running smoothly,
	// it shouldn't generate any error-level logs.
	ErrorLevel

	// DPanicLevel logs are particularly important errors. In development the
	// logger panics after writing the message.
	DPanicLevel

	// PanicLevel logs a message, then panics.
	PanicLevel

	// FatalLevel logs a message, then calls os.Exit(1).
	FatalLevel
)

// Options is the option set for Logger.
type Options struct {
	// Stdout sets the writer as stdout if it is true.
	Stdout bool `yaml:"stdout"`

	// logger ouput format, Valid values are "json", "console" and "logfmt", default is logfmt
	Format string `yaml:"format"`

	// Filename is the file to write logs to.  Backup log files will be retained
	// in the same directory.
	Filename string `yaml:"filename"`

	// MaxSize is the maximum size in megabytes of the log file before it gets rotated.
	MaxSize int `yaml:"max_size"`

	// MaxAge is the maximum number of days to retain old log files based on the
	// timestamp encoded in their filename.  Note that a day is defined as 24
	// hours and may not exactly correspond to calendar days due to daylight
	// savings, leap seconds, etc. The default is not to remove old log files
	// based on age.
	MaxAge int `yaml:"max_age"`

	// MaxBackups is the maximum number of old log files to retain. The default
	// is to retain all old log files (though MaxAge may still cause them to get
	// deleted.)
	MaxBackups int `yaml:"max_backups"`

	// Level is a logging priority. Higher levels are more important.
	Level string `yaml:"level"`
}

type RateCall struct {
	mut    sync.RWMutex
	called map[string]int64
}

func NewRateCall() *RateCall {
	return &RateCall{
		called: map[string]int64{},
	}
}

func (r *RateCall) Call(d time.Duration, key string) bool {
	now := time.Now().UnixNano()

	r.mut.RLock()
	last := r.called[key]
	should := now-last > d.Nanoseconds()
	r.mut.RUnlock()

	if !should {
		return false
	}

	r.mut.Lock()
	r.called[key] = now
	r.mut.Unlock()
	return true
}

// Logger represents the global SugaredLogger
type Logger struct {
	sugared   *zap.SugaredLogger
	warnRate  *RateCall
	errorRate *RateCall
}

// With adds a variadic number of fields to the logging context. It accepts a
// mix of strongly-typed Field objects and loosely-typed key-value pairs. When
// processing pairs, the first element of the pair is used as the field key
// and the second as the field value.
func (l Logger) With(args ...interface{}) Logger {
	return Logger{sugared: l.sugared.With(args...)}
}

// Println is the alias for Info
func (l Logger) Println(args ...interface{}) {
	l.sugared.Info(args...)
}

// Printf is the alias for Infof
func (l Logger) Printf(template string, args ...interface{}) {
	l.sugared.Infof(template, args...)
}

// Debug uses fmt.Sprint to construct and log a message.
func (l Logger) Debug(args ...interface{}) {
	l.sugared.Debug(args...)
}

// Info uses fmt.Sprint to construct and log a message.
func (l Logger) Info(args ...interface{}) {
	l.sugared.Info(args...)
}

// Warn uses fmt.Sprint to construct and log a message.
func (l Logger) Warn(args ...interface{}) {
	l.sugared.Warn(args...)
}

// Error uses fmt.Sprint to construct and log a message.
func (l Logger) Error(args ...interface{}) {
	l.sugared.Error(args...)
}

// Panic uses fmt.Sprint to construct and log a message, then panics.
func (l Logger) Panic(args ...interface{}) {
	l.sugared.Panic(args...)
}

// Fatal uses fmt.Sprint to construct and log a message, then calls os.Exit.
func (l Logger) Fatal(args ...interface{}) {
	l.sugared.Fatal(args...)
}

// Debugf uses fmt.Sprintf to log a templated message.
func (l Logger) Debugf(template string, args ...interface{}) {
	l.sugared.Debugf(template, args...)
}

// Infof uses fmt.Sprintf to log a templated message.
func (l Logger) Infof(template string, args ...interface{}) {
	l.sugared.Infof(template, args...)
}

// Warnf uses fmt.Sprintf to log a templated message.
func (l Logger) Warnf(template string, args ...interface{}) {
	l.sugared.Warnf(template, args...)
}

// Errorf uses fmt.Sprintf to log a templated message.
func (l Logger) Errorf(template string, args ...interface{}) {
	l.sugared.Errorf(template, args...)
}

// Panicf uses fmt.Sprintf to log a templated message, then panics.
func (l Logger) Panicf(template string, args ...interface{}) {
	l.sugared.Panicf(template, args...)
}

// Fatalf uses fmt.Sprintf to log a templated message, then calls os.Exit.
func (l Logger) Fatalf(template string, args ...interface{}) {
	l.sugared.Fatalf(template, args...)
}

// Debugw logs a message with some additional context. The variadic key-value
// pairs are treated as they are in With.
func (l Logger) Debugw(msg string, keysAndValues ...interface{}) {
	l.sugared.Debugw(msg, keysAndValues...)
}

// Infow logs a message with some additional context. The variadic key-value
// pairs are treated as they are in With.
func (l Logger) Infow(msg string, keysAndValues ...interface{}) {
	l.sugared.Infow(msg, keysAndValues...)
}

// Warnw logs a message with some additional context. The variadic key-value
// pairs are treated as they are in With.
func (l Logger) Warnw(msg string, keysAndValues ...interface{}) {
	l.sugared.Warnw(msg, keysAndValues...)
}

// Errorw logs a message with some additional context. The variadic key-value
// pairs are treated as they are in With.
func (l Logger) Errorw(msg string, keysAndValues ...interface{}) {
	l.sugared.Errorw(msg, keysAndValues...)
}

// DPanicw logs a message with some additional context. In development, the
// logger then panics. (See DPanicLevel for details.) The variadic key-value
// pairs are treated as they are in With.
func (l Logger) DPanicw(msg string, keysAndValues ...interface{}) {
	l.sugared.DPanicw(msg, keysAndValues...)
}

// Panicw logs a message with some additional context, then panics. The
// variadic key-value pairs are treated as they are in With.
func (l Logger) Panicw(msg string, keysAndValues ...interface{}) {
	l.sugared.Panicw(msg, keysAndValues...)
}

// Fatalw logs a message with some additional context, then calls os.Exit. The
// variadic key-value pairs are treated as they are in With.
func (l Logger) Fatalw(msg string, keysAndValues ...interface{}) {
	l.sugared.Fatalw(msg, keysAndValues...)
}

// New returns the logger instance with Production Config by default.
func New(opt Options) Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Local().Format("2006-01-02 15:04:05.000"))
	}
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	var encoder zapcore.Encoder
	switch opt.Format {
	case "json":
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	default: // console
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	var w zapcore.WriteSyncer
	if opt.Stdout {
		w = zapcore.AddSync(os.Stdout)
	} else {
		// 初始化日志目录
		if err := os.MkdirAll(filepath.Dir(opt.Filename), os.ModePerm); err != nil {
			panic(err)
		}

		w = zapcore.AddSync(&lumberjack.Logger{
			Filename:   opt.Filename,
			MaxSize:    opt.MaxSize,
			MaxBackups: opt.MaxBackups,
			MaxAge:     opt.MaxAge,
			LocalTime:  true,
		})
	}

	// 在这里将 level 转换为实际的 level 值
	level := loggerLevelMap[opt.Level]

	core := zapcore.NewCore(encoder, w, zapcore.Level(level))
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(1))
	return Logger{
		sugared:   logger.Sugar(),
		warnRate:  NewRateCall(),
		errorRate: NewRateCall(),
	}
}

var (
	stdOpt = Options{Stdout: true, Format: "logfmt"}
	std    = New(stdOpt)
)

// StandardLogger returns the standard logger with stdout output.
func StandardLogger() Logger {
	return std
}

// SetOptions sets the options for the standard logger.
func SetOptions(opt Options) {
	stdOpt = opt
	std = New(opt)
}

// SetLoggerLevel set the logger level
func SetLoggerLevel(l string) {
	l = strings.ToLower(strings.TrimSpace(l))
	_, ok := loggerLevelMap[l]
	if !ok {
		return
	}

	// skip the same level
	if stdOpt.Level == l {
		return
	}
	stdOpt.Level = l
	std = New(stdOpt)
}

// LoggerLevel returns the logger level
func LoggerLevel() string {
	if stdOpt.Level == "" {
		return "info"
	}
	return stdOpt.Level
}

// With adds a variadic number of fields to the logging context. It accepts a
// mix of strongly-typed Field objects and loosely-typed key-value pairs. When
// processing pairs, the first element of the pair is used as the field key
// and the second as the field value.
func With(args ...interface{}) Logger {
	s := std
	s.sugared = std.sugared.With(args...)
	return s
}

// Println is the alias for Info
func Println(args ...interface{}) {
	std.sugared.Info(args...)
}

// Printf is the alias for Infof
func Printf(template string, args ...interface{}) {
	std.sugared.Infof(template, args...)
}

// Debug uses fmt.Sprint to construct and log a message.
func Debug(args ...interface{}) {
	std.sugared.Debug(args...)
}

// Info uses fmt.Sprint to construct and log a message.
func Info(args ...interface{}) {
	std.sugared.Info(args...)
}

// Warn uses fmt.Sprint to construct and log a message.
func Warn(args ...interface{}) {
	std.sugared.Warn(args...)
}

// WarnRate sets log rate with warn message
func WarnRate(d time.Duration, key string, args ...interface{}) {
	if std.warnRate.Call(d, key) {
		std.sugared.Warn(args...)
	}
}

// Error uses fmt.Sprint to construct and log a message.
func Error(args ...interface{}) {
	std.sugared.Error(args...)
}

// ErrorRate sets log rate with error message
func ErrorRate(d time.Duration, key string, args ...interface{}) {
	if std.errorRate.Call(d, key) {
		std.sugared.Error(args...)
	}
}

// Panic uses fmt.Sprint to construct and log a message, then panics.
func Panic(args ...interface{}) {
	std.sugared.Panic(args...)
}

// Fatal uses fmt.Sprint to construct and log a message, then calls os.Exit.
func Fatal(args ...interface{}) {
	std.sugared.Fatal(args...)
}

// Debugf uses fmt.Sprintf to log a templated message.
func Debugf(template string, args ...interface{}) {
	std.sugared.Debugf(template, args...)
}

// Infof uses fmt.Sprintf to log a templated message.
func Infof(template string, args ...interface{}) {
	std.sugared.Infof(template, args...)
}

// Warnf uses fmt.Sprintf to log a templated message.
func Warnf(template string, args ...interface{}) {
	std.sugared.Warnf(template, args...)
}

// WarnfRate sets log rate with warn templated message
func WarnfRate(d time.Duration, key string, template string, args ...interface{}) {
	if std.warnRate.Call(d, key) {
		std.sugared.Warnf(template, args...)
	}
}

// Errorf uses fmt.Sprintf to log a templated message.
func Errorf(template string, args ...interface{}) {
	std.sugared.Errorf(template, args...)
}

// ErrorfRate sets log rate with error templated message
func ErrorfRate(d time.Duration, key string, template string, args ...interface{}) {
	if std.errorRate.Call(d, key) {
		std.sugared.Errorf(template, args...)
	}
}

// Panicf uses fmt.Sprintf to log a templated message, then panics.
func Panicf(template string, args ...interface{}) {
	std.sugared.Panicf(template, args...)
}

// Fatalf uses fmt.Sprintf to log a templated message, then calls os.Exit.
func Fatalf(template string, args ...interface{}) {
	std.sugared.Fatalf(template, args...)
}

// Debugw logs a message with some additional context. The variadic key-value
// pairs are treated as they are in With.
func Debugw(msg string, keysAndValues ...interface{}) {
	std.sugared.Debugw(msg, keysAndValues...)
}

// Infow logs a message with some additional context. The variadic key-value
// pairs are treated as they are in With.
func Infow(msg string, keysAndValues ...interface{}) {
	std.sugared.Infow(msg, keysAndValues...)
}

// Warnw logs a message with some additional context. The variadic key-value
// pairs are treated as they are in With.
func Warnw(msg string, keysAndValues ...interface{}) {
	std.sugared.Warnw(msg, keysAndValues...)
}

// Errorw logs a message with some additional context. The variadic key-value
// pairs are treated as they are in With.
func Errorw(msg string, keysAndValues ...interface{}) {
	std.sugared.Errorw(msg, keysAndValues...)
}

// DPanicw logs a message with some additional context. In development, the
// logger then panics. (See DPanicLevel for details.) The variadic key-value
// pairs are treated as they are in With.
func DPanicw(msg string, keysAndValues ...interface{}) {
	std.sugared.DPanicw(msg, keysAndValues...)
}

// Panicw logs a message with some additional context, then panics. The
// variadic key-value pairs are treated as they are in With.
func Panicw(msg string, keysAndValues ...interface{}) {
	std.sugared.Panicw(msg, keysAndValues...)
}

// Fatalw logs a message with some additional context, then calls os.Exit. The
// variadic key-value pairs are treated as they are in With.
func Fatalw(msg string, keysAndValues ...interface{}) {
	std.sugared.Fatalw(msg, keysAndValues...)
}
