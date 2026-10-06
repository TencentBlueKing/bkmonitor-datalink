// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package logging

import (
	"fmt"
	"io"
	"log/slog"
)

// New 使用配置构造一个新的 slog Logger。
func New(cfg Config, output io.Writer) (*slog.Logger, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	level, err := parseLevel(cfg.Level)
	if err != nil {
		return nil, err
	}

	options := &slog.HandlerOptions{Level: level}
	var handler slog.Handler
	switch cfg.Format {
	case FormatJSON:
		handler = slog.NewJSONHandler(output, options)
	case FormatText:
		handler = slog.NewTextHandler(output, options)
	default:
		return nil, fmt.Errorf("unsupported logging format: %q", cfg.Format)
	}

	return slog.New(handler), nil
}

func parseLevel(value string) (slog.Level, error) {
	switch value {
	case LevelDebug:
		return slog.LevelDebug, nil
	case LevelInfo:
		return slog.LevelInfo, nil
	case LevelWarn:
		return slog.LevelWarn, nil
	case LevelError:
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported logging level: %q", value)
	}
}
