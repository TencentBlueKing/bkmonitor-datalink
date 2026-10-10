// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

// Package credential loads deployment credentials once, without exporting plaintext.
package credential

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/TencentBlueKing/bk-kms-sdk/go/decrypt"
	"github.com/spf13/viper"
)

type control struct {
	Enabled                      bool
	EnvelopeFile, PrivateKeyFile string
}

type password struct {
	Password *string `json:"password"`
}
type appIdentity struct {
	Code   *string `json:"app_code"`
	Secret *string `json:"app_secret"`
}
type token struct {
	Token *string `json:"token"`
}
type telemetry struct {
	Token   *string        `json:"token"`
	Headers *stringHeaders `json:"headers"`
}

type stringHeaders map[string]string

func (h *stringHeaders) UnmarshalJSON(data []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return fmt.Errorf("invalid header map")
	}
	if fields == nil {
		return fmt.Errorf("invalid header map")
	}
	values := make(stringHeaders, len(fields))
	for key, raw := range fields {
		var value string
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
			return fmt.Errorf("invalid header value")
		}
		values[key] = value
	}
	*h = values
	return nil
}

type consulAuth struct {
	Token      *string `json:"token"`
	Username   *string `json:"username"`
	Password   *string `json:"password"`
	PrivateKey *string `json:"tls_private_key_base64"`
}
type defaultGroup[T any] struct {
	Default *T `json:"default"`
}
type payload struct {
	Version   int                        `json:"schema_version"`
	Service   string                     `json:"service"`
	App       *defaultGroup[appIdentity] `json:"bkapp_id_secret"`
	Redis     *defaultGroup[password]    `json:"redis"`
	Sentinel  *defaultGroup[password]    `json:"redis_sentinel"`
	BkData    *defaultGroup[token]       `json:"bkdata"`
	Telemetry *defaultGroup[telemetry]   `json:"telemetry"`
	Consul    *defaultGroup[consulAuth]  `json:"consul"`
}

// Snapshot is immutable after validation. Apply always copies mutable values.
type Snapshot struct {
	control   control
	payload   payload
	overrides map[string]any
}

var managedKeys = []string{
	"redis.password", "redis.sentinel_password", "bk_api.secret", "bk_data.token",
	"trace.otlp.token", "trace.otlp.headers", "consul.token", "consul.username",
	"consul.password", "consul.tls.key_file", "consul.tls.key_pem",
}

func envKey(key string) string {
	return "UNIFY-QUERY_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
}

func readControl(raw *viper.Viper) (control, error) {
	var c control
	for _, key := range []string{"kms.enabled", "kms.envelope_file", "kms.private_key_file"} {
		if _, present := os.LookupEnv(envKey(key)); present {
			return c, fmt.Errorf("kms control environment is not allowed: %s", key)
		}
	}
	if raw.InConfig("kms") {
		m, ok := raw.Get("kms").(map[string]any)
		if !ok {
			return c, fmt.Errorf("invalid kms configuration")
		}
		for k, val := range m {
			switch k {
			case "enabled":
				v, ok := val.(bool)
				if !ok {
					return c, fmt.Errorf("invalid kms.enabled type")
				}
				c.Enabled = v
			case "envelope_file", "private_key_file":
				v, ok := val.(string)
				if !ok {
					return c, fmt.Errorf("invalid kms.%s type", k)
				}
				if k == "envelope_file" {
					c.EnvelopeFile = v
				} else {
					c.PrivateKeyFile = v
				}
			default:
				return c, fmt.Errorf("unknown kms configuration field")
			}
		}
	}
	if c.Enabled && (c.EnvelopeFile == "" || c.PrivateKeyFile == "") {
		return c, fmt.Errorf("kms.envelope_file and kms.private_key_file are required")
	}
	return c, nil
}

