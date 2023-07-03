// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package logging

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime/pprof"
	"sync"
	"time"

	"github.com/pkg/errors"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Level int8

var loggerLevelMap = map[string]Level{
	"debug":  DebugLevel,
	"info":   InfoLevel,
	"warn":   WarnLevel,
	"error":  ErrorLevel,
	"dpanic": DPanicLevel,
	"panic":  PanicLevel,
	"fatal":  FatalLevel,
}

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
	Stdout bool

	// logger ouput format, Valid values are "json", "console" and "logfmt", default is logfmt
	Format string

	// Filename is the file to write logs to.  Backup log files will be retained
	// in the same directory.
	Filename string

	// MaxSize is the maximum size in megabytes of the log file before it gets rotated.
	MaxSize int

	// MaxAge is the maximum number of days to retain old log files based on the
	// timestamp encoded in their filename.  Note that a day is defined as 24
	// hours and may not exactly correspond to calendar days due to daylight
	// savings, leap seconds, etc. The default is not to remove old log files
	// based on age.
	MaxAge int

	// MaxBackups is the maximum number of old log files to retain. The default
	// is to retain all old log files (though MaxAge may still cause them to get
	// deleted.)
	MaxBackups int

	// Level is a logging priority. Higher levels are more important.
	Level string
}

// Logger represents the global SugaredLogger
type Logger struct {
	sugared  *zap.SugaredLogger
	writer   io.Writer
	mut      sync.RWMutex
	sampling map[string]int64
}

func (l *Logger) Writer() io.Writer {
	return l.writer
}

func (l *Logger) Debug(args ...interface{}) {
	l.sugared.Debug(args...)
}

func (l *Logger) Debugf(template string, args ...interface{}) {
	l.sugared.Debugf(template, args...)
}

func (l *Logger) Printf(template string, args ...interface{}) {
	l.sugared.Infof(template, args...)
}

func (l *Logger) Println(args ...interface{}) {
	l.sugared.Info(args...)
}

func (l *Logger) Info(args ...interface{}) {
	l.sugared.Info(args...)
}

func (l *Logger) Infof(template string, args ...interface{}) {
	l.sugared.Infof(template, args...)
}

func (l *Logger) Warn(args ...interface{}) {
	l.sugared.Warn(args...)
}

func (l *Logger) Warnf(template string, args ...interface{}) {
	l.sugared.Warnf(template, args...)
}

func (l *Logger) Error(args ...interface{}) {
	l.sugared.Error(args...)
}

func (l *Logger) Errorf(template string, args ...interface{}) {
	l.sugared.Errorf(template, args...)
}

func (l *Logger) Panic(args ...interface{}) {
	l.sugared.Panic(args...)
}

func (l *Logger) Panicf(template string, args ...interface{}) {
	l.sugared.Panicf(template, args...)
}

func (l *Logger) Fatal(args ...interface{}) {
	l.sugared.Fatal(args...)
}

func (l *Logger) Fatalf(template string, args ...interface{}) {
	l.sugared.Fatalf(template, args...)
}

func (l *Logger) MinuteErrorSampling(name string, args ...interface{}) {
	l.ErrorSampling(name, 60, args...)
}

func (l *Logger) MinuteErrorfSampling(name, template string, args ...interface{}) {
	l.ErrorfSampling(name, 60, template, args...)
}

func (l *Logger) ErrorSampling(name string, seconds int64, args ...interface{}) {
	now := time.Now().Unix()
	var ok bool

	l.mut.RLock()
	last := l.sampling[name]
	if now-last > seconds {
		ok = true
	}
	l.mut.RUnlock()

	if ok {
		l.mut.Lock()
		l.sampling[name] = now
		l.mut.Unlock()
		l.sugared.Error(args...)
		return
	}
	l.sugared.Warn(args...)
}

func (l *Logger) ErrorfSampling(name string, seconds int64, template string, args ...interface{}) {
	now := time.Now().Unix()
	var ok bool

	l.mut.RLock()
	last := l.sampling[name]
	if now-last > seconds {
		ok = true
	}
	l.mut.RUnlock()

	if ok {
		l.mut.Lock()
		l.sampling[name] = now
		l.mut.Unlock()
		l.sugared.Errorf(template, args...)
		return
	}
	l.sugared.Warnf(template, args...)
}

