// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafkaclient

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestSecurityConfigValidate(t *testing.T) {
	t.Parallel()

	validSASL := func(mechanism string) *SASLConfig {
		return &SASLConfig{Mechanism: mechanism, Username: "linkd", Password: "secret"}
	}
	tests := []struct {
		name      string
		config    SecurityConfig
		wantError string
	}{
		{name: "default plaintext"},
		{name: "ssl system roots", config: SecurityConfig{Protocol: SecurityProtocolSSL}},
		{
			name: "ssl custom server name",
			config: SecurityConfig{
				Protocol: SecurityProtocolSSL,
				TLS:      &TLSConfig{ServerName: "kafka.internal"},
			},
		},
		{
			name:   "sasl plain",
			config: SecurityConfig{Protocol: SecurityProtocolSASLPlaintext, SASL: validSASL(SASLMechanismPlain)},
		},
		{
			name:   "sasl scram sha256",
			config: SecurityConfig{Protocol: SecurityProtocolSASLPlaintext, SASL: validSASL(SASLMechanismSCRAMSHA256)},
		},
		{
			name:   "sasl scram sha512 over tls",
			config: SecurityConfig{Protocol: SecurityProtocolSASLSSL, SASL: validSASL(SASLMechanismSCRAMSHA512)},
		},
		{name: "unknown protocol", config: SecurityConfig{Protocol: "unknown"}, wantError: "protocol must be one of"},
		{
			name:      "tls on plaintext",
			config:    SecurityConfig{Protocol: SecurityProtocolPlaintext, TLS: &TLSConfig{}},
			wantError: "tls must not be configured",
		},
		{
			name:      "sasl on ssl",
			config:    SecurityConfig{Protocol: SecurityProtocolSSL, SASL: validSASL(SASLMechanismPlain)},
			wantError: "sasl must not be configured",
		},
		{
			name:      "missing sasl",
			config:    SecurityConfig{Protocol: SecurityProtocolSASLSSL},
			wantError: "sasl is required",
		},
		{
			name: "ca sources conflict",
			config: SecurityConfig{Protocol: SecurityProtocolSSL, TLS: &TLSConfig{
				CAFile: "ca.pem", CAPEM: "pem",
			}},
			wantError: "mutually exclusive",
		},
		{
			name: "ca ignored by insecure mode",
			config: SecurityConfig{Protocol: SecurityProtocolSSL, TLS: &TLSConfig{
				CAPEM: "pem", InsecureSkipVerify: true,
			}},
			wantError: "must not be set when insecure_skip_verify",
		},
		{
			name: "incomplete file certificate pair",
			config: SecurityConfig{Protocol: SecurityProtocolSSL, TLS: &TLSConfig{
				ClientCertFile: "client.crt",
			}},
			wantError: "must be configured together",
		},
		{
			name: "incomplete inline certificate pair",
			config: SecurityConfig{Protocol: SecurityProtocolSSL, TLS: &TLSConfig{
				ClientKeyPEM: "private",
			}},
			wantError: "must be configured together",
		},
		{
			name: "mixed certificate modes",
			config: SecurityConfig{Protocol: SecurityProtocolSSL, TLS: &TLSConfig{
				ClientCertFile: "client.crt", ClientKeyFile: "client.key",
				ClientCertPEM: "certificate", ClientKeyPEM: "private",
			}},
			wantError: "file and inline PEM modes are mutually exclusive",
		},
		{
			name: "invalid sasl mechanism",
			config: SecurityConfig{Protocol: SecurityProtocolSASLPlaintext, SASL: &SASLConfig{
				Mechanism: "gssapi", Username: "linkd", Password: "secret",
			}},
			wantError: "mechanism must be one of",
		},
		{
			name: "missing sasl username",
			config: SecurityConfig{Protocol: SecurityProtocolSASLPlaintext, SASL: &SASLConfig{
				Mechanism: SASLMechanismPlain, Password: "secret",
			}},
			wantError: "username is required",
		},
		{
			name: "missing sasl password",
			config: SecurityConfig{Protocol: SecurityProtocolSASLPlaintext, SASL: &SASLConfig{
				Mechanism: SASLMechanismPlain, Username: "linkd",
			}},
			wantError: "password is required",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.config.Validate()
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("Validate() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Validate() error = %v, want containing %q", err, test.wantError)
			}
		})
	}
}

