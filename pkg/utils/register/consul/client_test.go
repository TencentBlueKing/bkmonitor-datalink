// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License. See the License at http://opensource.org/licenses/MIT.

package consul

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/consul/api"
	"github.com/stretchr/testify/require"
)

func TestExplicitAuthenticationAppliesToRegistrationAndKV(t *testing.T) {
	var requests int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "test-token", r.Header.Get("X-Consul-Token"))
		user, pass, ok := r.BasicAuth()
		require.True(t, ok)
		require.Equal(t, "test-user", user)
		require.Equal(t, "test-password", pass)
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/kv/key" {
			_, _ = w.Write([]byte(`[]`))
		} else {
			_, _ = w.Write([]byte(`true`))
		}
	}))
	defer server.Close()
	opts := &ClientOptions{Token: "test-token", Username: "test-user", Password: "test-password"}
	registration, err := NewClientWithOptions(server.URL, opts)
	require.NoError(t, err)
	require.NoError(t, registration.CheckRegister("service", "check", "10s"))
	kv, err := NewAPIClient(server.URL, opts)
	require.NoError(t, err)
	_, _, err = kv.KV().Get("key", nil)
	require.NoError(t, err)
	require.Equal(t, int32(2), atomic.LoadInt32(&requests))
}

func TestLegacyClientPreservesEnvironmentAndExplicitRejectsIt(t *testing.T) {
	t.Setenv("CONSUL_HTTP_TOKEN", "legacy")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "legacy", r.Header.Get("X-Consul-Token"))
		_, _ = w.Write([]byte(`true`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL)
	require.NoError(t, err)
	require.NoError(t, client.CheckRegister("service", "check", "10s"))
	_, err = NewAPIClient(server.URL, &ClientOptions{Token: "explicit"})
	require.ErrorContains(t, err, "CONSUL_HTTP_TOKEN")
	for _, key := range []string{"CONSUL_HTTP_TOKEN_FILE", "CONSUL_HTTP_AUTH", "CONSUL_CLIENT_KEY"} {
		t.Run(key, func(t *testing.T) {
			t.Setenv("CONSUL_HTTP_TOKEN", "")
			t.Setenv(key, "unread-test-file")
			_, err := NewAPIClient(server.URL, &ClientOptions{})
			require.ErrorContains(t, err, key)
		})
	}
}

func TestExplicitAnonymousDoesNotSendBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Empty(t, r.Header.Get("Authorization"))
		require.Empty(t, r.Header.Get("X-Consul-Token"))
		_, _ = w.Write([]byte(`true`))
	}))
	defer server.Close()
	client, err := NewClientWithOptions(server.URL, &ClientOptions{})
	require.NoError(t, err)
	require.NoError(t, client.CheckRegister("service", "check", "10s"))
}

func TestMemoryTLSMaterialAppliesToBothClients(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	certTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-client"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certTemplate, certTemplate, &key.PublicKey, key)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	clientCAs := x509.NewCertPool()
	require.True(t, clientCAs.AppendCertsFromPEM(certPEM))
	var requests int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Len(t, r.TLS.PeerCertificates, 1)
		atomic.AddInt32(&requests, 1)
		if r.URL.Path == "/v1/kv/key" {
			_, _ = w.Write([]byte(`[]`))
		} else {
			_, _ = w.Write([]byte(`true`))
		}
	}))
	server.TLS = &tls.Config{ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: clientCAs, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	serverCA := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
	opts := &ClientOptions{TLSConfig: api.TLSConfig{CAPem: serverCA, CertPEM: certPEM, KeyPEM: keyPEM}}
	registration, err := NewClientWithOptions(server.URL, opts)
	require.NoError(t, err)
	require.NoError(t, registration.CheckRegister("service", "check", "10s"))
	kv, err := NewAPIClient(server.URL, opts)
	require.NoError(t, err)
	_, _, err = kv.KV().Get("key", nil)
	require.NoError(t, err)
	require.Equal(t, int32(2), atomic.LoadInt32(&requests))
}
