// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package logging 提供 Linkd 进程日志的配置和构造能力。
package logging

import "fmt"

const (
	// LevelDebug、LevelInfo、LevelWarn 和 LevelError 是支持的日志级别。
	LevelDebug = "debug"
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"

	// FormatJSON 和 FormatText 是支持的日志输出格式。
	FormatJSON = "json"
	FormatText = "text"
)

// Config 描述 Linkd 进程日志配置。
type Config struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// DefaultConfig 返回日志配置的代码默认值。
func DefaultConfig() Config {
	return Config{
		Level:  LevelInfo,
		Format: FormatJSON,
	}
}

// Validate 校验日志级别和输出格式。
func (c Config) Validate() error {
	switch c.Level {
	case LevelDebug, LevelInfo, LevelWarn, LevelError:
	default:
		return fmt.Errorf("logging.level must be one of debug, info, warn, error: %q", c.Level)
	}

	switch c.Format {
	case FormatJSON, FormatText:
	default:
		return fmt.Errorf("logging.format must be one of json, text: %q", c.Format)
	}

	return nil
}
