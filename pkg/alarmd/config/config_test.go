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

func TestDefaultRequiresExplicitKafkaCoordinates(t *testing.T) {
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
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "broker") {
		t.Fatalf("default configuration error = %v, want missing Kafka broker", err)
	}
}

func TestLoadBuildsConsumerAndSinkCoordinatesFromOneInputTopic(t *testing.T) {
	cfg, err := Load(writeConfig(t, validRuntimeConfig()))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	consumer := cfg.Kafka.ConsumerCoordinates()
	sink := cfg.Kafka.DecisionSinkCoordinates()
	if consumer.Topic != "alarmd-shadow-input" || sink.InputTopic != consumer.Topic {
		t.Fatalf("input topics consumer=%q sink=%q, want one shared coordinate", consumer.Topic, sink.InputTopic)
	}
	consumer.Brokers[0] = "mutated:9092"
	if sink.Brokers[0] != "127.0.0.1:9092" || cfg.Kafka.Brokers[0] != "127.0.0.1:9092" {
		t.Fatal("consumer coordinates did not deep-copy brokers")
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

	path := filepath.Join(t.TempDir(), "alarmd.yaml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func validRuntimeConfig() string {
	return `mode: shadow
http:
  listen: 127.0.0.1:8080
shutdown_timeout: 10s
kafka:
  brokers:
    - 127.0.0.1:9092
  input_topic: alarmd-shadow-input
  output_topic: alarmd-shadow-output
  allowed_output_topics:
    - alarmd-shadow-output
  group_id: alarmd-shadow
  client_id: alarmd
  broker_version: 2.6.0
`
}
