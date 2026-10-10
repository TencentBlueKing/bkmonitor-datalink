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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func kacPluginFixture() PluginsConfig {
	return PluginsConfig{KAC: &KACPluginConfig{Enabled: true, AlarmEventIndex: "cw_kac_saas_3.0_alarm_event", Elasticsearch: KACElasticsearchConfig{Addresses: []string{"http://localhost:9200"}, BasicAuth: &ResourceBasicAuth{Username: "linkd", Password: "private-es"}}, ActionEndpoint: "https://kac.example/internal/linkd/action", JWT: JWTConfig{SecretKey: "private-kac"}}}
}

func TestKACPluginValidationAndRedaction(t *testing.T) {
	if (PluginsConfig{}).KACEnabled() {
		t.Fatal("absent plugin enabled")
	}
	cfg := kacPluginFixture()
	if !cfg.KACEnabled() || cfg.Validate() != nil {
		t.Fatal("complete plugin rejected")
	}
	defaults := cfg.KAC.WithDefaults()
	if defaults.Elasticsearch.NumberOfShards != 3 || *defaults.Elasticsearch.NumberOfReplicas != 2 || defaults.Elasticsearch.TotalFieldsLimit != 5000 || defaults.JWT.Username != "admin" {
		t.Fatal("KAC defaults differ")
	}
	public := Config{Plugins: cfg}.Redacted()
	raw, _ := json.Marshal(public.Plugins)
	yamlRaw, err := yaml.Marshal(public.Plugins)
	if err != nil {
		t.Fatal(err)
	}
	if public.Plugins.KAC.JWT.SecretKey != redactedSecret || strings.Contains(string(raw)+string(yamlRaw), "private-") || cfg.KAC.JWT.SecretKey != "private-kac" || cfg.KAC.Elasticsearch.BasicAuth.Password != "private-es" {
		t.Fatal("redaction leaked or mutated credentials")
	}
	for name, change := range map[string]func(*KACPluginConfig){
		"missing index":         func(c *KACPluginConfig) { c.AlarmEventIndex = "" },
		"wildcard index":        func(c *KACPluginConfig) { c.AlarmEventIndex = "alarm*" },
		"missing ES":            func(c *KACPluginConfig) { c.Elasticsearch.Addresses = nil },
		"unsafe action URL":     func(c *KACPluginConfig) { c.ActionEndpoint = "https://private:secret@kac.example/action" },
		"missing action":        func(c *KACPluginConfig) { c.ActionEndpoint = "" },
		"missing signing key":   func(c *KACPluginConfig) { c.JWT.SecretKey = "" },
		"blank signing key":     func(c *KACPluginConfig) { c.JWT.SecretKey = " \n\t" },
		"oversized signing key": func(c *KACPluginConfig) { c.JWT.SecretKey = strings.Repeat("x", 16<<10+1) },
		"redacted signing key":  func(c *KACPluginConfig) { c.JWT.SecretKey = redactedSecret },
		"blank username":        func(c *KACPluginConfig) { c.JWT.Username = " \n\t" },
		"oversized username":    func(c *KACPluginConfig) { c.JWT.Username = strings.Repeat("x", 257) },
		"invalid shard budget":  func(c *KACPluginConfig) { c.Elasticsearch.NumberOfShards = 1025 },
	} {
		t.Run(name, func(t *testing.T) {
			v := kacPluginFixture()
			change(v.KAC)
			err := v.Validate()
			if err == nil || strings.Contains(err.Error(), "private") {
				t.Fatal("invalid config accepted or leaked", err)
			}
		})
	}
}

func TestKACOldConfigurationRejected(t *testing.T) {
	//nolint:gosec // G101: 这些字符串仅验证已移除字段拒绝，不包含真实凭据。
	for name, body := range map[string]string{
		"tenant credentials":  "resources:\n  kac_delivery: []\n",
		"source targets":      "event_sources:\n  - event_source_id: source\n    kac_targets: []\n",
		"projection endpoint": "plugins:\n  kac:\n    projection_endpoint: https://kac.example/projection\n",
		"tenant override":     "plugins:\n  kac:\n    bk_tenant_id: tenant\n",
		"static JWT":          "plugins:\n  kac:\n    internal_token: old-static-jwt\n",
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(p, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(p, Overrides{}); err == nil {
				t.Fatal("removed configuration accepted")
			}
		})
	}
}

func TestKACGlobalConfigurationLoads(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yaml")
	raw := []byte("plugins:\n  kac:\n    enabled: true\n    alarm_event_index: cw_kac_saas_3.0_alarm_event\n    elasticsearch:\n      addresses: [http://localhost:9200]\n      number_of_replicas: 0\n    action_endpoint: https://kac.example/action\n    jwt:\n      secret_key: private-kac\n      username: linkd\n")
	if err := os.WriteFile(p, raw, 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p, Overrides{})
	if err != nil || !cfg.Plugins.KACEnabled() {
		t.Fatal(err)
	}
	if *cfg.Plugins.KAC.WithDefaults().Elasticsearch.NumberOfReplicas != 0 || cfg.Plugins.KAC.JWT.SecretKey != "private-kac" || cfg.Plugins.KAC.JWT.Username != "linkd" {
		t.Fatal("explicit plugin settings lost")
	}
}

func TestKACJWTSigningKeyAndUsernameBudgets(t *testing.T) {
	for _, key := range []string{"private\nsecret", "密钥", strings.Repeat("x", 16<<10)} {
		cfg := kacPluginFixture()
		cfg.KAC.JWT.SecretKey = key
		cfg.KAC.JWT.Username = strings.Repeat("x", 256)
		if err := cfg.Validate(); err != nil {
			t.Fatal("valid signing key or username boundary rejected", err)
		}
	}
}
