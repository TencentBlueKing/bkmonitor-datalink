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
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"github.com/sirupsen/logrus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/common"
	proxyEventbus "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/event"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/golang/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/golang/utils"
)

// Entry :
type Entry struct {
	*logrus.Entry
}

func getPackageName(f string) string {
	for {
		lastPeriod := strings.LastIndex(f, ".")
		lastSlash := strings.LastIndex(f, "/")
		if lastPeriod > lastSlash {
			f = f[:lastPeriod]
		} else {
			break
		}
	}

	return f
}

// Errorf :
func (e *Entry) Errorf(format string, args ...interface{}) {
	if e.Entry.Logger.IsLevelEnabled(ErrorLevel) {
		pc, file, line, ok := runtime.Caller(1)
		f := runtime.FuncForPC(pc)
		output := fmt.Sprintf(format, args...)
		if ok {
			entry := e.Entry.WithField("stack", file+":"+strconv.Itoa(line)+":"+f.Name())
			entry.Log(ErrorLevel, output)
			return
		}
		e.Entry.Log(ErrorLevel, output)

	}
}

// Warnf :
func (e *Entry) Warnf(format string, args ...interface{}) {
	if e.Entry.Logger.IsLevelEnabled(WarnLevel) {
		pc, file, line, ok := runtime.Caller(1)
		f := runtime.FuncForPC(pc)
		output := fmt.Sprintf(format, args...)
		if ok {
			entry := e.Entry.WithField("stack", file+":"+strconv.Itoa(line)+":"+f.Name())
			entry.Log(WarnLevel, output)
			return
		}
		e.Entry.Log(WarnLevel, output)
	}
}

// Infof :
func (e *Entry) Infof(format string, args ...interface{}) {
	if e.Entry.Logger.IsLevelEnabled(InfoLevel) {
		pc, file, line, ok := runtime.Caller(1)
		f := runtime.FuncForPC(pc)
		output := fmt.Sprintf(format, args...)
		if ok {
			entry := e.Entry.WithField("stack", file+":"+strconv.Itoa(line)+":"+f.Name())
			entry.Log(InfoLevel, output)
			return
		}
		e.Entry.Log(InfoLevel, output)

	}
}

// Debugf :
func (e *Entry) Debugf(format string, args ...interface{}) {
	if e.Entry.Logger.IsLevelEnabled(DebugLevel) {
		pc, file, line, ok := runtime.Caller(1)
		f := runtime.FuncForPC(pc)
		output := fmt.Sprintf(format, args...)
		if ok {
			entry := e.Entry.WithField("stack", file+":"+strconv.Itoa(line)+":"+f.Name())
			entry.Log(DebugLevel, output)
			return
		}
		e.Entry.Log(DebugLevel, output)

	}
}

// Tracef :
func (e *Entry) Tracef(format string, args ...interface{}) {
	if e.Entry.Logger.IsLevelEnabled(TraceLevel) {
		pc, file, line, ok := runtime.Caller(1)
		f := runtime.FuncForPC(pc)
		output := fmt.Sprintf(format, args...)
		if ok {
			entry := e.Entry.WithField("stack", file+":"+strconv.Itoa(line)+":"+f.Name())
			entry.Log(TraceLevel, output)
			return
		}
		e.Entry.Log(TraceLevel, output)

	}
}

// NewEntry :
func NewEntry(fields map[string]interface{}) *Entry {
	return &Entry{StdLogger.WithFields(fields)}
}

// StdLogger 标准输出
var StdLogger *logrus.Logger

// DebugLevel :
var DebugLevel = logrus.DebugLevel

// TraceLevel :
var TraceLevel = logrus.TraceLevel

// InfoLevel :
var InfoLevel = logrus.InfoLevel

// WarnLevel :
var WarnLevel = logrus.WarnLevel

// ErrorLevel :
var ErrorLevel = logrus.ErrorLevel

func initLoggerConf(c common.Configuration) {
	c.SetDefault("logger.formatter.name", "text")
	c.SetDefault("logger.level", "info")
	c.SetDefault("logger.out.name", "stdout")
	c.SetDefault("logger.out.options.file", "github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy.log")
	c.SetDefault("logger.out.options.max_days", "2d")
	c.SetDefault("logger.out.options.duration", "4h")
	c.SetDefault("logger.out.options.rotate", true)
}

// initLogger :
func initLogger(c common.Configuration) {
	err := ConfigLogger(c, StdLogger)
	if err != nil {
		panic(err)
	}
}

var levelMap = map[string]logrus.Level{
	"trace": logrus.TraceLevel,
	"debug": logrus.DebugLevel,
	"info":  logrus.InfoLevel,
	"warn":  logrus.WarnLevel,
	"error": logrus.ErrorLevel,
	"panic": logrus.PanicLevel,
	"fatal": logrus.FatalLevel,
}

// ConfigLogger :
func ConfigLogger(c common.Configuration, logger *logrus.Logger) error {
	level, ok := levelMap[c.GetString("logger.level")]
	if !ok {
		panic("init logger failed,logger.level not found")
	}
	logger.SetLevel(level)
	formatter := c.GetString("logger.formatter.name")
	switch formatter {
	case "text":
		{
			logger.SetFormatter(&logrus.TextFormatter{
				FullTimestamp: true,
			})
		}
	case "json":
		{
			logger.SetFormatter(&logrus.JSONFormatter{})
		}
	default:
		{
			panic("formatter(logger.formatter.name) not found")
		}
	}
	outName := c.GetString("logger.out.name")
	switch outName {
	case "stdout":
		logger.SetOutput(os.Stdout)
	case "stderr":
		logger.SetOutput(os.Stderr)
	case "null":
		logger.SetOutput(new(NullWriter))
	case "file":
		{
			fileName := c.GetString("logger.out.options.file")
			// 日志存活时间,过期则清理
			maxAge := c.GetString("logger.out.options.max_days")
			rotateAge, err := time.ParseDuration(maxAge)
			if err != nil {
				logger.Fatalf("init logger age failed,error:%s", err)
			}
			// 是否分割
			rotate := c.GetBool("logger.out.options.rotate")
			// 日志分割的时长,决定了日志分片大小,与日志存活时间一起决定日志分片数
			duration := c.GetString("logger.out.options.duration")
			rotateTime, err := time.ParseDuration(duration)
			if err != nil {
				logger.Fatalf("init logger time failed,error:%s", err)
			}
			if rotate {
				fw, err := rotatelogs.New(fileName+".%Y%m%d%H",
					rotatelogs.WithLinkName(fileName),
					rotatelogs.WithMaxAge(rotateAge),
					rotatelogs.WithRotationTime(rotateTime),
				)
				if err != nil {
					logger.Fatalf("init logger failed,error:%s", err)
				}
				logger.SetOutput(fw)

			} else {
				file, err := os.OpenFile(fileName, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o644)
				if err != nil {
					logger.Fatalf("init logger failed,error:%s", err)
				}
				logger.SetOutput(file)
			}

		}
	default:
		return fmt.Errorf("unknown output name %s", outName)
	}

	return nil
}

func init() {
	StdLogger = logrus.StandardLogger()
	utils.CheckError(eventbus.Subscribe(proxyEventbus.EvSysConfigPreParse, initLoggerConf))
	utils.CheckError(eventbus.Subscribe(proxyEventbus.EvSysConfigPostParse, initLogger))
}
