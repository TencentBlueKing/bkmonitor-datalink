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
	"math"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"linkd/internal/kafkaclient"
)

func strategyConfig() HookConfig {
	return HookConfig{Name: "active", Type: HookTypeActiveAlertByStrategy, Config: HookParameters{Redis: &RedisConfig{Address: "redis:6379"}, KeyPrefix: "active"}}
}

func TestHookValidation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*HookConfig)
		valid  bool
	}{
		{"default", func(*HookConfig) {}, true},
		{"milliseconds", func(h *HookConfig) { ms := int64(100); h.Config.TimeoutMilliseconds = &ms }, true},
		{"zero", func(h *HookConfig) { ms := int64(0); h.Config.TimeoutMilliseconds = &ms }, false},
		{"negative", func(h *HookConfig) { ms := int64(-1); h.Config.TimeoutMilliseconds = &ms }, false},
		{"overflow", func(h *HookConfig) { ms := int64(math.MaxInt64); h.Config.TimeoutMilliseconds = &ms }, false},
		{"missing redis", func(h *HookConfig) { h.Config.Redis = nil }, false},
		{"empty redis", func(h *HookConfig) { h.Config.Redis = &RedisConfig{} }, false},
		{"empty prefix", func(h *HookConfig) { h.Config.KeyPrefix = "" }, false},
		{"unknown type", func(h *HookConfig) { h.Type = "unknown" }, false},
		{"mixed parameters", func(h *HookConfig) { h.Config.Topic = "alerts" }, false},
		{"sentinel", func(h *HookConfig) {
			h.Config.Redis = &RedisConfig{Mode: RedisModeSentinel, Sentinel: &RedisSentinelConfig{MasterName: "main", Addresses: []string{"sentinel:26379"}}}
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := strategyConfig()
			tt.change(&h)
			err := ValidateHooks([]HookConfig{h})
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
		})
	}
	h := strategyConfig()
	if *h.WithDefaults().Config.TimeoutMilliseconds != 1000 || h.Config.TimeoutMilliseconds != nil {
		t.Fatal("default mutated source or is not milliseconds")
	}
	if ValidateHooks(nil) != nil || ValidateHooks([]HookConfig{h, h}) == nil {
		t.Fatal("empty list or duplicate validation")
	}
	many := make([]HookConfig, MaxHooksPerSource+1)
	if ValidateHooks(many) == nil {
		t.Fatal("unbounded hook count")
	}
}

func TestHookJSONYAMLRoundTripAndRedaction(t *testing.T) {
	first := strategyConfig()
	first.Config.Redis = &RedisConfig{Mode: RedisModeSentinel, Password: "redis-private", Sentinel: &RedisSentinelConfig{MasterName: "main", Addresses: []string{"sentinel:26379"}, Password: "sentinel-private"}}
	kafka := HookConfig{Name: "alerts", Type: HookTypeKafka, Config: HookParameters{Brokers: []string{"kafka:9093"}, Topic: "alerts", Security: kafkaclient.SecurityConfig{Protocol: "sasl_ssl", SASL: &kafkaclient.SASLConfig{Mechanism: "plain", Username: "user", Password: "kafka-private"}, TLS: &kafkaclient.TLSConfig{ClientKeyPEM: "key-private"}}}}
	source := EventSource{Hooks: []HookConfig{first, kafka}}
	for _, codec := range []struct {
		name      string
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{{"json", json.Marshal, json.Unmarshal}, {"yaml", yaml.Marshal, yaml.Unmarshal}} {
		t.Run(codec.name, func(t *testing.T) {
			data, err := codec.marshal(source)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "master_name") {
				t.Fatal("redis JSON/YAML field names diverged")
			}
			var restored EventSource
			if err := codec.unmarshal(data, &restored); err != nil {
				t.Fatal(err)
			}
			if len(restored.Hooks) != 2 || restored.Hooks[0].Config.Redis.Sentinel.Password != "sentinel-private" {
				t.Fatal("lost hook parameters")
			}
			redacted := restored.Redacted()
			safe, err := codec.marshal(redacted)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"redis-private", "sentinel-private", "kafka-private", "key-private"} {
				if strings.Contains(string(safe), secret) {
					t.Fatal("redaction leaked secret")
				}
			}
			redacted.Hooks[0].Config.Redis.Sentinel.Addresses[0] = "changed"
			if restored.Hooks[0].Config.Redis.Sentinel.Addresses[0] != "sentinel:26379" || restored.Hooks[1].Config.Security.SASL.Password != "kafka-private" {
				t.Fatal("redaction aliased original")
			}
		})
	}
}

func TestLoadSourceHooksAndRejectRemovedFields(t *testing.T) {
	base := `event_sources:
  - event_source_id: source
    enabled: true
    hooks:
      - name: active
        type: active-alert-by-strategy
        config:
          redis: {address: "redis:6379"}
          key_prefix: active
          timeout_milliseconds: 100
    storage:
      type: kafka
      kafka:
        brokers: ["kafka:9092"]
        topic: raw
        consumer_group: cleaner
`
	cfg, err := load(writeConfig(t, base), Overrides{}, mapLookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.EventSources[0].Hooks) != 1 || *cfg.EventSources[0].Hooks[0].Config.TimeoutMilliseconds != 100 {
		t.Fatal("load dropped hooks")
	}
	for _, bad := range []string{strings.Replace(base, "timeout_milliseconds: 100", "timeout_milliseconds: null", 1), strings.Replace(base, "timeout_milliseconds", "timeout_seconds", 1), "lifecycle:\n  output: {}\n" + base} {
		if _, err := load(writeConfig(t, bad), Overrides{}, mapLookup(nil)); err == nil {
			t.Fatal("accepted removed field")
		}
	}
	for _, bad := range []string{`{"name":"a","type":"active-alert-by-strategy","config":{"timeout_seconds":1}}`, `{"name":"a","type":"active-alert-by-strategy","config":{"redis":{"address":"redis:6379","secret":1}}}`} {
		decoder := json.NewDecoder(strings.NewReader(bad))
		decoder.DisallowUnknownFields()
		var h HookConfig
		if decoder.Decode(&h) == nil {
			t.Fatal("accepted unknown plugin field")
		}
	}
}
