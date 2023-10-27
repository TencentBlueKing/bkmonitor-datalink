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
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
)

const (
	defaultMaxSize    = 500 // 默认为 500MB
	defaultMaxDays    = 5
	defaultMaxBackups = 3

	ConfLogLevel      = "logger.level"
	ConfOutName       = "logger.out.name"
	ConfOutFile       = "logger.out.options.file"
	ConfOutMaxDays    = "logger.out.options.max_days"
	ConfOutMaxSize    = "logger.out.options.max_size"
	ConfOutLevel      = "logger.out.options.level"
	ConfOutMaxBackups = "logger.out.options.max_backups"
)

func byteToMegabyte(n int) int {
	return int(float64(n / 1024 / 1024))
}

func initLogger(c define.Configuration) {
	c.SetDefault(ConfLogLevel, "info")
	c.SetDefault(ConfOutName, "stderr")
	c.SetDefault(ConfOutFile, "transfer.log")
	c.SetDefault(ConfOutLevel, "trace")
	c.SetDefault(ConfOutMaxSize, defaultMaxSize)
	c.SetDefault(ConfOutMaxDays, defaultMaxDays)
	c.SetDefault(ConfOutMaxBackups, defaultMaxBackups)
}

func updateLogger(c define.Configuration) {
	ConfigLogger(c)
}

func NewLoggerOption(c define.Configuration) Options {
	maxSize := c.GetInt(ConfOutMaxSize)
	maxSize = byteToMegabyte(maxSize)
	if maxSize <= 0 {
		maxSize = defaultMaxSize
	}

	maxBackups := c.GetInt(ConfOutMaxBackups)
	if maxBackups <= 0 {
		maxBackups = defaultMaxBackups
	}

	outName := c.GetString(ConfOutName)
	switch outName {
	case "file":
		return Options{
			Filename:   c.GetString(ConfOutFile),
			MaxSize:    maxSize,
			MaxAge:     c.GetInt(ConfOutMaxDays),
			MaxBackups: maxBackups,
			Level:      c.GetString(ConfOutLevel),
		}

	default: // cases: "stdout", "stderr", "null"
		return Options{
			Stdout: true,
			Level:  c.GetString(ConfLogLevel),
		}
	}
}

// ConfigLogger :
func ConfigLogger(c define.Configuration) {
	SetOptions(NewLoggerOption(c))
}

func NewLogger(c define.Configuration) *Logger {
	return New(NewLoggerOption(c))
}

func init() {
	if err := eventbus.Subscribe(eventbus.EvSysConfigPreParse, initLogger); err != nil {
		panic(err)
	}
	if err := eventbus.Subscribe(eventbus.EvSysConfigPostParse, updateLogger); err != nil {
		panic(err)
	}
}
