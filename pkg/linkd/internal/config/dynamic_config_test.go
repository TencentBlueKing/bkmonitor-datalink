// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
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

	"go.yaml.in/yaml/v3"
)

func TestImportUsesCurrentControlPlaneSeverity(t *testing.T) {
	source := validEventSource()
	source.DefaultSeverity = "fatal"
	source.SeverityMapping = map[string]string{"P0": "fatal"}
	b, err := yaml.Marshal(map[string]any{"event_sources": []EventSource{source}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "sources.yaml")
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(path, Overrides{}); err == nil {
		t.Fatal("static config unexpectedly recognized KAC grade")
	}
	current := SeverityConfig{DefaultSeverity: "fatal", Levels: []SeverityLevel{{Name: "fatal", Priority: 0}}}
	loaded, err := LoadWithSeverity(path, Overrides{}, current)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.EventSources) != 1 || loaded.Severity.DefaultSeverity != "fatal" {
		t.Fatal("import did not use current grade")
	}
}

func TestDisabledDynamicConfigNeedsNoDependencies(t *testing.T) {
	for _, value := range []string{"control_plane:\n  dynamic_config:\n    enabled: false\n", "control_plane:\n  dynamic_config:\n    sources:\n      unused:\n        type: kingeye_alarmlevel\n"} {
		cfg := Default()
		if err := yaml.Unmarshal([]byte(value), &cfg); err != nil {
			t.Fatal(err)
		}
		if err := cfg.Validate(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDynamicConfigValidationAndRedaction(t *testing.T) {
	c := DynamicConfigConfig{Enabled: true, Sources: map[string]DynamicSourceConfig{"levels": {Type: DynamicSourceAlarmLevel, MySQL: &MySQLConfig{Address: "db:3306", Database: "kingeye", Username: "reader", Password: "private-pass"}}, "future": {Type: DynamicSourceRedis, Redis: &RedisConfig{Password: "redis-private", Sentinel: &RedisSentinelConfig{Password: "sentinel-private"}}}}, Bindings: DynamicBindings{Severity: &DynamicBinding{Source: "levels"}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	redacted := c.Redacted()
	b, err := yaml.Marshal(redacted)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-pass", "redis-private", "sentinel-private"} {
		if strings.Contains(string(b), secret) {
			t.Fatal("credentials not redacted")
		}
	}
	if c.Sources["levels"].MySQL.Password != "private-pass" {
		t.Fatal("redaction changed input")
	}
	bad := c.Clone()
	s := bad.Sources["levels"]
	s.Table = "alarmlevel; DROP TABLE x"
	bad.Sources["levels"] = s
	if bad.Validate() == nil {
		t.Fatal("unsafe SQL identifier accepted")
	}
	bad.Enabled = false
	if err := bad.Validate(); err != nil {
		t.Fatal(err)
	}
}
