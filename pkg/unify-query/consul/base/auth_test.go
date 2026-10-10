package base

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func testConsulHandler(t *testing.T, requests *atomic.Int32) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "example-user" || password != "basic-secret" || r.Header.Get("X-Consul-Token") != "acl-secret" {
			http.Error(w, "unauthorized", 401)
			return
		}
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Consul-Index", "1")
		if r.URL.Query().Get("index") != "" {
			<-r.Context().Done()
			return
		}
		_, _ = w.Write([]byte(`[{"Key":"test/config","Value":"dmFsdWU=","ModifyIndex":1}]`))
	})
}

func verifyKVAndWatch(t *testing.T, client *Client, requests *atomic.Int32) {
	t.Helper()
	kv, err := client.Get("test/config")
	require.NoError(t, err)
	require.Equal(t, "value", string(kv.Value))
	ch, err := client.Watch("test/config", "")
	require.NoError(t, err)
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatal("authenticated Watch did not deliver")
	}
	require.GreaterOrEqual(t, requests.Load(), int32(2))
	require.NoError(t, client.StopWatch("test/config", "path"))
}

func TestExplicitAuthKVAndWatch(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(testConsulHandler(t, &requests))
	defer server.Close()
	client, err := NewClientWithAuth(server.URL, "", "", "", &AuthConfig{Token: "acl-secret", Username: "example-user", Password: "basic-secret"})
	require.NoError(t, err)
	defer client.Close()
	verifyKVAndWatch(t, client, &requests)
}

func TestLegacyClientKeepsEnvironmentAuth(t *testing.T) {
	t.Setenv("CONSUL_HTTP_TOKEN", "acl-secret")
	t.Setenv("CONSUL_HTTP_AUTH", "example-user:basic-secret")
	var requests atomic.Int32
	server := httptest.NewServer(testConsulHandler(t, &requests))
	defer server.Close()
	// Compile and exercise the unchanged four-argument API.
	client, err := NewClient(server.URL, "", "", "")
	require.NoError(t, err)
	defer client.Close()
	verifyKVAndWatch(t, client, &requests)
}

func TestExplicitAuthRejectsImplicitEnvironment(t *testing.T) {
	for _, env := range []string{"CONSUL_HTTP_TOKEN", "CONSUL_HTTP_TOKEN_FILE", "CONSUL_HTTP_AUTH", "CONSUL_CLIENT_KEY"} {
		t.Run(env, func(t *testing.T) {
			t.Setenv(env, "fake-secret")
			_, err := NewClientWithAuth("http://127.0.0.1", "", "", "", &AuthConfig{})
			require.Error(t, err)
			require.NotContains(t, err.Error(), "fake-secret")
		})
	}
}

func TestMemoryTLSKVAndWatch(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	cert := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-local"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth}, BasicConstraintsValid: true, IsCA: true, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, cert, cert, &private.PublicKey, private)
	require.NoError(t, err)
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)})
	pair, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	pool := x509.NewCertPool()
	require.True(t, pool.AppendCertsFromPEM(certPEM))
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(testConsulHandler(t, &requests))
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
	server.StartTLS()
	defer server.Close()
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(caFile, certPEM, 0600))
	client, err := NewClientWithAuth(server.URL, caFile, "ignored-key-file", "ignored-cert-file", &AuthConfig{Token: "acl-secret", Username: "example-user", Password: "basic-secret", KeyPEM: keyPEM, CertPEM: certPEM})
	require.NoError(t, err)
	defer client.Close()
	verifyKVAndWatch(t, client, &requests)
}