func checkInputs(raw, runtime *viper.Viper) error {
	for _, key := range managedKeys {
		if raw.InConfig(key) {
			v := raw.Get(key)
			if v != nil && fmt.Sprint(v) != "" && fmt.Sprint(v) != "map[]" {
				return fmt.Errorf("plaintext credential configuration is not allowed: %s", key)
			}
		}
		if v := os.Getenv(envKey(key)); v != "" {
			return fmt.Errorf("credential environment is not allowed: %s", key)
		}
	}
	for _, key := range []string{
		"CONSUL_HTTP_TOKEN", "CONSUL_HTTP_TOKEN_FILE", "CONSUL_HTTP_AUTH", "CONSUL_CLIENT_KEY",
		"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS",
		"OTEL_EXPORTER_OTLP_CLIENT_KEY", "OTEL_EXPORTER_OTLP_TRACES_CLIENT_KEY",
	} {
		if os.Getenv(key) != "" {
			return fmt.Errorf("credential environment is not allowed: %s", key)
		}
	}
	for _, key := range []string{"consul.consul_address", "redis.host", "bk_api.address", "bk_data.address", "trace.otlp.host", "influxdb.target_address"} {
		if HasURLCredentials(runtime.GetString(key)) {
			return fmt.Errorf("credential in address is not allowed: %s", key)
		}
	}
	for _, addr := range runtime.GetStringSlice("redis.sentinel_address") {
		if HasURLCredentials(addr) {
			return fmt.Errorf("credential in address is not allowed: redis.sentinel_address")
		}
	}
	return nil
}

// Load reads a matching envelope/private-key pair and validates before publishing.
func Load(raw, runtime *viper.Viper) (*Snapshot, error) {
	c, err := readControl(raw)
	if err != nil {
		return nil, err
	}
	s := &Snapshot{control: c, overrides: make(map[string]any)}
	if !c.Enabled {
		return s, nil
	}
	if err := checkInputs(raw, runtime); err != nil {
		return nil, err
	}
	envelope, err := os.ReadFile(c.EnvelopeFile)
	if err != nil {
		return nil, fmt.Errorf("kms read envelope file failed")
	}
	key, err := os.ReadFile(c.PrivateKeyFile)
	if err != nil {
		return nil, fmt.Errorf("kms read private key file failed")
	}
	plaintext, err := decrypt.Decrypt(strings.TrimSpace(string(envelope)), strings.TrimSpace(string(key)))
	if err != nil {
		return nil, fmt.Errorf("kms decrypt failed")
	}
	decoder := json.NewDecoder(bytes.NewBufferString(plaintext))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&s.payload); err != nil {
		return nil, fmt.Errorf("kms payload schema is invalid")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("kms payload must contain one JSON object")
	}
	if s.payload.Version != 1 {
		return nil, fmt.Errorf("kms schema_version is unsupported")
	}
	if s.payload.Service != "unify-query" {
		return nil, fmt.Errorf("kms service must be unify-query")
	}
	if err := s.bind(); err != nil {
		return nil, err
	}
	if err := s.validate(raw, runtime); err != nil {
		return nil, err
	}
	return s, nil
}

func missing(key string) error { return fmt.Errorf("kms payload field is required: %s", key) }

func (s *Snapshot) bind() error {
	p := &s.payload
	set := func(key string, value *string) {
		if value != nil {
			s.overrides[key] = *value
		}
	}
	// Clear all implicit/default credentials even for optional consumers.
	for _, key := range managedKeys {
		s.overrides[key] = ""
	}
	s.overrides["trace.otlp.headers"] = map[string]string{}
	if p.App != nil {
		if p.App.Default == nil || p.App.Default.Code == nil || p.App.Default.Secret == nil {
			return missing("bkapp_id_secret.default.app_code/app_secret")
		}
		set("bk_api.code", p.App.Default.Code)
		set("bk_api.secret", p.App.Default.Secret)
	}
	if p.Redis != nil {
		if p.Redis.Default == nil || p.Redis.Default.Password == nil {
			return missing("redis.default.password")
		}
		set("redis.password", p.Redis.Default.Password)
	}
	if p.Sentinel != nil {
		if p.Sentinel.Default == nil || p.Sentinel.Default.Password == nil {
			return missing("redis_sentinel.default.password")
		}
		set("redis.sentinel_password", p.Sentinel.Default.Password)
	}
	if p.BkData != nil {
		if p.BkData.Default == nil || p.BkData.Default.Token == nil {
			return missing("bkdata.default.token")
		}
		set("bk_data.token", p.BkData.Default.Token)
	}
	if p.Telemetry != nil {
		if p.Telemetry.Default == nil || p.Telemetry.Default.Token == nil || p.Telemetry.Default.Headers == nil {
			return missing("telemetry.default.token/headers")
		}
		set("trace.otlp.token", p.Telemetry.Default.Token)
		s.overrides["trace.otlp.headers"] = map[string]string(*p.Telemetry.Default.Headers)
	}
	if p.Consul != nil {
		if p.Consul.Default == nil {
			return missing("consul.default")
		}
		c := p.Consul.Default
		if (c.Username == nil) != (c.Password == nil) {
			return missing("consul.default.username/password")
		}
		set("consul.token", c.Token)
		set("consul.username", c.Username)
		set("consul.password", c.Password)
		if c.PrivateKey != nil {
			pem, err := base64.StdEncoding.DecodeString(*c.PrivateKey)
			if err != nil {
				return fmt.Errorf("kms consul.default.tls_private_key_base64 is invalid")
			}
			s.overrides["consul.tls.key_pem"] = string(pem)
		}
	}
	return nil
}

