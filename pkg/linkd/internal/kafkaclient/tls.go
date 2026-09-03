// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafkaclient

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"os"
)

const maxTLSMaterialBytes = 1 << 20

// BuildTLSConfig 校验并读取 TLS 材料，返回可供 Kafka client 使用的配置。
// 非 TLS 协议返回 nil；所有 TLS 连接固定要求 TLS 1.2 及以上。
func (c SecurityConfig) BuildTLSConfig() (*tls.Config, error) {
	c = c.WithDefaults()
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if c.Protocol != SecurityProtocolSSL && c.Protocol != SecurityProtocolSASLSSL {
		return nil, nil
	}

	result := &tls.Config{MinVersion: tls.VersionTLS12}
	if c.TLS == nil {
		return result, nil
	}
	result.ServerName = c.TLS.ServerName
	// InsecureSkipVerify 只在用户显式配置时开启；配置校验禁止同时声明无效的 CA。
	result.InsecureSkipVerify = c.TLS.InsecureSkipVerify

	caPEM, caSource, err := c.TLS.caPEM()
	if err != nil {
		return nil, err
	}
	if len(caPEM) > 0 {
		roots, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system CA pool: %w", err)
		}
		if roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(caPEM) {
			return nil, fmt.Errorf("%s does not contain a valid certificate", caSource)
		}
		result.RootCAs = roots
	}

	certPEM, keyPEM, certificateSource, err := c.TLS.clientCertificatePEM()
	if err != nil {
		return nil, err
	}
	if len(certPEM) > 0 {
		certificate, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", certificateSource, err)
		}
		result.Certificates = []tls.Certificate{certificate}
	}
	return result, nil
}

func (c TLSConfig) caPEM() ([]byte, string, error) {
	if c.CAFile != "" {
		data, err := readTLSMaterial("tls.ca_file", c.CAFile)
		return data, fmt.Sprintf("tls.ca_file %q", c.CAFile), err
	}
	return []byte(c.CAPEM), "tls.ca_pem", nil
}

func (c TLSConfig) clientCertificatePEM() ([]byte, []byte, string, error) {
	if c.ClientCertFile != "" {
		certificate, err := readTLSMaterial("tls.client_cert_file", c.ClientCertFile)
		if err != nil {
			return nil, nil, "", err
		}
		key, err := readTLSMaterial("tls.client_key_file", c.ClientKeyFile)
		if err != nil {
			return nil, nil, "", err
		}
		return certificate, key, fmt.Sprintf(
			"tls.client_cert_file %q and tls.client_key_file %q",
			c.ClientCertFile,
			c.ClientKeyFile,
		), nil
	}
	return []byte(c.ClientCertPEM), []byte(c.ClientKeyPEM), "tls.client_cert_pem and tls.client_key_pem", nil
}

func readTLSMaterial(field, path string) ([]byte, error) {
	// path 来自已经过结构校验的显式 Kafka TLS 配置，读取该路径正是此函数的职责。
	file, err := os.Open(path) //nolint:gosec // G304: operator-controlled certificate or private-key path.
	if err != nil {
		return nil, fmt.Errorf("read %s %q: %w", field, path, err)
	}
	defer func() {
		_ = file.Close()
	}()

	data, err := io.ReadAll(io.LimitReader(file, maxTLSMaterialBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s %q: %w", field, path, err)
	}
	if len(data) > maxTLSMaterialBytes {
		return nil, fmt.Errorf("read %s %q: file exceeds %d bytes", field, path, maxTLSMaterialBytes)
	}
	return data, nil
}