func TestSecurityConfigCloneRedactAndResolvePaths(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	original := SecurityConfig{
		Protocol: SecurityProtocolSASLSSL,
		TLS: &TLSConfig{
			CAFile:         "certs/ca.pem",
			ClientCertFile: "certs/client.crt",
			ClientKeyFile:  "certs/client.key",
		},
		SASL: &SASLConfig{Mechanism: SASLMechanismPlain, Username: "linkd", Password: "secret"},
	}
	resolved, err := original.ResolvePaths(baseDir)
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	for field, got := range map[string]string{
		"ca_file":          resolved.TLS.CAFile,
		"client_cert_file": resolved.TLS.ClientCertFile,
		"client_key_file":  resolved.TLS.ClientKeyFile,
	} {
		if !filepath.IsAbs(got) || !strings.HasPrefix(got, baseDir) {
			t.Fatalf("%s = %q, want absolute path under %q", field, got, baseDir)
		}
	}
	if original.TLS.CAFile != "certs/ca.pem" {
		t.Fatalf("ResolvePaths() changed original = %#v", original.TLS)
	}

	inline := SecurityConfig{
		Protocol: SecurityProtocolSASLSSL,
		TLS: &TLSConfig{
			CAPEM:         "public-ca",
			ClientCertPEM: "public-cert",
			ClientKeyPEM:  "private-key",
		},
		SASL: &SASLConfig{Mechanism: SASLMechanismPlain, Username: "linkd", Password: "secret"},
	}
	redacted := inline.Redacted()
	if redacted.SASL.Password != redactedSecret || redacted.TLS.ClientKeyPEM != redactedSecret {
		t.Fatalf("Redacted() = %#v", redacted)
	}
	if redacted.TLS.CAPEM != "public-ca" || redacted.TLS.ClientCertPEM != "public-cert" {
		t.Fatalf("Redacted() hid public certificate material = %#v", redacted.TLS)
	}
	redacted.TLS.CAPEM = "changed"
	redacted.SASL.Username = "changed"
	if inline.TLS.CAPEM != "public-ca" || inline.SASL.Username != "linkd" {
		t.Fatalf("Redacted() changed original = %#v", inline)
	}
}

func TestNormalizeBrokersAndValidateTopic(t *testing.T) {
	t.Parallel()

	got, err := NormalizeBrokers([]string{"KAFKA.Example.COM:09092", "[2001:0db8::1]:9093"})
	if err != nil {
		t.Fatalf("NormalizeBrokers() error = %v", err)
	}
	want := []string{"kafka.example.com:9092", "[2001:db8::1]:9093"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeBrokers() = %#v, want %#v", got, want)
	}
	for _, brokers := range [][]string{
		nil,
		{"kafka-without-port"},
		{"kafka:70000"},
		{"KAFKA:9092", "kafka:09092"},
	} {
		if _, err := NormalizeBrokers(brokers); err == nil {
			t.Fatalf("NormalizeBrokers(%#v) error = nil", brokers)
		}
	}
	for _, topic := range []string{"raw-events", "alerts.current", "_internal"} {
		if err := ValidateTopic(topic); err != nil {
			t.Fatalf("ValidateTopic(%q) error = %v", topic, err)
		}
	}
	for _, topic := range []string{"", ".", "..", "bad topic", strings.Repeat("a", 250)} {
		if err := ValidateTopic(topic); err == nil {
			t.Fatalf("ValidateTopic(%q) error = nil", topic)
		}
	}
}

func TestClientOptionsValidation(t *testing.T) {
	t.Parallel()

	options, err := ClientOptions(
		[]string{"KAFKA:09092"},
		"linkd-test",
		SecurityConfig{Protocol: SecurityProtocolPlaintext},
	)
	if err != nil || len(options) == 0 {
		t.Fatalf("ClientOptions() = %d options, %v", len(options), err)
	}
	if _, err := ClientOptions([]string{"missing-port"}, "linkd", SecurityConfig{}); err == nil {
		t.Fatal("ClientOptions() accepted invalid broker")
	}
	if _, err := ClientOptions([]string{"kafka:9092"}, " bad", SecurityConfig{}); err == nil {
		t.Fatal("ClientOptions() accepted invalid client ID")
	}
}
