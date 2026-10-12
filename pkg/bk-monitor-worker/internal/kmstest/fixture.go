// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

// Package kmstest constructs local envelopes consumed by the official KMS SDK.
package kmstest

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Isolate keeps InitConfig's package globals out of unrelated tests. The child
// runs only this test; no service, database or other package test is started.
func Isolate(t *testing.T) bool {
	t.Helper()
	if os.Getenv("BMW_KMS_TEST_PROCESS") == t.Name() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$")
	cmd.Env = append(os.Environ(), "BMW_KMS_TEST_PROCESS="+t.Name())
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	return true
}

func Payload(t testing.TB, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(data, &payload))
	return payload
}

// Write replaces the pair at stable paths, as a mounted credential update does.
// The SDK exposes decryption only; encryption follows the existing RSA/AES-CTR fixture.
func Write(t testing.TB, dir string, payload map[string]any) map[string]any {
	t.Helper()
	plaintext, err := json.Marshal(payload)
	require.NoError(t, err)
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	key := []byte("kms-test-aes-key")
	block, err := aes.NewCipher(key)
	require.NoError(t, err)
	iv := make([]byte, aes.BlockSize)
	_, err = rand.Read(iv)
	require.NoError(t, err)
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCTR(block, iv).XORKeyStream(ciphertext, plaintext)
	encryptedKey, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &private.PublicKey, key, nil)
	require.NoError(t, err)
	envelope, err := json.Marshal(map[string]string{
		"asymmetric_type": "RSA", "symmetric_type": "AES", "symmetric_mode": "CTR",
		"encrypted_key": base64.StdEncoding.EncodeToString(encryptedKey),
		"ciphertext":    base64.StdEncoding.EncodeToString(append(iv, ciphertext...)),
	})
	require.NoError(t, err)
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)})
	envelopeFile, keyFile := filepath.Join(dir, "envelope"), filepath.Join(dir, "private-key")
	require.NoError(t, os.WriteFile(envelopeFile, []byte(base64.StdEncoding.EncodeToString(envelope)), 0600))
	require.NoError(t, os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(privatePEM)), 0600))
	return map[string]any{"enabled": true, "envelope_file": envelopeFile, "private_key_file": keyFile}
}

func ConfigFile(t testing.TB, values map[string]any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bmw.yaml")
	WriteConfig(t, path, values)
	return path
}

func WriteConfig(t testing.TB, path string, values map[string]any) {
	t.Helper()
	data, err := yaml.Marshal(values)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, data, 0600))
}
