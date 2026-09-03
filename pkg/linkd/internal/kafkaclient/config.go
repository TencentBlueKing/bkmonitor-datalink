// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package kafkaclient 提供 Kafka consumer 与 producer 共用的连接校验、安全配置和客户端选项。
package kafkaclient

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

const (
	// SecurityProtocolPlaintext 表示不启用 TLS 和 SASL。
	SecurityProtocolPlaintext = "plaintext"
	// SecurityProtocolSSL 表示仅启用 TLS。
	SecurityProtocolSSL = "ssl"
	// SecurityProtocolSASLPlaintext 表示在明文连接上启用 SASL。
	SecurityProtocolSASLPlaintext = "sasl_plaintext"
	// SecurityProtocolSASLSSL 表示同时启用 TLS 和 SASL。
	SecurityProtocolSASLSSL = "sasl_ssl"

	// SASLMechanismPlain 表示 SASL/PLAIN 认证。
	SASLMechanismPlain = "plain"
	// SASLMechanismSCRAMSHA256 表示 SCRAM-SHA-256 认证。
	SASLMechanismSCRAMSHA256 = "scram_sha_256"
	// SASLMechanismSCRAMSHA512 表示 SCRAM-SHA-512 认证。
	SASLMechanismSCRAMSHA512 = "scram_sha_512"
)

const redactedSecret = "******"

// SecurityConfig 描述 Kafka 传输、TLS 与 SASL 配置。
type SecurityConfig struct {
	Protocol string      `yaml:"protocol"`
	TLS      *TLSConfig  `yaml:"tls,omitempty"`
	SASL     *SASLConfig `yaml:"sasl,omitempty"`
}

// TLSConfig 描述 Kafka TLS 信任根、客户端证书和服务端校验配置。
// 文件路径和内联 PEM 分别适合挂载 Secret 与单文件配置；同一类材料不得重复声明来源。
type TLSConfig struct {
	CAFile             string `yaml:"ca_file,omitempty"`
	CAPEM              string `yaml:"ca_pem,omitempty"`
	ClientCertFile     string `yaml:"client_cert_file,omitempty"`
	ClientKeyFile      string `yaml:"client_key_file,omitempty"`
	ClientCertPEM      string `yaml:"client_cert_pem,omitempty"`
	ClientKeyPEM       string `yaml:"client_key_pem,omitempty"`
	ServerName         string `yaml:"server_name,omitempty"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify,omitempty"`
}

