// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDefaultIsSafeShadowConfiguration(t *testing.T) {
	cfg := Default()

	if cfg.Mode != ModeShadow {
		t.Fatalf("default mode = %q, want %q", cfg.Mode, ModeShadow)
	}
	if cfg.HTTP.Listen == "" {
		t.Fatal("default HTTP listen address must not be empty")
	}
	if cfg.ShutdownTimeout.Duration() <= 0 {
		t.Fatal("default shutdown timeout must be positive")
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default configuration must be valid: %v", err)
	}
}

func TestLoadRejectsModesThatCanProduceAuthoritativeOutput(t *testing.T) {
	for _, mode := range []string{"owner", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			_, err := Load(writeConfig(t, "mode: "+mode+"\n"))
			if err == nil {
				t.Fatalf("Load() accepted unsafe mode %q", mode)
			}
			if !strings.Contains(err.Error(), "mode") {
				t.Fatalf("Load() error = %q, want mode context", err)
			}
		})
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	_, err := Load(writeConfig(t, "mode: shadow\nproduction_topic: forbidden\n"))
	if err == nil {
		t.Fatal("Load() accepted an unknown field")
	}
	if !strings.Contains(err.Error(), "production_topic") {
		t.Fatalf("Load() error = %q, want unknown field name", err)
	}
}

func TestLoadRejectsInvalidHTTPAndTimeout(t *testing.T) {
	tests := map[string]string{
		"listen":         "mode: shadow\nhttp:\n  listen: invalid\n",
		"empty host":     "mode: shadow\nhttp:\n  listen: :8080\n",
		"zero port":      "mode: shadow\nhttp:\n  listen: 127.0.0.1:0\n",
		"port too large": "mode: shadow\nhttp:\n  listen: 127.0.0.1:65536\n",
		"timeout":        "mode: shadow\nshutdown_timeout: 0s\n",
	}

	for name, contents := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, contents)); err == nil {
				t.Fatalf("Load() accepted invalid %s", name)
			}
		})
	}
}

func TestDurationRejectsInvalidText(t *testing.T) {
	var duration Duration
	if err := duration.UnmarshalText([]byte("forever")); err == nil {
		t.Fatal("UnmarshalText() accepted an invalid duration")
	}
	if err := duration.UnmarshalText([]byte("3s")); err != nil {
		t.Fatalf("UnmarshalText() rejected a valid duration: %v", err)
	}
	if got := duration.Duration(); got != 3*time.Second {
		t.Fatalf("Duration() = %s, want 3s", got)
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "alarm-engine.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
