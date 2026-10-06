// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package telemetry

import (
	"strings"
	"testing"
)

func TestConfigValidation(t *testing.T) {
	t.Parallel()

	valid := testConfig()
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if address := valid.ListenAddress(); address != "127.0.0.1:9464" {
		t.Fatalf("ListenAddress() = %q", address)
	}

	tests := []struct {
		name      string
		change    func(*Config)
		wantError string
	}{
		{name: "exporter", change: func(config *Config) { config.Metrics.Exporter = "otlp" }, wantError: "must be"},
		{
			name:      "missing endpoint",
			change:    func(config *Config) { config.Metrics.Prometheus.ListenAddress = "" },
			wantError: "is required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := valid
			test.change(&config)
			err := config.Validate()
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func testConfig() Config {
	return Config{Metrics: MetricsConfig{
		Exporter:   ExporterPrometheus,
		Prometheus: PrometheusConfig{ListenAddress: "127.0.0.1:9464"},
	}}
}
