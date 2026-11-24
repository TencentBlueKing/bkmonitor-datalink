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
)

func TestCollectorDeploymentConfiguration(t *testing.T) {
	cfg := CollectorConfig{Endpoint: "http://127.0.0.1:8091/execute", Token: "internal-secret", TimeoutSeconds: 35, MaxRequestBytes: 1 << 20, MaxResponseBytes: 8 << 20}
	if err := cfg.validate(); err != nil {
		t.Fatal(err)
	}
	redacted := (Config{Collector: &cfg}).Redacted()
	if redacted.Collector.Token != "******" || cfg.Token != "internal-secret" {
		t.Fatal("collector token redaction failed")
	}
	cfg.CompletedRetentionSeconds = -1
	if err := cfg.validate(); err == nil {
		t.Fatal("negative retention accepted")
	}
	cfg.CompletedRetentionSeconds = 1<<63 - 1
	if err := cfg.validate(); err == nil {
		t.Fatal("overflowing retention accepted")
	}
	cfg.CompletedRetentionSeconds = 86400
	cfg.MaxConcurrentRequests = -1
	if err := cfg.validate(); err == nil {
		t.Fatal("negative ingress concurrency accepted")
	}
	cfg.MaxConcurrentRequests = 4
	cfg.RecoveryBatchSize = -1
	if err := cfg.validate(); err == nil {
		t.Fatal("negative recovery batch accepted")
	}
	cfg.RecoveryBatchSize = 32
	cfg.Endpoint = "file:///tmp/socket"
	if err := cfg.validate(); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
}

func TestCollectionSecretsAndClone(t *testing.T) {
	original := EventSource{Collection: &SourceCollection{Plugin: "uc", Mode: "pull", IntervalMinutes: 5, Secret: "ingress-secret", Configs: map[string]any{"auth": map[string]any{"password": "private"}}}}
	redacted := original.Redacted()
	if redacted.Collection.Secret != "******" || redacted.Collection.Configs["auth"] != "******" {
		t.Fatal("credentials not redacted")
	}
	restored := redacted.WithPreservedCollectionSecrets(original)
	if restored.Collection.Secret != "ingress-secret" {
		t.Fatal("ingress credential not preserved")
	}
	restored.Collection.Configs["auth"].(map[string]any)["password"] = "changed"
	if original.Collection.Configs["auth"].(map[string]any)["password"] != "private" {
		t.Fatal("collection clone shares nested state")
	}
}

func TestCollectionConfiguration(t *testing.T) {
	for _, tc := range []struct {
		config SourceCollection
		valid  bool
	}{
		{SourceCollection{Plugin: "rest_api_push", Mode: "push", Secret: "secret"}, true},
		{SourceCollection{Plugin: "uc", Mode: "pull", IntervalMinutes: 5}, true},
		{SourceCollection{Plugin: "uc", Mode: "pull"}, false},
		{SourceCollection{Plugin: "rest_api_push", Mode: "push"}, false},
		{SourceCollection{Plugin: "uc", Mode: "pull", IntervalMinutes: 5, Configs: map[string]any{"password": "******"}}, false},
	} {
		if err := tc.config.validate(); (err == nil) != tc.valid {
			t.Fatalf("unexpected validation: %v", err)
		}
	}
}

