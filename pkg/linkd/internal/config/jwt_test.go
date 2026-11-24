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
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

func TestJWTConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name, yaml, username, secret string
		env                          map[string]string
		invalid                      bool
	}{
		{name: "default", yaml: "{}", username: "admin"},
		{name: "yaml", yaml: "dispatch:\n  jwt:\n    secret_key: yaml-secret\n    username: operator\n", secret: "yaml-secret", username: "operator"},
		{name: "environment", yaml: "dispatch:\n  jwt:\n    secret_key: yaml-secret\n    username: operator\n", env: map[string]string{"LINKD_JWT_SECRET_KEY": "env-secret", "LINKD_JWT_USERNAME": "service"}, secret: "env-secret", username: "service"},
		{name: "explicit empty secret", yaml: "dispatch:\n  jwt:\n    secret_key: yaml-secret\n", env: map[string]string{"LINKD_JWT_SECRET_KEY": ""}, username: "admin"},
		{name: "blank username", yaml: "dispatch:\n  jwt:\n    username: '   '\n", invalid: true},
		{name: "old YAML rejected", yaml: "dispatch:\n  api_token: legacy\n", invalid: true},
		{name: "old environment ignored", yaml: "{}", env: map[string]string{"LINKD_API_TOKEN": "legacy"}, username: "admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := load(writeConfig(t, tc.yaml), Overrides{}, mapLookup(tc.env))
			if tc.invalid {
				if err == nil {
					t.Fatal("invalid configuration accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.Dispatch.JWT.SecretKey != tc.secret || cfg.Dispatch.JWT.Username != tc.username {
				t.Fatal("incorrect JWT configuration")
			}
		})
	}
}

func TestJWTConfigRedaction(t *testing.T) {
	cfg := Config{Dispatch: DispatchConfig{JWT: JWTConfig{SecretKey: "sensitive-jwt-key", Username: "admin"}}} //nolint:gosec // G101: 公开测试常量，仅用于验证脱敏。
	redacted := cfg.Redacted()
	if cfg.Dispatch.JWT.SecretKey != "sensitive-jwt-key" {
		t.Fatal("original key mutated")
	}
	for _, marshal := range []func(any) ([]byte, error){json.Marshal, yaml.Marshal} {
		value, err := marshal(redacted)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(value), "sensitive-jwt-key") {
			t.Fatal("secret leaked")
		}
	}
	value, err := json.Marshal(cfg)
	if err != nil || strings.Contains(string(value), "sensitive-jwt-key") {
		t.Fatal("JSON leaked key")
	}
}
