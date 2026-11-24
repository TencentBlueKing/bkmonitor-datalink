// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License.
package config

import (
	"encoding/json"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
	"linkd/internal/kafkaclient"
)

func TestHookValidation(t *testing.T) {
	valid := HookConfig{Name: "output", Type: HookTypeKAC, Config: HookParameters{Brokers: []string{"kafka:9092"}, Topic: "alerts"}}
	if err := ValidateHooks([]HookConfig{valid}); err != nil {
		t.Fatal(err)
	}
	if ValidateHooks([]HookConfig{valid, valid}) == nil {
		t.Fatal("duplicate accepted")
	}
	for _, kind := range []string{"active-alert-by-strategy", "unknown"} {
		old := valid
		old.Type = kind
		if ValidateHooks([]HookConfig{old}) == nil {
			t.Fatal("removed hook accepted")
		}
	}

	for _, raw := range []string{`{"redis":{"address":"redis:6379"}}`, `{"key_prefix":"a"}`, `{"timeout_milliseconds":100}`, `{"pool_size":16}`} {
		var p HookParameters
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatal("historical parameters cannot be read", err)
		}
		p.Brokers = []string{"kafka:9092"}
		p.Topic = "alerts"
		if ValidateHooks([]HookConfig{{Name: "output", Type: HookTypeKAC, Config: p}}) == nil {
			t.Fatal("removed parameters accepted for publication", raw)
		}
	}

}

func TestRemovedKafkaHookIgnoresOldParametersInBothCodecs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		decode func([]byte, any) error
		raw    string
	}{
		{"JSON extra fields", json.Unmarshal, `{"name":"old output!","type":"kafka","unused":true,"config":{"brokers":false,"max_message_bytes":"obsolete","security":{"protocol":"unknown","password":"old-secret"},"unused":{"x":1}}}`},
		{"JSON scalar config", json.Unmarshal, `{"type":"kafka","config":"obsolete"}`},
		{"JSON no config", json.Unmarshal, `{"type":"kafka"}`},
		{"YAML extra fields", yaml.Unmarshal, "name: old output!\ntype: kafka\nunused: true\nconfig:\n  brokers: false\n  max_message_bytes: obsolete\n  security: {protocol: unknown, password: old-secret}\n  unused: {x: 1}\n"},
		{"YAML scalar config", yaml.Unmarshal, "type: kafka\nconfig: obsolete\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hook HookConfig
			if err := tc.decode([]byte(tc.raw), &hook); err != nil {
				t.Fatal(err)
			}
			if hook.Type != HookTypeKafka {
				t.Fatal(hook.Type)
			}
			if err := ValidateHooks([]HookConfig{hook}); err != nil {
				t.Fatal("obsolete hook was validated", err)
			}
			encoded, err := json.Marshal(hook)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(encoded), "old-secret") || strings.Contains(string(encoded), "obsolete") {
				t.Fatal("obsolete payload retained", string(encoded))
			}
		})
	}
	// 只忽略明确移除的 kafka 类型，在用 KAC 类型仍保留原来的参数边界。
	for _, raw := range []string{`{"type":"kac","config":{"unused":true}}`, `{"type":"kac","unused":true,"config":{}}`} {
		var hook HookConfig
		if json.Unmarshal([]byte(raw), &hook) == nil {
			t.Fatal("active KAC accepted unknown parameters", raw)
		}
	}
}

func TestRemovedKafkaHooksDoNotConsumeNamesOrCapacity(t *testing.T) {
	active := HookConfig{Name: "output", Type: HookTypeKAC, Config: HookParameters{Brokers: []string{"kafka:9092"}, Topic: "alerts"}}
	hooks := []HookConfig{active}
	for range MaxHooksPerSource + 1 {
		hooks = append(hooks, HookConfig{Name: active.Name, Type: HookTypeKafka, Config: HookParameters{MaxMessageBytes: -1, Security: kafkaclient.SecurityConfig{Protocol: "obsolete"}}})
	}
	if err := ValidateHooks(hooks); err != nil {
		t.Fatal("ignored hook consumed name or capacity", err)
	}
	if ValidateHooks(append(hooks, active)) == nil {
		t.Fatal("active duplicate accepted")
	}
}

func TestPreserveHookSecretsByInstance(t *testing.T) {
	old := EventSource{Hooks: []HookConfig{{Name: "output", Type: HookTypeKAC, Config: HookParameters{Brokers: []string{"kafka:9092"}, Topic: "alerts", Security: kafkaclient.SecurityConfig{Protocol: "sasl_plaintext", SASL: &kafkaclient.SASLConfig{Mechanism: "plain", Username: "reader", Password: "secret"}}}}}}
	for _, codec := range []struct {
		marshal   func(any) ([]byte, error)
		unmarshal func([]byte, any) error
	}{{json.Marshal, json.Unmarshal}, {yaml.Marshal, yaml.Unmarshal}} {
		safe := old.Redacted()
		data, err := codec.marshal(safe)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "\"secret\"") {
			t.Fatal("secret leaked")
		}
		var restored EventSource
		if err := codec.unmarshal(data, &restored); err != nil {
			t.Fatal(err)
		}
		got := restored.WithPreservedHookSecrets(old)
		if got.Hooks[0].Config.Security.SASL.Password != "secret" {
			t.Fatal("secret lost")
		}
		got.Hooks[0].Config.Security.SASL.Password = "changed"
		if old.Hooks[0].Config.Security.SASL.Password != "secret" {
			t.Fatal("aliased source")
		}
	}
}