func TestLoadCollectorAndSourceCollection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "collector.yaml")
	content := `collector:
  endpoint: http://python-runtime:8091/execute
  token: internal-secret
  timeout_seconds: 35
  max_request_bytes: 1048576
  max_response_bytes: 8388608
  completed_retention_seconds: 86400
  recovery_batch_size: 16
  max_concurrent_requests: 7
storage:
  repository: elasticsearch
  elasticsearch:
    addresses: [http://elasticsearch:9200]
    index_prefix: linkd
  redis:
    address: redis:6379
event_sources:
  - event_source_id: source
    related_tenant_id: tenant
    enabled: true
    collection:
      plugin: uc
      plugin_source_id: original-kac-source
      source_name: 原来源名称
      timezone: UTC
      initial_state:
        meta_info: initial
      mode: pull
      interval_minutes: 5
      recovery_interval_minutes: 2
      configs:
        password: plugin-secret
    storage:
      type: kafka
      kafka:
        brokers: [kafka:9092]
        topic: events
        consumer_group: linkd
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(path, Overrides{}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Collector == nil || cfg.Collector.CompletedRetentionSeconds != 86400 || cfg.Collector.RecoveryBatchSize != 16 || cfg.Collector.MaxConcurrentRequests != 7 || cfg.Collector.Token != "internal-secret" {
		t.Fatal("runtime settings lost")
	}
	if len(cfg.EventSources) != 1 || cfg.EventSources[0].Collection == nil || cfg.EventSources[0].Collection.Plugin != "uc" || cfg.EventSources[0].Collection.PluginSourceID != "original-kac-source" || cfg.EventSources[0].Collection.SourceName != "原来源名称" || cfg.EventSources[0].Collection.Timezone != "UTC" || cfg.EventSources[0].Collection.InitialState["meta_info"] != "initial" || cfg.EventSources[0].Collection.RecoveryIntervalMinutes != 2 {
		t.Fatal("source plugin settings lost")
	}
	redacted, err := MarshalRedacted(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(redacted), "internal-secret") || strings.Contains(string(redacted), "plugin-secret") {
		t.Fatal("loaded credentials not redacted")
	}
}

func TestCollectionInitialContextCloneAndTimezone(t *testing.T) {
	source := SourceCollection{Plugin: "uc", Mode: "pull", IntervalMinutes: 1, Timezone: "UTC", InitialState: map[string]any{"nested": map[string]any{"cursor": "initial"}}}
	clone := source.clone()
	clone.InitialState["nested"].(map[string]any)["cursor"] = "changed"
	if source.InitialState["nested"].(map[string]any)["cursor"] != "initial" {
		t.Fatal("initial state shared across snapshots")
	}
	if err := source.validate(); err != nil {
		t.Fatal(err)
	}
	source.Timezone = "unknown/timezone"
	if err := source.validate(); err == nil {
		t.Fatal("invalid timezone accepted")
	}
}

func TestCollectorRemainsDisabledForExistingConfiguration(t *testing.T) {
	if Default().Collector != nil {
		t.Fatal("default configuration starts collector")
	}
	path := filepath.Join(t.TempDir(), "existing.yaml")
	if err := os.WriteFile(path, []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := load(path, Overrides{}, func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Collector != nil {
		t.Fatal("loading existing configuration enabled collector")
	}
}

func TestSharedCollectionSecretsFollowTenantAfterReorder(t *testing.T) {
	original := EventSource{Collection: &SourceCollection{Plugin: "rest_api_push", Mode: "push", TenantBindings: []CollectionTenantBinding{
		{TenantID: "one", Enabled: true, Secret: "secret-one", Configs: map[string]any{"auth": map[string]any{"token": "token-one"}}},
		{TenantID: "two", Enabled: true, Secret: "secret-two", Configs: map[string]any{"auth": map[string]any{"token": "token-two"}}},
	}}}
	if err := original.Collection.validate(); err != nil {
		t.Fatal(err)
	}
	edited := original.Redacted()
	for _, row := range edited.Collection.TenantBindings {
		if row.Secret != redactedSecret || row.Configs["auth"] != redactedSecret {
			t.Fatal("shared credentials exposed")
		}
	}
	rows := edited.Collection.TenantBindings
	rows[0], rows[1] = rows[1], rows[0]
	rows[1].Secret = "rotated-one"
	restored := edited.WithPreservedCollectionSecrets(original)
	if restored.Collection.TenantBindings[0].Secret != "secret-two" || restored.Collection.TenantBindings[1].Secret != "rotated-one" {
		t.Fatal("credentials restored by list position")
	}
	nested := restored.Collection.TenantBindings[0].Configs["auth"].(map[string]any)
	if nested["token"] != "token-two" {
		t.Fatal("cross-tenant config restored")
	}
	nested["token"] = "changed"
	if original.Collection.TenantBindings[1].Configs["auth"].(map[string]any)["token"] != "token-two" {
		t.Fatal("shared nested config mutated")
	}
	edited.Collection.TenantBindings[0].TenantID = "new"
	if err := edited.WithPreservedCollectionSecrets(original).Collection.validate(); err == nil {
		t.Fatal("unknown tenant inherited masked credentials")
	}
}

func TestSharedCollectionRejectsAmbiguousAuthentication(t *testing.T) {
	base := SourceCollection{Plugin: "rest_api_push", Mode: "push", TenantBindings: []CollectionTenantBinding{
		{TenantID: "one", Enabled: true, Secret: "secret-one"},
		{TenantID: "two", Enabled: true, Secret: "secret-two"},
	}}
	for name, mutate := range map[string]func(*SourceCollection){
		"duplicate tenant":          func(c *SourceCollection) { c.TenantBindings[1].TenantID = "one" },
		"duplicate secret":          func(c *SourceCollection) { c.TenantBindings[1].Secret = "secret-one" },
		"missing secret":            func(c *SourceCollection) { c.TenantBindings[0].Secret = "" },
		"root secret":               func(c *SourceCollection) { c.Secret = "root" },
		"root configs":              func(c *SourceCollection) { c.Configs = map[string]any{"token": "root"} },
		"scheduled shared recovery": func(c *SourceCollection) { c.RecoveryIntervalMinutes = 1 },
		"pull":                      func(c *SourceCollection) { c.Mode = "pull"; c.IntervalMinutes = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			c := base.clone()
			mutate(c)
			if err := c.validate(); err == nil {
				t.Fatal("invalid shared binding accepted")
			}
		})
	}
	source := EventSource{RelatedTenantID: "one", Collection: &base}
	if err := source.validate(SeverityConfig{}); err == nil || !strings.Contains(err.Error(), "empty related_tenant_id") {
		t.Fatalf("ambiguous scope: %v", err)
	}
}
