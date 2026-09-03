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
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type certificateFixture struct {
	caPEM         []byte
	serverCertPEM []byte
	serverKeyPEM  []byte
	clientCertPEM []byte
	clientKeyPEM  []byte
}

func TestBuildTLSConfigInlineMTLSHandshake(t *testing.T) {
	t.Parallel()

	fixture := newCertificateFixture(t)
	security := SecurityConfig{
		Protocol: SecurityProtocolSSL,
		TLS: &TLSConfig{
			CAPEM:         string(fixture.caPEM),
			ClientCertPEM: string(fixture.clientCertPEM),
			ClientKeyPEM:  string(fixture.clientKeyPEM),
			ServerName:    "kafka.test",
		},
	}
	clientConfig, err := security.BuildTLSConfig()
	if err != nil {
		t.Fatalf("BuildTLSConfig() error = %v", err)
	}
	if clientConfig.MinVersion != tls.VersionTLS12 || clientConfig.ServerName != "kafka.test" ||
		clientConfig.InsecureSkipVerify || len(clientConfig.Certificates) != 1 || clientConfig.RootCAs == nil {
		t.Fatalf("BuildTLSConfig() = %#v", clientConfig)
	}

	serverCertificate, err := tls.X509KeyPair(fixture.serverCertPEM, fixture.serverKeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair(server) error = %v", err)
	}
	clientRoots := x509.NewCertPool()
	if !clientRoots.AppendCertsFromPEM(fixture.caPEM) {
		t.Fatal("AppendCertsFromPEM(client CA) = false")
	}
	serverConfig := &tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{serverCertificate},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientRoots,
	}

	serverSide, clientSide := net.Pipe()
	defer func() {
		_ = serverSide.Close()
	}()
	defer func() {
		_ = clientSide.Close()
	}()
	serverTLS := tls.Server(serverSide, serverConfig)
	clientTLS := tls.Client(clientSide, clientConfig)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- serverTLS.HandshakeContext(ctx)
	}()
	if err := clientTLS.HandshakeContext(ctx); err != nil {
		t.Fatalf("client HandshakeContext() error = %v", err)
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("server HandshakeContext() error = %v", err)
	}
}

func TestBuildTLSConfigFileModeAndFailures(t *testing.T) {
	t.Parallel()

	fixture := newCertificateFixture(t)
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "ca.pem"), fixture.caPEM)
	writeTestFile(t, filepath.Join(directory, "client.crt"), fixture.clientCertPEM)
	writeTestFile(t, filepath.Join(directory, "client.key"), fixture.clientKeyPEM)

	security := SecurityConfig{
		Protocol: SecurityProtocolSSL,
		TLS: &TLSConfig{
			CAFile:         "ca.pem",
			ClientCertFile: "client.crt",
			ClientKeyFile:  "client.key",
		},
	}
	resolved, err := security.ResolvePaths(directory)
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}
	config, err := resolved.BuildTLSConfig()
	if err != nil {
		t.Fatalf("BuildTLSConfig() error = %v", err)
	}
	if config.RootCAs == nil || len(config.Certificates) != 1 {
		t.Fatalf("BuildTLSConfig() = %#v", config)
	}

	tests := []struct {
		name      string
		config    SecurityConfig
		wantError string
	}{
		{
			name: "missing CA file",
			config: SecurityConfig{Protocol: SecurityProtocolSSL, TLS: &TLSConfig{
				CAFile: filepath.Join(directory, "missing.pem"),
			}},
			wantError: "missing.pem",
		},
		{
			name: "invalid inline CA",
			config: SecurityConfig{Protocol: SecurityProtocolSSL, TLS: &TLSConfig{
				CAPEM: "not-a-certificate",
			}},
			wantError: "tls.ca_pem",
		},
		{
			name: "certificate key mismatch",
			config: SecurityConfig{Protocol: SecurityProtocolSSL, TLS: &TLSConfig{
				ClientCertPEM: string(fixture.clientCertPEM),
				ClientKeyPEM:  string(fixture.serverKeyPEM),
			}},
			wantError: "private key does not match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := test.config.BuildTLSConfig()
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("BuildTLSConfig() error = %v, want containing %q", err, test.wantError)
			}
		})
	}

	oversizedPath := filepath.Join(directory, "oversized.pem")
	writeTestFile(t, oversizedPath, bytes.Repeat([]byte("x"), maxTLSMaterialBytes+1))
	_, err = (SecurityConfig{
		Protocol: SecurityProtocolSSL,
		TLS:      &TLSConfig{CAFile: oversizedPath},
	}).BuildTLSConfig()
	if err == nil || !strings.Contains(err.Error(), "file exceeds") {
		t.Fatalf("BuildTLSConfig(oversized) error = %v", err)
	}
}

func TestBuildTLSConfigDefaultsAndInsecureMode(t *testing.T) {
	t.Parallel()

	plaintext, err := (SecurityConfig{}).BuildTLSConfig()
	if err != nil || plaintext != nil {
		t.Fatalf("BuildTLSConfig(plaintext) = %#v, %v", plaintext, err)
	}
	secure, err := (SecurityConfig{Protocol: SecurityProtocolSSL}).BuildTLSConfig()
	if err != nil {
		t.Fatalf("BuildTLSConfig(ssl) error = %v", err)
	}
	if secure.MinVersion != tls.VersionTLS12 || secure.InsecureSkipVerify {
		t.Fatalf("BuildTLSConfig(ssl) = %#v", secure)
	}
	insecure, err := (SecurityConfig{
		Protocol: SecurityProtocolSSL,
		TLS: &TLSConfig{
			ServerName:         "kafka.test",
			InsecureSkipVerify: true,
		},
	}).BuildTLSConfig()
	if err != nil {
		t.Fatalf("BuildTLSConfig(insecure) error = %v", err)
	}
	if !insecure.InsecureSkipVerify || insecure.ServerName != "kafka.test" {
		t.Fatalf("BuildTLSConfig(insecure) = %#v", insecure)
	}
}

func newCertificateFixture(t *testing.T) certificateFixture {
	t.Helper()
	now := time.Now().UTC()
	caKey := newTestKey(t)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Linkd Test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate(CA) error = %v", err)
	}
	caCertificate, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("ParseCertificate(CA) error = %v", err)
	}

	serverCert, serverKey := issueTestCertificate(
		t,
		big.NewInt(2),
		"kafka.test",
		[]string{"kafka.test"},
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		caCertificate,
		caKey,
		now,
	)
	clientCert, clientKey := issueTestCertificate(
		t,
		big.NewInt(3),
		"linkd-client",
		nil,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		caCertificate,
		caKey,
		now,
	)
	return certificateFixture{
		caPEM:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		serverCertPEM: serverCert,
		serverKeyPEM:  serverKey,
		clientCertPEM: clientCert,
		clientKeyPEM:  clientKey,
	}
}

func issueTestCertificate(
	t *testing.T,
	serial *big.Int,
	commonName string,
	dnsNames []string,
	usages []x509.ExtKeyUsage,
	ca *x509.Certificate,
	caKey *ecdsa.PrivateKey,
	now time.Time,
) ([]byte, []byte) {
	t.Helper()
	key := newTestKey(t)
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		DNSNames:     dnsNames,
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  usages,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CreateCertificate(%s) error = %v", commonName, err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey(%s) error = %v", commonName, err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
}

func newTestKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	return key
}

func writeTestFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
