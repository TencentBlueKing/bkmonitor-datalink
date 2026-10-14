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
	"encoding/json"
	"errors"
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

func TestErrorLogIncludesCallerAndRedactsCredentials(t *testing.T) {
	var output bytes.Buffer
	logger, err := New(DefaultConfig(), &output)
	if err != nil {
		t.Fatal(err)
	}
	logger.Warn("dependency failed", "module", "enrich", "operation", "processor_process", "error", errors.New(
		`connect https://reader:private-url@service/query?token=private-query: timeout; reader:private-dsn@tcp(mysql:3306)/db; password="private password" api_key=private-api authorization=Bearer private-bearer`))
	var record struct {
		Source struct {
			Function, File string
			Line           int
		}
		Error string
	}
	if err := json.Unmarshal(output.Bytes(), &record); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(record.Source.Function, "TestErrorLogIncludesCallerAndRedactsCredentials") || !strings.HasSuffix(record.Source.File, "logging_test.go") || record.Source.Line == 0 {
		t.Fatalf("caller missing: %+v", record.Source)
	}
	if strings.Contains(record.Error, "private") || !strings.Contains(record.Error, "timeout") || !strings.Contains(record.Error, "mysql:3306") {
		t.Fatalf("error redaction lost cause or leaked credentials: %s", record.Error)
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