func (s *Snapshot) validate(raw, runtime *viper.Viper) error {
	p := &s.payload
	if p.Redis == nil {
		return missing("redis.default.password")
	}
	if runtime.GetString("redis.mode") == "sentinel" && p.Sentinel == nil {
		return missing("redis_sentinel.default.password")
	}
	if runtime.GetString("bk_api.address") != "" && p.App == nil {
		return missing("bkapp_id_secret.default")
	}
	if runtime.GetString("bk_data.authentication_method") == "token" && p.BkData == nil {
		return missing("bkdata.default.token")
	}
	if runtime.GetBool("trace.enable") && p.Telemetry == nil {
		return missing("telemetry.default")
	}
	if p.App != nil {
		code := *p.App.Default.Code
		if raw.InConfig("bk_api.code") && raw.GetString("bk_api.code") != "" && raw.GetString("bk_api.code") != code {
			return fmt.Errorf("kms app_code differs from bk_api.code")
		}
		if v := os.Getenv(envKey("bk_api.code")); v != "" && v != code {
			return fmt.Errorf("kms app_code differs from bk_api.code environment")
		}
	}
	key, _ := s.overrides["consul.tls.key_pem"].(string)
	certFile := runtime.GetString("consul.tls.cert_file")
	if key != "" || certFile != "" {
		if key == "" || certFile == "" {
			return fmt.Errorf("kms consul TLS private key and consul.tls.cert_file must be paired")
		}
		cert, err := os.ReadFile(certFile)
		if err != nil {
			return fmt.Errorf("kms read consul TLS certificate failed")
		}
		if _, err := tls.X509KeyPair(cert, []byte(key)); err != nil {
			return fmt.Errorf("kms consul TLS key/certificate pair is invalid")
		}
		s.overrides["consul.tls.cert_pem"] = string(cert)
	} else {
		s.overrides["consul.tls.cert_pem"] = ""
	}
	return nil
}

// Reuse validates a reload against the frozen startup snapshot; Secret files are not read.
func (s *Snapshot) Reuse(raw, runtime *viper.Viper) (*Snapshot, error) {
	c, err := readControl(raw)
	if err != nil {
		return nil, err
	}
	if c != s.control {
		return nil, fmt.Errorf("kms mode and file paths cannot change on reload; restart is required")
	}
	if !c.Enabled {
		return s, nil
	}
	if err := checkInputs(raw, runtime); err != nil {
		return nil, err
	}
	// validate may refresh public certificate data on a candidate copy only.
	copy := *s
	copy.overrides = cloneOverrides(s.overrides)
	if err := copy.validate(raw, runtime); err != nil {
		return nil, err
	}
	return &copy, nil
}

func cloneOverrides(values map[string]any) map[string]any {
	copy := make(map[string]any, len(values))
	for k, v := range values {
		if headers, ok := v.(map[string]string); ok {
			m := make(map[string]string, len(headers))
			for k, v := range headers {
				m[k] = v
			}
			copy[k] = m
		} else {
			copy[k] = v
		}
	}
	return copy
}

func (s *Snapshot) Apply(v *viper.Viper) {
	for key, value := range cloneOverrides(s.overrides) {
		v.Set(key, value)
	}
}

func (s *Snapshot) Enabled() bool { return s != nil && s.control.Enabled }
