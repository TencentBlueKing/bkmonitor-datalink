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
	"fmt"
	"os"
	"sync"

	"github.com/spf13/viper"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/eventbus"
)

var once sync.Once

// setDefaultConfig
func setDefaultConfig() {
	viper.SetDefault(LevelConfigPath, "info")
	viper.SetDefault(PathConfigPath, "")
}

// 从string到AtomicLevel的转换
func setLogLevel(level string) {
	switch level {
	case "debug":
		loggerLevel.SetLevel(zap.DebugLevel)
	case "info":
		loggerLevel.SetLevel(zap.InfoLevel)
	case "warning":
		loggerLevel.SetLevel(zap.WarnLevel)
	case "error":
		loggerLevel.SetLevel(zap.ErrorLevel)
	case "fatal":
		loggerLevel.SetLevel(zap.FatalLevel)
	default:
		loggerLevel.SetLevel(zap.InfoLevel)
	}
}

// 初始化日志配置
func initLogConfig() {
	var (
		encoder zapcore.Encoder
		err     error
	)

	// 配置日志级别
	setLogLevel(viper.GetString(LevelConfigPath))

	// 日志路径及轮转配置
	var writeSyncer zapcore.WriteSyncer
	if viper.GetString(PathConfigPath) == "" {
		writeSyncer = zapcore.Lock(os.Stdout)
	} else {
		if syncer, err = NewReopenableWriteSyncer(viper.GetString(PathConfigPath)); err != nil {
			fmt.Printf("failed to create syncer for->[%s]", err)
			return
		}
		writeSyncer = syncer
	}
	// 配置日志格式
	encoder = zapcore.NewConsoleEncoder(zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "message",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	})

	DefaultLogger = &Logger{
		logger: zap.New(
			zapcore.NewCore(encoder, writeSyncer, loggerLevel),
			zap.AddCaller(), zap.AddCallerSkip(4),
		),
	}
}

// init
func init() {
	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPreParse, setDefaultConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for log module for default config, maybe log module won't working.",
			eventbus.EventSignalConfigPreParse,
		)
	}

	if err := eventbus.EventBus.Subscribe(eventbus.EventSignalConfigPostParse, initLogConfig); err != nil {
		fmt.Printf(
			"failed to subscribe event->[%s] for log module for new config, maybe log module won't working.",
			eventbus.EventSignalConfigPostParse,
		)
	}
}

// InitTestLogger 加载单元测试日志配置
func InitTestLogger() {
	// 加载配置
	viper.Set(LevelConfigPath, "debug")
	initLogConfig()
}
