// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
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

	"linkd/internal/telemetry"
)

func TestLoadTelemetryPrometheusConfig(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "linkd.yaml")
	content := `telemetry:
  metrics:
    exporter: prometheus
    prometheus:
      listen_address: 127.0.0.1:9464
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	config, err := Load(path, Overrides{})
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if config.Telemetry == nil || config.Telemetry.Metrics.Exporter != telemetry.ExporterPrometheus {
		t.Fatalf("Load() telemetry = %#v", config.Telemetry)
	}
	redacted, err := MarshalRedacted(config)
	if err != nil {
		t.Fatalf("MarshalRedacted() error = %v", err)
	}
	if !strings.Contains(string(redacted), "listen_address: 127.0.0.1:9464") {
		t.Fatalf("MarshalRedacted() = %s", redacted)
	}
}