// SASLConfig 描述 Kafka 用户名密码认证。
type SASLConfig struct {
	Mechanism string `yaml:"mechanism"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
}

// WithDefaults 返回补齐 plaintext 默认协议且不共享嵌套对象的副本。
func (c SecurityConfig) WithDefaults() SecurityConfig {
	c = c.Clone()
	if c.Protocol == "" {
		c.Protocol = SecurityProtocolPlaintext
	}
	return c
}

// Clone 返回不共享 TLS 与 SASL 指针的副本。
func (c SecurityConfig) Clone() SecurityConfig {
	cloned := c
	if c.TLS != nil {
		tlsConfig := *c.TLS
		cloned.TLS = &tlsConfig
	}
	if c.SASL != nil {
		sasl := *c.SASL
		cloned.SASL = &sasl
	}
	return cloned
}

// Redacted 返回隐藏 SASL password 与内联客户端私钥的深拷贝。
func (c SecurityConfig) Redacted() SecurityConfig {
	redacted := c.Clone()
	if redacted.SASL != nil && redacted.SASL.Password != "" {
		redacted.SASL.Password = redactedSecret
	}
	if redacted.TLS != nil && redacted.TLS.ClientKeyPEM != "" {
		redacted.TLS.ClientKeyPEM = redactedSecret
	}
	return redacted
}

// Validate 校验协议、TLS、SASL 机制和凭据的静态组合，不读取外部文件。
func (c SecurityConfig) Validate() error {
	c = c.WithDefaults()
	tlsEnabled := c.Protocol == SecurityProtocolSSL || c.Protocol == SecurityProtocolSASLSSL
	saslEnabled := c.Protocol == SecurityProtocolSASLPlaintext || c.Protocol == SecurityProtocolSASLSSL

	switch c.Protocol {
	case SecurityProtocolPlaintext, SecurityProtocolSSL,
		SecurityProtocolSASLPlaintext, SecurityProtocolSASLSSL:
	default:
		return fmt.Errorf(
			"protocol must be one of plaintext, ssl, sasl_plaintext, sasl_ssl: %q",
			c.Protocol,
		)
	}

	if !tlsEnabled && c.TLS != nil {
		return fmt.Errorf("tls must not be configured when protocol is %q", c.Protocol)
	}
	if c.TLS != nil {
		if err := c.TLS.Validate(); err != nil {
			return fmt.Errorf("tls.%w", err)
		}
	}
	if !saslEnabled {
		if c.SASL != nil {
			return fmt.Errorf("sasl must not be configured when protocol is %q", c.Protocol)
		}
		return nil
	}
	if c.SASL == nil {
		return fmt.Errorf("sasl is required when protocol is %q", c.Protocol)
	}
	if err := c.SASL.Validate(); err != nil {
		return fmt.Errorf("sasl.%w", err)
	}
	return nil
}

// Validate 校验 TLS 材料来源组合和可选文本字段，不读取证书文件。
func (c TLSConfig) Validate() error {
	for _, field := range []struct {
		name  string
		value string
	}{
		{name: "ca_file", value: c.CAFile},
		{name: "client_cert_file", value: c.ClientCertFile},
		{name: "client_key_file", value: c.ClientKeyFile},
		{name: "server_name", value: c.ServerName},
	} {
		if field.value != "" {
			if err := validateText(field.name, field.value); err != nil {
				return err
			}
		}
	}

	if c.CAFile != "" && c.CAPEM != "" {
		return fmt.Errorf("ca_file and ca_pem are mutually exclusive")
	}
	if c.InsecureSkipVerify && (c.CAFile != "" || c.CAPEM != "") {
		return fmt.Errorf("ca_file and ca_pem must not be set when insecure_skip_verify is true")
	}

	fileCertConfigured := c.ClientCertFile != ""
	fileKeyConfigured := c.ClientKeyFile != ""
	inlineCertConfigured := c.ClientCertPEM != ""
	inlineKeyConfigured := c.ClientKeyPEM != ""
	if fileCertConfigured != fileKeyConfigured {
		return fmt.Errorf("client_cert_file and client_key_file must be configured together")
	}
	if inlineCertConfigured != inlineKeyConfigured {
		return fmt.Errorf("client_cert_pem and client_key_pem must be configured together")
	}
	if fileCertConfigured && inlineCertConfigured {
		return fmt.Errorf("client certificate file and inline PEM modes are mutually exclusive")
	}
	return nil
}

// Validate 校验 SASL 机制和必填凭据。
func (c SASLConfig) Validate() error {
	switch c.Mechanism {
	case SASLMechanismPlain, SASLMechanismSCRAMSHA256, SASLMechanismSCRAMSHA512:
	default:
		return fmt.Errorf("mechanism must be one of plain, scram_sha_256, scram_sha_512: %q", c.Mechanism)
	}
	if err := validateText("username", c.Username); err != nil {
		return err
	}
	if c.Password == "" {
		return fmt.Errorf("password is required")
	}
	return nil
}

// ResolvePaths 返回将 TLS 相对文件路径按 baseDir 解析后的副本。
// 不解析符号链接，避免把配置加载语义绑定到文件当前是否已经挂载。
func (c SecurityConfig) ResolvePaths(baseDir string) (SecurityConfig, error) {
	c = c.WithDefaults()
	if err := c.Validate(); err != nil {
		return SecurityConfig{}, err
	}
	if c.TLS == nil {
		return c, nil
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return SecurityConfig{}, fmt.Errorf("resolve TLS base directory %q: %w", baseDir, err)
	}
	for _, path := range []*string{
		&c.TLS.CAFile,
		&c.TLS.ClientCertFile,
		&c.TLS.ClientKeyFile,
	} {
		if *path == "" || filepath.IsAbs(*path) {
			continue
		}
		*path = filepath.Clean(filepath.Join(absBase, *path))
	}
	return c, nil
}

func validateText(field, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if strings.TrimSpace(value) != value {
		return fmt.Errorf("%s must not contain leading or trailing whitespace", field)
	}
	if strings.ContainsFunc(value, unicode.IsControl) {
		return fmt.Errorf("%s must not contain control characters", field)
	}
	return nil
}
