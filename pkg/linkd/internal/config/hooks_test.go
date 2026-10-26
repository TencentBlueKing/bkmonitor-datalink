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
	valid := HookConfig{Name: "output", Type: HookTypeKafka, Config: HookParameters{Brokers: []string{"kafka:9092"}, Topic: "alerts"}}
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
		if ValidateHooks([]HookConfig{{Name: "output", Type: HookTypeKafka, Config: p}}) == nil {
			t.Fatal("removed parameters accepted for publication", raw)
		}
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