func (l *Logger) WarnIf(message string, err error) {
	if err == nil {
		return
	}
	l.sugared.Warnf("%s %v", message, err)
}

func (l *Logger) PanicIf(err error) {
	if err == nil {
		return
	}
	l.sugared.Panicf("%+v", err)
}

func (l *Logger) IgnorePanics(fn func()) {
	defer func() {
		switch err := recover().(type) {
		case nil:
			return
		case error:
			l.Errorf("recovered panics: %+v", errors.WithStack(err))
		}
	}()
	fn()
}

func (l *Logger) Goroutines() {
	buf := bytes.NewBuffer(nil)
	l.PanicIf(pprof.Lookup("goroutine").WriteTo(buf, 2))
	l.Infof("goroutine details: \n%s", buf.String())
}

// New returns the logger instance with Production Config by default.
func New(opt Options) *Logger {
	encoderConfig := zap.NewProductionEncoderConfig()
	encoderConfig.EncodeTime = func(t time.Time, enc zapcore.PrimitiveArrayEncoder) {
		enc.AppendString(t.Local().Format("2006-01-02 15:04:05.000"))
	}
	encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	var encoder zapcore.Encoder
	switch opt.Format {
	case "json":
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	default:
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
	logger := zap.New(core, zap.AddCaller(), zap.AddCallerSkip(2))
	return &Logger{
		sugared:  logger.Sugar(),
		writer:   w,
		sampling: map[string]int64{},
	}
}

var (
	stdOpt = Options{Stdout: true, Format: "console"}
	std    = New(stdOpt)
)

// SetOptions sets the options for the standard logger.
func SetOptions(opt Options) {
	stdOpt = opt
	std = New(opt)
}

func SetLevel(level string) {
	stdOpt.Level = level
	std = New(stdOpt)
}

func GetOptions() Options {
	return stdOpt
}

func GetStdLogger() *Logger {
	return std
}

func GetStdWriter() io.Writer {
	return std.Writer()
}

func Debug(args ...interface{}) {
	std.Debug(args...)
}

func Debugf(template string, args ...interface{}) {
	std.Debugf(template, args...)
}

func Printf(template string, args ...interface{}) {
	std.Infof(template, args...)
}

func Println(args ...interface{}) {
	std.Info(args...)
}

func Info(args ...interface{}) {
	std.Info(args...)
}

func Infof(template string, args ...interface{}) {
	std.Infof(template, args...)
}

func Warn(args ...interface{}) {
	std.Warn(args...)
}

func Warnf(template string, args ...interface{}) {
	std.Warnf(template, args...)
}

func Error(args ...interface{}) {
	std.Error(args...)
}

func Errorf(template string, args ...interface{}) {
	std.Errorf(template, args...)
}

func Panic(args ...interface{}) {
	std.Panic(args...)
}

func Panicf(template string, args ...interface{}) {
	std.Panicf(template, args...)
}

func Fatal(args ...interface{}) {
	std.Fatal(args...)
}

func Fatalf(template string, args ...interface{}) {
	std.Fatalf(template, args...)
}

func WarnIf(template string, args ...interface{}) {
	std.Warnf(template, args...)
}

func PanicIf(err error) {
	std.PanicIf(err)
}

func IgnorePanics(fn func()) {
	std.IgnorePanics(fn)
}

func Goroutines() {
	std.Goroutines()
}

func ErrorSampling(name string, seconds int64, args ...interface{}) {
	std.ErrorSampling(name, seconds, args...)
}

func MinuteErrorSampling(name string, args ...interface{}) {
	std.MinuteErrorSampling(name, args...)
}

func ErrorfSampling(name string, seconds int64, template string, args ...interface{}) {
	std.ErrorfSampling(name, seconds, template, args...)
}

func MinuteErrorfSampling(name, template string, args ...interface{}) {
	std.MinuteErrorfSampling(name, template, args...)
}
