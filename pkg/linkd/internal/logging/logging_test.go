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
	"bytes"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	t.Parallel()

	want := Config{Level: LevelInfo, Format: FormatJSON}
	if got := DefaultConfig(); got != want {
		t.Fatalf("DefaultConfig() = %#v, want %#v", got, want)
	}
}

func TestConfigValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		config    Config
		wantError string
	}{
		{name: "debug json", config: Config{Level: LevelDebug, Format: FormatJSON}},
		{name: "info text", config: Config{Level: LevelInfo, Format: FormatText}},
		{name: "warn json", config: Config{Level: LevelWarn, Format: FormatJSON}},
		{name: "error text", config: Config{Level: LevelError, Format: FormatText}},
		{name: "invalid level", config: Config{Level: "trace", Format: FormatJSON}, wantError: "logging.level"},
		{name: "invalid format", config: Config{Level: LevelInfo, Format: "console"}, wantError: "logging.format"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.config.Validate()
			if test.wantError == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if test.wantError != "" && (err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestNewJSONLoggerFiltersByLevel(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := New(Config{Level: LevelInfo, Format: FormatJSON}, &output)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Debug("hidden")
	logger.Info("visible", "component", "test")

	text := output.String()
	if strings.Contains(text, "hidden") {
		t.Fatalf("logger output unexpectedly contains debug message: %s", text)
	}
	if !strings.Contains(text, `"msg":"visible"`) || !strings.Contains(text, `"component":"test"`) {
		t.Fatalf("logger output = %s", text)
	}
}

func TestNewTextLogger(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := New(Config{Level: LevelDebug, Format: FormatText}, &output)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Debug("visible")
	if text := output.String(); !strings.Contains(text, "level=DEBUG") || !strings.Contains(text, "msg=visible") {
		t.Fatalf("logger output = %s", text)
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	t.Parallel()

	_, err := New(Config{Level: "trace", Format: FormatJSON}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "logging.level") {
		t.Fatalf("New() error = %v", err)
	}
}
