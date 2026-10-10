// Copyright (C) 2026 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License.

// Package kmstest constructs local test envelopes for the official SDK.
package kmstest

import (
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
	"path/filepath"
	"testing"
)

func Files(t testing.TB, plaintext string) (string, string) {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	key := []byte("kms-test-aes-key")
	// SDK uses a 16-byte AES key and a nonce prefixed to the ciphertext.
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	iv := make([]byte, aes.BlockSize)
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plaintext))
	cipher.NewCTR(block, iv).XORKeyStream(ciphertext, []byte(plaintext))
	encryptedKey, err := rsa.EncryptOAEP(sha256.New(), rand.Reader, &private.PublicKey, key, nil)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]string{
		"asymmetric_type": "RSA", "symmetric_type": "AES", "symmetric_mode": "CTR",
		"encrypted_key": base64.StdEncoding.EncodeToString(encryptedKey),
		"ciphertext":    base64.StdEncoding.EncodeToString(append(iv, ciphertext...)),
	})
	if err != nil {
		t.Fatal(err)
	}
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)})
	dir := t.TempDir()
	envelopeFile, keyFile := filepath.Join(dir, "envelope"), filepath.Join(dir, "private-key")
	if err := os.WriteFile(envelopeFile, []byte(base64.StdEncoding.EncodeToString(envelope)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte(base64.StdEncoding.EncodeToString(privatePEM)), 0600); err != nil {
		t.Fatal(err)
	}
	return envelopeFile, keyFile
}
