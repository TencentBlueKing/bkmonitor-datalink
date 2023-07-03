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
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
)

var StdLogger *zap.SugaredLogger

func GetLogger() *zap.SugaredLogger {
	if StdLogger == nil {
		Init()
	}
	return StdLogger
}

func NewLogger() *zap.SugaredLogger {
	var ws zapcore.WriteSyncer

	switch config.Configuration.Logging.Output {
	case "stdout", "stderr", "null":
		// 标准输出
		ws = zapcore.Lock(os.Stdout)
	case "file":
		// 文件输出
		loggingOptions := &config.Configuration.Logging.Options
		ws = zapcore.AddSync(&lumberjack.Logger{
			Filename:   loggingOptions.Filename,
			MaxSize:    loggingOptions.MaxSize,
			MaxAge:     loggingOptions.MaxAge,
			MaxBackups: loggingOptions.MaxBackups,
			LocalTime:  loggingOptions.LocalTime,
			Compress:   loggingOptions.Compress,
		})
	default:
		ws = zapcore.Lock(os.Stdout)
	}

	// 日志级别解析转换
	logLevel := new(zapcore.Level)
	err := logLevel.Set(config.Configuration.Logging.Level)
	if err != nil {
		panic(err)
	}

	// 对日期进行格式化
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "time"
	encoderCfg.EncodeTime = zapcore.RFC3339TimeEncoder

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoderCfg),
		ws,
		logLevel,
	)
	logger := zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))

	return logger.Sugar()
}

func Init() {
	StdLogger = NewLogger()
}
